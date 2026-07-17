package shared

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// MetricsCollector acumula métricas durante a execução de um cenário.
// Não é thread-safe — uso single-goroutine.
//
// O campo IsWarmup é público porque o orquestrador alterna entre warmup
// (não registra) e measurement (registra) chamando os mesmos métodos.
//
// Para detectar interferência: quando um item é evictado por outro tenant,
// incrementamos o denominador do proprietário e guardamos o item em
// evictedItemsTracker. Se o dono original requisitar esse item novamente,
// incrementamos o numerador desse tenant.
type MetricsCollector struct {
	IsWarmup bool

	globalHits   int64
	globalMisses int64

	// perTenantHits[tenant] = hits do tenant
	perTenantHits   map[string]int64
	perTenantMisses map[string]int64

	// shadowRehits[tenant] = vezes em que o miss bateu na shadow queue
	shadowRehits map[string]int64

	// Para detecção de interferência:
	// evictedItemsTracker[tenant|productID] = (originalOwner, evictorTenant)
	// Apenas evicções cross-tenant entram aqui.
	evictedItemsTracker map[string]evictedInfo

	// Re-acessos de items que foram evictados por outros tenants.
	// Numerador da interferência por tenant.
	interferenceHits map[string]int64

	// Total de items do tenant removidos por outros tenants.
	// Denominador da interferência por tenant.
	interferenceTotal map[string]int64
}

type evictedInfo struct {
	originalOwner string
	evictedBy     string // se != originalOwner, foi cross-tenant
}

// NewMetricsCollector cria um coletor zerado, com IsWarmup=false.
func NewMetricsCollector() *MetricsCollector {
	return &MetricsCollector{
		perTenantHits:       make(map[string]int64),
		perTenantMisses:     make(map[string]int64),
		shadowRehits:        make(map[string]int64),
		evictedItemsTracker: make(map[string]evictedInfo),
		interferenceHits:    make(map[string]int64),
		interferenceTotal:   make(map[string]int64),
	}
}

func evictedItemKey(tenant, product string) string {
	return tenant + "|" + product
}

// RecordHit registra um cache hit. No-op durante warmup.
//
// Em hit, o item está no cache. Se ainda havia algum marcador antigo de
// evicção cross-tenant para esse item, ele deixa de estar pendente.
func (m *MetricsCollector) RecordHit(tenant, product string, ts float64) {
	if m.IsWarmup {
		return
	}
	m.globalHits++
	m.perTenantHits[tenant]++
	delete(m.evictedItemsTracker, evictedItemKey(tenant, product))
}

// RecordMiss registra um cache miss. No-op durante warmup.
//
// Se o item está em evictedItemsTracker e foi evictado cross-tenant,
// isso conta no numerador da interferência sofrida pelo tenant solicitante.
func (m *MetricsCollector) RecordMiss(tenant, product string, ts float64, size int64) {
	if m.IsWarmup {
		return
	}
	m.globalMisses++
	m.perTenantMisses[tenant]++

	key := evictedItemKey(tenant, product)
	if info, ok := m.evictedItemsTracker[key]; ok {
		if info.originalOwner == tenant && info.evictedBy != info.originalOwner {
			// Numerador: item meu removido por outro tenant voltou a ser requisitado.
			m.interferenceHits[tenant]++
		}
		// Item deixa de estar em "estado evictado" porque será re-inserido pelo simulador
		delete(m.evictedItemsTracker, key)
	}
}

// RecordEviction registra que o causer (que estava processando uma request)
// causou a evicção de um item de victim. Evicções cross-tenant entram no
// denominador da interferência sofrida por victim.
func (m *MetricsCollector) RecordEviction(victim, causer, product string) {
	if m.IsWarmup {
		return
	}
	if victim != causer {
		// Denominador: item do victim removido por outro tenant.
		m.interferenceTotal[victim]++
		m.evictedItemsTracker[evictedItemKey(victim, product)] = evictedInfo{
			originalOwner: victim,
			evictedBy:     causer,
		}
	}
}

// RecordShadowQueueRehit registra que o tenant teve um re-hit na shadow queue
// (Memshare). No-op durante warmup.
func (m *MetricsCollector) RecordShadowQueueRehit(tenant, product string) {
	if m.IsWarmup {
		return
	}
	m.shadowRehits[tenant]++
}

// Export serializa as métricas para um JSON em outputPath.
func (m *MetricsCollector) Export(scenarioID, policyName string, capacityBytes int64, capacityPct, warmupSec, measurementSec int, outputPath string) error {
	res := ScenarioResult{
		ScenarioID:         scenarioID,
		Policy:             policyName,
		CapacityBytes:      capacityBytes,
		CapacityPct:        capacityPct,
		WarmupSeconds:      warmupSec,
		MeasurementSeconds: measurementSec,
		GlobalHits:         m.globalHits,
		GlobalMisses:       m.globalMisses,
	}

	if total := res.GlobalHits + res.GlobalMisses; total > 0 {
		res.GlobalHitRatio = float64(res.GlobalHits) / float64(total)
	}
	for _, v := range m.interferenceHits {
		res.InterferenceReturns += v
	}
	for _, v := range m.interferenceTotal {
		res.CrossTenantEvictions += v
	}
	if res.CrossTenantEvictions > 0 {
		res.SystemInterference = float64(res.InterferenceReturns) / float64(res.CrossTenantEvictions)
	}

	// Coletar todos os tenants vistos em hits, misses ou métricas auxiliares.
	tenantSet := make(map[string]struct{})
	for k := range m.perTenantHits {
		tenantSet[k] = struct{}{}
	}
	for k := range m.perTenantMisses {
		tenantSet[k] = struct{}{}
	}
	for k := range m.interferenceHits {
		tenantSet[k] = struct{}{}
	}
	for k := range m.interferenceTotal {
		tenantSet[k] = struct{}{}
	}
	for k := range m.shadowRehits {
		tenantSet[k] = struct{}{}
	}
	res.NumTenants = len(tenantSet)

	res.PerTenant = make(map[string]*TenantMetrics, len(tenantSet))
	for tenant := range tenantSet {
		hits := m.perTenantHits[tenant]
		misses := m.perTenantMisses[tenant]
		tm := &TenantMetrics{
			Hits:                 hits,
			Misses:               misses,
			InterferenceReturns:  m.interferenceHits[tenant],
			CrossTenantEvictions: m.interferenceTotal[tenant],
			ShadowQueueRehits:    m.shadowRehits[tenant],
		}
		if total := hits + misses; total > 0 {
			tm.HitRatio = float64(hits) / float64(total)
		}
		// Interferência sofrida: itens removidos por outros tenants que
		// retornaram / total de itens removidos por outros tenants.
		denom := m.interferenceTotal[tenant]
		if denom > 0 {
			tm.Interference = float64(m.interferenceHits[tenant]) / float64(denom)
		}
		res.PerTenant[tenant] = tm
	}

	// Wall time não é conhecido aqui; orquestrador pode setar via campo.
	// Vamos deixar zero (orquestrador faz Export e pode complementar).

	data, err := json.MarshalIndent(&res, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	if err := os.WriteFile(outputPath, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", outputPath, err)
	}
	return nil
}

// ExportWithWallTime variante que aceita o tempo de execução.
func (m *MetricsCollector) ExportWithWallTime(scenarioID, policyName string, capacityBytes int64, capacityPct, warmupSec, measurementSec int, wallTime time.Duration, outputPath string) error {
	if err := m.Export(scenarioID, policyName, capacityBytes, capacityPct, warmupSec, measurementSec, outputPath); err != nil {
		return err
	}
	// Re-ler, ajustar, reescrever.
	data, err := os.ReadFile(outputPath)
	if err != nil {
		return err
	}
	var res ScenarioResult
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	res.WallTimeSeconds = wallTime.Seconds()
	out, err := json.MarshalIndent(&res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, out, 0o644)
}

// Stats retorna um sumário rápido para logging.
func (m *MetricsCollector) Stats() (hits, misses int64, hitRatio float64) {
	hits = m.globalHits
	misses = m.globalMisses
	if total := hits + misses; total > 0 {
		hitRatio = float64(hits) / float64(total)
	}
	return
}
