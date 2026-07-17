package partition

import (
	"testing"

	"cache-simulator/shared"
)

func TestNoPartition_BasicHitMiss(t *testing.T) {
	m := shared.NewMetricsCollector()
	sim := NewNoPartitionSim(1000, m)

	// 4 misses iniciais
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "b", Timestamp: 2, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "c", Timestamp: 3, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 4, Size: 100}) // hit

	hits, misses, hr := m.Stats()
	if hits != 1 {
		t.Errorf("hits esperados 1, got %d", hits)
	}
	if misses != 3 {
		t.Errorf("misses esperados 3, got %d", misses)
	}
	if hr != 0.25 {
		t.Errorf("hr esperado 0.25, got %v", hr)
	}
}

func TestNoPartition_CrossTenantInterference(t *testing.T) {
	m := shared.NewMetricsCollector()
	// Cache de 200 bytes, items de 100 cada → cabe 2.
	sim := NewNoPartitionSim(200, m)

	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "p1", Timestamp: 1, Size: 100}) // miss, cache=[p1]
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "p2", Timestamp: 2, Size: 100}) // miss, cache=[p1,p2]
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "p3", Timestamp: 3, Size: 100}) // miss → evicta p1 (de t1)
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "p1", Timestamp: 4, Size: 100}) // miss → interferência

	// p1 foi evictado por t2, e t1 (dono original) o pediu de novo → interferência
	hits, misses, _ := m.Stats()
	if hits != 0 {
		t.Errorf("hits esperados 0, got %d", hits)
	}
	if misses != 4 {
		t.Errorf("misses esperados 4, got %d", misses)
	}

	// Validar via export JSON
	dir := t.TempDir()
	out := dir + "/result.json"
	if err := m.Export("E_test", "no_partition", 200, 5, 0, 5, out); err != nil {
		t.Fatal(err)
	}
}

func TestNoPartition_OversizedItemMisses(t *testing.T) {
	m := shared.NewMetricsCollector()
	sim := NewNoPartitionSim(100, m)

	// item maior que cache → registra miss mas não tenta inserir
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "huge", Timestamp: 1, Size: 1000})

	_, misses, _ := m.Stats()
	if misses != 1 {
		t.Errorf("oversized item deveria gerar 1 miss, got %d", misses)
	}
	if sim.cache.Len() != 0 {
		t.Errorf("cache não deveria ter aceito o item, mas tem %d itens", sim.cache.Len())
	}
}

func TestNoPartition_WarmupNoMetrics(t *testing.T) {
	m := shared.NewMetricsCollector()
	m.IsWarmup = true
	sim := NewNoPartitionSim(1000, m)

	// processa requests durante warmup
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 2, Size: 100})

	hits, misses, _ := m.Stats()
	if hits != 0 || misses != 0 {
		t.Errorf("warmup não deveria registrar nada, got %d hits / %d misses", hits, misses)
	}
	// Mas o cache DEVE ter sido populado
	if !sim.cache.Contains("a") {
		t.Error("cache deveria ter sido populado durante warmup")
	}
}

func TestNoPartition_SnapshotRestore(t *testing.T) {
	m1 := shared.NewMetricsCollector()
	sim1 := NewNoPartitionSim(1000, m1)
	sim1.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim1.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "b", Timestamp: 2, Size: 100})

	data, err := sim1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	m2 := shared.NewMetricsCollector()
	sim2 := NewNoPartitionSim(0, m2)
	if err := sim2.Restore(data); err != nil {
		t.Fatal(err)
	}

	// hit em b, no novo simulador
	sim2.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "b", Timestamp: 3, Size: 100})
	hits, _, _ := m2.Stats()
	if hits != 1 {
		t.Errorf("após restore, esperava 1 hit em b, got %d", hits)
	}
}
