package credits

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheSurvivesARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credits.json")
	c := Open(path)
	c.Put("a", Credits{Artist: "A", Album: "Al"})
	c.Put("b", Credits{Artist: "B"})
	c.Wait()
	got := Open(path)
	if v, ok := got.Get("a"); !ok || v != (Credits{Artist: "A", Album: "Al"}) {
		t.Errorf("a = %+v, %t", v, ok)
	}
	if v, ok := got.Get("b"); !ok || v.Artist != "B" {
		t.Errorf("b = %+v, %t", v, ok)
	}
	if _, ok := got.Get("zzz"); ok {
		t.Error("an unknown track was found")
	}
}

func TestOldestEntriesGoFirstAndUpdatesRenew(t *testing.T) {
	c := &Cache{}
	for i := range MaxEntries {
		c.Put(fmt.Sprint("id", i), Credits{Artist: "x"})
	}
	c.Put("id0", Credits{Artist: "renewed"})
	c.Put("new", Credits{Artist: "x"})
	if _, ok := c.Get("id1"); ok {
		t.Error("the oldest entry was kept")
	}
	if v, ok := c.Get("id0"); !ok || v.Artist != "renewed" {
		t.Errorf("a renewed entry was dropped: %+v %t", v, ok)
	}
	if c.Len() != MaxEntries {
		t.Errorf("Len = %d, want %d", c.Len(), MaxEntries)
	}
}

func TestABadFileIsAnEmptyCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credits.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := Open(path)
	if c.Len() != 0 {
		t.Error("a bad file was read")
	}
	c.Put("a", Credits{Artist: "A"})
	c.Wait()
	if Open(path).Len() != 1 {
		t.Error("the cache did not replace the bad file")
	}
}
