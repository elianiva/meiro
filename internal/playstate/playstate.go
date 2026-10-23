// Package playstate is what was playing when the app closed: the queue, the
// track in it, and how far into the track the music had come. It is kept
// beside the settings and restored, paused, at the next launch.
package playstate

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/youtube"
)

// MaxEpisodes is how many part-heard podcast episodes are remembered. The
// oldest is forgotten first.
const MaxEpisodes = 100

// MaxHistoryItems is how many tracks before the current one a saved queue
// keeps. Upcoming tracks are kept so the queue can continue after a restart.
const MaxHistoryItems = 20

// State is a queue and the place in it. The zero value is no queue.
type State struct {
	Items []youtube.MusicItem `json:"items"`
	// Index is the place of the current track in Items.
	Index int `json:"index"`
	// RecStart is the place of the first generated track, or -1.
	RecStart int `json:"recStart"`
	// PlayNext is the track the user asked to play next, if any.
	PlayNext string `json:"playNext,omitempty"`
	// Source names where the chosen tracks came from.
	Source string `json:"source,omitempty"`
	// Position is how far into the current track playback had come.
	Position time.Duration `json:"position,omitempty"`
	// Episodes are the podcast episodes left part-way through, the one left
	// last first. They outlive the queue they were heard from.
	Episodes []Episode `json:"episodes,omitempty"`
}

// Episode is where listening to a podcast episode stopped.
type Episode struct {
	VideoID  string        `json:"videoId"`
	Position time.Duration `json:"position"`
}

// Empty reports whether the state holds no queue.
func (s *State) Empty() bool { return len(s.Items) == 0 }

// Window drops old played tracks, keeping at most MaxHistoryItems before the
// current one. It keeps every upcoming track, so a long queue can continue
// after a restart. The receiver is not changed.
func (s State) Window() State {
	if s.Index <= MaxHistoryItems {
		return s
	}
	start := s.Index - MaxHistoryItems
	s.Items = s.Items[start:]
	s.Index -= start
	if s.RecStart >= 0 {
		s.RecStart = max(s.RecStart-start, 0)
		if s.RecStart >= len(s.Items) {
			s.RecStart = -1
		}
	}
	return s
}

// sanitize drops what a file may hold that the queue cannot: tracks without
// an ID, a place outside the queue, a play-next mark for a track that is not
// there. It returns a state with no queue when the current track is gone; the
// episodes stay.
func (s State) sanitize() State {
	var episodes []Episode
	for _, episode := range s.Episodes {
		if episode.VideoID != "" && episode.Position > 0 && len(episodes) < MaxEpisodes {
			episodes = append(episodes, episode)
		}
	}
	if s.Index < 0 || s.Index >= len(s.Items) || s.Items[s.Index].VideoID == "" {
		return State{Episodes: episodes}
	}
	out := State{Episodes: episodes}
	out.RecStart = -1
	for i, item := range s.Items {
		if item.VideoID == "" {
			continue
		}
		if i == s.Index {
			out.Index = len(out.Items)
		}
		if i == s.RecStart {
			out.RecStart = len(out.Items)
		}
		out.Items = append(out.Items, item)
	}
	if slices.ContainsFunc(out.Items, func(t youtube.MusicItem) bool { return t.VideoID == s.PlayNext }) {
		out.PlayNext = s.PlayNext
	}
	out.Source = s.Source
	if s.Position > 0 {
		out.Position = s.Position
	}
	return out.Window()
}

// Load reads the saved state. A missing or unreadable file is no state.
func Load(path string) State {
	if path == "" {
		return State{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("reading playback state: %v", err)
		}
		return State{}
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		log.Printf("playback state in %s is not readable, starting empty: %v", path, err)
		return State{}
	}
	return s.sanitize()
}

func (s State) save(path string) error {
	data, err := json.Marshal(s.Window())
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

// writeDelay gives a burst of changes time to settle before the final state
// is written.
const writeDelay = 2 * time.Second

// Writer saves the state off the main thread, one write at a time. A save
// asked for while another waits replaces it, so a queue edited many times in
// a moment costs one write. The zero value writes nowhere; set Path.
type Writer struct {
	// Path is where the state is written; empty keeps it in memory.
	Path string

	mu      sync.Mutex
	next    *State
	running bool
	done    chan struct{}
	flush   chan struct{}
}

// Save writes s soon. It keeps its own copy of the tracks.
func (w *Writer) Save(s State) {
	if w.Path == "" {
		return
	}
	s.Items, s.Episodes = slices.Clone(s.Items), slices.Clone(s.Episodes)
	w.mu.Lock()
	w.next = &s
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.done = make(chan struct{})
	w.flush = make(chan struct{}, 1)
	done := w.done
	w.mu.Unlock()
	go w.drain(done)
}

func (w *Writer) drain(done chan struct{}) {
	defer close(done)
	for {
		w.mu.Lock()
		if w.next == nil {
			w.running = false
			w.mu.Unlock()
			return
		}
		flush := w.flush
		w.mu.Unlock()
		select {
		case <-time.After(writeDelay):
		case <-flush:
		}

		w.mu.Lock()
		s := w.next
		w.next = nil
		w.mu.Unlock()
		if err := s.save(w.Path); err != nil {
			log.Printf("saving playback state: %v", err)
		}
	}
}

// Wait writes the latest pending state at once and returns when it is on
// disk.
func (w *Writer) Wait() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}
	done := w.done
	select {
	case w.flush <- struct{}{}:
	default:
	}
	w.mu.Unlock()
	<-done
}
