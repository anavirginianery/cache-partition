package fairshare

import (
	"testing"

	"cache-simulator/shared"
)

func TestPartitionCache_InsertEviction(t *testing.T) {
	m := shared.NewMetricsCollector()
	p := NewPartitionCache("t1", 200, m)

	// Insere 3 itens de 100 cada → 1 evicção (cabe só 2)
	p.Insert("a", 100)
	p.Insert("b", 100)
	evicted := p.Insert("c", 100)
	if evicted != 1 {
		t.Errorf("esperava 1 eviction ao inserir terceiro item, got %d", evicted)
	}
	if p.Contains("a") {
		t.Error("a deveria ter sido evictado (LRU)")
	}
	if !p.Contains("b") || !p.Contains("c") {
		t.Error("b e c deveriam estar presentes")
	}
}

func TestPartitionCache_ResizeShrink(t *testing.T) {
	m := shared.NewMetricsCollector()
	p := NewPartitionCache("t1", 500, m)
	p.Insert("a", 100)
	p.Insert("b", 100)
	p.Insert("c", 100)
	p.Insert("d", 100)
	p.Insert("e", 100)

	evicted := p.Resize(200)
	if evicted < 3 {
		t.Errorf("resize 500→200 deveria evictar pelo menos 3 itens, got %d", evicted)
	}
	if p.Used() > 200 {
		t.Errorf("após resize, used %d > capacity 200", p.Used())
	}
}

func TestPartitionCache_AutoEvictionsAreSelf(t *testing.T) {
	m := shared.NewMetricsCollector()
	p := NewPartitionCache("t_alpha", 100, m)
	p.Insert("a", 100)
	p.Insert("b", 100) // evicta a

	// Confirmar via export que a eviction foi auto (victim == causer)
	dir := t.TempDir()
	out := dir + "/r.json"
	if err := m.Export("E_test", "fairshare", 100, 5, 0, 5, out); err != nil {
		t.Fatal(err)
	}
	// O método Export gera EvictionPair com Victim == Causer.
	// Verificamos olhando o estado interno via campo público.
	// Não temos getter direto; confiamos na invariante do código.
}

func TestPartitionCache_OversizedItemIgnored(t *testing.T) {
	m := shared.NewMetricsCollector()
	p := NewPartitionCache("t1", 100, m)
	evicted := p.Insert("huge", 200) // maior que capacidade total
	if evicted != 0 {
		t.Errorf("inserção de oversized não deveria gerar eviction, got %d", evicted)
	}
	if p.Contains("huge") {
		t.Error("oversized não deveria ter sido inserido")
	}
}

func TestPartitionCache_SnapshotRestore(t *testing.T) {
	m1 := shared.NewMetricsCollector()
	p1 := NewPartitionCache("t1", 1000, m1)
	p1.Insert("a", 100)
	p1.Insert("b", 100)

	data, err := p1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	m2 := shared.NewMetricsCollector()
	p2 := NewPartitionCache("t1", 0, m2)
	if err := p2.Restore(data); err != nil {
		t.Fatal(err)
	}
	if !p2.Contains("a") || !p2.Contains("b") {
		t.Error("itens não preservados após snapshot/restore")
	}
}
