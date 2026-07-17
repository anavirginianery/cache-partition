package fairshare

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cache-simulator/shared"
)

// makeWindowsDemandFile cria um arquivo JSON de teste com curvas sintéticas.
func makeWindowsDemandFile(t *testing.T, dir string) string {
	t.Helper()
	doc := windowsDemandFile{
		WindowSeconds: 10,
		SlideSeconds:  5,
		NumWindows:    3,
		Windows: []WindowDemand{
			{
				WindowID: 0, StartS: 0, EndS: 10,
				Tenants: map[string][]HRPointJSON{
					"t1": {{0, 0}, {100, 0.5}, {200, 0.8}},
					"t2": {{0, 0}, {100, 0.3}, {500, 0.6}},
				},
			},
			{
				WindowID: 1, StartS: 5, EndS: 15,
				Tenants: map[string][]HRPointJSON{
					"t1": {{0, 0}, {100, 0.6}, {200, 0.9}},
					"t2": {{0, 0}, {200, 0.5}},
				},
			},
			{
				WindowID: 2, StartS: 10, EndS: 20,
				Tenants: map[string][]HRPointJSON{
					"t1": {{0, 0}, {100, 0.7}},
				},
			},
		},
	}
	data, err := json.Marshal(&doc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "windows_demand.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFairShare_ConfigValidation(t *testing.T) {
	m := shared.NewMetricsCollector()
	if _, err := New(Config{}, m); err == nil {
		t.Error("config sem capacidade deveria falhar")
	}
	if _, err := New(Config{CapacityBytes: 100}, m); err == nil {
		t.Error("config sem WindowsDemandPath deveria falhar")
	}
}

func TestFairShare_BasicHitMiss(t *testing.T) {
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m := shared.NewMetricsCollector()
	sim, err := New(Config{
		CapacityBytes:       1000,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 100,
		WindowsDemandPath:   p,
	}, m)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 10, Size: 50})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 11, Size: 50}) // hit
	hits, misses, _ := m.Stats()
	if hits != 1 {
		t.Errorf("hits esperados 1, got %d", hits)
	}
	if misses != 1 {
		t.Errorf("misses esperados 1, got %d", misses)
	}
}

func TestFairShare_ZeroInterference(t *testing.T) {
	// FairShare deve ter ZERO interferência cross-tenant por construção:
	// cada tenant tem cache isolado.
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m := shared.NewMetricsCollector()
	sim, err := New(Config{
		CapacityBytes:       300,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Requests de múltiplos tenants com items pequenos.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 0, Size: 50})
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "b", Timestamp: 1, Size: 50})
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "c", Timestamp: 2, Size: 50})
	sim.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "d", Timestamp: 3, Size: 50})

	// Exportar e verificar que ev pairs (se existirem) têm Victim==Causer.
	dirOut := t.TempDir()
	out := filepath.Join(dirOut, "r.json")
	if err := m.Export("E_test", "fairshare", 300, 5, 0, 5, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res shared.ScenarioResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	for _, ev := range res.EvictionPairs {
		if ev.VictimTenant != ev.CauserTenant {
			t.Errorf("FairShare deveria ter SOMENTE auto-evicções, got victim=%s causer=%s",
				ev.VictimTenant, ev.CauserTenant)
		}
	}
}

func TestFairShare_ReallocationOnWindowChange(t *testing.T) {
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m := shared.NewMetricsCollector()
	sim, err := New(Config{
		CapacityBytes:       500,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Antes de completar a primeira janela, ainda não existe demanda passada
	// completa para usar.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 2, Size: 50})
	if sim.currentWindowID != -1 {
		t.Errorf("currentWindowID esperado -1, got %d", sim.currentWindowID)
	}

	// Em ts=10, a janela 0 ([0,10)) acabou de completar.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "b", Timestamp: 10, Size: 50})
	if sim.currentWindowID != 0 {
		t.Errorf("currentWindowID esperado 0, got %d", sim.currentWindowID)
	}

	// Em ts=14, a janela 1 ([5,15)) ainda não completou; continua usando 0.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "c", Timestamp: 14, Size: 50})
	if sim.currentWindowID != 0 {
		t.Errorf("currentWindowID esperado 0, got %d", sim.currentWindowID)
	}

	// Em ts=15, a janela 1 completou.
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "d", Timestamp: 15, Size: 50})
	if sim.currentWindowID != 1 {
		t.Errorf("currentWindowID esperado 1, got %d", sim.currentWindowID)
	}
}

func TestFairShare_UnknownTenantMissesUntilNextAllocation(t *testing.T) {
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m := shared.NewMetricsCollector()
	sim, err := New(Config{
		CapacityBytes:       500,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	sim.ProcessRequest(shared.Request{TenantID: "unknown", ProductID: "a", Timestamp: 10, Size: 50})
	if _, ok := sim.caches["unknown"]; ok {
		t.Fatal("tenant sem alocação não deveria ganhar partição por fora")
	}
	_, misses, _ := m.Stats()
	if misses != 1 {
		t.Errorf("tenant desconhecido deveria gerar miss, got %d", misses)
	}
}

func TestFairShare_WarmupNoMetrics(t *testing.T) {
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m := shared.NewMetricsCollector()
	m.IsWarmup = true
	sim, err := New(Config{
		CapacityBytes:       300,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m)
	if err != nil {
		t.Fatal(err)
	}
	sim.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 1, Size: 50})
	hits, misses, _ := m.Stats()
	if hits != 0 || misses != 0 {
		t.Errorf("warmup não deveria registrar nada, got %d/%d", hits, misses)
	}
}

func TestFairShare_SnapshotRestore(t *testing.T) {
	dir := t.TempDir()
	p := makeWindowsDemandFile(t, dir)
	m1 := shared.NewMetricsCollector()
	sim1, err := New(Config{
		CapacityBytes:       300,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m1)
	if err != nil {
		t.Fatal(err)
	}
	sim1.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 10, Size: 50})
	sim1.ProcessRequest(shared.Request{TenantID: "t2", ProductID: "b", Timestamp: 11, Size: 50})

	data, err := sim1.Snapshot()
	if err != nil {
		t.Fatal(err)
	}

	m2 := shared.NewMetricsCollector()
	sim2, err := New(Config{
		CapacityBytes:       300,
		WindowSeconds:       10,
		SlideSeconds:        5,
		FloorBytesPerTenant: 50,
		WindowsDemandPath:   p,
	}, m2)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim2.Restore(data); err != nil {
		t.Fatal(err)
	}

	// Hit em a no t1 após restore
	sim2.ProcessRequest(shared.Request{TenantID: "t1", ProductID: "a", Timestamp: 12, Size: 50})
	hits, _, _ := m2.Stats()
	if hits != 1 {
		t.Errorf("após restore, esperava 1 hit em a, got %d", hits)
	}
}
