// Package memshare implementa o simulador da política Memshare.
//
// A shadow_queue.go define a estrutura ShadowQueue, uma fila FIFO bounded
// (em bytes) que armazena IDs de items recentemente evictados de um tenant.
// Quando um miss bate em sua shadow queue, isso sinaliza que o tenant
// se beneficiaria de mais memória — Memshare então tenta realocar.
package memshare

import (
	"container/list"
)

// shadowEntry é o conteúdo de cada nó da shadow queue.
type shadowEntry struct {
	ProductID string
	Size      int64
}

// ShadowQueue é uma fila FIFO bounded em BYTES com lookup O(1).
// Items mais antigos são descartados quando o limite é atingido.
//
// Não thread-safe (uso single-goroutine, igual ao resto do simulador).
type ShadowQueue struct {
	capacityBytes int64
	usedBytes     int64
	items         *list.List
	index         map[string]*list.Element
}

// NewShadowQueue cria uma shadow queue com a capacidade dada (em bytes).
//
// O dimensionamento da shadow queue é decisão do caller. No design adotado
// (Task 11): capacityBytes = K × R_i, onde R_i = (cache_capacity ×
// reserved_proportion) / num_tenants e K é configurável (default 2).
// A fórmula faz sentido para a carga: cada tenant pode "lembrar" K vezes
// sua memória reservada, dimensionando shadow queue à escala da operação.
func NewShadowQueue(capacityBytes int64) *ShadowQueue {
	return &ShadowQueue{
		capacityBytes: capacityBytes,
		items:         list.New(),
		index:         make(map[string]*list.Element),
	}
}

// Capacity retorna a capacidade configurada (bytes).
func (q *ShadowQueue) Capacity() int64 { return q.capacityBytes }

// Used retorna os bytes atualmente armazenados.
func (q *ShadowQueue) Used() int64 { return q.usedBytes }

// Len retorna o número de items.
func (q *ShadowQueue) Len() int { return q.items.Len() }

// Contains retorna true se a productID está na shadow queue.
func (q *ShadowQueue) Contains(productID string) bool {
	_, ok := q.index[productID]
	return ok
}

// Add insere um item evictado na shadow queue. Se o tamanho do item for
// maior que a capacidade total, o item é simplesmente ignorado (não
// consegue caber). Se já existe, atualiza o tamanho e move para o front
// (vira "mais recente"). Se a capacidade for excedida, items mais antigos
// (do back) são removidos até caber.
func (q *ShadowQueue) Add(productID string, size int64) {
	if size <= 0 || size > q.capacityBytes {
		q.Remove(productID)
		return
	}
	if el, ok := q.index[productID]; ok {
		// Atualizar tamanho do existente e mover para o front.
		old := el.Value.(*shadowEntry)
		q.usedBytes += size - old.Size
		old.Size = size
		q.items.MoveToFront(el)
	} else {
		entry := &shadowEntry{ProductID: productID, Size: size}
		el := q.items.PushFront(entry)
		q.index[productID] = el
		q.usedBytes += size
	}
	// Trim do back até caber.
	for q.usedBytes > q.capacityBytes {
		back := q.items.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*shadowEntry)
		q.items.Remove(back)
		delete(q.index, entry.ProductID)
		q.usedBytes -= entry.Size
	}
}

// Remove remove explicitamente um item (no-op se ausente).
// Útil quando o item volta ao cache por hit/insert e não deve mais ser
// considerado "recentemente evictado".
func (q *ShadowQueue) Remove(productID string) {
	if el, ok := q.index[productID]; ok {
		entry := el.Value.(*shadowEntry)
		q.items.Remove(el)
		delete(q.index, productID)
		q.usedBytes -= entry.Size
	}
}
