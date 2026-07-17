package memshare

import "testing"

func TestShadowQueue_AddContains(t *testing.T) {
	q := NewShadowQueue(1000)
	q.Add("a", 100)
	q.Add("b", 200)
	if !q.Contains("a") || !q.Contains("b") {
		t.Fatal("itens deveriam estar presentes")
	}
	if q.Used() != 300 {
		t.Errorf("used esperado 300, got %d", q.Used())
	}
	if q.Len() != 2 {
		t.Errorf("len esperado 2, got %d", q.Len())
	}
}

func TestShadowQueue_FIFOEvictionByBytes(t *testing.T) {
	q := NewShadowQueue(300)
	q.Add("a", 100) // [a]
	q.Add("b", 100) // [b, a]
	q.Add("c", 100) // [c, b, a]
	q.Add("d", 100) // [d, c, b] — a evictado
	if q.Contains("a") {
		t.Error("a deveria ter sido evictado")
	}
	if !q.Contains("b") || !q.Contains("c") || !q.Contains("d") {
		t.Error("b, c, d deveriam estar presentes")
	}
	if q.Used() != 300 {
		t.Errorf("used esperado 300, got %d", q.Used())
	}
}

func TestShadowQueue_AddExisting(t *testing.T) {
	q := NewShadowQueue(1000)
	q.Add("a", 100)
	q.Add("a", 150) // update tamanho
	if q.Used() != 150 {
		t.Errorf("após update esperava 150 bytes, got %d", q.Used())
	}
	if q.Len() != 1 {
		t.Errorf("len deveria ser 1, got %d", q.Len())
	}
}

func TestShadowQueue_OversizedItemIgnored(t *testing.T) {
	q := NewShadowQueue(100)
	q.Add("huge", 200) // maior que capacidade total
	if q.Contains("huge") {
		t.Error("item maior que capacidade não deveria ser adicionado")
	}
	if q.Used() != 0 {
		t.Errorf("used deveria ser 0, got %d", q.Used())
	}
}

func TestShadowQueue_OversizedUpdateRemovesExisting(t *testing.T) {
	q := NewShadowQueue(100)
	q.Add("a", 50)
	q.Add("a", 200)

	if q.Contains("a") {
		t.Error("update oversized deveria remover entrada antiga para evitar shadow rehit falso")
	}
	if q.Used() != 0 {
		t.Errorf("used deveria ser 0 após remover entrada oversized, got %d", q.Used())
	}
}

func TestShadowQueue_Remove(t *testing.T) {
	q := NewShadowQueue(1000)
	q.Add("a", 100)
	q.Add("b", 200)
	q.Remove("a")
	if q.Contains("a") {
		t.Error("a deveria ter sido removido")
	}
	if q.Used() != 200 {
		t.Errorf("used esperado 200, got %d", q.Used())
	}
	q.Remove("ausente") // no-op não deve crashar
}

func TestShadowQueue_NegativeSizeIgnored(t *testing.T) {
	q := NewShadowQueue(1000)
	q.Add("a", -5)
	q.Add("a", 0)
	if q.Contains("a") {
		t.Error("size <= 0 não deveria adicionar")
	}
}
