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

const MaxEpisodes = 100

const MaxHistoryItems = 20

type State struct {
	Items        []youtube.MusicItem `json:"items"`
	Index        int                 `json:"index"`
	RecStart     int                 `json:"recStart"`
	PlayNext     string              `json:"playNext,omitempty"`
	Source       string              `json:"source,omitempty"`
	PlaylistMore string              `json:"playlistMore,omitempty"`
	Position     time.Duration       `json:"position,omitempty"`
	Dislikes     []string            `json:"dislikes,omitempty"`
	Episodes     []Episode           `json:"episodes,omitempty"`
}

type Episode struct {
	VideoID  string        `json:"videoId"`
	Position time.Duration `json:"position"`
}

func (s *State) Empty() bool { return len(s.Items) == 0 }

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
	out.Source, out.PlaylistMore = s.Source, s.PlaylistMore
	for _, id := range s.Dislikes {
		if slices.ContainsFunc(out.Items, func(t youtube.MusicItem) bool { return t.VideoID == id }) {
			out.Dislikes = append(out.Dislikes, id)
		}
	}
	if s.Position > 0 {
		out.Position = s.Position
	}
	return out.Window()
}

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

const writeDelay = 2 * time.Second

type Writer struct {
	Path string

	mu      sync.Mutex
	next    *State
	running bool
	done    chan struct{}
	flush   chan struct{}
}

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
