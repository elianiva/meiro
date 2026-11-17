package lastfm

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/fsutil"
)

const (
	MaxPending = 2000
	MaxAge     = 13 * 24 * time.Hour
)

type stored struct {
	Artist          string `json:"artist"`
	Title           string `json:"title"`
	Album           string `json:"album,omitempty"`
	DurationSeconds int    `json:"duration,omitempty"`
	StartedUnix     int64  `json:"started"`
	Chosen          bool   `json:"chosen,omitempty"`
}

func store(s Scrobble) stored {
	return stored{s.Artist, s.Title, s.Album, int(s.Duration / time.Second), s.Started.Unix(), s.Chosen}
}

func (s stored) scrobble() Scrobble {
	return Scrobble{
		Track:   Track{Artist: s.Artist, Title: s.Title, Album: s.Album, Duration: time.Duration(s.DurationSeconds) * time.Second},
		Started: time.Unix(s.StartedUnix, 0),
		Chosen:  s.Chosen,
	}
}

type Outbox struct {
	path string

	mu      sync.Mutex
	items   []stored
	dirty   bool
	running bool
	done    chan struct{}
}

func OpenOutbox(path string) *Outbox {
	o := &Outbox{path: path}
	if path == "" {
		return o
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("last.fm: reading pending scrobbles: %v", err)
		}
		return o
	}
	if err := json.Unmarshal(data, &o.items); err != nil {
		log.Printf("last.fm: pending scrobbles in %s are not readable, starting fresh: %v", path, err)
		o.items = nil
	}
	return o
}

func (o *Outbox) Add(s Scrobble) {
	o.mu.Lock()
	o.items = append(o.items, store(s))
	if over := len(o.items) - MaxPending; over > 0 {
		o.items = slices.Delete(o.items, 0, over)
	}
	o.changedLocked()
	o.mu.Unlock()
}

func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.items)
}

func (o *Outbox) Batch(now time.Time) []Scrobble {
	o.mu.Lock()
	defer o.mu.Unlock()
	cutoff := now.Add(-MaxAge).Unix()
	if n := len(o.items); n > 0 {
		o.items = slices.DeleteFunc(o.items, func(s stored) bool { return s.StartedUnix < cutoff })
		if len(o.items) != n {
			o.changedLocked()
		}
	}
	batch := make([]Scrobble, 0, min(len(o.items), MaxBatch))
	for _, s := range o.items[:min(len(o.items), MaxBatch)] {
		batch = append(batch, s.scrobble())
	}
	return batch
}

func (o *Outbox) Done(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n = min(n, len(o.items))
	if n <= 0 {
		return
	}
	o.items = slices.Delete(o.items, 0, n)
	o.changedLocked()
}

func (o *Outbox) Clear() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.items) == 0 {
		return
	}
	o.items = nil
	o.changedLocked()
}

func (o *Outbox) changedLocked() {
	if o.path == "" {
		return
	}
	o.dirty = true
	if o.running {
		return
	}
	o.running, o.done = true, make(chan struct{})
	go o.drain(o.done)
}

func (o *Outbox) drain(done chan struct{}) {
	defer close(done)
	for {
		o.mu.Lock()
		if !o.dirty {
			o.running = false
			o.mu.Unlock()
			return
		}
		items := slices.Clone(o.items)
		o.dirty = false
		o.mu.Unlock()
		if items == nil {
			items = []stored{}
		}
		data, err := json.Marshal(items)
		if err == nil {
			err = fsutil.WriteFileAtomic(o.path, data, 0o600)
		}
		if err != nil {
			log.Printf("last.fm: saving pending scrobbles: %v", err)
		}
	}
}

func (o *Outbox) Wait() {
	o.mu.Lock()
	done := o.done
	running := o.running
	o.mu.Unlock()
	if running {
		<-done
	}
}
