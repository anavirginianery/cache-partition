package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleConfig = `# comentário inicial
trace_path: /tmp/trace.csv
workload_profile: workload_profile.json
results_dir: results
snapshots_dir: snapshots

warmup_seconds: 600       # 10 min
total_seconds: 3600

capacities_pct:
  - 5
  - 10
  - 50

policies_enabled:
  - no_partition
  - memshare

memshare_reserved_proportion: 0.5
memshare_credit_bytes: auto
memshare_shadow_queue_k: 2
memshare_compact_interval: 100000
memshare_seed: 42
`

func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad_Success(t *testing.T) {
	p := writeTempConfig(t, sampleConfig)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TracePath != "/tmp/trace.csv" {
		t.Errorf("trace_path errado: %s", cfg.TracePath)
	}
	if cfg.WarmupSeconds != 600 {
		t.Errorf("warmup_seconds errado: %d", cfg.WarmupSeconds)
	}
	if cfg.TotalSeconds != 3600 {
		t.Errorf("total_seconds errado: %d", cfg.TotalSeconds)
	}
	if len(cfg.CapacitiesPct) != 3 {
		t.Errorf("capacities_pct deveria ter 3 itens, got %d: %v", len(cfg.CapacitiesPct), cfg.CapacitiesPct)
	}
	if cfg.CapacitiesPct[0] != 5 || cfg.CapacitiesPct[2] != 50 {
		t.Errorf("capacities_pct errado: %v", cfg.CapacitiesPct)
	}
	if len(cfg.PoliciesEnabled) != 2 {
		t.Errorf("policies_enabled errado: %v", cfg.PoliciesEnabled)
	}
	if cfg.MemshareReservedProportion != 0.5 {
		t.Errorf("memshare_reserved_proportion errado: %v", cfg.MemshareReservedProportion)
	}
	if cfg.MemshareCreditBytes != 0 {
		t.Errorf("memshare_credit_bytes 'auto' deveria virar 0, got %d", cfg.MemshareCreditBytes)
	}
	if cfg.MemshareShadowQueueK != 2.0 {
		t.Errorf("memshare_shadow_queue_k errado: %v", cfg.MemshareShadowQueueK)
	}
	if cfg.MemshareCompactInterval != 100000 {
		t.Errorf("memshare_compact_interval errado: %d", cfg.MemshareCompactInterval)
	}
	if cfg.MemshareSeed != 42 {
		t.Errorf("memshare_seed errado: %d", cfg.MemshareSeed)
	}
}

func TestLoad_CreditBytesExplicit(t *testing.T) {
	cfgStr := `trace_path: /tmp/x.csv
warmup_seconds: 600
total_seconds: 3600
capacities_pct:
  - 5
policies_enabled:
  - memshare
memshare_credit_bytes: 65536
`
	p := writeTempConfig(t, cfgStr)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MemshareCreditBytes != 65536 {
		t.Errorf("memshare_credit_bytes esperado 65536, got %d", cfg.MemshareCreditBytes)
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	cfgStr := `trace_path: /tmp/x.csv
warmup_seconds: 600
total_seconds: 3600
capacities_pct:
  - 5
policies_enabled:
  - memshare
`
	p := writeTempConfig(t, cfgStr)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.MemshareReservedProportion != 0.5 {
		t.Error("default reserved_proportion não aplicado")
	}
	if cfg.MemshareShadowQueueK != 2.0 {
		t.Error("default shadow_queue_k não aplicado")
	}
}

func TestLoad_MissingTracePath(t *testing.T) {
	bad := `warmup_seconds: 600
total_seconds: 3600
capacities_pct:
  - 5
policies_enabled:
  - no_partition
`
	p := writeTempConfig(t, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("esperava erro de trace_path obrigatório")
	}
}

func TestLoad_BadPolicy(t *testing.T) {
	bad := `trace_path: /tmp/x.csv
warmup_seconds: 600
total_seconds: 3600
capacities_pct:
  - 5
policies_enabled:
  - politica_invalida
`
	p := writeTempConfig(t, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("esperava erro de política inválida")
	}
}

func TestLoad_TotalLessThanWarmup(t *testing.T) {
	bad := `trace_path: /tmp/x.csv
warmup_seconds: 3600
total_seconds: 600
capacities_pct:
  - 5
policies_enabled:
  - no_partition
`
	p := writeTempConfig(t, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("esperava erro de total < warmup")
	}
}

func TestLoad_BadReservedProportion(t *testing.T) {
	bad := `trace_path: /tmp/x.csv
warmup_seconds: 600
total_seconds: 3600
capacities_pct:
  - 5
policies_enabled:
  - memshare
memshare_reserved_proportion: 1.5
`
	p := writeTempConfig(t, bad)
	if _, err := Load(p); err == nil {
		t.Fatal("esperava erro de reserved_proportion fora de [0,1]")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	if _, err := Load("/path/que/nao/existe.yaml"); err == nil {
		t.Fatal("esperava erro de arquivo inexistente")
	}
}
