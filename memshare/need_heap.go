package memshare

import (
	"container/heap"
	"math"
)

// NeedHeap encontra o tenant com menor N_i = T_i / A_i em O(log n) amortizado.
// Usado pelo Memshare para escolher a vítima de evicção em cada miss.
//
// Estratégia: lazy deletion via versionamento.
//   - Cada tenant tem uma "versão" atual (versions[tenant]).
//   - Quando T_i ou A_i muda, a versão é incrementada e uma nova entrada
//     é pushada para o heap com o novo valor de N_i.
//   - Pop descarta entradas cujo Version != versions[tenant] (obsoletas).
//
// Tenants com A_i = 0 são considerados "inelegíveis para evicção" e não
// devem participar — o caller (memshare.go) só chama Update quando A_i > 0
// e remove o tenant quando A_i = 0 (zerando a versão para invalidar
// quaisquer entradas pendentes).
//
// Não thread-safe.
type NeedHeap struct {
	pq       needPQ
	versions map[string]uint64 // tenant → versão "ativa"
}

// NewNeedHeap cria um heap vazio.
func NewNeedHeap() *NeedHeap {
	return &NeedHeap{
		pq:       needPQ{},
		versions: make(map[string]uint64),
	}
}

// Update registra um novo valor de need para o tenant, invalidando
// qualquer entrada anterior. Se A_i = 0, em vez disso chame Remove.
func (h *NeedHeap) Update(tenant string, need float64) {
	if math.IsNaN(need) || math.IsInf(need, 0) {
		return // proteção contra T/0
	}
	h.versions[tenant]++
	heap.Push(&h.pq, &needEntry{
		Need:    need,
		Tenant:  tenant,
		Version: h.versions[tenant],
	})
}

// Remove invalida todas as entradas do tenant (incrementa versão).
// Use quando A_i = 0 ou quando o tenant não deve ser evictado.
func (h *NeedHeap) Remove(tenant string) {
	h.versions[tenant]++
	// não pushamos nada novo — futuras pops ignoram entradas antigas.
}

// PopMin retorna o tenant com menor N_i válido.
// Entradas obsoletas (versão diferente da atual) são descartadas.
// ok=false se o heap está vazio.
//
// Após PopMin retornar um tenant, o caller geralmente fará a evicção
// e chamará Update com o novo N_i (T_i ainda igual, A_i diminuiu).
// O Pop *não* re-insere — o caller é responsável por chamar Update
// se quiser que o tenant continue elegível.
func (h *NeedHeap) PopMin() (tenant string, need float64, ok bool) {
	for h.pq.Len() > 0 {
		entry := heap.Pop(&h.pq).(*needEntry)
		if entry.Version != h.versions[entry.Tenant] {
			continue // obsoleta
		}
		// Importante: Pop é destrutivo. Para que o tenant continue
		// elegível, o caller precisa chamar Update novamente após o pop.
		// Aqui invalidamos a versão atual para forçar isso.
		h.versions[entry.Tenant]++
		return entry.Tenant, entry.Need, true
	}
	return "", 0, false
}

// PeekMin retorna o tenant com menor N_i sem removê-lo.
// Diferente de PopMin: NÃO invalida a versão.
// Internamente, descarta entradas obsoletas até achar uma válida.
// Útil quando queremos consultar o vencedor sem alterar estado.
func (h *NeedHeap) PeekMin() (tenant string, need float64, ok bool) {
	for h.pq.Len() > 0 {
		top := h.pq[0]
		if top.Version != h.versions[top.Tenant] {
			heap.Pop(&h.pq) // descarta obsoleta e continua
			continue
		}
		return top.Tenant, top.Need, true
	}
	return "", 0, false
}

// Len retorna o tamanho bruto do heap (inclui entradas obsoletas).
// Útil só para debug/instrumentação.
func (h *NeedHeap) Len() int { return h.pq.Len() }

// Compact reconstrói o heap mantendo apenas a entrada mais recente de cada
// tenant. Operação O(n). Útil para evitar crescimento ilimitado em traces
// muito grandes (lazy deletion acumula entradas obsoletas indefinidamente).
//
// Estratégia: iterar todas as entradas, manter apenas as cuja versão bate
// com versions[tenant], rebuilder o heap com elas.
func (h *NeedHeap) Compact() {
	if h.pq.Len() == 0 {
		return
	}
	// Manter primeira entrada válida vista por tenant.
	keep := make([]*needEntry, 0, len(h.versions))
	seen := make(map[string]bool, len(h.versions))
	for _, e := range h.pq {
		if e.Version != h.versions[e.Tenant] {
			continue
		}
		if seen[e.Tenant] {
			continue
		}
		seen[e.Tenant] = true
		keep = append(keep, e)
	}
	h.pq = keep
	heap.Init(&h.pq)
}

// ===== implementação container/heap =====

type needEntry struct {
	Need    float64
	Tenant  string
	Version uint64
}

type needPQ []*needEntry

func (pq needPQ) Len() int { return len(pq) }
func (pq needPQ) Less(i, j int) bool {
	// min-heap por Need (T/A); empate pela ordem lexicográfica de tenant
	// para determinismo (importante em testes).
	if pq[i].Need != pq[j].Need {
		return pq[i].Need < pq[j].Need
	}
	return pq[i].Tenant < pq[j].Tenant
}
func (pq needPQ) Swap(i, j int) { pq[i], pq[j] = pq[j], pq[i] }
func (pq *needPQ) Push(x any) {
	*pq = append(*pq, x.(*needEntry))
}
func (pq *needPQ) Pop() any {
	old := *pq
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*pq = old[:n-1]
	return it
}
