package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSummarizeCountsTheRangeOnly(t *testing.T) {
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	entries := []Entry{
		{VideoID: "a", Title: "A", Artist: "X", PlayedAt: now.AddDate(0, 0, -30)}, // outside
		{VideoID: "a", Title: "A", Artist: "X", PlayedAt: now.AddDate(0, 0, -1)},
		{VideoID: "a", Title: "A", Artist: "X", PlayedAt: now},
		{VideoID: "b", Title: "B", Artist: "Y", PlayedAt: now},
	}
	s := Summarize(entries, now, 7)
	if s.Plays != 3 || s.Artists != 2 || len(s.ByDay) != 7 {
		t.Fatalf("summary = %+v", s)
	}
	if s.ByDay[6].Plays != 2 || s.ByDay[5].Plays != 1 {
		t.Errorf("by day = %+v", s.ByDay)
	}
	if s.TopTracks[0].Title != "A" || s.TopTracks[0].Plays != 2 {
		t.Errorf("top tracks = %+v", s.TopTracks)
	}
}

func TestAppendCapsAndWriteReadsBack(t *testing.T) {
	var h []Entry
	for i := range MaxEntries + 3 {
		h = Append(h, Entry{VideoID: string(rune('a' + i%26))})
	}
	if len(h) != MaxEntries {
		t.Fatalf("len = %d", len(h))
	}
	path := filepath.Join(t.TempDir(), "history.json")
	if err := Write(path, h[:2]); err != nil {
		t.Fatal(err)
	}
	if got := Read(path); len(got) != 2 || got[0] != h[0] {
		t.Errorf("read back %+v", got)
	}
}
