package shared

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleTrace = `tenant,product,timestamp,size
t1,p1,0,100
t2,p2,5500000,200
t1,p3,10000000,150
t3,p4,15200000,300
t2,p5,20000000,250
`

func writeTempTrace(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "trace.csv")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	return p
}

func collect(t *testing.T, ch <-chan Request) []Request {
	t.Helper()
	var out []Request
	for r := range ch {
		out = append(out, r)
	}
	return out
}

func TestIterTrace_AllRecords(t *testing.T) {
	p := writeTempTrace(t, sampleTrace)
	out, errCh := IterTrace(p, -1, -1)
	got := collect(t, out)
	if err := <-errCh; err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("esperava 5 requests, obteve %d", len(got))
	}
	if got[0].TenantID != "t1" || got[0].ProductID != "p1" || got[0].Size != 100 {
		t.Errorf("primeira request errada: %+v", got[0])
	}
	if got[2].Timestamp != 10 {
		t.Errorf("timestamp da terceira request errada: %v", got[2].Timestamp)
	}
	if got[1].Timestamp != 5.5 {
		t.Errorf("timestamp em microssegundos deveria virar 5.5s, got %v", got[1].Timestamp)
	}
}

func TestIterTrace_StartTimestampFilter(t *testing.T) {
	p := writeTempTrace(t, sampleTrace)
	out, errCh := IterTrace(p, 10, -1)
	got := collect(t, out)
	if err := <-errCh; err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("esperava 3 requests (>= 10s), obteve %d", len(got))
	}
	if got[0].ProductID != "p3" {
		t.Errorf("primeira request com filtro >=10s deveria ser p3, foi %s", got[0].ProductID)
	}
}

func TestIterTrace_EndTimestampFilter(t *testing.T) {
	p := writeTempTrace(t, sampleTrace)
	out, errCh := IterTrace(p, -1, 10)
	got := collect(t, out)
	if err := <-errCh; err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("esperava 2 requests (< 10s), obteve %d", len(got))
	}
	if got[1].ProductID != "p2" {
		t.Errorf("segunda request com filtro <10s deveria ser p2, foi %s", got[1].ProductID)
	}
}

func TestIterTrace_RangeFilter(t *testing.T) {
	p := writeTempTrace(t, sampleTrace)
	out, errCh := IterTrace(p, 5, 15)
	got := collect(t, out)
	if err := <-errCh; err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("esperava 2 requests no intervalo [5,15), obteve %d", len(got))
	}
	if got[0].ProductID != "p2" || got[1].ProductID != "p3" {
		t.Errorf("intervalo [5,15) errado: %v", got)
	}
}

func TestIterTrace_FileNotFound(t *testing.T) {
	out, errCh := IterTrace("/path/nao/existe.csv", -1, -1)
	for range out {
	}
	if err := <-errCh; err == nil {
		t.Fatal("esperava erro de arquivo inexistente")
	}
}

func TestIterTrace_BadLine(t *testing.T) {
	bad := `tenant,product,timestamp,size
t1,p1,0,100
linha_invalida_sem_virgulas
`
	p := writeTempTrace(t, bad)
	out, errCh := IterTrace(p, -1, -1)
	for range out {
	}
	if err := <-errCh; err == nil {
		t.Fatal("esperava erro de parse na linha inválida")
	}
}
