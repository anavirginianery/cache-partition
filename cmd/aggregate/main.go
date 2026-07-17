// Package main consolida os arquivos results/E*.json em CSVs para análise.
//
// Saídas geradas em analysis/:
//
//   - agg_global.csv       — uma linha por cenário com métricas globais
//   - agg_per_tenant.csv   — uma linha por (cenário, tenant)
//   - agg_comparisons.csv  — para cada (capacidade, política != baseline),
//     calcula % tenants melhorados e % com interferência reduzida vs.
//     baseline No Partition na mesma capacidade.
//
// Uso: go run ./cmd/aggregate [-results=results] [-out=analysis]
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"cache-simulator/shared"
)

func main() {
	resultsDir := flag.String("results", "results", "diretório com os JSONs de cenário")
	outDir := flag.String("out", "analysis", "diretório de saída para os CSVs")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("criar %s: %v", *outDir, err)
	}

	files, err := filepath.Glob(filepath.Join(*resultsDir, "E*.json"))
	if err != nil || len(files) == 0 {
		log.Fatalf("nenhum arquivo encontrado em %s", *resultsDir)
	}
	sort.Strings(files)

	scenarios := make([]*shared.ScenarioResult, 0, len(files))
	for _, f := range files {
		s, err := loadScenario(f)
		if err != nil {
			log.Printf("aviso: pular %s: %v", f, err)
			continue
		}
		scenarios = append(scenarios, s)
	}
	if len(scenarios) == 0 {
		log.Fatal("nenhum cenário válido carregado")
	}

	fmt.Printf("Cenários carregados: %d\n", len(scenarios))

	if err := writeGlobalCSV(filepath.Join(*outDir, "agg_global.csv"), scenarios); err != nil {
		log.Fatalf("agg_global: %v", err)
	}
	if err := writePerTenantCSV(filepath.Join(*outDir, "agg_per_tenant.csv"), scenarios); err != nil {
		log.Fatalf("agg_per_tenant: %v", err)
	}
	if err := writeComparisonsCSV(filepath.Join(*outDir, "agg_comparisons.csv"), scenarios); err != nil {
		log.Fatalf("agg_comparisons: %v", err)
	}

	fmt.Printf("\nSaídas:\n")
	fmt.Printf("  %s\n", filepath.Join(*outDir, "agg_global.csv"))
	fmt.Printf("  %s\n", filepath.Join(*outDir, "agg_per_tenant.csv"))
	fmt.Printf("  %s\n", filepath.Join(*outDir, "agg_comparisons.csv"))
}

func loadScenario(path string) (*shared.ScenarioResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler: %w", err)
	}
	var s shared.ScenarioResult
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return &s, nil
}

// writeGlobalCSV: 1 linha por cenário.
func writeGlobalCSV(path string, scenarios []*shared.ScenarioResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{
		"scenario_id", "policy", "capacity_pct", "capacity_bytes",
		"global_hits", "global_misses", "global_hit_ratio",
		"system_interference", "interference_returns", "cross_tenant_evictions",
		"num_tenants", "wall_time_seconds",
	}); err != nil {
		return err
	}
	for _, s := range scenarios {
		row := []string{
			s.ScenarioID,
			s.Policy,
			strconv.Itoa(s.CapacityPct),
			strconv.FormatInt(s.CapacityBytes, 10),
			strconv.FormatInt(s.GlobalHits, 10),
			strconv.FormatInt(s.GlobalMisses, 10),
			strconv.FormatFloat(s.GlobalHitRatio, 'f', 6, 64),
			strconv.FormatFloat(s.SystemInterference, 'f', 6, 64),
			strconv.FormatInt(s.InterferenceReturns, 10),
			strconv.FormatInt(s.CrossTenantEvictions, 10),
			strconv.Itoa(s.NumTenants),
			strconv.FormatFloat(s.WallTimeSeconds, 'f', 3, 64),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// writePerTenantCSV: 1 linha por (cenário, tenant).
func writePerTenantCSV(path string, scenarios []*shared.ScenarioResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{
		"scenario_id", "policy", "capacity_pct", "tenant",
		"hits", "misses", "hit_ratio", "interference",
		"interference_returns", "cross_tenant_evictions", "shadow_rehits",
	}); err != nil {
		return err
	}
	for _, s := range scenarios {
		// Ordenar tenants para determinismo.
		ids := make([]string, 0, len(s.PerTenant))
		for id := range s.PerTenant {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			tm := s.PerTenant[id]
			row := []string{
				s.ScenarioID,
				s.Policy,
				strconv.Itoa(s.CapacityPct),
				id,
				strconv.FormatInt(tm.Hits, 10),
				strconv.FormatInt(tm.Misses, 10),
				strconv.FormatFloat(tm.HitRatio, 'f', 6, 64),
				strconv.FormatFloat(tm.Interference, 'f', 6, 64),
				strconv.FormatInt(tm.InterferenceReturns, 10),
				strconv.FormatInt(tm.CrossTenantEvictions, 10),
				strconv.FormatInt(tm.ShadowQueueRehits, 10),
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// writeComparisonsCSV: para cada capacidade, compara cada política à baseline
// (no_partition na mesma capacidade) calculando % tenants melhorados e %
// com interferência reduzida.
func writeComparisonsCSV(path string, scenarios []*shared.ScenarioResult) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write([]string{
		"capacity_pct", "policy",
		"global_hr_baseline", "global_hr_policy", "delta_hr_pp",
		"pct_tenants_improved_hr",
		"pct_tenants_reduced_interference",
		"avg_interference_baseline", "avg_interference_policy",
		"num_tenants_compared",
	}); err != nil {
		return err
	}

	// Indexar por (capacity_pct, policy).
	byCap := make(map[int]map[string]*shared.ScenarioResult)
	for _, s := range scenarios {
		if byCap[s.CapacityPct] == nil {
			byCap[s.CapacityPct] = make(map[string]*shared.ScenarioResult)
		}
		byCap[s.CapacityPct][s.Policy] = s
	}

	caps := make([]int, 0, len(byCap))
	for c := range byCap {
		caps = append(caps, c)
	}
	sort.Ints(caps)

	for _, c := range caps {
		base := byCap[c]["no_partition"]
		if base == nil {
			continue // sem baseline para essa capacidade
		}
		for _, policy := range []string{"memshare", "fairshare"} {
			s := byCap[c][policy]
			if s == nil {
				continue
			}
			cmp := compareScenarios(base, s)
			row := []string{
				strconv.Itoa(c),
				policy,
				strconv.FormatFloat(base.GlobalHitRatio, 'f', 6, 64),
				strconv.FormatFloat(s.GlobalHitRatio, 'f', 6, 64),
				strconv.FormatFloat((s.GlobalHitRatio-base.GlobalHitRatio)*100, 'f', 4, 64),
				strconv.FormatFloat(cmp.PctImprovedHR*100, 'f', 4, 64),
				strconv.FormatFloat(cmp.PctReducedInterference*100, 'f', 4, 64),
				strconv.FormatFloat(cmp.AvgInterferenceBaseline, 'f', 6, 64),
				strconv.FormatFloat(cmp.AvgInterferencePolicy, 'f', 6, 64),
				strconv.Itoa(cmp.NumCompared),
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	return nil
}

type comparison struct {
	PctImprovedHR           float64
	PctReducedInterference  float64
	AvgInterferenceBaseline float64
	AvgInterferencePolicy   float64
	NumCompared             int
}

// compareScenarios calcula métricas de comparação entre baseline e política.
// Considera apenas tenants presentes em AMBOS os cenários (interseção).
func compareScenarios(base, policy *shared.ScenarioResult) comparison {
	var improved, reduced int
	var sumIntfBase, sumIntfPolicy float64
	var n int

	for tenant, btm := range base.PerTenant {
		ptm, ok := policy.PerTenant[tenant]
		if !ok {
			continue
		}
		n++
		if ptm.HitRatio > btm.HitRatio {
			improved++
		}
		if ptm.Interference < btm.Interference {
			reduced++
		}
		sumIntfBase += btm.Interference
		sumIntfPolicy += ptm.Interference
	}
	c := comparison{NumCompared: n}
	if n > 0 {
		c.PctImprovedHR = float64(improved) / float64(n)
		c.PctReducedInterference = float64(reduced) / float64(n)
		c.AvgInterferenceBaseline = sumIntfBase / float64(n)
		c.AvgInterferencePolicy = sumIntfPolicy / float64(n)
	}
	return c
}
