package fairshare

import (
	"testing"
)

func TestMaxMinAllocate_EmptyInputs(t *testing.T) {
	if got := MaxMinAllocate(nil, 1000); len(got) != 0 {
		t.Errorf("nil curves: esperava map vazio, got %v", got)
	}
	curves := map[string][]HRPoint{
		"t1": {{0, 0}, {100, 0.5}},
	}
	if got := MaxMinAllocate(curves, 0); len(got) != 0 {
		t.Errorf("capacity=0: esperava map vazio, got %v", got)
	}
}

func TestMaxMinAllocate_TwoEqualTenants(t *testing.T) {
	// Dois tenants com curvas idênticas → alocação igual
	curve := []HRPoint{
		{0, 0},
		{100, 0.5},
		{200, 0.8},
		{300, 0.9},
	}
	curves := map[string][]HRPoint{
		"t1": curve,
		"t2": curve,
	}
	got := MaxMinAllocate(curves, 400)
	// Deveria alocar ~200 para cada
	if got["t1"] < 150 || got["t1"] > 250 {
		t.Errorf("t1 esperado próximo a 200, got %d", got["t1"])
	}
	if got["t2"] < 150 || got["t2"] > 250 {
		t.Errorf("t2 esperado próximo a 200, got %d", got["t2"])
	}
	// Soma ≤ capacity
	total := got["t1"] + got["t2"]
	if total > 400 {
		t.Errorf("soma %d > capacity 400", total)
	}
}

func TestMaxMinAllocate_AsymmetricCurves(t *testing.T) {
	// t1 tem curva saturável (atinge alto HR com pouca memória)
	// t2 tem curva insaciável (precisa de muito para o mesmo HR)
	// Max-Min deve dar mais memória a t2 (pior HR) até equilibrar.
	t1Curve := []HRPoint{
		{0, 0},
		{50, 0.7},
		{100, 0.8},
		{500, 0.85},
	}
	t2Curve := []HRPoint{
		{0, 0},
		{100, 0.3},
		{500, 0.6},
		{1000, 0.8},
	}
	curves := map[string][]HRPoint{
		"t1": t1Curve,
		"t2": t2Curve,
	}
	got := MaxMinAllocate(curves, 600)

	hr1 := InterpolateHR(t1Curve, got["t1"])
	hr2 := InterpolateHR(t2Curve, got["t2"])

	// HRs devem estar próximos (Max-Min equaliza)
	diff := hr1 - hr2
	if diff < 0 {
		diff = -diff
	}
	if diff > 0.15 {
		t.Errorf("HRs muito diferentes: hr1=%.3f hr2=%.3f", hr1, hr2)
	}
	// Soma ≤ capacity
	if got["t1"]+got["t2"] > 600 {
		t.Errorf("soma excede capacity")
	}
}

func TestMaxMinAllocate_RespectsCapacity(t *testing.T) {
	curve := []HRPoint{
		{0, 0},
		{1000, 0.9},
	}
	curves := map[string][]HRPoint{
		"t1": curve,
		"t2": curve,
		"t3": curve,
	}
	got := MaxMinAllocate(curves, 300)
	var total int64
	for _, v := range got {
		total += v
	}
	if total > 300 {
		t.Errorf("total %d excede capacity 300", total)
	}
}

func TestMaxMinAllocateWithFloor_Basic(t *testing.T) {
	curve := []HRPoint{
		{0, 0},
		{100, 0.5},
		{200, 0.8},
	}
	curves := map[string][]HRPoint{
		"t1": curve,
		"t2": curve,
	}
	got := MaxMinAllocateWithFloor(curves, 400, 50)
	// Cada um pelo menos 50, e o restante distribuído por Max-Min
	if got["t1"] < 50 {
		t.Errorf("t1 < floor: %d", got["t1"])
	}
	if got["t2"] < 50 {
		t.Errorf("t2 < floor: %d", got["t2"])
	}
}

func TestMaxMinAllocateWithFloor_FloorExceedsCapacity(t *testing.T) {
	curve := []HRPoint{{0, 0}, {100, 0.5}}
	curves := map[string][]HRPoint{
		"t1": curve,
		"t2": curve,
	}
	// floor=200 cada, mas capacity=300 → não cabe; distribui igual
	got := MaxMinAllocateWithFloor(curves, 300, 200)
	if got["t1"] != 150 || got["t2"] != 150 {
		t.Errorf("floor inalcançável: esperava 150/150, got %d/%d", got["t1"], got["t2"])
	}
}

func TestUsefulDemandForMaxHR(t *testing.T) {
	curve := []HRPoint{
		{0, 0},
		{100, 0.5},
		{200, 0.8},
		{300, 0.8},
	}
	if got := UsefulDemandForMaxHR(curve); got != 200 {
		t.Errorf("demanda útil esperada 200, got %d", got)
	}
}

func TestMaxMinAllocateDemand(t *testing.T) {
	demands := map[string]int64{
		"small": 100,
		"mid":   200,
		"big":   1000,
	}
	got := MaxMinAllocateDemand(demands, 600)
	if got["small"] != 100 || got["mid"] != 200 || got["big"] != 300 {
		t.Errorf("esperava small/mid/big = 100/200/300, got %d/%d/%d",
			got["small"], got["mid"], got["big"])
	}
}

func TestMaxMinAllocateDemand_RedistributesExtraAboveDemand(t *testing.T) {
	demands := map[string]int64{
		"a": 100,
		"b": 200,
	}
	got := MaxMinAllocateDemand(demands, 1000)
	if got["a"] != 450 || got["b"] != 550 {
		t.Errorf("esperava redistribuição do excedente 450/550, got %d/%d",
			got["a"], got["b"])
	}
}

func TestMaxMinAllocateByUsefulDemand_FloorDoesNotExceedUsefulDemand(t *testing.T) {
	curves := map[string][]HRPoint{
		"small": {
			{0, 0},
			{100, 0.8},
			{200, 0.8},
		},
		"big": {
			{0, 0},
			{500, 0.5},
			{1000, 0.8},
		},
	}
	got := MaxMinAllocateByUsefulDemand(curves, 1000, 200)
	if got["small"] != 100 {
		t.Errorf("small deveria parar na demanda útil 100, got %d", got["small"])
	}
	if got["big"] != 900 {
		t.Errorf("big deveria receber o restante útil, got %d", got["big"])
	}
	var total int64
	for _, allocation := range got {
		total += allocation
	}
	if total > 1000 {
		t.Errorf("total %d excede capacity 1000", total)
	}
}

func TestMaxMinAllocateByUsefulDemand_RedistributesExtraAfterUsefulDemand(t *testing.T) {
	curves := map[string][]HRPoint{
		"small": {
			{0, 0},
			{100, 0.8},
		},
		"big": {
			{0, 0},
			{1000, 0.8},
		},
	}
	got := MaxMinAllocateByUsefulDemand(curves, 2000, 200)
	if got["small"] != 550 || got["big"] != 1450 {
		t.Errorf("esperava excedente redistribuído 550/1450, got %d/%d",
			got["small"], got["big"])
	}
}

func TestSortedTenantsByHR(t *testing.T) {
	curve := []HRPoint{{0, 0}, {100, 0.5}, {200, 0.8}}
	curves := map[string][]HRPoint{
		"a": curve,
		"b": curve,
		"c": curve,
	}
	allocation := map[string]int64{
		"a": 200, // HR ~0.8
		"b": 50,  // HR ~0.25
		"c": 150, // HR ~0.65
	}
	sorted := SortedTenantsByHR(curves, allocation)
	if sorted[0] != "b" {
		t.Errorf("primeiro deveria ser b (menor HR), got %s", sorted[0])
	}
	if sorted[2] != "a" {
		t.Errorf("último deveria ser a (maior HR), got %s", sorted[2])
	}
}
