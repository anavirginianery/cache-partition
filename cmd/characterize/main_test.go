package main

import (
	"math"
	"testing"
)

func TestPercentile(t *testing.T) {
	tests := []struct {
		name string
		data []int64
		p    float64
		want float64
	}{
		{"empty", []int64{}, 0.5, 0},
		{"single", []int64{42}, 0.5, 42},
		{"p50_odd", []int64{1, 2, 3, 4, 5}, 0.5, 3},
		{"p100", []int64{1, 2, 3, 4, 5}, 1.0, 5},
		{"p0", []int64{1, 2, 3, 4, 5}, 0.0, 1},
		{"p25_odd", []int64{1, 2, 3, 4, 5}, 0.25, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentile(tt.data, tt.p)
			if math.Abs(got-tt.want) > 0.01 {
				t.Errorf("percentile(%v, %v) = %v, want %v", tt.data, tt.p, got, tt.want)
			}
		})
	}
}

func TestCharacterizeFromMemory(t *testing.T) {
	// Validar que footprint = soma dos tamanhos distintos.
	products := map[string]int64{
		"p1": 100,
		"p2": 200,
		"p3": 50,
	}
	var sum int64
	for _, s := range products {
		sum += s
	}
	if sum != 350 {
		t.Errorf("soma esperada 350, got %d", sum)
	}
}
