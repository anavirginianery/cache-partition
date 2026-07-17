package shared

import (
	"bytes"
	"container/list"
	"encoding/gob"
	"fmt"
)

// cacheItem é o conteúdo de cada elemento da lista interna do LRU.
type cacheItem struct {
	Key    string
	Tenant string
	Size   int64
}

// LRUCache é uma cache LRU genérica com tracking do tenant proprietário.
// Não é thread-safe — uso single-goroutine.
//
// Capacidade é em bytes. Items são inseridos no front da lista (mais recente);
// evicção remove do back (menos recente).
type LRUCache struct {
	capacity  int64
	usedBytes int64
	items     *list.List
	index     map[string]*list.Element
}

// NewLRUCache cria uma nova LRU com a capacidade dada (em bytes).
func NewLRUCache(capacity int64) *LRUCache {
	return &LRUCache{
		capacity: capacity,
		items:    list.New(),
		index:    make(map[string]*list.Element),
	}
}

// Capacity retorna a capacidade total em bytes.
func (c *LRUCache) Capacity() int64 {
	return c.capacity
}

// Used retorna o número de bytes atualmente em uso.
func (c *LRUCache) Used() int64 {
	return c.usedBytes
}

// Len retorna o número de itens na cache.
func (c *LRUCache) Len() int {
	return c.items.Len()
}

// Contains retorna true se a chave está presente.
func (c *LRUCache) Contains(key string) bool {
	_, ok := c.index[key]
	return ok
}

// GetTenant retorna o tenant proprietário do item, ou ("", false) se ausente.
func (c *LRUCache) GetTenant(key string) (string, bool) {
	el, ok := c.index[key]
	if !ok {
		return "", false
	}
	return el.Value.(*cacheItem).Tenant, true
}

// Insert adiciona um novo item ao front. Se a chave já existe, faz update
// (atualiza tenant/size se mudaram, e move para o front).
//
// O caller é responsável por garantir que há espaço suficiente — Insert
// não evicta automaticamente. Use Used() + size <= Capacity() antes.
func (c *LRUCache) Insert(key, tenant string, size int64) {
	if el, ok := c.index[key]; ok {
		// Update existente: ajustar bytes e mover para frente.
		old := el.Value.(*cacheItem)
		c.usedBytes += size - old.Size
		old.Tenant = tenant
		old.Size = size
		c.items.MoveToFront(el)
		return
	}
	it := &cacheItem{Key: key, Tenant: tenant, Size: size}
	el := c.items.PushFront(it)
	c.index[key] = el
	c.usedBytes += size
}

// Promote move um item existente para o front (marca como recentemente usado).
// No-op se a chave não existir.
func (c *LRUCache) Promote(key string) {
	if el, ok := c.index[key]; ok {
		c.items.MoveToFront(el)
	}
}

// EvictLRU remove e retorna o item menos recentemente usado.
// ok=false se a cache está vazia.
func (c *LRUCache) EvictLRU() (key, tenant string, size int64, ok bool) {
	el := c.items.Back()
	if el == nil {
		return "", "", 0, false
	}
	it := el.Value.(*cacheItem)
	c.items.Remove(el)
	delete(c.index, it.Key)
	c.usedBytes -= it.Size
	return it.Key, it.Tenant, it.Size, true
}

// Resize redimensiona a capacidade. Se a nova capacidade for menor que
// usedBytes, evicta items LRU até caber. Retorna a lista de items evictados
// (pode ser nil se nenhum foi removido).
func (c *LRUCache) Resize(newCap int64) []EvictedItem {
	c.capacity = newCap
	var evicted []EvictedItem
	for c.usedBytes > c.capacity {
		k, t, s, ok := c.EvictLRU()
		if !ok {
			break
		}
		evicted = append(evicted, EvictedItem{Key: k, Tenant: t, Size: s})
	}
	return evicted
}

// snapshotState é a representação serializável da cache.
// Lista de items do back (LRU) para o front (mais recente).
type snapshotState struct {
	Capacity int64
	Items    []cacheItem // ordem: do menos para o mais recente
}

// Snapshot serializa o estado interno para bytes (gob).
// Permite restaurar a cache em outro momento (warmup → measurement).
func (c *LRUCache) Snapshot() ([]byte, error) {
	st := snapshotState{
		Capacity: c.capacity,
		Items:    make([]cacheItem, 0, c.items.Len()),
	}
	// Iterar do back (LRU) para o front, preservando ordem.
	for el := c.items.Back(); el != nil; el = el.Prev() {
		st.Items = append(st.Items, *el.Value.(*cacheItem))
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&st); err != nil {
		return nil, fmt.Errorf("encode snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// Restore reconstrói o estado da cache a partir de um snapshot (gob bytes).
// O estado prévio é descartado.
//
// st.Items está ordenado do menos recente (LRU) para o mais recente (MRU).
// Iterando nessa ordem e fazendo PushFront em cada item, o último item
// (MRU) acaba na frente da lista — exatamente o que queremos.
func (c *LRUCache) Restore(data []byte) error {
	var st snapshotState
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&st); err != nil {
		return fmt.Errorf("decode snapshot: %w", err)
	}
	c.capacity = st.Capacity
	c.items = list.New()
	c.index = make(map[string]*list.Element, len(st.Items))
	c.usedBytes = 0
	for i := range st.Items {
		it := st.Items[i]
		el := c.items.PushFront(&it)
		c.index[it.Key] = el
		c.usedBytes += it.Size
	}
	return nil
}
