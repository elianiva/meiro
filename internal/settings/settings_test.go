package settings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriterKeepsTheLastStateAndLoadRepairsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	w := &Writer{Path: path}
	s := Defaults()
	s.Mode = "dark"
	w.Save(&s)
	s.Seed = "#d81b78"
	w.Save(&s)
	s.Seed = "not a colour" // a change after the last save is not written
	w.Wait()

	got := Load(path)
	if got.Mode != "dark" || got.Seed != "#d81b78" {
		t.Errorf("loaded %+v", got)
	}

	// A file with values out of range comes back with the defaults for them,
	// and a field it lacks takes its default.
	if err := os.WriteFile(path, []byte(`{"volume":500,"seed":"red","cacheSongs":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, want := Load(path), Defaults()
	if got.Volume != want.Volume || got.Seed != want.Seed || got.CacheSongs != want.CacheSongs || !got.AutoPlay {
		t.Errorf("repaired %+v", got)
	}
}

func TestRememberKeepsRecentSearchesUniqueAndBounded(t *testing.T) {
	var s Settings
	for _, q := range []string{"a", "b", "A", "c", "d", "e", "f", "g", "h", "i"} {
		s.Remember(q)
	}
	if len(s.Recent) != maxRecent || s.Recent[0] != "i" {
		t.Errorf("recent = %v", s.Recent)
	}
	s.Forget("i")
	if s.Recent[0] != "h" {
		t.Errorf("forget left %v", s.Recent)
	}
}
