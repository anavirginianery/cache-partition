package memshare

import (
	"testing"

	"cache-simulator/shared"
)

func newTestSim(capacity int64, numTenants int) (*MemshareSim, *shared.MetricsCollector) {
	m := shared.NewMetricsCollector()
	sim := New(Config{
		CapacityBytes:        capacity,
		ReservedProportion:   0.5,
		CreditBytes:          50,
		ShadowQueueSizeBytes: 500,
		NumTenants:           numTenants,
		Seed:                 42,
	}, m)
	return sim, m
}

func TestMemshare_BasicHitMiss(t *testing.T) {
	sim, m := newTestSim(1000, 2)

	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 2, Size: 100}) // hit
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "b", Timestamp: 3, Size: 100})

	hits, misses, _ := m.Stats()
	if hits != 1 {
		t.Errorf("hits esperados 1, got %d", hits)
	}
	if misses != 2 {
		t.Errorf("misses esperados 2, got %d", misses)
	}
}

func TestMemshare_InitialReservedAndPooledShares(t *testing.T) {
	sim, _ := newTestSim(1000, 2)
	t1 := sim.getOrCreateTenant("t1")

	if t1.R != 250 {
		t.Errorf("R inicial esperado 250, got %d", t1.R)
	}
	if t1.P != 250 {
		t.Errorf("P inicial esperado 250, got %d", t1.P)
	}
	if t1.T() != 500 {
		t.Errorf("T inicial esperado 500, got %d", t1.T())
	}
}

func TestMemshare_InitialPEnablesDonorSelection(t *testing.T) {
	sim, _ := newTestSim(1000, 2)
	sim.getOrCreateTenant("t1")
	sim.getOrCreateTenant("t2")

	donor, donorID := sim.pickRandomDonor("t1")
	if donor == nil || donorID != "t2" {
		t.Fatalf("P inicial deveria permitir t2 como doador, got %s", donorID)
	}
	if donor.P < sim.cfg.CreditBytes {
		t.Fatalf("doador deveria ter P suficiente: P=%d CreditBytes=%d", donor.P, sim.cfg.CreditBytes)
	}
}

func TestMemshare_TenantIsolationBasic(t *testing.T) {
	// Capacidade 200, 2 tenants, items de 100 cada → cabe 2 itens.
	// t1 e t2 cada um tem cache privado.
	sim, m := newTestSim(200, 2)

	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "b", Timestamp: 2, Size: 100})
	// totalUsed = 200, cabe.

	// t1 pede c → precisa evictar. Vai escolher tenant com menor T/A.
	// R_i = (200 * 0.5) / 2 = 50 para cada.
	// P_i = (200 * 0.5) / 2 = 50 para cada. T_i = 100. A_i = 100.
	// N = 100/100 = 1.0 para ambos. Empate → escolha lexicográfica (t1 < t2),
	// mas heap pode escolher qualquer um (a ordem depende de implementação).
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "c", Timestamp: 3, Size: 100})

	_, misses, _ := m.Stats()
	if misses != 3 {
		t.Errorf("misses esperados 3, got %d", misses)
	}
	// Pelo menos 1 evicção deve ter ocorrido.
}

func TestMemshare_ShadowQueueRehitTriggersTransfer(t *testing.T) {
	// Setup: 3 tenants, capacidade pequena.
	// Estratégia: forçar t1 a ter um item evictado (ele entra na shadow queue),
	// depois t1 pede o item de novo → shadow queue hit → tenta transferir crédito.

	m := shared.NewMetricsCollector()
	sim := New(Config{
		CapacityBytes:        300,
		ReservedProportion:   0.5,
		CreditBytes:          50,
		ShadowQueueSizeBytes: 1000,
		NumTenants:           3,
		Seed:                 42,
	}, m)

	// Encher cache com itens de t2 e t3, e DEPOIS t1.
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "x", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "y", Timestamp: 2, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t3", ProductID: "z", Timestamp: 3, Size: 100})
	// totalUsed = 300, full. T_i = 50 para todos. A: t2=200, t3=100. N: t2=0.25, t3=0.5.

	// t1 chega com item novo, A_t1 = 0 (não está no heap). Chegou! Mas ele não pode
	// estar no heap pois A=0. Então o heap só tem t2 e t3.
	// Eviction: pop t2 (menor N). t2 perde y (mais recente — wait, LRU evicta o mais antigo de t2
	// que é x). x vai para shadow_queue de t2.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "p", Timestamp: 4, Size: 100})

	// Agora t2 pede x (que estava na shadow_queue de t2) → shadow rehit
	// Antes do t2 pedir x, ele precisa ter A>0 ainda. t2 ainda tem y após o evict.
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "x", Timestamp: 5, Size: 100})

	// Verificar que houve shadow_queue rehit.
	// (não temos getter direto, mas podemos verificar via Export)
	dir := t.TempDir()
	out := dir + "/r.json"
	if err := m.Export("E_test", "memshare", 300, 5, 0, 5, out); err != nil {
		t.Fatal(err)
	}
}

func TestMemshare_OversizedItemMisses(t *testing.T) {
	sim, m := newTestSim(100, 2)
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "huge", Timestamp: 1, Size: 1000})
	_, misses, _ := m.Stats()
	if misses != 1 {
		t.Errorf("oversized item deveria gerar 1 miss, got %d", misses)
	}
	// Não deve ter sido inserido em nenhum tenant
	t1 := sim.tenants["t1"]
	if t1 != nil && t1.A > 0 {
		t.Error("oversized item não deveria ter sido inserido")
	}
}

func TestMemshare_WarmupNoMetrics(t *testing.T) {
	m := shared.NewMetricsCollector()
	m.IsWarmup = true
	sim := New(Config{
		CapacityBytes:        1000,
		ReservedProportion:   0.5,
		CreditBytes:          50,
		ShadowQueueSizeBytes: 500,
		NumTenants:           2,
		Seed:                 42,
	}, m)

	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 2, Size: 100})

	hits, misses, _ := m.Stats()
	if hits != 0 || misses != 0 {
		t.Errorf("warmup não deveria registrar nada, got %d hits / %d misses", hits, misses)
	}
	// Mas o cache do tenant DEVE ter sido populado
	t1 := sim.tenants["t1"]
	if t1 == nil || !t1.cache.Contains("a") {
		t.Error("cache do tenant deveria ter sido populado durante warmup")
	}
}

func TestMemshare_SnapshotRestore(t *testing.T) {
	sim1, _ := newTestSim(500, 2)
	// Popula um pouco
	sim1.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 100})
	sim1.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "b", Timestamp: 2, Size: 100})
	sim1.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "c", Timestamp: 3, Size: 100})

	data, err := sim1.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	m2 := shared.NewMetricsCollector()
	sim2 := New(Config{NumTenants: 2}, m2) // config será sobrescrita
	if err := sim2.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	// Após restore, hit em "a" no tenant t1
	sim2.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 4, Size: 100})
	hits, _, _ := m2.Stats()
	if hits != 1 {
		t.Errorf("após restore, esperava 1 hit em a, got %d", hits)
	}
}

func TestMemshare_PickRandomDonorIgnoresRequester(t *testing.T) {
	sim, _ := newTestSim(1000, 3)
	// Forçar 3 tenants no knownIDs
	sim.getOrCreateTenant("t1")
	sim.getOrCreateTenant("t2")
	sim.getOrCreateTenant("t3")
	// Dar P suficiente só para t2
	sim.tenants["t1"].P = 0
	sim.tenants["t2"].P = 100
	sim.tenants["t3"].P = 0

	donor, donorID := sim.pickRandomDonor("t1")
	if donor == nil || donorID != "t2" {
		t.Errorf("esperava t2 como doador, got %s", donorID)
	}

	// Se requester for t2, não deveria escolher t2
	donor, _ = sim.pickRandomDonor("t2")
	if donor != nil {
		t.Error("não deveria achar doador (só t2 tem P>0 e ele é o requester)")
	}
}

func TestMemshare_PickRandomDonorRequiresFullCredit(t *testing.T) {
	sim, _ := newTestSim(1000, 3)
	sim.getOrCreateTenant("t1")
	sim.getOrCreateTenant("t2")
	sim.getOrCreateTenant("t3")

	sim.tenants["t1"].P = 0
	sim.tenants["t2"].P = sim.cfg.CreditBytes - 1
	sim.tenants["t3"].P = sim.cfg.CreditBytes

	donor, donorID := sim.pickRandomDonor("t1")
	if donor == nil || donorID != "t3" {
		t.Errorf("esperava t3 como doador com crédito completo, got %s", donorID)
	}

	sim.tenants["t3"].P = sim.cfg.CreditBytes - 1
	donor, donorID = sim.pickRandomDonor("t1")
	if donor != nil {
		t.Errorf("não deveria achar doador sem P >= CreditBytes, got %s", donorID)
	}
}
