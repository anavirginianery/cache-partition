// Package main implementa a caracterização do workload (Task 6).
// Programa standalone que lê o trace inteiro e produz workload_profile.json.
//
// Uso: go run ./cmd/characterize -trace=/path/to/trace.csv -out=workload_profile.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"sort"
	"time"

	"cache-simulator/shared"
)

// WorkloadProfile contém o resultado da caracterização.
// Gravado como workload_profile.json.
type WorkloadProfile struct {
	TracePath              string             `json:"trace_path"`
	TotalRequests          int64              `json:"total_requests"`
	NumTenants             int                `json:"num_tenants"`
	NumDistinctItems       int                `json:"num_distinct_items"`
	FootprintBytes         int64              `json:"footprint_bytes"`
	AvgItemSize            float64            `json:"avg_item_size"`
	RepetitionRate         float64            `json:"repetition_rate"`
	DurationSeconds        float64            `json:"duration_seconds"`
	FirstTimestamp         float64            `json:"first_timestamp"`
	LastTimestamp          float64            `json:"last_timestamp"`
	RequestsPerTenant      map[string]float64 `json:"requests_per_tenant_percentiles"`
	WallTimeSeconds        float64            `json:"wall_time_seconds"`
}

func main() {
	tracePath := flag.String("trace", "", "caminho para o trace CSV")
	outPath := flag.String("out", "workload_profile.json", "caminho de saída do JSON")
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("flag -trace é obrigatória")
	}

	start := time.Now()
	profile, err := characterize(*tracePath)
	if err != nil {
		log.Fatalf("caracterização falhou: %v", err)
	}
	profile.WallTimeSeconds = time.Since(start).Seconds()

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", *outPath, err)
	}

	fmt.Printf("\n=== Caracterização concluída em %.1fs ===\n", profile.WallTimeSeconds)
	fmt.Printf("  Trace:               %s\n", profile.TracePath)
	fmt.Printf("  Total requests:      %d\n", profile.TotalRequests)
	fmt.Printf("  Tenants ativos:      %d\n", profile.NumTenants)
	fmt.Printf("  Items distintos:     %d\n", profile.NumDistinctItems)
	fmt.Printf("  Footprint (bytes):   %d (%.2f MB)\n", profile.FootprintBytes, float64(profile.FootprintBytes)/(1024*1024))
	fmt.Printf("  Tamanho médio item:  %.2f bytes\n", profile.AvgItemSize)
	fmt.Printf("  Taxa de repetição:   %.4f\n", profile.RepetitionRate)
	fmt.Printf("  Duração:             %.1fs (%.2f h)\n", profile.DurationSeconds, profile.DurationSeconds/3600)
	fmt.Printf("  Requests/tenant p50: %.0f\n", profile.RequestsPerTenant["p50"])
	fmt.Printf("  Requests/tenant p99: %.0f\n", profile.RequestsPerTenant["p99"])
	fmt.Printf("  Saída em:            %s\n", *outPath)
}

func characterize(path string) (*WorkloadProfile, error) {
	out, errCh := shared.IterTrace(path, -1, -1)

	tenants := make(map[string]int64) // tenant → count
	products := make(map[string]int64) // product → tamanho (último visto)
	var totalReqs int64
	var repetitions int64
	var firstTs, lastTs float64
	first := true

	for req := range out {
		totalReqs++
		tenants[req.TenantID]++

		if _, seen := products[req.ProductID]; seen {
			repetitions++
		}
		products[req.ProductID] = req.Size

		if first {
			firstTs = req.Timestamp
			first = false
		}
		lastTs = req.Timestamp
	}
	if err := <-errCh; err != nil {
		return nil, err
	}

	// Footprint = soma dos tamanhos dos items distintos.
	var footprint int64
	for _, sz := range products {
		footprint += sz
	}

	avgItemSize := 0.0
	if len(products) > 0 {
		avgItemSize = float64(footprint) / float64(len(products))
	}

	repetitionRate := 0.0
	if totalReqs > 0 {
		repetitionRate = float64(repetitions) / float64(totalReqs)
	}

	// Percentis de requests/tenant.
	counts := make([]int64, 0, len(tenants))
	for _, c := range tenants {
		counts = append(counts, c)
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i] < counts[j] })

	pcts := map[string]float64{
		"p50": percentile(counts, 0.50),
		"p75": percentile(counts, 0.75),
		"p90": percentile(counts, 0.90),
		"p95": percentile(counts, 0.95),
		"p99": percentile(counts, 0.99),
	}

	return &WorkloadProfile{
		TracePath:         path,
		TotalRequests:     totalReqs,
		NumTenants:        len(tenants),
		NumDistinctItems:  len(products),
		FootprintBytes:    footprint,
		AvgItemSize:       avgItemSize,
		RepetitionRate:    repetitionRate,
		DurationSeconds:   lastTs - firstTs,
		FirstTimestamp:    firstTs,
		LastTimestamp:     lastTs,
		RequestsPerTenant: pcts,
	}, nil
}

// percentile retorna o valor no percentil p (0.0–1.0) de um slice ordenado.
func percentile(sorted []int64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if p <= 0 {
		return float64(sorted[0])
	}
	if p >= 1 {
		return float64(sorted[len(sorted)-1])
	}
	idx := p * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return float64(sorted[lo])
	}
	frac := idx - float64(lo)
	return float64(sorted[lo])*(1-frac) + float64(sorted[hi])*frac
}
