package history

import (
	"fmt"
	"os"
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

func TestWriterKeepsTheLatestSaveAndWaitsForIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	var w Writer
	for i := range 50 {
		w.Save(path, []Entry{{VideoID: string(rune('a' + i%26)), Title: "t"}, {VideoID: "last", Title: string(rune('0' + i%10))}})
	}
	w.Wait()
	got := Read(path)
	if len(got) != 2 || got[1].Title != "9" {
		t.Errorf("the file holds %+v, want the 50th save", got)
	}
}

func TestWriterCopiesWhatItIsGiven(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	entries := []Entry{{VideoID: "a"}}
	var w Writer
	w.Save(path, entries)
	entries[0].VideoID = "changed"
	w.Wait()
	if got := Read(path); len(got) != 1 || got[0].VideoID != "a" {
		t.Errorf("the file holds %+v after the caller changed its slice", got)
	}
}

func TestWritingNothingLeavesAnEmptyHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := Write(path, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "[]" {
		t.Errorf("file = %q, %v; want []", data, err)
	}
}

// fullHistory is a history at its cap, with titles and artists the length of
// real ones.
func fullHistory() []Entry {
	var h []Entry
	for i := range MaxEntries {
		h = Append(h, Entry{VideoID: fmt.Sprintf("dQw4w9WgX%02d", i%100), Title: "A fairly long track title (Remastered 2011)", Artist: "Some Artist & Another Artist", PlayedAt: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	return h
}

// BenchmarkWrite and BenchmarkRead are what a play and a launch cost with the
// history full; the file's size is reported with them.
func BenchmarkWrite(b *testing.B) {
	h, path := fullHistory(), filepath.Join(b.TempDir(), "history.json")
	b.ReportAllocs()
	for b.Loop() {
		if err := Write(path, h); err != nil {
			b.Fatal(err)
		}
	}
	info, _ := os.Stat(path)
	b.ReportMetric(float64(info.Size())/1024, "file-KB")
}

func BenchmarkRead(b *testing.B) {
	path := filepath.Join(b.TempDir(), "history.json")
	if err := Write(path, fullHistory()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if got := Read(path); len(got) != MaxEntries {
			b.Fatalf("read %d entries", len(got))
		}
	}
}

func BenchmarkSummarize(b *testing.B) {
	h, now := fullHistory(), time.Now()
	b.ReportAllocs()
	for b.Loop() {
		Summarize(h, now, 30)
	}
}
