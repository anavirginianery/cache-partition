package memshare

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"math/rand"

	"cache-simulator/shared"
)

// Config contém os parâmetros do Memshare para um cenário.
type Config struct {
	// CapacityBytes é a capacidade total do cache compartilhado.
	CapacityBytes int64

	// ReservedProportion ∈ [0,1]: fração da capacidade alocada como reserved.
	// O restante é shared (pooled) e pode ser realocado dinamicamente.
	// Default no design: 0.5 (50% reservada).
	ReservedProportion float64

	// CreditBytes (C) é o tamanho do crédito transferido por shadow queue hit.
	// Design adotado: tamanho médio dos itens distintos (~819 bytes).
	CreditBytes int64

	// ShadowQueueSizeBytes é a capacidade da shadow queue de cada tenant.
	// Decisão (Task 11): K × R_i, onde R_i = (CapacityBytes ×
	// ReservedProportion) / NumTenants. Default K = 2.
	// O orquestrador é quem calcula esse valor e passa pronto.
	ShadowQueueSizeBytes int64

	// NumTenants é usado para calcular R_i e P_i iniciais.
	// Tenants não vistos no warmup mas que aparecem na medição são
	// criados sob demanda com cotas baseadas nesse número.
	NumTenants int

	// CompactInterval (>0) força Compact() do need-heap a cada N requests.
	// Se 0, sem compactação automática (lazy deletion pura).
	// Default sugerido: 100_000.
	CompactInterval int64

	// Seed para RNG de seleção de doadores. 0 → aleatório do tempo.
	Seed int64
}

// tenantState mantém o estado por tenant.
type tenantState struct {
	R       int64 // memória reservada (fixa após inicialização)
	P       int64 // memória compartilhada (variável; ≥ 0)
	A       int64 // bytes em uso (sum das sizes do cache do tenant)
	cache   *shared.LRUCache
	shadowQ *ShadowQueue
}

// T retorna a cota total = R + P.
func (s *tenantState) T() int64 { return s.R + s.P }

// MemshareSim simula a política Memshare conforme a seção 2 do design.
//
// Pontos-chave do algoritmo:
//   - Cada tenant tem cache LRU privado, shadow queue, e parâmetros R, P, A.
//   - Eviction síncrona per-request: a cada miss que precisa de espaço,
//     escolhemos o tenant com menor N_i = T_i / A_i (via min-heap) e
//     evictamos seu LRU até caber.
//   - Shadow queue hit: tenta transferir CreditBytes do P de um doador
//     aleatório (com P >= CreditBytes) para o P do tenant atual.
type MemshareSim struct {
	cfg     Config
	metrics *shared.MetricsCollector

	tenants    map[string]*tenantState
	totalUsed  int64 // soma de A_i (cache total ocupado em bytes globais)
	heap       *NeedHeap
	rng        *rand.Rand
	knownIDs   []string // lista de tenant IDs vistos (para escolha aleatória O(1) do doador)
	requestCnt int64
}

// New cria um simulador Memshare com a configuração dada.
// Tenants são criados sob demanda (na primeira request de cada um).
func New(cfg Config, metrics *shared.MetricsCollector) *MemshareSim {
	if cfg.NumTenants <= 0 {
		cfg.NumTenants = 1 // proteção
	}
	if cfg.ReservedProportion < 0 || cfg.ReservedProportion > 1 {
		cfg.ReservedProportion = 0.5
	}
	if cfg.CreditBytes <= 0 {
		cfg.CreditBytes = 1 // mínimo
	}
	if cfg.ShadowQueueSizeBytes < 0 {
		cfg.ShadowQueueSizeBytes = 0
	}
	seed := cfg.Seed
	if seed == 0 {
		seed = 42 // determinístico por padrão (reprodutibilidade)
	}
	return &MemshareSim{
		cfg:      cfg,
		metrics:  metrics,
		tenants:  make(map[string]*tenantState, cfg.NumTenants),
		heap:     NewNeedHeap(),
		rng:      rand.New(rand.NewSource(seed)),
		knownIDs: make([]string, 0, cfg.NumTenants),
	}
}

// reservedPerTenant retorna R_i: (capacity × reserved_proportion) / num_tenants.
func (s *MemshareSim) reservedPerTenant() int64 {
	totalReserved := float64(s.cfg.CapacityBytes) * s.cfg.ReservedProportion
	return int64(totalReserved / float64(s.cfg.NumTenants))
}

// pooledPerTenant retorna P_i inicial: a parcela shared dividida igualmente.
func (s *MemshareSim) pooledPerTenant() int64 {
	totalPooled := float64(s.cfg.CapacityBytes) * (1 - s.cfg.ReservedProportion)
	return int64(totalPooled / float64(s.cfg.NumTenants))
}

// getOrCreateTenant retorna o estado do tenant, criando se ausente.
func (s *MemshareSim) getOrCreateTenant(id string) *tenantState {
	if t, ok := s.tenants[id]; ok {
		return t
	}
	r := s.reservedPerTenant()
	p := s.pooledPerTenant()
	t := &tenantState{
		R:       r,
		P:       p,
		A:       0,
		cache:   shared.NewLRUCache(s.cfg.CapacityBytes), // capacidade individual = teto global; controle real via T
		shadowQ: NewShadowQueue(s.cfg.ShadowQueueSizeBytes),
	}
	s.tenants[id] = t
	s.knownIDs = append(s.knownIDs, id)
	return t
}

// updateHeap recalcula need(tenant) e atualiza o heap.
// Se A == 0, o tenant é removido (não há nada para evictar).
func (s *MemshareSim) updateHeap(id string, t *tenantState) {
	if t.A <= 0 {
		s.heap.Remove(id)
		return
	}
	need := float64(t.T()) / float64(t.A)
	if math.IsNaN(need) || math.IsInf(need, 0) {
		s.heap.Remove(id)
		return
	}
	s.heap.Update(id, need)
}

// pickRandomDonor retorna um tenant aleatório (≠ requester) com P suficiente
// para doar um crédito inteiro.
// Retorna nil se nenhum doador existir.
//
// Design adotado (sem retry de N tentativas): uma única amostragem.
// Se não tiver candidato no índice escolhido, varremos linearmente
// (aceitável para 5000 tenants).
func (s *MemshareSim) pickRandomDonor(requester string) (*tenantState, string) {
	n := len(s.knownIDs)
	if n <= 1 {
		return nil, ""
	}
	start := s.rng.Intn(n)
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		id := s.knownIDs[idx]
		if id == requester {
			continue
		}
		t := s.tenants[id]
		if t != nil && t.P >= s.cfg.CreditBytes {
			return t, id
		}
	}
	return nil, ""
}

// transferCredit move até CreditBytes de P[donor] para P[recipient].
// Atualiza heap dos dois.
func (s *MemshareSim) transferCredit(recipient *tenantState, recipientID string, donor *tenantState, donorID string) {
	transfer := s.cfg.CreditBytes
	if transfer > donor.P {
		transfer = donor.P
	}
	if transfer <= 0 {
		return
	}
	donor.P -= transfer
	recipient.P += transfer
	// Heap depende de N = T/A; mudamos T (via P) → atualizar de ambos.
	if donor.A > 0 {
		s.updateHeap(donorID, donor)
	}
	if recipient.A > 0 {
		s.updateHeap(recipientID, recipient)
	}
}

// evictUntilFits evicta até que totalUsed + incomingSize ≤ capacity.
// Retorna número de evictions realizadas.
func (s *MemshareSim) evictUntilFits(incomingSize int64, causerID string) int {
	count := 0
	for s.totalUsed+incomingSize > s.cfg.CapacityBytes {
		victimID, _, ok := s.heap.PopMin()
		if !ok {
			// Não há ninguém com A > 0 no heap — não deveria acontecer
			// se totalUsed > 0. Quebra para evitar loop infinito.
			break
		}
		victim := s.tenants[victimID]
		if victim == nil || victim.A <= 0 {
			// Stale: continue procurando.
			continue
		}
		// Evict LRU do tenant vítima.
		key, _, sz, ok := victim.cache.EvictLRU()
		if !ok {
			continue
		}
		victim.A -= sz
		s.totalUsed -= sz
		victim.shadowQ.Add(key, sz)
		s.metrics.RecordEviction(victimID, causerID, key)
		// Recolocar tenant no heap se ainda tem A > 0.
		if victim.A > 0 {
			s.updateHeap(victimID, victim)
		}
		count++
	}
	return count
}

// ProcessRequest processa uma única requisição.
func (s *MemshareSim) ProcessRequest(req shared.Request) {
	s.requestCnt++
	if s.cfg.CompactInterval > 0 && s.requestCnt%s.cfg.CompactInterval == 0 {
		s.heap.Compact()
	}

	if req.Size <= 0 || req.Size > s.cfg.CapacityBytes {
		// Item inválido ou maior que cache total — registra miss e ignora.
		s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
		return
	}

	t := s.getOrCreateTenant(req.TenantID)

	// Hit?
	if t.cache.Contains(req.ProductID) {
		t.cache.Promote(req.ProductID)
		s.metrics.RecordHit(req.TenantID, req.ProductID, req.Timestamp)
		// Hit limpa qualquer entrada na shadow queue (item está presente).
		t.shadowQ.Remove(req.ProductID)
		return
	}

	// Miss. Primeiro: verificar shadow queue (sinaliza que tenant precisa de mais memória).
	if t.shadowQ.Contains(req.ProductID) {
		s.metrics.RecordShadowQueueRehit(req.TenantID, req.ProductID)
		donor, donorID := s.pickRandomDonor(req.TenantID)
		if donor != nil {
			s.transferCredit(t, req.TenantID, donor, donorID)
		}
		t.shadowQ.Remove(req.ProductID)
	}

	// Evictar até caber.
	s.evictUntilFits(req.Size, req.TenantID)

	// Inserir no cache do tenant.
	t.cache.Insert(req.ProductID, req.TenantID, req.Size)
	t.A += req.Size
	s.totalUsed += req.Size
	s.updateHeap(req.TenantID, t)

	s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
}

// Stats retorna um sumário rápido (debug/logging).
func (s *MemshareSim) Stats() string {
	return fmt.Sprintf("memshare: %d tenants, totalUsed=%d/%d bytes, heap_size=%d",
		len(s.tenants), s.totalUsed, s.cfg.CapacityBytes, s.heap.Len())
}

// ===== Snapshot/Restore =====

type snapshotPayload struct {
	Cfg        Config
	TotalUsed  int64
	KnownIDs   []string
	RequestCnt int64
	// Por tenant: estado + caches/shadow_queues serializados.
	TenantStates map[string]tenantSnapshot
}

type tenantSnapshot struct {
	R, P, A     int64
	CacheGob    []byte
	ShadowItems []shadowSnapshotEntry // ordem do back (mais antigo) ao front (mais recente)
}

type shadowSnapshotEntry struct {
	ProductID string
	Size      int64
}

// Snapshot serializa o estado completo do simulador.
func (s *MemshareSim) Snapshot() ([]byte, error) {
	pl := snapshotPayload{
		Cfg:          s.cfg,
		TotalUsed:    s.totalUsed,
		KnownIDs:     append([]string(nil), s.knownIDs...),
		RequestCnt:   s.requestCnt,
		TenantStates: make(map[string]tenantSnapshot, len(s.tenants)),
	}
	for id, t := range s.tenants {
		cacheData, err := t.cache.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("snapshot cache do tenant %s: %w", id, err)
		}
		// Shadow queue: serializar ordem (back→front).
		shadowItems := make([]shadowSnapshotEntry, 0, t.shadowQ.Len())
		for el := t.shadowQ.items.Back(); el != nil; el = el.Prev() {
			e := el.Value.(*shadowEntry)
			shadowItems = append(shadowItems, shadowSnapshotEntry{ProductID: e.ProductID, Size: e.Size})
		}
		pl.TenantStates[id] = tenantSnapshot{
			R:           t.R,
			P:           t.P,
			A:           t.A,
			CacheGob:    cacheData,
			ShadowItems: shadowItems,
		}
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&pl); err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return buf.Bytes(), nil
}

// Restore reconstrói o estado a partir de um snapshot.
// O heap é reconstruído do zero a partir dos estados restaurados.
func (s *MemshareSim) Restore(data []byte) error {
	var pl snapshotPayload
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&pl); err != nil {
		return fmt.Errorf("decode payload: %w", err)
	}
	s.cfg = pl.Cfg
	s.totalUsed = pl.TotalUsed
	s.knownIDs = pl.KnownIDs
	s.requestCnt = pl.RequestCnt
	s.tenants = make(map[string]*tenantState, len(pl.TenantStates))
	s.heap = NewNeedHeap()

	for id, ts := range pl.TenantStates {
		cache := shared.NewLRUCache(s.cfg.CapacityBytes)
		if err := cache.Restore(ts.CacheGob); err != nil {
			return fmt.Errorf("restore cache do tenant %s: %w", id, err)
		}
		sq := NewShadowQueue(s.cfg.ShadowQueueSizeBytes)
		// shadowItems está em ordem back→front. Adicionar em ordem para
		// preservar: mais antigo primeiro, mais recente por último.
		for _, e := range ts.ShadowItems {
			sq.Add(e.ProductID, e.Size)
		}
		t := &tenantState{
			R:       ts.R,
			P:       ts.P,
			A:       ts.A,
			cache:   cache,
			shadowQ: sq,
		}
		s.tenants[id] = t
		s.updateHeap(id, t)
	}
	return nil
}
