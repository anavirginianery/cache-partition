// Package main é o orquestrador dos experimentos do simulador de cache.
//
// Fluxo:
//  1. Carrega config/experiment.yaml
//  2. Carrega workload_profile.json (gerado por cmd/characterize)
//  3. Para cada (política, capacidade) habilitada:
//     a. Calcula capacity_bytes = footprint × pct/100
//     b. Constrói o simulador
//     c. Fase warmup [0, warmup_seconds): metrics.IsWarmup = true
//     d. Salva snapshot pós-warmup (Task 8 — acelera re-runs do mesmo cenário)
//     e. Fase measurement [warmup_seconds, total_seconds): metrics.IsWarmup = false
//     f. Exporta results/E{N}.json
//
// Snapshots são opcionais: se já existir um snapshot do mesmo cenário,
// o orquestrador pula o warmup e restaura o estado salvo.
//
// Uso: go run . [-config=config/experiment.yaml] [-snapshots=false]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"cache-simulator/config"
	"cache-simulator/fairshare"
	"cache-simulator/memshare"
	"cache-simulator/partition"
	"cache-simulator/shared"
)

// workloadProfile é um subset do schema gerado por cmd/characterize.
type workloadProfile struct {
	FootprintBytes int64   `json:"footprint_bytes"`
	NumTenants     int     `json:"num_tenants"`
	TotalRequests  int64   `json:"total_requests"`
	AvgItemSize    float64 `json:"avg_item_size"`
}

// simulator é a interface comum que cada política deve implementar para
// ser orquestrada. Permite tratar No Partition, Memshare e FairShare de
// forma uniforme.
type simulator interface {
	ProcessRequest(req shared.Request)
	Snapshot() ([]byte, error)
	Restore([]byte) error
}

func main() {
	cfgPath := flag.String("config", "config/experiment.yaml", "caminho do config")
	useSnapshots := flag.Bool("snapshots", true, "usar snapshots pós-warmup (acelera re-runs)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	profile, err := loadProfile(cfg.WorkloadProfile)
	if err != nil {
		log.Fatalf("workload profile: %v", err)
	}

	if err := os.MkdirAll(cfg.ResultsDir, 0o755); err != nil {
		log.Fatalf("criar results_dir: %v", err)
	}
	if err := os.MkdirAll(cfg.SnapshotsDir, 0o755); err != nil {
		log.Fatalf("criar snapshots_dir: %v", err)
	}

	measurementSeconds := cfg.TotalSeconds - cfg.WarmupSeconds
	fmt.Printf("=== Orquestrador de Experimentos ===\n")
	fmt.Printf("Trace:          %s\n", cfg.TracePath)
	fmt.Printf("Footprint:      %d bytes (%.2f MB)\n", profile.FootprintBytes, float64(profile.FootprintBytes)/(1024*1024))
	fmt.Printf("Tenants:        %d\n", profile.NumTenants)
	fmt.Printf("Total reqs:     %d\n", profile.TotalRequests)
	fmt.Printf("Avg item size:  %.1f bytes\n", profile.AvgItemSize)
	fmt.Printf("Warmup:         %ds\n", cfg.WarmupSeconds)
	fmt.Printf("Measurement:    %ds\n", measurementSeconds)
	fmt.Printf("Capacidades:    %v\n", cfg.CapacitiesPct)
	fmt.Printf("Políticas:      %v\n", cfg.PoliciesEnabled)
	fmt.Printf("Snapshots:      %v\n", *useSnapshots)
	fmt.Printf("Cenários:       %d total\n\n", len(cfg.CapacitiesPct)*len(cfg.PoliciesEnabled))

	totalStart := time.Now()
	for _, policy := range cfg.PoliciesEnabled {
		for _, capPct := range cfg.CapacitiesPct {
			capBytes := (profile.FootprintBytes * int64(capPct)) / 100
			scenarioID := scenarioID(policy, capPct, cfg.CapacitiesPct)

			if err := runScenario(cfg, profile, policy, scenarioID, capBytes, capPct, *useSnapshots); err != nil {
				log.Printf("[%s] ERRO: %v", scenarioID, err)
				continue
			}
		}
	}
	fmt.Printf("\n=== Tempo total: %v ===\n", time.Since(totalStart))
}

func loadProfile(path string) (*workloadProfile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("ler %s: %w", path, err)
	}
	var p workloadProfile
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if p.FootprintBytes <= 0 {
		return nil, fmt.Errorf("footprint_bytes inválido em %s: %d", path, p.FootprintBytes)
	}
	return &p, nil
}

// scenarioID mapeia (política, capacidade) para o identificador sequencial
// do design experimental:
//   E1-E7   = no_partition
//   E8-E14  = fairshare
//   E15-E21 = memshare
func scenarioID(policy string, capPct int, allCaps []int) string {
	idx := -1
	for i, c := range allCaps {
		if c == capPct {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Sprintf("E_%s_%d", policy, capPct)
	}
	base := 0
	switch policy {
	case "no_partition":
		base = 1
	case "fairshare":
		base = 8
	case "memshare":
		base = 15
	default:
		return fmt.Sprintf("E_%s_%d", policy, capPct)
	}
	return fmt.Sprintf("E%d", base+idx)
}

// snapshotPath constrói o caminho do snapshot para (policy, capPct).
func snapshotPath(cfg *config.Config, policy string, capPct int) string {
	return filepath.Join(cfg.SnapshotsDir, fmt.Sprintf("%s_%d.gob", policy, capPct))
}

func runScenario(cfg *config.Config, profile *workloadProfile, policy, scenarioID string,
	capBytes int64, capPct int, useSnapshots bool) error {

	measurementSeconds := cfg.TotalSeconds - cfg.WarmupSeconds
	fmt.Printf("[%s] %s cap=%d%% (%d bytes / %.2f MB)\n",
		scenarioID, policy, capPct, capBytes, float64(capBytes)/(1024*1024))

	scenarioStart := time.Now()
	metrics := shared.NewMetricsCollector()

	sim, err := buildSim(cfg, profile, policy, capBytes, metrics)
	if err != nil {
		return fmt.Errorf("build sim: %w", err)
	}

	snapPath := snapshotPath(cfg, policy, capPct)
	warmupDuration := time.Duration(0)

	// Tenta restaurar snapshot existente.
	restored := false
	if useSnapshots {
		if data, err := os.ReadFile(snapPath); err == nil {
			if err := sim.Restore(data); err == nil {
				fmt.Printf("    | snapshot restaurado de %s (warmup pulado)\n", snapPath)
				restored = true
			} else {
				fmt.Printf("    | snapshot inválido (%v), executando warmup\n", err)
			}
		}
	}

	if !restored {
		// Fase warmup
		warmupStart := time.Now()
		metrics.IsWarmup = true
		if err := iterAndProcess(cfg.TracePath, 0, float64(cfg.WarmupSeconds), sim.ProcessRequest); err != nil {
			return fmt.Errorf("warmup: %w", err)
		}
		warmupDuration = time.Since(warmupStart)
		fmt.Printf("    | warmup=%v\n", warmupDuration.Round(time.Millisecond))

		// Salva snapshot pós-warmup.
		if useSnapshots {
			if data, err := sim.Snapshot(); err == nil {
				if err := os.WriteFile(snapPath, data, 0o644); err != nil {
					fmt.Printf("    | aviso: falha ao salvar snapshot: %v\n", err)
				}
			} else {
				fmt.Printf("    | aviso: falha ao gerar snapshot: %v\n", err)
			}
		}
	}

	// Fase measurement
	measureStart := time.Now()
	metrics.IsWarmup = false
	if err := iterAndProcess(cfg.TracePath,
		float64(cfg.WarmupSeconds), float64(cfg.TotalSeconds), sim.ProcessRequest); err != nil {
		return fmt.Errorf("measurement: %w", err)
	}
	measureDuration := time.Since(measureStart)

	wallTime := time.Since(scenarioStart)

	hits, misses, hr := metrics.Stats()
	outPath := filepath.Join(cfg.ResultsDir, scenarioID+".json")
	if err := metrics.ExportWithWallTime(scenarioID, policy, capBytes, capPct,
		cfg.WarmupSeconds, measurementSeconds, wallTime, outPath); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	fmt.Printf("    | measure=%v | wall=%v | hits=%d misses=%d HR=%.4f%%\n",
		measureDuration.Round(time.Millisecond), wallTime.Round(time.Millisecond),
		hits, misses, hr*100)
	return nil
}

// buildSim constrói o simulador apropriado para a política.
func buildSim(cfg *config.Config, profile *workloadProfile, policy string,
	capBytes int64, metrics *shared.MetricsCollector) (simulator, error) {

	switch policy {
	case "no_partition":
		return partition.NewNoPartitionSim(capBytes, metrics), nil

	case "memshare":
		// Crédito C: usar config se especificado, senão avg_item_size do profile.
		credit := cfg.MemshareCreditBytes
		if credit <= 0 {
			credit = int64(profile.AvgItemSize)
			if credit <= 0 {
				credit = 1
			}
		}
		// R_i = (capacity × reserved_proportion) / num_tenants
		reservedTotal := float64(capBytes) * cfg.MemshareReservedProportion
		ri := int64(reservedTotal / float64(profile.NumTenants))
		// Shadow queue size = K × R_i (Task 11)
		shadowQ := int64(cfg.MemshareShadowQueueK * float64(ri))
		if shadowQ < credit {
			// Garantir que cabe pelo menos 1 crédito.
			shadowQ = credit
		}

		return memshare.New(memshare.Config{
			CapacityBytes:        capBytes,
			ReservedProportion:   cfg.MemshareReservedProportion,
			CreditBytes:          credit,
			ShadowQueueSizeBytes: shadowQ,
			NumTenants:           profile.NumTenants,
			CompactInterval:      cfg.MemshareCompactInterval,
			Seed:                 cfg.MemshareSeed,
		}, metrics), nil

	case "fairshare":
		// Floor: usar config se especificado, senão avg_item_size do profile.
		floor := cfg.FairshareFloorBytes
		if floor <= 0 {
			floor = int64(profile.AvgItemSize)
			if floor <= 0 {
				floor = 1
			}
		}
		return fairshare.New(fairshare.Config{
			CapacityBytes:       capBytes,
			WindowSeconds:       cfg.FairshareWindowSeconds,
			SlideSeconds:        cfg.FairshareSlideSeconds,
			FloorBytesPerTenant: floor,
			WindowsDemandPath:   cfg.WindowsDemand,
		}, metrics)

	default:
		return nil, fmt.Errorf("política desconhecida: %q", policy)
	}
}

// iterAndProcess é o loop principal: lê o trace no intervalo [start, end)
// e chama processFn para cada request.
func iterAndProcess(tracePath string, start, end float64, processFn func(shared.Request)) error {
	out, errCh := shared.IterTrace(tracePath, start, end)
	for req := range out {
		processFn(req)
	}
	return <-errCh
}
