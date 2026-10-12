package lru

import "testing"

func TestCacheDropsTheLeastRecentlyUsed(t *testing.T) {
	c := New[string, int](2)
	c.Put("a", 1)
	c.Put("b", 2)
	if _, ok := c.Get("a"); !ok { // a is now the most recent
		t.Fatal("a was dropped early")
	}
	c.Put("c", 3)
	if _, ok := c.Peek("b"); ok {
		t.Error("b should have been dropped as the least recently used")
	}
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Errorf("a = %v, %v", v, ok)
	}
	if got := c.GetOrPut("d", func() int { return 4 }); got != 4 || c.Len() != 2 {
		t.Errorf("GetOrPut = %v, len %d", got, c.Len())
	}
	c.Delete("d")
	c.Clear()
	if c.Len() != 0 {
		t.Errorf("Clear left %d entries", c.Len())
	}
}

func TestCacheKeepsOrderThroughUpdatesAndDeletes(t *testing.T) {
	c := New[int, int](3)
	for i := range 3 {
		c.Put(i, i)
	}
	c.Put(0, 10) // 0 is now the most recent
	c.Delete(1)
	c.Put(3, 3)
	c.Put(4, 4) // drops 2, the least recent left
	for key, want := range map[int]bool{0: true, 1: false, 2: false, 3: true, 4: true} {
		if _, ok := c.Peek(key); ok != want {
			t.Errorf("key %d present=%v, want %v", key, ok, want)
		}
	}
	if v, _ := c.Peek(0); v != 10 {
		t.Errorf("update lost: %d", v)
	}
}
