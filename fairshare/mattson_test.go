package fairshare

import (
	"math"
	"testing"
)

func TestMattson_EmptyCurve(t *testing.T) {
	m := NewMattson()
	if curve := m.HRCurve(); curve != nil {
		t.Errorf("curva vazia esperava nil, got %v", curve)
	}
}

func TestMattson_SingleAccessAllMisses(t *testing.T) {
	m := NewMattson()
	for i := 0; i < 10; i++ {
		m.Record("a", 100) // primeiro miss; depois 9 hits no front
	}
	if m.TotalRequests() != 10 {
		t.Errorf("requests esperados 10, got %d", m.TotalRequests())
	}
	if m.TotalHits() != 9 {
		t.Errorf("hits esperados 9 (10 acessos - 1 miss inicial), got %d", m.TotalHits())
	}
	curve := m.HRCurve()
	// HR final = 9/10 = 0.9
	if math.Abs(curve[len(curve)-1].HR-0.9) > 0.0001 {
		t.Errorf("HR final esperado 0.9, got %v", curve[len(curve)-1].HR)
	}
	// HR no bucket 0 (dist ≤ 1 byte) deve ser 0.9 também (todos os hits foram em distância 0)
	// porque o item estava sempre no topo da stack.
	if math.Abs(curve[1].HR-0.9) > 0.0001 {
		t.Errorf("HR no bucket 1 esperado 0.9 (todos hits em dist 0), got %v", curve[1].HR)
	}
}

func TestMattson_TwoItemsAlternating(t *testing.T) {
	m := NewMattson()
	// Padrão a, b, a, b, a, b — alternados
	// 1º a: miss
	// 1º b: miss (push front; agora stack = [b, a])
	// 2º a: hit, distância = 100 (b está antes de a)
	// 2º b: hit, distância = 100 (a está antes de b)
	// continua...
	for i := 0; i < 6; i++ {
		if i%2 == 0 {
			m.Record("a", 100)
		} else {
			m.Record("b", 100)
		}
	}
	if m.TotalRequests() != 6 {
		t.Errorf("requests esperados 6, got %d", m.TotalRequests())
	}
	if m.TotalHits() != 4 {
		t.Errorf("hits esperados 4 (6 - 2 misses), got %d", m.TotalHits())
	}
	// Todos os 4 hits estavam em distância 100 bytes.
	// bucketFor(100) = ?
	// 100 - 1 = 99 = 0x63 → 7 bits → bucket 7
	curve := m.HRCurve()
	// curve[0] é o ponto sintético size=0; logo bucket 7 fica em curve[8].
	// HR para tamanho ≥ 128 (bucket 7) deve incluir os 4 hits → 4/6.
	if math.Abs(curve[8].HR-(4.0/6.0)) > 0.0001 {
		t.Errorf("HR no bucket 7 esperado 4/6, got %v", curve[8].HR)
	}
}

func TestMattson_Reset(t *testing.T) {
	m := NewMattson()
	m.Record("a", 100)
	m.Record("a", 100) // 1 hit
	if m.TotalHits() != 1 {
		t.Fatal("setup falhou")
	}
	m.Reset()
	if m.TotalRequests() != 0 || m.TotalHits() != 0 {
		t.Errorf("após Reset, totais deveriam ser 0, got reqs=%d hits=%d", m.TotalRequests(), m.TotalHits())
	}
	if curve := m.HRCurve(); curve != nil {
		t.Errorf("após Reset, curva deveria ser nil, got %v", curve)
	}
}

func TestBucketFor(t *testing.T) {
	tests := []struct {
		dist int64
		want int
	}{
		{0, 0},
		{1, 0},
		{2, 1},
		{3, 2},
		{4, 2},
		{5, 3},
		{8, 3},
		{9, 4},
		{16, 4},
		{17, 5},
		{32, 5},
		{1024, 10},
		{1 << 30, 30},
		{1 << 40, NumBuckets - 1}, // overflow
	}
	for _, tt := range tests {
		got := bucketFor(tt.dist)
		if got != tt.want {
			t.Errorf("bucketFor(%d) = %d, want %d", tt.dist, got, tt.want)
		}
	}
}

func TestInterpolateHR(t *testing.T) {
	curve := []HRPoint{
		{SizeBytes: 0, HR: 0.0},
		{SizeBytes: 100, HR: 0.5},
		{SizeBytes: 200, HR: 0.8},
		{SizeBytes: 300, HR: 0.9},
	}
	// Em pontos exatos
	if got := InterpolateHR(curve, 100); got != 0.5 {
		t.Errorf("ponto exato 100: esperava 0.5, got %v", got)
	}
	// Interpolação no meio
	if got := InterpolateHR(curve, 150); math.Abs(got-0.65) > 0.0001 {
		t.Errorf("interp 150: esperava 0.65, got %v", got)
	}
	// Acima do máximo
	if got := InterpolateHR(curve, 1000); got != 0.9 {
		t.Errorf("acima do máximo: esperava 0.9, got %v", got)
	}
	// Abaixo do mínimo (size=0)
	if got := InterpolateHR(curve, 0); got != 0 {
		t.Errorf("size=0: esperava 0, got %v", got)
	}
}

func TestSizeForHR(t *testing.T) {
	curve := []HRPoint{
		{SizeBytes: 0, HR: 0.0},
		{SizeBytes: 100, HR: 0.5},
		{SizeBytes: 200, HR: 0.8},
		{SizeBytes: 300, HR: 0.9},
	}
	// HR alvo exato
	if got := SizeForHR(curve, 0.5); got != 100 {
		t.Errorf("HR=0.5: esperava 100, got %d", got)
	}
	// Interpolação
	if got := SizeForHR(curve, 0.65); got != 150 {
		t.Errorf("HR=0.65: esperava 150, got %d", got)
	}
	// Acima do máximo
	if got := SizeForHR(curve, 0.95); got != 300 {
		t.Errorf("HR=0.95 (acima): esperava 300, got %d", got)
	}
}
