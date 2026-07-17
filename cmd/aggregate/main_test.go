package main

import (
	"testing"

	"cache-simulator/shared"
)

func TestCompareScenarios_Basic(t *testing.T) {
	base := &shared.ScenarioResult{
		PerTenant: map[string]*shared.TenantMetrics{
			"t1": {HitRatio: 0.5, Interference: 0.3},
			"t2": {HitRatio: 0.6, Interference: 0.2},
			"t3": {HitRatio: 0.7, Interference: 0.1},
		},
	}
	policy := &shared.ScenarioResult{
		PerTenant: map[string]*shared.TenantMetrics{
			"t1": {HitRatio: 0.6, Interference: 0.2}, // melhor + interferência menor
			"t2": {HitRatio: 0.4, Interference: 0.3}, // pior + interferência maior
			"t3": {HitRatio: 0.7, Interference: 0.0}, // igual HR + zero interferência
			// t4 ausente → não conta
		},
	}
	c := compareScenarios(base, policy)
	if c.NumCompared != 3 {
		t.Errorf("NumCompared esperado 3, got %d", c.NumCompared)
	}
	// Improvements: t1 (0.6>0.5) → 1; t2 não; t3 não (igual)
	if c.PctImprovedHR != 1.0/3.0 {
		t.Errorf("PctImprovedHR esperado 1/3, got %v", c.PctImprovedHR)
	}
	// Reduced interference: t1 (0.2<0.3), t3 (0.0<0.1) → 2
	if c.PctReducedInterference != 2.0/3.0 {
		t.Errorf("PctReducedInterference esperado 2/3, got %v", c.PctReducedInterference)
	}
}

func TestCompareScenarios_NoOverlap(t *testing.T) {
	base := &shared.ScenarioResult{
		PerTenant: map[string]*shared.TenantMetrics{
			"t1": {HitRatio: 0.5},
		},
	}
	policy := &shared.ScenarioResult{
		PerTenant: map[string]*shared.TenantMetrics{
			"tX": {HitRatio: 0.9},
		},
	}
	c := compareScenarios(base, policy)
	if c.NumCompared != 0 {
		t.Errorf("sem overlap, NumCompared deveria ser 0")
	}
}
