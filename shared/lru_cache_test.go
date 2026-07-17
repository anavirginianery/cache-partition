package shared

import (
	"testing"
)

func TestLRUCache_InsertAndContains(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 100)
	c.Insert("b", "t2", 200)

	if !c.Contains("a") || !c.Contains("b") {
		t.Fatal("itens deveriam estar presentes")
	}
	if c.Used() != 300 {
		t.Errorf("usedBytes esperado 300, got %d", c.Used())
	}
	if tn, ok := c.GetTenant("a"); !ok || tn != "t1" {
		t.Errorf("GetTenant(a) errado: %s/%v", tn, ok)
	}
}

func TestLRUCache_EvictionOrder(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 100)
	c.Insert("b", "t2", 100)
	c.Insert("c", "t3", 100)
	// ordem do LRU: a, b, c (c = mais recente)

	k, _, _, ok := c.EvictLRU()
	if !ok || k != "a" {
		t.Errorf("evict 1: esperava a, got %s", k)
	}
	k, _, _, ok = c.EvictLRU()
	if !ok || k != "b" {
		t.Errorf("evict 2: esperava b, got %s", k)
	}
	k, _, _, ok = c.EvictLRU()
	if !ok || k != "c" {
		t.Errorf("evict 3: esperava c, got %s", k)
	}
	if _, _, _, ok := c.EvictLRU(); ok {
		t.Error("evict de cache vazia deveria retornar ok=false")
	}
}

func TestLRUCache_Promote(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 100)
	c.Insert("b", "t2", 100)
	c.Insert("c", "t3", 100)
	c.Promote("a") // a vai para o front; agora ordem LRU: b, c, a

	k, _, _, _ := c.EvictLRU()
	if k != "b" {
		t.Errorf("após promover a, primeiro evict deveria ser b, got %s", k)
	}
}

func TestLRUCache_InsertUpdate(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 100)
	c.Insert("a", "t2", 250) // update mesma key, novo tamanho/tenant
	if c.Used() != 250 {
		t.Errorf("após update esperava 250 bytes, got %d", c.Used())
	}
	if tn, _ := c.GetTenant("a"); tn != "t2" {
		t.Errorf("após update tenant deveria ser t2, got %s", tn)
	}
	if c.Len() != 1 {
		t.Errorf("Len deveria ser 1, got %d", c.Len())
	}
}

func TestLRUCache_ResizeShrink(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 200)
	c.Insert("b", "t2", 200)
	c.Insert("c", "t3", 200)
	c.Insert("d", "t4", 200)
	c.Insert("e", "t5", 200)
	// 1000 bytes usados (full). Resize para 400.

	evicted := c.Resize(400)
	if c.Used() > 400 {
		t.Errorf("após shrink, used %d > 400", c.Used())
	}
	if len(evicted) < 3 {
		t.Errorf("deveria ter evictado pelo menos 3, got %d", len(evicted))
	}
	if c.Capacity() != 400 {
		t.Errorf("capacity errada: %d", c.Capacity())
	}
}

func TestLRUCache_ResizeExpand(t *testing.T) {
	c := NewLRUCache(500)
	c.Insert("a", "t1", 200)
	c.Insert("b", "t2", 200)

	evicted := c.Resize(2000)
	if len(evicted) != 0 {
		t.Errorf("expand não deveria evictar, mas evictou %d", len(evicted))
	}
	if c.Used() != 400 {
		t.Errorf("used inalterado após expand, got %d", c.Used())
	}
	if !c.Contains("a") || !c.Contains("b") {
		t.Error("items perdidos após expand")
	}
}

func TestLRUCache_SnapshotRestore(t *testing.T) {
	c := NewLRUCache(1000)
	c.Insert("a", "t1", 100)
	c.Insert("b", "t2", 200)
	c.Insert("c", "t3", 150)
	c.Promote("a") // ordem LRU: b, c, a

	data, err := c.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	c2 := NewLRUCache(0)
	if err := c2.Restore(data); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if c2.Capacity() != 1000 {
		t.Errorf("capacity restaurada errada: %d", c2.Capacity())
	}
	if c2.Used() != 450 {
		t.Errorf("used restaurado errado: %d", c2.Used())
	}
	if c2.Len() != 3 {
		t.Errorf("len restaurado errado: %d", c2.Len())
	}

	// Mesma ordem LRU: b deve sair primeiro
	k, _, _, _ := c2.EvictLRU()
	if k != "b" {
		t.Errorf("ordem LRU restaurada errada, primeiro evict deveria ser b, got %s", k)
	}
	k, _, _, _ = c2.EvictLRU()
	if k != "c" {
		t.Errorf("segundo evict deveria ser c, got %s", k)
	}
	k, _, _, _ = c2.EvictLRU()
	if k != "a" {
		t.Errorf("terceiro evict deveria ser a, got %s", k)
	}
}

func TestLRUCache_GetTenantAbsent(t *testing.T) {
	c := NewLRUCache(100)
	if _, ok := c.GetTenant("nope"); ok {
		t.Error("GetTenant em key ausente deveria retornar false")
	}
}

func TestLRUCache_PromoteAbsent(t *testing.T) {
	c := NewLRUCache(100)
	c.Insert("a", "t1", 50)
	c.Promote("nope") // não deveria crashar
	if !c.Contains("a") {
		t.Error("promote de ausente quebrou estado")
	}
}
