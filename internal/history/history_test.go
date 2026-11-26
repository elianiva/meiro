package history

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestSummarizeCountsTheRangeOnly(t *testing.T) {
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, time.UTC)
	entries := []Entry{
		{VideoID: "a", Title: "A", Artist: "X", PlayedAt: now.AddDate(0, 0, -30)},
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

func TestGroupByDayUsesTheLocationAndKeepsTheNewestFirst(t *testing.T) {
	jakarta := time.FixedZone("UTC+7", 7*60*60)
	entries := []Entry{
		{VideoID: "old", PlayedAt: time.Date(2026, 3, 8, 23, 30, 0, 0, jakarta)},
		{VideoID: "first", PlayedAt: time.Date(2026, 3, 9, 0, 30, 0, 0, jakarta)},
		{VideoID: "second", PlayedAt: time.Date(2026, 3, 9, 23, 0, 0, 0, jakarta)},
	}
	groups := GroupByDay(entries, jakarta)
	if len(groups) != 2 {
		t.Fatalf("groups = %+v, want two days", groups)
	}
	if want := time.Date(2026, 3, 9, 0, 0, 0, 0, jakarta); !groups[0].Day.Equal(want) {
		t.Errorf("newest day = %v, want %v", groups[0].Day, want)
	}
	if got := ids(groups[0].Entries); !slices.Equal(got, []string{"second", "first"}) {
		t.Errorf("newest day holds %v, want second then first", got)
	}
	if got := ids(groups[1].Entries); !slices.Equal(got, []string{"old"}) {
		t.Errorf("older day holds %v, want old", got)
	}

	// In UTC the first play still belongs to 8 March, so it joins the older day.
	utc := GroupByDay(entries, time.UTC)
	if len(utc) != 2 || !utc[0].Day.Equal(time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("UTC groups = %+v", utc)
	}
	if got := ids(utc[1].Entries); !slices.Equal(got, []string{"first", "old"}) {
		t.Errorf("UTC older day holds %v, want first then old", got)
	}
}

func ids(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, entry := range entries {
		out[i] = entry.VideoID
	}
	return out
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

func fullHistory() []Entry {
	var h []Entry
	for i := range MaxEntries {
		h = Append(h, Entry{VideoID: fmt.Sprintf("dQw4w9WgX%02d", i%100), Title: "A fairly long track title (Remastered 2011)", Artist: "Some Artist & Another Artist", PlayedAt: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	return h
}

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
