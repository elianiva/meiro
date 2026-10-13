package playstate

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elianiva/meiro/youtube"
)

func tracks(n int) []youtube.MusicItem {
	items := make([]youtube.MusicItem, n)
	for i := range items {
		items[i] = youtube.MusicItem{VideoID: fmt.Sprintf("v%d", i), Title: fmt.Sprintf("Track %d", i)}
	}
	return items
}

func TestWindowKeepsTheCurrentTrackAndTheBoundaryInStep(t *testing.T) {
	s := State{Items: tracks(1000), Index: 500, RecStart: 700, PlayNext: "v501"}.Window()
	if len(s.Items) != MaxItems {
		t.Fatalf("%d tracks kept, want %d", len(s.Items), MaxItems)
	}
	if got := s.Items[s.Index].VideoID; got != "v500" {
		t.Errorf("current track became %s", got)
	}
	// 700 is beyond the window that starts at 480, so there is no boundary.
	if s.RecStart != -1 {
		t.Errorf("RecStart = %d, want -1", s.RecStart)
	}

	s = State{Items: tracks(1000), Index: 990, RecStart: 950}.Window()
	if got := s.Items[s.Index].VideoID; got != "v990" || s.Items[s.RecStart].VideoID != "v950" {
		t.Errorf("at the end, current %s and boundary %s", got, s.Items[s.RecStart].VideoID)
	}
}

func TestWriterAndLoadRoundTripAndRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playback.json")
	var w Writer
	w.Path = path
	w.Save(State{Items: tracks(5), Index: 2, RecStart: 4, PlayNext: "v3", Source: "Mix", Position: 42 * time.Second})
	w.Wait()

	got := Load(path)
	if len(got.Items) != 5 || got.Index != 2 || got.RecStart != 4 || got.PlayNext != "v3" || got.Source != "Mix" || got.Position != 42*time.Second {
		t.Fatalf("loaded %+v", got)
	}

	// A hand-edited file with the current place outside the queue is no state.
	if err := os.WriteFile(path, []byte(`{"items":[{"videoId":"a"}],"index":3,"recStart":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); !got.Empty() {
		t.Errorf("loaded %+v from an out-of-range index", got)
	}
	// Tracks without an ID go, and the places follow.
	if err := os.WriteFile(path, []byte(`{"items":[{"title":"x"},{"videoId":"a"},{"videoId":"b"}],"index":2,"recStart":2,"playNext":"gone"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got = Load(path)
	if len(got.Items) != 2 || got.Index != 1 || got.RecStart != 1 || got.PlayNext != "" {
		t.Errorf("repaired to %+v", got)
	}
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path); !got.Empty() {
		t.Errorf("loaded %+v from garbage", got)
	}
	if got := Load(filepath.Join(t.TempDir(), "missing.json")); !got.Empty() {
		t.Errorf("loaded %+v from a missing file", got)
	}
}
