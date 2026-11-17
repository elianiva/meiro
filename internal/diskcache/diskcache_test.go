package diskcache

import (
	"bytes"
	"testing"
	"time"
)

func TestPutGetAndTrim(t *testing.T) {
	c, err := Open(t.TempDir(), 25)
	if err != nil {
		t.Fatal(err)
	}
	ten := bytes.Repeat([]byte("x"), 10)
	c.Put("a", ten)
	time.Sleep(2 * time.Millisecond)
	c.Put("b", ten)
	time.Sleep(2 * time.Millisecond)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("a missing")
	}
	time.Sleep(2 * time.Millisecond)
	c.Put("c", ten)
	if c.Has("b") {
		t.Error("b should have been trimmed")
	}
	if !c.Has("a") || !c.Has("c") {
		t.Error("a and c should remain")
	}
	if got, _ := c.Get("c"); !bytes.Equal(got, ten) {
		t.Error("c content wrong")
	}
}

func TestReopenKeepsFiles(t *testing.T) {
	dir := t.TempDir()
	c, _ := Open(dir, 100)
	c.Put("k", []byte("v"))
	c2, err := Open(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := c2.Get("k"); !ok || string(got) != "v" {
		t.Errorf("got %q %v", got, ok)
	}
}

func TestTooLargeNotKept(t *testing.T) {
	c, _ := Open(t.TempDir(), 3)
	c.Put("k", []byte("toolong"))
	if c.Has("k") {
		t.Error("oversize file kept")
	}
}
