package offline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/youtube"
)

const indexName = "downloads.json"

type Library struct {
	Songs       []Song       `json:"songs"`
	Collections []Collection `json:"collections"`

	index map[string]int
}

type Song struct {
	Item   youtube.MusicItem `json:"item"`
	File   string            `json:"file"`
	Single bool              `json:"single,omitempty"`
}

type Collection struct {
	Item   youtube.MusicItem `json:"item"`
	Tracks []string          `json:"tracks"`
}

func (c Collection) ID() string { return c.Item.Target().ID }

func Read(dir string) Library {
	proc.SweepStale(dir, nil, ".audio-")
	var lib Library
	data, err := os.ReadFile(filepath.Join(dir, indexName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		log.Printf("reading downloads: %v", err)
		return lib
	default:
		if err := json.Unmarshal(data, &lib); err != nil {
			log.Printf("downloads index in %s is not readable, starting fresh: %v", dir, err)
			return Library{}
		}
	}
	lib.reindex()
	lib.sweep(dir)
	return lib
}

func (l *Library) sweep(dir string) {
	listed := make(map[string]bool, len(l.Songs))
	for _, song := range l.Songs {
		listed[song.File] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		key, _, _ := strings.Cut(name, ".")
		if entry.IsDir() || listed[name] || len(key) != sha256.Size*2 {
			continue
		}
		if _, err := hex.DecodeString(key); err != nil {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			log.Printf("removing unlisted download %s: %v", name, err)
		}
	}
}

func Write(dir string, lib Library) error {
	if lib.Songs == nil {
		lib.Songs = []Song{}
	}
	if lib.Collections == nil {
		lib.Collections = []Collection{}
	}
	data, err := json.Marshal(lib)
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(dir, indexName), data, 0o600)
}

func (l *Library) reindex() {
	l.index = make(map[string]int, len(l.Songs))
	for i, song := range l.Songs {
		l.index[song.Item.VideoID] = i
	}
}

func (l *Library) Song(videoID string) (Song, bool) {
	i, ok := l.index[videoID]
	if !ok {
		return Song{}, false
	}
	return l.Songs[i], true
}

func (l *Library) Add(song Song) {
	songs := make([]Song, 0, len(l.Songs)+1)
	songs = append(songs, song)
	for _, s := range l.Songs {
		if s.Item.VideoID != song.Item.VideoID {
			songs = append(songs, s)
		}
	}
	l.Songs = songs
	l.reindex()
}

func (l *Library) Remove(videoID string) (string, bool) {
	song, ok := l.Song(videoID)
	if !ok {
		return "", false
	}
	l.Songs = slices.DeleteFunc(slices.Clone(l.Songs), func(s Song) bool { return s.Item.VideoID == videoID })
	l.reindex()
	return song.File, true
}

func (l *Library) Collection(id string) (Collection, bool) {
	for _, c := range l.Collections {
		if c.ID() == id {
			return c, true
		}
	}
	return Collection{}, false
}

func (l *Library) AddCollection(c Collection) {
	collections := make([]Collection, 0, len(l.Collections)+1)
	collections = append(collections, c)
	for _, other := range l.Collections {
		if other.ID() != c.ID() {
			collections = append(collections, other)
		}
	}
	l.Collections = collections
}

func (l *Library) SetCollectionTracks(id string, tracks []string) bool {
	for i := range l.Collections {
		if l.Collections[i].ID() == id {
			l.Collections[i].Tracks = tracks
			return true
		}
	}
	return false
}

func (l *Library) RemoveCollection(id string) []string {
	removed, ok := l.Collection(id)
	if !ok {
		return nil
	}
	l.Collections = slices.DeleteFunc(slices.Clone(l.Collections), func(c Collection) bool { return c.ID() == id })
	var files []string
	for _, videoID := range removed.Tracks {
		song, ok := l.Song(videoID)
		if !ok || song.Single || l.InCollection(videoID) {
			continue
		}
		if file, ok := l.Remove(videoID); ok {
			files = append(files, file)
		}
	}
	return files
}

func (l *Library) InCollection(videoID string) bool {
	for _, c := range l.Collections {
		if slices.Contains(c.Tracks, videoID) {
			return true
		}
	}
	return false
}

func (l *Library) Tracks(c Collection) []youtube.MusicItem {
	var items []youtube.MusicItem
	for _, videoID := range c.Tracks {
		if song, ok := l.Song(videoID); ok {
			items = append(items, song.Item)
		}
	}
	return items
}

func Download(ctx context.Context, dir, videoID, streamURL string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	temp, err := audio.Fetch(ctx, streamURL, dir)
	if err != nil {
		return "", err
	}
	defer fsutil.RemoveAll(filepath.Dir(temp))
	sum := sha256.Sum256([]byte(videoID))
	name := hex.EncodeToString(sum[:]) + filepath.Ext(temp)
	if err := os.Rename(temp, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	return name, nil
}

type Writer struct {
	mu      sync.Mutex
	dir     string
	next    Library
	pending bool
	running bool
	done    chan struct{}
}

func (w *Writer) Save(dir string, lib Library) {
	if dir == "" {
		return
	}
	w.mu.Lock()
	w.dir, w.next, w.pending = dir, lib, true
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
		dir, lib := w.dir, w.next
		w.next, w.pending = Library{}, false
		w.mu.Unlock()
		if err := Write(dir, lib); err != nil {
			log.Printf("saving downloads: %v", err)
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
