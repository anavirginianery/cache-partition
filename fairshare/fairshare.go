package fairshare

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"

	"cache-simulator/shared"
)

// Config contém os parâmetros do FairShare para um cenário.
type Config struct {
	// CapacityBytes é a capacidade total do cache (soma das partições).
	CapacityBytes int64

	// WindowSeconds é o tamanho da janela de demanda em segundos.
	// Default: 600 (10 min).
	WindowSeconds int

	// SlideSeconds é o intervalo de slide entre janelas em segundos.
	// Default: 300 (5 min). A cada SlideSeconds, ocorre uma nova alocação Max-Min.
	SlideSeconds int

	// FloorBytesPerTenant é o piso mínimo (bytes) que cada tenant ativo
	// recebe na alocação. Default sugerido: avg_item_size do workload
	// (~819 bytes), garantindo que cada tenant possa armazenar ao menos 1 item.
	FloorBytesPerTenant int64

	// WindowsDemandPath é o caminho do windows_demand.json gerado por
	// cmd/precompute_demand. As curvas são carregadas no startup.
	WindowsDemandPath string
}

// FairShareSim simula a política FairShare Max-Min com partições isoladas
// por tenant.
//
// Funcionamento:
//   - Carrega windows_demand.json (curvas HR pré-computadas por tenant/janela)
//   - Mantém uma PartitionCache por tenant, com capacity inicialmente igual
//   - A cada timestamp, aciona Max-Min Allocator usando a janela anterior já
//     completa; em ts=600s com window=600s, usa a janela [0,600)
//   - Para cada tenant, faz Resize na sua partition cache
//   - Processa request: hit/miss/insert no cache do tenant (auto-evicção
//     fica isolada — sem interferência cross-tenant)
type FairShareSim struct {
	cfg     Config
	metrics *shared.MetricsCollector

	// Curvas pré-computadas: windows[i].Tenants[tenantID] = []HRPoint
	windows []WindowDemand

	caches map[string]*PartitionCache

	currentWindowID int
	nextReallocTs   float64
	requestCnt      int64
}

// WindowDemand é o subset do schema de windows_demand.json.
type WindowDemand struct {
	WindowID int                      `json:"window_id"`
	StartS   float64                  `json:"start_s"`
	EndS     float64                  `json:"end_s"`
	Tenants  map[string][]HRPointJSON `json:"tenants"`
}

// HRPointJSON é o formato serializado em windows_demand.json.
type HRPointJSON struct {
	SizeBytes int64   `json:"size_bytes"`
	HR        float64 `json:"hr"`
}

// windowsDemandFile é o documento completo lido de disco.
type windowsDemandFile struct {
	WindowSeconds int            `json:"window_seconds"`
	SlideSeconds  int            `json:"slide_seconds"`
	NumWindows    int            `json:"num_windows"`
	Windows       []WindowDemand `json:"windows"`
}

// New constrói o simulador, carregando as curvas pré-computadas.
func New(cfg Config, metrics *shared.MetricsCollector) (*FairShareSim, error) {
	if cfg.CapacityBytes <= 0 {
		return nil, fmt.Errorf("CapacityBytes deve ser > 0")
	}
	if cfg.WindowSeconds <= 0 {
		cfg.WindowSeconds = 600
	}
	if cfg.SlideSeconds <= 0 {
		cfg.SlideSeconds = 300
	}
	if cfg.FloorBytesPerTenant < 0 {
		cfg.FloorBytesPerTenant = 0
	}
	if cfg.WindowsDemandPath == "" {
		return nil, fmt.Errorf("WindowsDemandPath obrigatório")
	}

	doc, err := loadWindowsDemand(cfg.WindowsDemandPath)
	if err != nil {
		return nil, fmt.Errorf("carregar windows_demand: %w", err)
	}

	// Sanidade básica: confirmar que window/slide do arquivo batem com a config.
	if doc.WindowSeconds != cfg.WindowSeconds || doc.SlideSeconds != cfg.SlideSeconds {
		return nil, fmt.Errorf("window/slide divergem: config=%d/%d, demand=%d/%d",
			cfg.WindowSeconds, cfg.SlideSeconds, doc.WindowSeconds, doc.SlideSeconds)
	}

	return &FairShareSim{
		cfg:             cfg,
		metrics:         metrics,
		windows:         doc.Windows,
		caches:          make(map[string]*PartitionCache),
		currentWindowID: -1,
		nextReallocTs:   0,
	}, nil
}

func loadWindowsDemand(path string) (*windowsDemandFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler %s: %w", path, err)
	}
	var doc windowsDemandFile
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(doc.Windows) == 0 {
		return nil, fmt.Errorf("windows vazio em %s", path)
	}
	return &doc, nil
}

// pickWindowForTimestamp retorna o índice da janela mais recente (maior id)
// cujo fim é <= ts. Isso evita olhar para o futuro: em ts=600s com window=600s
// e slide=300s, a janela escolhida é a 0 ([0,600)), não a janela começando em
// 600s. Se ainda não existe janela completa, retorna -1.
func (s *FairShareSim) pickWindowForTimestamp(ts float64) int {
	// req.Timestamp já chega em segundos: shared.IterTrace converte o timestamp
	// bruto do trace de microssegundos para segundos.
	slide := float64(s.cfg.SlideSeconds)
	window := float64(s.cfg.WindowSeconds)
	idx := int((ts - window) / slide)
	if ts < window {
		return -1
	}
	if idx >= len(s.windows) {
		idx = len(s.windows) - 1
	}
	return idx
}

// reallocate aciona o Max-Min Allocator usando as curvas da janela dada
// e redimensiona todos os PartitionCaches.
//
// Tenants ativos na janela mas que NÃO têm cache ainda → cache criado.
// Tenants COM cache mas SEM atividade na janela entram com curva trivial,
// preservando isolamento sem criar partições fora da soma global.
func (s *FairShareSim) reallocate(windowID int) {
	if windowID < 0 || windowID >= len(s.windows) {
		return
	}
	w := s.windows[windowID]

	// Construir curves no formato do allocator.
	curves := make(map[string][]HRPoint, len(w.Tenants))
	for tenant, pts := range w.Tenants {
		c := make([]HRPoint, len(pts))
		for i, p := range pts {
			c[i] = HRPoint{SizeBytes: p.SizeBytes, HR: p.HR}
		}
		curves[tenant] = c
	}

	// Incluir tenants com cache existente mas sem atividade na janela
	// para não zerá-los abruptamente. A curve trivial gera demanda útil 0;
	// eles ainda podem receber parte do excedente se sobrar capacidade.
	for tenant := range s.caches {
		if _, ok := curves[tenant]; !ok {
			curves[tenant] = []HRPoint{{0, 0}}
		}
	}

	allocation := MaxMinAllocateByUsefulDemand(curves, s.cfg.CapacityBytes, s.cfg.FloorBytesPerTenant)

	// Criar caches para tenants novos; redimensionar existentes.
	for tenant, size := range allocation {
		c, ok := s.caches[tenant]
		if !ok {
			c = NewPartitionCache(tenant, size, s.metrics)
			s.caches[tenant] = c
			continue
		}
		c.Resize(size)
	}

	// Caches de tenants que SUMIRAM (não estão em allocation): zerar.
	for tenant, c := range s.caches {
		if _, ok := allocation[tenant]; !ok {
			c.Resize(0)
		}
	}
}

// ProcessRequest processa uma requisição.
//
// Em cada nova janela completa (a cada SlideSeconds no timeline), aciona
// Max-Min Allocator usando as curvas pré-computadas da janela anterior e
// redimensiona partitions.
func (s *FairShareSim) ProcessRequest(req shared.Request) {
	s.requestCnt++

	// Verificar transição de janela completa.
	wid := s.pickWindowForTimestamp(req.Timestamp)
	if wid >= 0 && wid != s.currentWindowID {
		s.reallocate(wid)
		s.currentWindowID = wid
	}

	if req.Size <= 0 {
		s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
		return
	}

	// Se o tenant ainda não recebeu partição via realocação, ele não pode
	// armazenar nesta janela. Isso preserva a capacidade global: partições
	// novas só nascem pelo allocator, não por floor criado fora da soma.
	c, ok := s.caches[req.TenantID]
	if !ok {
		s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
		return
	}

	if c.Contains(req.ProductID) {
		c.Promote(req.ProductID)
		s.metrics.RecordHit(req.TenantID, req.ProductID, req.Timestamp)
		return
	}

	// Miss. Inserir (auto-evicta se necessário).
	if req.Size > c.Capacity() {
		// Item maior que partition do tenant — não cabe sequer evictando tudo.
		s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
		return
	}
	c.Insert(req.ProductID, req.Size)
	s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
}

// Stats retorna sumário rápido.
func (s *FairShareSim) Stats() string {
	var totalUsed int64
	for _, c := range s.caches {
		totalUsed += c.Used()
	}
	return fmt.Sprintf("fairshare: %d caches, totalUsed=%d/%d bytes, currentWindow=%d",
		len(s.caches), totalUsed, s.cfg.CapacityBytes, s.currentWindowID)
}

// ===== Snapshot / Restore =====

type snapshotPayload struct {
	Cfg             Config
	CurrentWindowID int
	NextReallocTs   float64
	RequestCnt      int64
	// Estado por tenant: capacidade atual + dump da LRU interna.
	Caches map[string]cacheSnap
}

type cacheSnap struct {
	CacheGob []byte
}

// Snapshot serializa o estado completo. As curvas pré-computadas NÃO
// são incluídas (são imutáveis e vêm do arquivo windows_demand.json).
func (s *FairShareSim) Snapshot() ([]byte, error) {
	pl := snapshotPayload{
		Cfg:             s.cfg,
		CurrentWindowID: s.currentWindowID,
		NextReallocTs:   s.nextReallocTs,
		RequestCnt:      s.requestCnt,
		Caches:          make(map[string]cacheSnap, len(s.caches)),
	}
	for t, c := range s.caches {
		data, err := c.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("snapshot cache %s: %w", t, err)
		}
		pl.Caches[t] = cacheSnap{CacheGob: data}
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&pl); err != nil {
		return nil, fmt.Errorf("encode: %w", err)
	}
	return buf.Bytes(), nil
}

// Restore recarrega o estado. Janelas (curvas) já foram carregadas em New().
func (s *FairShareSim) Restore(data []byte) error {
	var pl snapshotPayload
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&pl); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	s.cfg = pl.Cfg
	s.currentWindowID = pl.CurrentWindowID
	s.nextReallocTs = pl.NextReallocTs
	s.requestCnt = pl.RequestCnt
	s.caches = make(map[string]*PartitionCache, len(pl.Caches))
	for t, cs := range pl.Caches {
		c := NewPartitionCache(t, 0, s.metrics)
		if err := c.Restore(cs.CacheGob); err != nil {
			return fmt.Errorf("restore cache %s: %w", t, err)
		}
		s.caches[t] = c
	}
	return nil
}
