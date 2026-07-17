// Package config carrega o YAML de configuração dos experimentos.
//
// Implementa um parser YAML mínimo dedicado ao schema do nosso config:
//   - Pares chave: valor (strings, ints, floats) no nível raiz
//   - Listas com itens precedidos por "- "
//   - Comentários começando com # (linha inteira ou inline após valor)
//
// Não suporta YAML avançado (anchors, multi-line strings, mapas aninhados além
// de 1 nível de lista). Suficiente para o config do simulador.
package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config é a representação parseada do YAML de configuração.
type Config struct {
	TracePath       string
	WorkloadProfile string
	WindowsDemand   string
	ResultsDir      string
	SnapshotsDir    string
	WarmupSeconds   int
	TotalSeconds    int
	CapacitiesPct   []int
	PoliciesEnabled []string

	// Memshare
	MemshareReservedProportion float64
	MemshareCreditBytes        int64 // 0 = "auto" (usar avg_item_size)
	MemshareShadowQueueK       float64
	MemshareCompactInterval    int64
	MemshareSeed               int64

	// FairShare
	FairshareWindowSeconds int
	FairshareSlideSeconds  int
	FairshareFloorBytes    int64 // 0 = "auto" (usar avg_item_size)
}

// Load lê e parseia um arquivo YAML do caminho dado.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("abrir %s: %w", path, err)
	}
	defer f.Close()

	cfg := &Config{
		ResultsDir:                 "results",
		SnapshotsDir:               "snapshots",
		WindowsDemand:              "windows_demand.json",
		MemshareReservedProportion: 0.5,
		MemshareCreditBytes:        0, // 0 = auto
		MemshareShadowQueueK:       2.0,
		MemshareCompactInterval:    100_000,
		MemshareSeed:               42,
		FairshareWindowSeconds:     600,
		FairshareSlideSeconds:      300,
		FairshareFloorBytes:        0, // 0 = auto
	}

	scanner := bufio.NewScanner(f)
	var currentList string

	for scanner.Scan() {
		raw := scanner.Text()
		line := stripComment(raw)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			if currentList == "" {
				return nil, fmt.Errorf("item de lista sem chave precedente: %q", raw)
			}
			val := strings.TrimSpace(trimmed[2:])
			val = strings.Trim(val, `"'`)
			if err := cfg.addToList(currentList, val); err != nil {
				return nil, err
			}
			continue
		}

		idx := strings.IndexByte(trimmed, ':')
		if idx < 0 {
			return nil, fmt.Errorf("linha sem ':': %q", raw)
		}
		key := strings.TrimSpace(trimmed[:idx])
		val := strings.TrimSpace(trimmed[idx+1:])
		val = strings.Trim(val, `"'`)

		if val == "" {
			currentList = key
			continue
		}
		currentList = ""

		if err := cfg.setScalar(key, val); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanner: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func stripComment(line string) string {
	if idx := strings.IndexByte(line, '#'); idx >= 0 {
		return line[:idx]
	}
	return line
}

func (c *Config) setScalar(key, val string) error {
	switch key {
	case "trace_path":
		c.TracePath = val
	case "workload_profile":
		c.WorkloadProfile = val
	case "results_dir":
		c.ResultsDir = val
	case "snapshots_dir":
		c.SnapshotsDir = val
	case "warmup_seconds":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("warmup_seconds inválido %q: %w", val, err)
		}
		c.WarmupSeconds = n
	case "total_seconds":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("total_seconds inválido %q: %w", val, err)
		}
		c.TotalSeconds = n
	case "memshare_reserved_proportion":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("memshare_reserved_proportion inválido %q: %w", val, err)
		}
		c.MemshareReservedProportion = f
	case "memshare_credit_bytes":
		if val == "auto" {
			c.MemshareCreditBytes = 0
		} else {
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Errorf("memshare_credit_bytes inválido %q: %w", val, err)
			}
			c.MemshareCreditBytes = n
		}
	case "memshare_shadow_queue_k":
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return fmt.Errorf("memshare_shadow_queue_k inválido %q: %w", val, err)
		}
		c.MemshareShadowQueueK = f
	case "memshare_compact_interval":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("memshare_compact_interval inválido %q: %w", val, err)
		}
		c.MemshareCompactInterval = n
	case "memshare_seed":
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return fmt.Errorf("memshare_seed inválido %q: %w", val, err)
		}
		c.MemshareSeed = n
	case "windows_demand":
		c.WindowsDemand = val
	case "fairshare_window_seconds":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("fairshare_window_seconds inválido %q: %w", val, err)
		}
		c.FairshareWindowSeconds = n
	case "fairshare_slide_seconds":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("fairshare_slide_seconds inválido %q: %w", val, err)
		}
		c.FairshareSlideSeconds = n
	case "fairshare_floor_bytes":
		if val == "auto" {
			c.FairshareFloorBytes = 0
		} else {
			n, err := strconv.ParseInt(val, 10, 64)
			if err != nil {
				return fmt.Errorf("fairshare_floor_bytes inválido %q: %w", val, err)
			}
			c.FairshareFloorBytes = n
		}
	default:
		// Chave desconhecida: ignorar silenciosamente (futuras extensões)
	}
	return nil
}

func (c *Config) addToList(key, val string) error {
	switch key {
	case "capacities_pct":
		n, err := strconv.Atoi(val)
		if err != nil {
			return fmt.Errorf("capacities_pct inválido %q: %w", val, err)
		}
		c.CapacitiesPct = append(c.CapacitiesPct, n)
	case "policies_enabled":
		c.PoliciesEnabled = append(c.PoliciesEnabled, val)
	default:
		// Lista desconhecida: ignorar
	}
	return nil
}

func (c *Config) validate() error {
	if c.TracePath == "" {
		return fmt.Errorf("trace_path obrigatório")
	}
	if c.WorkloadProfile == "" {
		c.WorkloadProfile = "workload_profile.json"
	}
	if c.WarmupSeconds <= 0 {
		return fmt.Errorf("warmup_seconds deve ser > 0")
	}
	if c.TotalSeconds <= c.WarmupSeconds {
		return fmt.Errorf("total_seconds (%d) deve ser > warmup_seconds (%d)", c.TotalSeconds, c.WarmupSeconds)
	}
	if len(c.CapacitiesPct) == 0 {
		return fmt.Errorf("capacities_pct vazio")
	}
	if len(c.PoliciesEnabled) == 0 {
		return fmt.Errorf("policies_enabled vazio")
	}
	for _, p := range c.PoliciesEnabled {
		switch p {
		case "no_partition", "memshare", "fairshare":
			// ok
		default:
			return fmt.Errorf("política desconhecida em policies_enabled: %q", p)
		}
	}
	if c.MemshareReservedProportion < 0 || c.MemshareReservedProportion > 1 {
		return fmt.Errorf("memshare_reserved_proportion deve estar em [0,1], got %v", c.MemshareReservedProportion)
	}
	if c.MemshareShadowQueueK < 0 {
		return fmt.Errorf("memshare_shadow_queue_k deve ser >= 0, got %v", c.MemshareShadowQueueK)
	}
	if c.FairshareWindowSeconds <= 0 {
		return fmt.Errorf("fairshare_window_seconds deve ser > 0")
	}
	if c.FairshareSlideSeconds <= 0 {
		return fmt.Errorf("fairshare_slide_seconds deve ser > 0")
	}
	if c.FairshareWindowSeconds%c.FairshareSlideSeconds != 0 {
		return fmt.Errorf("fairshare_window_seconds (%d) deve ser múltiplo de fairshare_slide_seconds (%d)",
			c.FairshareWindowSeconds, c.FairshareSlideSeconds)
	}
	return nil
}
