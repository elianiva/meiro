// Package history keeps what was played on this device, and sums it up for
// the stats page.
package history

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"slices"
	"sort"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
)

// MaxEntries caps the history: about a year of heavy listening. Old entries
// go first when it overflows.
const MaxEntries = 5000

// Entry is one played track.
type Entry struct {
	VideoID  string    `json:"videoId"`
	Title    string    `json:"title,omitempty"`
	Artist   string    `json:"artist,omitempty"`
	PlayedAt time.Time `json:"playedAt"`
}

// Append adds entries to the end of a history and drops its oldest entries
// beyond MaxEntries, in place.
func Append(history []Entry, entries ...Entry) []Entry {
	history = append(history, entries...)
	if len(history) > MaxEntries {
		// Slide the newest entries down in place: reallocating the whole
		// history on every play once it is full copied it all each time.
		history = slices.Delete(history, 0, len(history)-MaxEntries)
	}
	return history
}

// Read reads a history file. A missing or unreadable file is an empty
// history rather than an error the user must see.
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

// Write replaces a history file atomically.
func Write(path string, entries []Entry) error {
	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

// Day is one day of listening.
type Day struct {
	Day   time.Time
	Plays int
}

// Count is the plays of one track or artist.
type Count struct {
	Title  string
	Artist string
	Plays  int
}

// Summary is what the stats page shows.
type Summary struct {
	Plays      int
	Artists    int
	ByDay      []Day
	TopTracks  []Count
	TopArtists []Count
}

// Summarize folds entries into totals, daily counts for the days days ending
// on the day of now, and the five most played tracks and artists.
func Summarize(entries []Entry, now time.Time, days int) Summary {
	var summary Summary
	tracks := make(map[string]*Count)
	artists := make(map[string]*Count)
	cutoff := startOfDay(now).AddDate(0, 0, -days+1)
	summary.ByDay = make([]Day, days)
	for d := range summary.ByDay {
		summary.ByDay[d].Day = cutoff.AddDate(0, 0, d)
	}
	// Days are told apart by their calendar number, so placing a play costs
	// arithmetic instead of formatting its date into a string to look up.
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

// CalendarDay numbers the day t falls on, in its own zone, counting from a
// fixed origin, so consecutive days differ by one whatever the zone's offset.
func CalendarDay(t time.Time) int {
	y, m, d := t.Date()
	return int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400)
}

// top keeps the n highest counts, most plays first.
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

// startOfDay is midnight of the day t falls on, in its own zone.
func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}
