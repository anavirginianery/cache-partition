package memshare

import (
	"math"
	"testing"
)

func TestNeedHeap_Empty(t *testing.T) {
	h := NewNeedHeap()
	if _, _, ok := h.PopMin(); ok {
		t.Error("PopMin em heap vazio deveria retornar ok=false")
	}
	if _, _, ok := h.PeekMin(); ok {
		t.Error("PeekMin em heap vazio deveria retornar ok=false")
	}
}

func TestNeedHeap_BasicOrdering(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 2.0)
	h.Update("b", 1.0)
	h.Update("c", 3.0)

	tenant, n, ok := h.PopMin()
	if !ok || tenant != "b" || n != 1.0 {
		t.Errorf("primeiro min: esperava (b,1.0), got (%s,%v)", tenant, n)
	}
}

func TestNeedHeap_UpdateInvalidatesOld(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 5.0)
	h.Update("b", 3.0)
	h.Update("a", 1.0) // a agora deve ser o min, valor antigo (5.0) obsoleto

	tenant, n, _ := h.PopMin()
	if tenant != "a" || n != 1.0 {
		t.Errorf("após update, esperava (a,1.0), got (%s,%v)", tenant, n)
	}
	// O próximo PopMin deve retornar b (a foi invalidado pelo Pop)
	tenant, n, _ = h.PopMin()
	if tenant != "b" || n != 3.0 {
		t.Errorf("segundo min: esperava (b,3.0), got (%s,%v)", tenant, n)
	}
}

func TestNeedHeap_PeekDoesNotConsume(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 2.0)
	h.Update("b", 1.0)

	t1, n1, _ := h.PeekMin()
	t2, n2, _ := h.PeekMin()
	if t1 != t2 || n1 != n2 {
		t.Errorf("Peek deveria retornar mesma coisa duas vezes: %s/%v vs %s/%v", t1, n1, t2, n2)
	}
	if t1 != "b" {
		t.Errorf("PeekMin esperava b, got %s", t1)
	}
}

func TestNeedHeap_RemoveInvalidatesAll(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 1.0)
	h.Update("b", 2.0)
	h.Remove("a") // a inelegível
	tenant, n, _ := h.PopMin()
	if tenant != "b" || n != 2.0 {
		t.Errorf("após remove(a), min deveria ser (b,2.0), got (%s,%v)", tenant, n)
	}
}

func TestNeedHeap_PopInvalidatesUntilUpdate(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 1.0)
	h.PopMin()
	// a foi popado e invalidado; não deveria voltar até nova Update
	if _, _, ok := h.PopMin(); ok {
		t.Error("após pop sem update, heap deveria estar vazio")
	}
	h.Update("a", 5.0)
	tenant, n, _ := h.PopMin()
	if tenant != "a" || n != 5.0 {
		t.Errorf("após reactivar a, esperava (a,5.0), got (%s,%v)", tenant, n)
	}
}

func TestNeedHeap_NaNAndInfIgnored(t *testing.T) {
	h := NewNeedHeap()
	h.Update("a", 1.0)
	h.Update("b", math.Inf(1))
	if h.Len() != 1 {
		t.Errorf("Inf deveria ser ignorado, len=%d", h.Len())
	}
}

func TestNeedHeap_Compact(t *testing.T) {
	h := NewNeedHeap()
	for i := 0; i < 100; i++ {
		h.Update("a", float64(i))
		h.Update("b", float64(i))
	}
	if h.Len() < 200 {
		t.Errorf("antes do compact, len deveria ser >= 200, got %d", h.Len())
	}
	h.Compact()
	if h.Len() != 2 {
		t.Errorf("após compact, deveria ter só 2 (a,b), got %d", h.Len())
	}
	// PeekMin deve dar uma das versões mais recentes (índice 99)
	_, n, _ := h.PeekMin()
	if n != 99.0 {
		t.Errorf("após compact, top deveria ter need=99, got %v", n)
	}
}
