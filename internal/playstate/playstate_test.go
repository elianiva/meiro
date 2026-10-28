package playstate

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

func TestWindowKeepsTheCurrentTrackAndEveryUpcomingTrack(t *testing.T) {
	s := State{Items: tracks(1000), Index: 500, RecStart: 700, PlayNext: "v501"}.Window()
	if len(s.Items) != 1000-500+MaxHistoryItems {
		t.Fatalf("%d tracks kept, want current, all upcoming and %d played", len(s.Items), MaxHistoryItems)
	}
	if got := s.Items[s.Index].VideoID; got != "v500" || s.Index != MaxHistoryItems {
		t.Errorf("current track is %s at %d", got, s.Index)
	}
	if got := s.Items[s.RecStart].VideoID; s.RecStart != 220 || got != "v700" {
		t.Errorf("recommendation boundary is %d at %s", s.RecStart, got)
	}
	if got := s.Items[len(s.Items)-1].VideoID; got != "v999" {
		t.Errorf("last upcoming track is %s, want v999", got)
	}

	s = State{Items: tracks(1000), Index: 990, RecStart: 950}.Window()
	if got := s.Items[s.Index].VideoID; got != "v990" || s.RecStart != 0 || s.Items[s.RecStart].VideoID != "v970" {
		t.Errorf("at the end, current %s and boundary %d at %s", got, s.RecStart, s.Items[s.RecStart].VideoID)
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

func TestQueueRestoresPlayerMetadataLinks(t *testing.T) {
	parts := []youtube.BylinePart{{Text: "Band", BrowseID: "UCband"}, {Text: " • "}, {Text: "Record", BrowseID: "MPREb_record"}}
	w := Writer{Path: filepath.Join(t.TempDir(), "playback.json")}
	w.Save(State{Items: []youtube.MusicItem{{VideoID: "song", Byline: &youtube.TrackByline{Parts: parts}}}, Index: 0, RecStart: -1})
	w.Wait()
	got := Load(w.Path)
	if len(got.Items) != 1 || !slices.Equal(got.Items[0].PlayerByline(), parts) {
		t.Fatal("restored queue lost artist or album links")
	}
}
