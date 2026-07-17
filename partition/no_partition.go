// Package partition implementa o simulador No Partition (LRU compartilhado),
// usado como baseline (E1-E7) na comparação experimental.
package partition

import (
	"fmt"

	"cache-simulator/shared"
)

// NoPartitionSim simula um cache LRU global compartilhado por todos os tenants.
// Não há isolamento: qualquer tenant pode evictar items de qualquer outro.
type NoPartitionSim struct {
	cache   *shared.LRUCache
	metrics *shared.MetricsCollector
}

// NewNoPartitionSim cria um simulador com a capacidade dada (em bytes)
// e um coletor de métricas.
func NewNoPartitionSim(capacity int64, metrics *shared.MetricsCollector) *NoPartitionSim {
	return &NoPartitionSim{
		cache:   shared.NewLRUCache(capacity),
		metrics: metrics,
	}
}

// ProcessRequest processa uma requisição:
//   - hit: promove o item, registra hit
//   - miss: evicta items LRU até caber, insere o novo item, registra miss
//
// Cross-tenant evictions são detectadas naturalmente (o item evictado
// pode pertencer a um tenant diferente do solicitante).
func (s *NoPartitionSim) ProcessRequest(req shared.Request) {
	// Item maior que a capacidade total: não cabe — registra miss e nada mais.
	if req.Size > s.cache.Capacity() {
		s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
		return
	}

	if s.cache.Contains(req.ProductID) {
		s.cache.Promote(req.ProductID)
		s.metrics.RecordHit(req.TenantID, req.ProductID, req.Timestamp)
		return
	}

	// Miss: evictar até caber.
	for s.cache.Used()+req.Size > s.cache.Capacity() {
		vKey, vTenant, _, ok := s.cache.EvictLRU()
		if !ok {
			break
		}
		s.metrics.RecordEviction(vTenant, req.TenantID, vKey)
	}

	s.cache.Insert(req.ProductID, req.TenantID, req.Size)
	s.metrics.RecordMiss(req.TenantID, req.ProductID, req.Timestamp, req.Size)
}

// Snapshot serializa o estado interno para bytes (gob), para uso pelo
// orquestrador entre as fases warmup e measurement.
func (s *NoPartitionSim) Snapshot() ([]byte, error) {
	return s.cache.Snapshot()
}

// Restore reconstrói o estado a partir de um snapshot.
func (s *NoPartitionSim) Restore(data []byte) error {
	return s.cache.Restore(data)
}

// Stats retorna estatísticas resumidas do cache (debug/logging).
func (s *NoPartitionSim) Stats() string {
	return fmt.Sprintf("cache: %d/%d bytes, %d items", s.cache.Used(), s.cache.Capacity(), s.cache.Len())
}
