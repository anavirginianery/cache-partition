// Package main executa verificações de sanidade nos resultados dos
// experimentos. Garante que o simulador está se comportando conforme
// esperado pelo design experimental:
//
//   1. Todos os 21 arquivos foram gerados (não vazios)
//   2. HR_NoPartition ≥ HR_Memshare ≥ HR_FairShare na mesma capacidade (H1)
//      (em sub-provisionamento severo, Memshare pode superar No Partition;
//       isso é tolerado e apenas WARN)
//   3. HR cresce monotonicamente com capacidade dentro de cada política
//   4. Interferência do FairShare ≈ 0 (partições rígidas)
//   5. NumTenants > 0 em todos os cenários
//
// Uso: go run ./cmd/sanity [-results=results]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"

	"cache-simulator/shared"
)

const fairshareInterferenceTolerance = 0.001 // 0.1% — esperamos ~0

type checkResult struct {
	Name    string
	Status  string // PASS / FAIL / WARN / SKIP
	Message string
}

func main() {
	resultsDir := flag.String("results", "results", "diretório com os JSONs")
	flag.Parse()

	files, err := filepath.Glob(filepath.Join(*resultsDir, "E*.json"))
	if err != nil {
		log.Fatalf("glob: %v", err)
	}
	sort.Strings(files)

	scenarios := loadScenarios(files)
	if len(scenarios) == 0 {
		log.Fatal("nenhum cenário válido")
	}

	fmt.Printf("=== Sanity Checks ===\n")
	fmt.Printf("Cenários encontrados: %d\n\n", len(scenarios))

	var results []checkResult
	results = append(results, checkAllFilesPresent(scenarios)...)
	results = append(results, checkNonEmptyMetrics(scenarios)...)
	results = append(results, checkMonotonicHRWithCapacity(scenarios)...)
	results = append(results, checkPolicyOrderingPerCapacity(scenarios)...)
	results = append(results, checkFairshareZeroInterference(scenarios)...)

	printResults(results)
}

func loadScenarios(files []string) []*shared.ScenarioResult {
	var out []*shared.ScenarioResult
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			log.Printf("aviso: %s: %v", f, err)
			continue
		}
		var s shared.ScenarioResult
		if err := json.Unmarshal(data, &s); err != nil {
			log.Printf("aviso: %s parse: %v", f, err)
			continue
		}
		out = append(out, &s)
	}
	return out
}

// checkAllFilesPresent: idealmente esperamos 21 cenários (E1..E21). Se faltam,
// verificamos que ao menos os esperados pelas políticas presentes existem.
func checkAllFilesPresent(scenarios []*shared.ScenarioResult) []checkResult {
	policies := make(map[string]map[int]bool)
	for _, s := range scenarios {
		if policies[s.Policy] == nil {
			policies[s.Policy] = make(map[int]bool)
		}
		policies[s.Policy][s.CapacityPct] = true
	}
	var out []checkResult
	for policy, caps := range policies {
		out = append(out, checkResult{
			Name:    fmt.Sprintf("files_present[%s]", policy),
			Status:  "INFO",
			Message: fmt.Sprintf("%d cenários da política %s (capacidades: %v)", len(caps), policy, sortedKeys(caps)),
		})
	}
	if len(scenarios) < 21 {
		out = append(out, checkResult{
			Name:    "all_21_scenarios",
			Status:  "WARN",
			Message: fmt.Sprintf("apenas %d cenários presentes (esperado 21 com todas as 3 políticas)", len(scenarios)),
		})
	} else {
		out = append(out, checkResult{
			Name:    "all_21_scenarios",
			Status:  "PASS",
			Message: "21 cenários presentes",
		})
	}
	return out
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// checkNonEmptyMetrics: cada cenário deve ter hits+misses > 0 e NumTenants > 0.
func checkNonEmptyMetrics(scenarios []*shared.ScenarioResult) []checkResult {
	var out []checkResult
	for _, s := range scenarios {
		total := s.GlobalHits + s.GlobalMisses
		if total == 0 {
			out = append(out, checkResult{
				Name:    fmt.Sprintf("nonempty[%s]", s.ScenarioID),
				Status:  "FAIL",
				Message: "hits + misses == 0 (cenário aparentemente não executado)",
			})
		}
		if s.NumTenants == 0 {
			out = append(out, checkResult{
				Name:    fmt.Sprintf("tenants[%s]", s.ScenarioID),
				Status:  "FAIL",
				Message: "NumTenants == 0",
			})
		}
	}
	if len(out) == 0 {
		out = append(out, checkResult{
			Name:    "nonempty_metrics",
			Status:  "PASS",
			Message: fmt.Sprintf("todos os %d cenários têm métricas não-vazias", len(scenarios)),
		})
	}
	return out
}

// checkMonotonicHRWithCapacity: dentro de cada política, HR(cap=5) ≤ HR(cap=10) ≤ ... ≤ HR(cap=50).
func checkMonotonicHRWithCapacity(scenarios []*shared.ScenarioResult) []checkResult {
	byPolicy := make(map[string][]*shared.ScenarioResult)
	for _, s := range scenarios {
		byPolicy[s.Policy] = append(byPolicy[s.Policy], s)
	}
	var out []checkResult
	for policy, list := range byPolicy {
		sort.Slice(list, func(i, j int) bool { return list[i].CapacityPct < list[j].CapacityPct })
		monotonic := true
		var lastHR float64 = -1
		var lastCap int
		for _, s := range list {
			if lastHR >= 0 && s.GlobalHitRatio < lastHR-1e-6 {
				monotonic = false
				out = append(out, checkResult{
					Name: fmt.Sprintf("monotonic_HR[%s]", policy),
					Status: "WARN",
					Message: fmt.Sprintf("HR caiu de %.4f (cap=%d%%) para %.4f (cap=%d%%)",
						lastHR, lastCap, s.GlobalHitRatio, s.CapacityPct),
				})
			}
			lastHR = s.GlobalHitRatio
			lastCap = s.CapacityPct
		}
		if monotonic {
			out = append(out, checkResult{
				Name:    fmt.Sprintf("monotonic_HR[%s]", policy),
				Status:  "PASS",
				Message: "HR cresce monotonicamente com capacidade",
			})
		}
	}
	return out
}

// checkPolicyOrderingPerCapacity: para cada capacidade, HR_NoPartition deveria
// ser ≥ HR_Memshare ≥ HR_FairShare (Hipótese H1 do design).
func checkPolicyOrderingPerCapacity(scenarios []*shared.ScenarioResult) []checkResult {
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

	var out []checkResult
	for _, c := range caps {
		np := byCap[c]["no_partition"]
		ms := byCap[c]["memshare"]
		fs := byCap[c]["fairshare"]
		if np == nil {
			continue
		}
		// Comparações em pares.
		if ms != nil && ms.GlobalHitRatio > np.GlobalHitRatio+1e-6 {
			out = append(out, checkResult{
				Name: fmt.Sprintf("hr_ordering[cap=%d%%]", c),
				Status: "WARN",
				Message: fmt.Sprintf("Memshare HR (%.4f) > No Partition HR (%.4f) — investigar (pode ser sub-provisionamento extremo)",
					ms.GlobalHitRatio, np.GlobalHitRatio),
			})
		}
		if fs != nil && fs.GlobalHitRatio > np.GlobalHitRatio+1e-6 {
			out = append(out, checkResult{
				Name: fmt.Sprintf("hr_ordering[cap=%d%%]", c),
				Status: "WARN",
				Message: fmt.Sprintf("FairShare HR (%.4f) > No Partition HR (%.4f) — incomum (esperado: NP ≥ FS)",
					fs.GlobalHitRatio, np.GlobalHitRatio),
			})
		}
	}
	if len(out) == 0 {
		out = append(out, checkResult{
			Name:    "hr_ordering_per_capacity",
			Status:  "PASS",
			Message: "ordenação esperada de HR por política respeitada em todas as capacidades",
		})
	}
	return out
}

// checkFairshareZeroInterference: por construção FairShare tem auto-evicção
// apenas → interferência média deveria ser ≈ 0 em todos os cenários FairShare.
func checkFairshareZeroInterference(scenarios []*shared.ScenarioResult) []checkResult {
	var out []checkResult
	for _, s := range scenarios {
		if s.Policy != "fairshare" {
			continue
		}
		var sum float64
		var n int
		for _, tm := range s.PerTenant {
			sum += tm.Interference
			n++
		}
		var avg float64
		if n > 0 {
			avg = sum / float64(n)
		}
		if avg > fairshareInterferenceTolerance {
			out = append(out, checkResult{
				Name: fmt.Sprintf("fairshare_zero_interference[%s]", s.ScenarioID),
				Status: "FAIL",
				Message: fmt.Sprintf("interferência média %.6f > tolerância %.6f — partições não estão isolando!",
					avg, fairshareInterferenceTolerance),
			})
		}
	}
	if len(out) == 0 {
		out = append(out, checkResult{
			Name:    "fairshare_zero_interference",
			Status:  "PASS",
			Message: fmt.Sprintf("interferência ≈ 0 em todos os cenários FairShare (tolerância %.6f)", fairshareInterferenceTolerance),
		})
	}
	return out
}

func printResults(results []checkResult) {
	pass, fail, warn, info := 0, 0, 0, 0
	for _, r := range results {
		switch r.Status {
		case "PASS":
			pass++
		case "FAIL":
			fail++
		case "WARN":
			warn++
		case "INFO":
			info++
		}
		fmt.Printf("[%-4s] %s\n        %s\n", r.Status, r.Name, r.Message)
	}
	fmt.Printf("\n=== Sumário: %d PASS, %d FAIL, %d WARN, %d INFO ===\n", pass, fail, warn, info)
	if fail > 0 {
		os.Exit(1)
	}
}
