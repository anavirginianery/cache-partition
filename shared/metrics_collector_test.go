package shared

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMetrics_WarmupNoOp(t *testing.T) {
	m := NewMetricsCollector()
	m.IsWarmup = true
	m.RecordHit("t1", "p1", 0)
	m.RecordMiss("t1", "p2", 0, 100)
	m.RecordEviction("t1", "t2", "p3")
	m.RecordShadowQueueRehit("t1", "p4")

	hits, misses, hr := m.Stats()
	if hits != 0 || misses != 0 || hr != 0 {
		t.Errorf("warmup deveria não registrar nada, got hits=%d misses=%d hr=%v", hits, misses, hr)
	}
}

func TestMetrics_HitMissAggregation(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordHit("t1", "p1", 0)
	m.RecordHit("t1", "p2", 0)
	m.RecordMiss("t2", "p3", 0, 100)
	m.RecordHit("t2", "p4", 0)

	hits, misses, hr := m.Stats()
	if hits != 3 {
		t.Errorf("hits esperados 3, got %d", hits)
	}
	if misses != 1 {
		t.Errorf("misses esperados 1, got %d", misses)
	}
	if hr != 0.75 {
		t.Errorf("hr esperado 0.75, got %v", hr)
	}
}

func TestMetrics_CrossTenantEvictionAndInterference(t *testing.T) {
	m := NewMetricsCollector()
	// t2 evicta produto p1 que pertence a t1
	m.RecordEviction("t1", "t2", "p1")

	// Agora t1 pede p1 de novo → miss + interferência
	m.RecordMiss("t1", "p1", 0, 100)

	if m.interferenceTotal["t1"] != 1 {
		t.Errorf("interferenceTotal[t1] deveria ser 1, got %d", m.interferenceTotal["t1"])
	}
	if m.interferenceHits["t1"] != 1 {
		t.Errorf("interferenceHits[t1] deveria ser 1, got %d", m.interferenceHits["t1"])
	}
}

func TestMetrics_SelfEvictionNotInterference(t *testing.T) {
	m := NewMetricsCollector()
	// t1 evicta seu próprio produto (auto-evicção, ex: FairShare)
	m.RecordEviction("t1", "t1", "p1")
	m.RecordMiss("t1", "p1", 0, 100)

	if m.interferenceTotal["t1"] != 0 {
		t.Errorf("auto-evicção não deveria contar como interferência, got %d", m.interferenceTotal["t1"])
	}
}

func TestMetrics_HitClearsEvictedTracker(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordEviction("t1", "t2", "p1")
	// p1 foi readicionado por algum motivo (não relevante aqui)
	m.RecordHit("t1", "p1", 0)
	// agora um miss em p1 NÃO deveria contar como interferência
	// (porque o hit limpou o estado evictado)
	m.RecordMiss("t1", "p1", 0, 100)
	if m.interferenceTotal["t1"] != 1 {
		t.Errorf("evicção cross-tenant deveria contar no denominador, got %d", m.interferenceTotal["t1"])
	}
	if m.interferenceHits["t1"] != 0 {
		t.Errorf("hit deveria ter limpado tracker, mas retorno foi registrado como interferência")
	}
}

func TestMetrics_InterferenceRatioUsesCrossTenantEvictionsAsDenominator(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordMiss("t1", "seed", 0, 100)

	m.RecordEviction("t1", "t2", "p1")
	m.RecordEviction("t1", "t2", "p2")
	m.RecordMiss("t1", "p1", 0, 100)

	dir := t.TempDir()
	out := filepath.Join(dir, "result.json")
	if err := m.Export("E1", "memshare", 1024, 5, 0, 1, out); err != nil {
		t.Fatalf("export: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res ScenarioResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	got := res.PerTenant["t1"].Interference
	if got != 0.5 {
		t.Errorf("interference esperado 0.5 (1 retorno / 2 evicções cross-tenant), got %v", got)
	}
	if res.PerTenant["t1"].InterferenceReturns != 1 {
		t.Errorf("interference_returns esperado 1, got %d", res.PerTenant["t1"].InterferenceReturns)
	}
	if res.PerTenant["t1"].CrossTenantEvictions != 2 {
		t.Errorf("cross_tenant_evictions esperado 2, got %d", res.PerTenant["t1"].CrossTenantEvictions)
	}
	if res.InterferenceReturns != 1 {
		t.Errorf("global interference_returns esperado 1, got %d", res.InterferenceReturns)
	}
	if res.CrossTenantEvictions != 2 {
		t.Errorf("global cross_tenant_evictions esperado 2, got %d", res.CrossTenantEvictions)
	}
	if res.SystemInterference != 0.5 {
		t.Errorf("system_interference esperado 0.5, got %v", res.SystemInterference)
	}
}

func TestMetrics_ExportIncludesTenantWithOnlyCrossTenantEviction(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordEviction("t1", "t2", "p1")

	dir := t.TempDir()
	out := filepath.Join(dir, "result.json")
	if err := m.Export("E1", "memshare", 1024, 5, 0, 1, out); err != nil {
		t.Fatalf("export: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res ScenarioResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}
	if _, ok := res.PerTenant["t1"]; !ok {
		t.Fatal("tenant que sofreu evicção cross-tenant deveria aparecer no per_tenant")
	}
	if res.PerTenant["t1"].Interference != 0 {
		t.Errorf("interference esperado 0 quando item removido ainda não retornou, got %v", res.PerTenant["t1"].Interference)
	}
	if res.PerTenant["t1"].CrossTenantEvictions != 1 {
		t.Errorf("cross_tenant_evictions esperado 1, got %d", res.PerTenant["t1"].CrossTenantEvictions)
	}
	if res.PerTenant["t1"].InterferenceReturns != 0 {
		t.Errorf("interference_returns esperado 0, got %d", res.PerTenant["t1"].InterferenceReturns)
	}
}

func TestMetrics_ExportJSONSchema(t *testing.T) {
	m := NewMetricsCollector()
	m.RecordHit("t1", "p1", 0)
	m.RecordMiss("t1", "p2", 0, 100)
	m.RecordHit("t2", "p3", 0)
	m.RecordEviction("t2", "t1", "p99")

	dir := t.TempDir()
	out := filepath.Join(dir, "result.json")
	err := m.Export("E1", "no_partition", 1024*1024, 5, 600, 3000, out)
	if err != nil {
		t.Fatalf("export: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var res ScenarioResult
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("parse JSON: %v", err)
	}

	if res.ScenarioID != "E1" {
		t.Errorf("scenario_id errado: %s", res.ScenarioID)
	}
	if res.Policy != "no_partition" {
		t.Errorf("policy errada: %s", res.Policy)
	}
	if res.GlobalHits != 2 || res.GlobalMisses != 1 {
		t.Errorf("totais errados: hits=%d misses=%d", res.GlobalHits, res.GlobalMisses)
	}
	if res.NumTenants != 2 {
		t.Errorf("num_tenants errado: %d", res.NumTenants)
	}
	if len(res.PerTenant) != 2 {
		t.Errorf("perTenant deveria ter 2, got %d", len(res.PerTenant))
	}
	if len(res.EvictionPairs) != 0 {
		t.Errorf("eviction_pairs não deveria ser exportado por padrão, got %d", len(res.EvictionPairs))
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse raw JSON: %v", err)
	}
	if _, ok := raw["eviction_pairs"]; ok {
		t.Error("campo eviction_pairs não deveria aparecer no JSON principal")
	}
	if res.CrossTenantEvictions != 1 {
		t.Errorf("cross_tenant_evictions global esperado 1, got %d", res.CrossTenantEvictions)
	}
}
