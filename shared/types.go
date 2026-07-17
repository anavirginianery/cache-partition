// Package shared contém tipos e estruturas compartilhadas entre os simuladores
// das três políticas de cache (No Partition, Memshare, FairShare).
package shared

// Request representa uma única requisição ao cache, lida do trace.
type Request struct {
	TenantID  string
	ProductID string
	Timestamp float64
	Size      int64
}

// EvictionRecord registra uma evicção: o tenant vítima perdeu um item por
// causa do tenant causador. Quando victim == causer, é auto-evicção
// (relevante para FairShare onde cada tenant evicta apenas a si mesmo).
type EvictionRecord struct {
	VictimTenant string
	CauserTenant string
	ProductID    string
	Timestamp    float64
}

// EvictedItem é o resultado de uma evicção forçada (ex: durante Resize).
// Usado pela LRUCache para informar quem foi removido.
type EvictedItem struct {
	Key    string
	Tenant string
	Size   int64
}

// TenantMetrics contém as métricas agregadas por tenant em um cenário.
type TenantMetrics struct {
	Hits                 int64   `json:"hits"`
	Misses               int64   `json:"misses"`
	HitRatio             float64 `json:"hit_ratio"`
	Interference         float64 `json:"interference"`
	InterferenceReturns  int64   `json:"interference_returns,omitempty"`
	CrossTenantEvictions int64   `json:"cross_tenant_evictions,omitempty"`
	ShadowQueueRehits    int64   `json:"shadow_queue_rehits,omitempty"`
}

// EvictionPair agrega quantas vezes o causador removeu items da vítima.
// Mantido para compatibilidade com resultados antigos; o JSON principal não
// exporta eviction_pairs por padrão para evitar arquivos gigantes.
type EvictionPair struct {
	VictimTenant string `json:"victim_tenant"`
	CauserTenant string `json:"causer_tenant"`
	Count        int64  `json:"count"`
}

// ScenarioResult é o conteúdo completo de um arquivo results/E{N}.json.
type ScenarioResult struct {
	ScenarioID           string                    `json:"scenario_id"`
	Policy               string                    `json:"policy"`
	CapacityBytes        int64                     `json:"capacity_bytes"`
	CapacityPct          int                       `json:"capacity_pct"`
	WarmupSeconds        int                       `json:"warmup_seconds"`
	MeasurementSeconds   int                       `json:"measurement_seconds"`
	GlobalHits           int64                     `json:"global_hits"`
	GlobalMisses         int64                     `json:"global_misses"`
	GlobalHitRatio       float64                   `json:"global_hit_ratio"`
	SystemInterference   float64                   `json:"system_interference,omitempty"`
	InterferenceReturns  int64                     `json:"interference_returns,omitempty"`
	CrossTenantEvictions int64                     `json:"cross_tenant_evictions,omitempty"`
	NumTenants           int                       `json:"num_tenants"`
	PerTenant            map[string]*TenantMetrics `json:"per_tenant"`
	EvictionPairs        []EvictionPair            `json:"eviction_pairs,omitempty"`
	WallTimeSeconds      float64                   `json:"wall_time_seconds"`
}
