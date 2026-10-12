// Package settings is what the user chose, kept between runs.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/fsutil"
	"github.com/elianiva/meiro/m3"
)

// Settings is what the user chose. The zero value is not the defaults; use
// Defaults or Load.
type Settings struct {
	// Seed is the colour the theme grows from, as "#rrggbb".
	Seed string `json:"seed"`
	// Mode is "system", "light" or "dark".
	Mode string `json:"mode"`
	// Style is how the seed is spent: see m3.Style.
	Style int `json:"style"`
	// Dynamic takes the seed from the artwork of the track playing.
	Dynamic bool `json:"dynamic"`
	// RailExpanded keeps the navigation rail open, with labels beside icons.
	RailExpanded bool `json:"railExpanded"`
	// Volume is the player's, from 0 to 100.
	Volume float64 `json:"volume"`
	// AutoPlay adds YouTube Music's generated queue after the selected tracks.
	AutoPlay bool `json:"autoPlay"`
	// CacheSongs is how many recently played audio files to keep on disk.
	CacheSongs int `json:"cacheSongs"`
	// CacheDirectory is the parent folder for the app-managed audio cache.
	CacheDirectory string `json:"cacheDirectory,omitempty"`
	// Recent holds the searches submitted, newest first.
	Recent []string `json:"recent,omitempty"`
	// Channel is the signed-in account's channel to act as, or empty for the
	// account's default one.
	Channel string `json:"channel,omitempty"`
	// SignInSource is the browser the session was imported from, or empty for
	// one that was pasted. YouTube retires the cookie of a browser session as
	// the browser goes on using it, so this is where the session is read again
	// when YouTube stops accepting the saved one.
	SignInSource string `json:"signInSource,omitempty"`
	// NotifyTrack shows a desktop notification when a new track starts.
	NotifyTrack bool `json:"notifyTrack"`
	// GlobalShortcut plays or pauses from anywhere with
	// CmdOrCtrl+Shift+Space.
	GlobalShortcut bool `json:"globalShortcut"`
	// ShowTray keeps an icon in the menu bar with transport controls.
	ShowTray bool `json:"showTray"`
	// OpenAtLogin starts Meiro when the user logs in.
	OpenAtLogin bool `json:"openAtLogin"`
}

// Defaults is what a first run starts with.
func Defaults() Settings {
	return Settings{Seed: "#6750a4", Mode: "system", Volume: 70, AutoPlay: true, CacheSongs: audio.DefaultLimit, NotifyTrack: true, ShowTray: true}
}

// clone returns a copy that shares no memory with s, for work that outlives
// the caller's use of the settings.
func (s *Settings) clone() Settings {
	c := *s
	c.Recent = slices.Clone(s.Recent)
	return c
}

// Theme returns the theme configuration the settings describe, set in font.
func (s *Settings) Theme(font string) m3.Config {
	seed := m3.DefaultSeed
	if parsed, ok := ParseSeed(s.Seed); ok {
		seed = parsed
	}
	mode := m3.System
	switch s.Mode {
	case "light":
		mode = m3.Light
	case "dark":
		mode = m3.Dark
	}
	style := m3.Style(s.Style)
	if style < 0 || int(style) >= len(m3.Styles()) {
		style = m3.TonalSpot
	}
	return m3.Config{Seed: seed, Mode: mode, Style: style, Font: font}
}

// SetMode keeps a theme mode.
func (s *Settings) SetMode(mode m3.Mode) {
	s.Mode = strings.ToLower(mode.String())
}

// ParseSeed reads "#rrggbb", and says no to anything else a file may hold.
func ParseSeed(text string) (ui.Color, bool) {
	digits, ok := strings.CutPrefix(text, "#")
	if !ok || len(digits) != 6 {
		return ui.Color{}, false
	}
	rgb, err := strconv.ParseUint(digits, 16, 32)
	if err != nil {
		return ui.Color{}, false
	}
	return ui.RGB(uint8(rgb>>16), uint8(rgb>>8), uint8(rgb)), true
}

// SeedHex writes a colour as "#rrggbb".
func SeedHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Remember puts a submitted search at the top of the recent ones.
func (s *Settings) Remember(query string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	s.Recent = slices.DeleteFunc(s.Recent, func(q string) bool { return strings.EqualFold(q, query) })
	s.Recent = append([]string{query}, s.Recent...)
	if len(s.Recent) > maxRecent {
		s.Recent = s.Recent[:maxRecent]
	}
}

// maxRecent is how many submitted searches the settings keep.
const maxRecent = 8

// Forget takes a search out of the recent ones.
func (s *Settings) Forget(query string) {
	s.Recent = slices.DeleteFunc(s.Recent, func(q string) bool { return q == query })
}

// Load reads the settings file, and returns the defaults for one that is
// missing or unreadable.
func Load(path string) Settings {
	s := Defaults()
	if path == "" {
		return s
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("reading settings: %v", err)
		}
		return s
	}
	// Start from defaults so settings added later get their default when an
	// older settings file does not contain the new field yet.
	read := s
	if err := json.Unmarshal(data, &read); err != nil {
		// The next save replaces the file, so say what was lost.
		log.Printf("settings in %s are not readable, starting from the defaults: %v", path, err)
		return s
	}
	if len(read.Recent) > maxRecent {
		read.Recent = read.Recent[:maxRecent]
	}
	if read.Volume < 0 || read.Volume > 100 {
		read.Volume = s.Volume
	}
	read.CacheSongs = audio.ValidLimit(read.CacheSongs)
	if _, ok := ParseSeed(read.Seed); !ok {
		read.Seed = s.Seed
	}
	if read.Mode == "" {
		read.Mode = s.Mode
	}
	return read
}

// save writes the settings atomically.
func (s *Settings) save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

// writeDelay gives a burst of settings changes time to settle before
// writing the final state to disk.
const writeDelay = 400 * time.Millisecond

// Writer saves settings off the main thread, one write at a time. A save
// asked for while another runs replaces any waiting one, and the writer waits
// for writeDelay before each write so a slider drag costs one write instead
// of one for each frame. The zero value writes nowhere; set Path.
type Writer struct {
	// Path is where the settings are written; empty keeps them in memory.
	Path string

	mu      sync.Mutex
	next    *Settings
	running bool
	done    chan struct{}
	flush   chan struct{}
}

// Save writes s soon. It keeps its own copy of s.
func (w *Writer) Save(s *Settings) {
	if w.Path == "" {
		return
	}
	c := s.clone()
	w.mu.Lock()
	w.next = &c
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
			log.Printf("saving settings: %v", err)
		}
	}
}

// Wait flushes the latest pending settings state and returns once its write is
// complete.
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
