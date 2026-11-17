package history

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
)

const MaxEntries = 5000

type Entry struct {
	VideoID  string    `json:"videoId"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

func Append(history []Entry, entries ...Entry) []Entry {
	history = append(history, entries...)
	if len(history) > MaxEntries {
		history = slices.Delete(history, 0, len(history)-MaxEntries)
	}
	return history
}

func Read(path string) []Entry {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("reading listening history: %v", err)
		}
		return nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		log.Printf("listening history in %s is not readable, starting fresh: %v", path, err)
		return nil
	}
	return entries
}

func Write(path string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

type Writer struct {
	mu      sync.Mutex
	path    string
	next    []Entry
	pending bool
	running bool
	done    chan struct{}
}

func (w *Writer) Save(path string, entries []Entry) {
	if path == "" {
		return
	}
	entries = slices.Clone(entries)
	w.mu.Lock()
	w.path, w.next, w.pending = path, entries, true
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.done = make(chan struct{})
	done := w.done
	w.mu.Unlock()
	go w.drain(done)
}

func (w *Writer) drain(done chan struct{}) {
	defer close(done)
	for {
		w.mu.Lock()
		if !w.pending {
			w.running = false
			w.mu.Unlock()
			return
		}
		path, entries := w.path, w.next
		w.next, w.pending = nil, false
		w.mu.Unlock()
		if err := Write(path, entries); err != nil {
			log.Printf("saving listening history: %v", err)
		}
	}
}

func (w *Writer) Wait() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	done := w.done
	w.mu.Unlock()
	<-done
}

type Day struct {
	Day   time.Time
	Plays int
}

type Count struct {
	Title  string
	Artist string
	Plays  int
}

type Summary struct {
	Plays      int
	Artists    int
	ByDay      []Day
	TopTracks  []Count
	TopArtists []Count
}

func Summarize(entries []Entry, now time.Time, days int) Summary {
	var summary Summary
	tracks := make(map[string]*Count)
	artists := make(map[string]*Count)
	cutoff := startOfDay(now).AddDate(0, 0, -days+1)
	summary.ByDay = make([]Day, days)
	for d := range summary.ByDay {
		summary.ByDay[d].Day = cutoff.AddDate(0, 0, d)
	}
	firstDay := CalendarDay(cutoff)
	for _, e := range entries {
		if e.PlayedAt.Before(cutoff) {
			continue
		}
		summary.Plays++
		key := e.VideoID
		if key == "" {
			key = e.Title
		}
		tc := tracks[key]
		if tc == nil {
			tc = &Count{Title: e.Title, Artist: e.Artist}
			tracks[key] = tc
		}
		tc.Plays++
		if e.Artist != "" {
			ac := artists[e.Artist]
			if ac == nil {
				ac = &Count{Title: e.Artist}
				artists[e.Artist] = ac
			}
			ac.Plays++
		}
		if i := CalendarDay(e.PlayedAt) - firstDay; i >= 0 && i < days {
			summary.ByDay[i].Plays++
		}
	}
	summary.Artists = len(artists)
	summary.TopTracks = top(tracks, 5)
	summary.TopArtists = top(artists, 5)
	return summary
}

func CalendarDay(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

func top(counts map[string]*Count, n int) []Count {
	all := make([]Count, 0, len(counts))
	for _, c := range counts {
		all = append(all, *c)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Plays != all[j].Plays {
			return all[i].Plays > all[j].Plays
		}
		return all[i].Title < all[j].Title
	})
	return all[:min(len(all), n)]
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
