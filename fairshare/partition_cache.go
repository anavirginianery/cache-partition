package fairshare

import (
	"cache-simulator/shared"
)

// PartitionCache é um wrapper sobre shared.LRUCache usado pelo FairShare,
// que automaticamente registra evictions causadas por Resize (auto-evicção)
// no MetricsCollector.
//
// FairShare opera com partições rígidas: cada tenant tem seu próprio cache
// LRU. Por isso, todas as evictions são auto-evicções (victim == causer)
// e não geram interferência cross-tenant — a propriedade fundamental do
// FairShare.
type PartitionCache struct {
	cache    *shared.LRUCache
	tenantID string
	metrics  *shared.MetricsCollector
}

// NewPartitionCache cria a partição inicial com a capacidade dada.
func NewPartitionCache(tenantID string, capacity int64, metrics *shared.MetricsCollector) *PartitionCache {
	return &PartitionCache{
		cache:    shared.NewLRUCache(capacity),
		tenantID: tenantID,
		metrics:  metrics,
	}
}

// Insert adiciona um item ao cache do tenant. Se não há espaço, evicta
// LRU items do PRÓPRIO tenant (auto-evicção).
//
// Retorna o número de items evictados durante a inserção.
func (p *PartitionCache) Insert(productID string, size int64) int {
	if size <= 0 || size > p.cache.Capacity() {
		return 0
	}
	evicted := 0
	for p.cache.Used()+size > p.cache.Capacity() {
		key, _, _, ok := p.cache.EvictLRU()
		if !ok {
			break
		}
		// Auto-evicção: o próprio tenant evicta seu item.
		p.metrics.RecordEviction(p.tenantID, p.tenantID, key)
		evicted++
	}
	p.cache.Insert(productID, p.tenantID, size)
	return evicted
}

// Contains delegate.
func (p *PartitionCache) Contains(productID string) bool { return p.cache.Contains(productID) }

// Promote delegate.
func (p *PartitionCache) Promote(productID string) { p.cache.Promote(productID) }

// Used delegate.
func (p *PartitionCache) Used() int64 { return p.cache.Used() }

// Capacity delegate.
func (p *PartitionCache) Capacity() int64 { return p.cache.Capacity() }

// Len delegate.
func (p *PartitionCache) Len() int { return p.cache.Len() }

// Resize altera a capacidade. Se a nova capacidade for menor que o uso
// atual, evicta items LRU até caber. Cada item evictado é registrado
// no metrics como auto-evicção.
//
// Retorna o número de items evictados.
func (p *PartitionCache) Resize(newCapacity int64) int {
	evicted := p.cache.Resize(newCapacity)
	for _, e := range evicted {
		p.metrics.RecordEviction(p.tenantID, p.tenantID, e.Key)
	}
	return len(evicted)
}

// Snapshot/Restore delegate (para serializar o estado do FairShare).
func (p *PartitionCache) Snapshot() ([]byte, error) { return p.cache.Snapshot() }
func (p *PartitionCache) Restore(data []byte) error  { return p.cache.Restore(data) }
