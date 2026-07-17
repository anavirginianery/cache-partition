// Package main pré-computa as curvas HR(s) por janela e por tenant para
// a política FairShare Max-Min.
//
// Estratégia:
//
//   - O FairShare usa janela deslizante (default: window=600s, slide=300s).
//   - Em 1h de trace, com slide=300s e window=600s, há 11 janelas:
//     [0,600), [300,900), [600,1200), ..., [3000,3600).
//   - Cada request com timestamp ts pertence às janelas i tais que
//     i*slide ≤ ts < i*slide + window. Para slide=300, window=600:
//     i = floor(ts/300) - 0 ou 1 (cada request alimenta até 2 janelas).
//   - Mantemos um Mattson por (tenant, janela_ativa). Quando uma janela
//     "fecha" (avançamos para fora dela), extraímos sua curva HR e
//     descartamos o Mattson dessa janela.
//
// Saída: windows_demand.json
//
// Esta pré-computação roda UMA VEZ no trace inteiro. Os 7 cenários FairShare
// (E8-E14) reutilizam o mesmo arquivo, eliminando a recomputação por
// capacidade.
//
// Uso:
//
//	go run ./cmd/precompute_demand \
//	  -trace=/path/to/trace.csv \
//	  -window=600 -slide=300 \
//	  -duration=3600 \
//	  -out=windows_demand.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cache-simulator/fairshare"
	"cache-simulator/shared"
)

// WindowDemand contém as curvas HR de todos os tenants ativos em uma janela.
type WindowDemand struct {
	WindowID int                     `json:"window_id"`
	StartS   float64                 `json:"start_s"`
	EndS     float64                 `json:"end_s"`
	Tenants  map[string][]HRPointOut `json:"tenants"`
}

// HRPointOut é a versão serializável de fairshare.HRPoint.
type HRPointOut struct {
	SizeBytes int64   `json:"size_bytes"`
	HR        float64 `json:"hr"`
}

// WindowsDemand é o documento completo gravado em windows_demand.json.
type WindowsDemand struct {
	TracePath        string         `json:"trace_path"`
	WindowSeconds    int            `json:"window_seconds"`
	SlideSeconds     int            `json:"slide_seconds"`
	DurationSeconds  int            `json:"duration_seconds"`
	NumWindows       int            `json:"num_windows"`
	NumTenantsTotal  int            `json:"num_tenants_total"`
	TotalRequests    int64          `json:"total_requests"`
	WallTimeSeconds  float64        `json:"wall_time_seconds"`
	Windows          []WindowDemand `json:"windows"`
}

func main() {
	tracePath := flag.String("trace", "", "caminho do trace CSV")
	windowS := flag.Int("window", 600, "tamanho da janela em segundos")
	slideS := flag.Int("slide", 300, "intervalo de slide em segundos")
	durationS := flag.Int("duration", 3600, "duração total do trace em segundos")
	outPath := flag.String("out", "windows_demand.json", "caminho de saída do JSON")
	flag.Parse()

	if *tracePath == "" {
		log.Fatal("flag -trace é obrigatória")
	}
	if *slideS <= 0 || *windowS <= 0 {
		log.Fatal("window e slide devem ser > 0")
	}
	if *windowS%*slideS != 0 {
		log.Fatalf("window (%d) deve ser múltiplo de slide (%d) para esta implementação", *windowS, *slideS)
	}

	// Determinar quantas janelas existem no trace.
	// Janela i começa em i*slide e termina em i*slide + window.
	// A última janela útil é tal que i*slide + window ≤ duration + slide
	// (permitindo última janela parcial).
	numWindows := (*durationS - *windowS) / *slideS + 1
	if numWindows < 1 {
		log.Fatalf("duração %d insuficiente para janela %d", *durationS, *windowS)
	}

	fmt.Printf("=== Pré-computação de demanda (FairShare) ===\n")
	fmt.Printf("Trace:          %s\n", *tracePath)
	fmt.Printf("Janela:         %ds (slide %ds)\n", *windowS, *slideS)
	fmt.Printf("Duração:        %ds\n", *durationS)
	fmt.Printf("Janelas:        %d\n", numWindows)
	fmt.Println()

	start := time.Now()

	// mattson[windowID][tenantID] → instância
	// Otimização: criar e descartar conforme janelas abrem/fecham.
	mattsons := make([]map[string]*fairshare.Mattson, numWindows)
	for i := range mattsons {
		mattsons[i] = make(map[string]*fairshare.Mattson)
	}

	out, errCh := shared.IterTrace(*tracePath, -1, -1)
	var totalReqs int64
	tenantsSet := make(map[string]struct{})

	for req := range out {
		totalReqs++
		tenantsSet[req.TenantID] = struct{}{}

		// Determinar janelas ativas para este timestamp.
		// Uma request em ts pertence às janelas i tais que
		//   i*slide ≤ ts < i*slide + window
		// equivalente:
		//   ts/slide - window/slide < i ≤ ts/slide
		//   max(0, floor(ts/slide) - (window/slide - 1)) ≤ i ≤ floor(ts/slide)
		k := int(req.Timestamp / float64(*slideS))
		w := *windowS / *slideS // # de janelas que se sobrepõem em qualquer timestamp
		startI := k - (w - 1)
		if startI < 0 {
			startI = 0
		}
		endI := k
		if endI >= numWindows {
			endI = numWindows - 1
		}
		for i := startI; i <= endI; i++ {
			// Confirmar que a janela [i*slide, i*slide+window) contém ts.
			lo := float64(i * *slideS)
			hi := lo + float64(*windowS)
			if req.Timestamp < lo || req.Timestamp >= hi {
				continue
			}
			m, ok := mattsons[i][req.TenantID]
			if !ok {
				m = fairshare.NewMattson()
				mattsons[i][req.TenantID] = m
			}
			m.Record(req.ProductID, req.Size)
		}
	}
	if err := <-errCh; err != nil {
		log.Fatalf("trace: %v", err)
	}

	// Construir o documento de saída.
	doc := WindowsDemand{
		TracePath:       *tracePath,
		WindowSeconds:   *windowS,
		SlideSeconds:    *slideS,
		DurationSeconds: *durationS,
		NumWindows:      numWindows,
		NumTenantsTotal: len(tenantsSet),
		TotalRequests:   totalReqs,
		Windows:         make([]WindowDemand, 0, numWindows),
	}
	for i := 0; i < numWindows; i++ {
		wd := WindowDemand{
			WindowID: i,
			StartS:   float64(i * *slideS),
			EndS:     float64(i**slideS + *windowS),
			Tenants:  make(map[string][]HRPointOut, len(mattsons[i])),
		}
		for tenant, m := range mattsons[i] {
			curve := m.HRCurve()
			pts := make([]HRPointOut, len(curve))
			for j, p := range curve {
				pts[j] = HRPointOut{SizeBytes: p.SizeBytes, HR: p.HR}
			}
			wd.Tenants[tenant] = pts
		}
		doc.Windows = append(doc.Windows, wd)
		// Liberar memória dos Mattsons da janela já processada.
		mattsons[i] = nil
	}
	doc.WallTimeSeconds = time.Since(start).Seconds()

	data, err := json.Marshal(&doc)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		log.Fatalf("write %s: %v", *outPath, err)
	}

	fmt.Printf("\n=== Concluído em %.1fs ===\n", doc.WallTimeSeconds)
	fmt.Printf("  Tenants únicos:    %d\n", doc.NumTenantsTotal)
	fmt.Printf("  Total requests:    %d\n", doc.TotalRequests)
	fmt.Printf("  Janelas geradas:   %d\n", len(doc.Windows))
	fmt.Printf("  Saída em:          %s (%d bytes)\n", *outPath, len(data))
}
