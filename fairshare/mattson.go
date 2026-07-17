// Package fairshare implementa o simulador FairShare Max-Min.
//
// mattson.go: algoritmo de Mattson (stack distance) que produz a curva
// HR(s) — Hit Ratio em função do tamanho da partição — para um tenant,
// processando seu histórico de requisições em uma janela.
//
// Conceito:
//   Em uma LRU de tamanho s, um item dá hit se sua "stack distance"
//   (em bytes acumulados desde o topo da stack até o item) for ≤ s.
//   Mattson permite calcular HR(s) para todos os tamanhos simultaneamente
//   em uma única passagem.
//
// Implementação:
//   - Stack como list.List doubly-linked (mais recente no front)
//   - Index map[productID]*list.Element para lookup O(1)
//   - Histograma logarítmico: bucket i corresponde a distâncias em
//     (2^(i-1), 2^i]. Distância 0 vai para bucket 0.
//   - Cobre 0 a 2^maxBucketBits bytes (~1 GB).
//
// Custo:
//   Cada hit é O(d), onde d é a posição do item na stack (em # de items).
//   Para tenants com janelas de ~600s e atividade típica, d ≈ 100-300.
//   Total: ~ 8M req × 200 ops = 1.6B ops na pior análise. É o gargalo
//   mais pesado do projeto, mas tolerável para pré-computação one-shot.
package fairshare

import (
	"container/list"
)

// MaxBucketBits define o range do histograma: distâncias até 2^MaxBucketBits
// bytes (~1 GB). Distâncias maiores caem no bucket maior.
const MaxBucketBits = 30
const NumBuckets = MaxBucketBits + 1

// Mattson mantém o estado para um único tenant em uma janela.
type Mattson struct {
	stack *list.List // front = mais recente
	index map[string]*list.Element

	// Histograma: histogram[i] = nº de hits cuja stack distance caiu no bucket i.
	histogram [NumBuckets]int64

	// Total de hits e misses observados (para denominador da curva HR).
	totalHits int64
	totalReqs int64
}

type stackItem struct {
	Key  string
	Size int64
}

// NewMattson cria uma instância vazia.
func NewMattson() *Mattson {
	return &Mattson{
		stack: list.New(),
		index: make(map[string]*list.Element),
	}
}

// Record processa uma requisição. Atualiza o histograma se for hit
// (item já estava na stack); senão, registra como miss e adiciona ao topo.
//
// O size é usado tanto para o cálculo da distância em bytes quanto para
// armazenar o tamanho associado à chave (em caso de mudança de tamanho
// entre acessos ao mesmo product, atualiza).
func (m *Mattson) Record(key string, size int64) {
	if size <= 0 {
		return // ignora request inválido
	}
	m.totalReqs++

	if el, ok := m.index[key]; ok {
		// Hit: calcula stack distance (bytes acumulados desde o front).
		var dist int64
		for e := m.stack.Front(); e != nil && e != el; e = e.Next() {
			dist += e.Value.(*stackItem).Size
		}
		bucket := bucketFor(dist)
		m.histogram[bucket]++
		m.totalHits++

		// Atualiza size se mudou e move ao front.
		item := el.Value.(*stackItem)
		item.Size = size
		m.stack.MoveToFront(el)
		return
	}

	// Miss: nunca visto na stack. Não conta para o histograma.
	item := &stackItem{Key: key, Size: size}
	el := m.stack.PushFront(item)
	m.index[key] = el
}

// HRPoint é um par (tamanho_de_partição_em_bytes, hit_ratio_estimado).
type HRPoint struct {
	SizeBytes int64
	HR        float64
}

// HRCurve retorna a curva HR(s) discreta como pontos cumulativos.
//
// Cada ponto corresponde ao limite superior de um bucket: HR para uma
// partição daquele tamanho. Pontos com count==0 ainda assim aparecem
// (curva monotonicamente crescente).
//
// HR para s = 0 é definido como 0; HR para s = ∞ é totalHits / totalReqs.
//
// Se não houver requisições, retorna nil.
func (m *Mattson) HRCurve() []HRPoint {
	if m.totalReqs == 0 {
		return nil
	}
	points := make([]HRPoint, 0, NumBuckets+1)
	points = append(points, HRPoint{SizeBytes: 0, HR: 0})
	var cum int64
	for i := 0; i < NumBuckets; i++ {
		cum += m.histogram[i]
		hr := float64(cum) / float64(m.totalReqs)
		points = append(points, HRPoint{
			SizeBytes: bucketUpperBound(i),
			HR:        hr,
		})
	}
	return points
}

// TotalRequests retorna o total de requisições processadas.
func (m *Mattson) TotalRequests() int64 { return m.totalReqs }

// TotalHits retorna o total de hits (item já estava na stack).
func (m *Mattson) TotalHits() int64 { return m.totalHits }

// Reset limpa o estado para reusar a instância em uma nova janela.
func (m *Mattson) Reset() {
	m.stack.Init()
	for k := range m.index {
		delete(m.index, k)
	}
	for i := range m.histogram {
		m.histogram[i] = 0
	}
	m.totalHits = 0
	m.totalReqs = 0
}

// ===== Funções auxiliares de bucketização logarítmica =====

// bucketFor retorna o índice do bucket para uma distância em bytes.
// bucket 0: dist ≤ 1
// bucket 1: dist ≤ 2
// bucket i: dist ≤ 2^i
func bucketFor(dist int64) int {
	if dist <= 1 {
		return 0
	}
	b := 0
	v := dist - 1 // garantir dist=2^k cai em bucket k
	for v > 0 {
		b++
		v >>= 1
	}
	if b >= NumBuckets {
		return NumBuckets - 1
	}
	return b
}

// bucketUpperBound retorna o limite superior (em bytes) do bucket i.
func bucketUpperBound(i int) int64 {
	if i == 0 {
		return 1
	}
	return int64(1) << uint(i)
}

// InterpolateHR retorna o HR estimado para um tamanho de partição arbitrário,
// interpolando linearmente entre os pontos da curva.
func InterpolateHR(curve []HRPoint, sizeBytes int64) float64 {
	if len(curve) == 0 {
		return 0
	}
	if sizeBytes <= 0 {
		return 0
	}
	// Curva é monotonicamente crescente em SizeBytes.
	if sizeBytes >= curve[len(curve)-1].SizeBytes {
		return curve[len(curve)-1].HR
	}
	// Busca binária do par (lo, hi) tal que lo.SizeBytes ≤ size < hi.SizeBytes
	lo, hi := 0, len(curve)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if curve[mid].SizeBytes <= sizeBytes {
			lo = mid
		} else {
			hi = mid
		}
	}
	// Interpolação linear entre curve[lo] e curve[hi].
	span := curve[hi].SizeBytes - curve[lo].SizeBytes
	if span <= 0 {
		return curve[lo].HR
	}
	frac := float64(sizeBytes-curve[lo].SizeBytes) / float64(span)
	return curve[lo].HR + frac*(curve[hi].HR-curve[lo].HR)
}

// SizeForHR retorna o menor tamanho de partição (em bytes) que atinge o HR alvo,
// interpolando linearmente entre pontos da curva. Se hrTarget excede o HR
// máximo da curva, retorna o tamanho do último ponto.
func SizeForHR(curve []HRPoint, hrTarget float64) int64 {
	if len(curve) == 0 || hrTarget <= 0 {
		return 0
	}
	if hrTarget >= curve[len(curve)-1].HR {
		return curve[len(curve)-1].SizeBytes
	}
	// Busca binária do par (lo, hi) tal que lo.HR ≤ hr < hi.HR
	lo, hi := 0, len(curve)-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if curve[mid].HR <= hrTarget {
			lo = mid
		} else {
			hi = mid
		}
	}
	span := curve[hi].HR - curve[lo].HR
	if span <= 0 {
		return curve[hi].SizeBytes
	}
	frac := (hrTarget - curve[lo].HR) / span
	delta := float64(curve[hi].SizeBytes - curve[lo].SizeBytes)
	return curve[lo].SizeBytes + int64(frac*delta)
}
