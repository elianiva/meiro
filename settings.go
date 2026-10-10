package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// settings is what the user chose, kept between runs.
type settings struct {
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
}

func defaultSettings() settings {
	return settings{Seed: "#6750a4", Mode: "system", Volume: 70, AutoPlay: true, CacheSongs: defaultAudioCacheLimit}
}

// clone returns a copy that shares no memory with s, for work that outlives
// the caller's use of the settings.
func (s *settings) clone() settings {
	c := *s
	c.Recent = slices.Clone(s.Recent)
	return c
}

// config returns the theme configuration the settings describe.
func (s *settings) config() m3.Config {
	seed := m3.DefaultSeed
	if parsed, ok := parseSeed(s.Seed); ok {
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
	return m3.Config{Seed: seed, Mode: mode, Style: style, Font: uiFont}
}

func (s *settings) setMode(mode m3.Mode) {
	s.Mode = strings.ToLower(mode.String())
}

// parseSeed reads "#rrggbb", and says no to anything else a file may hold.
func parseSeed(text string) (ui.Color, bool) {
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

func seedHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// remember puts a submitted search at the top of the recent ones.
func (s *settings) remember(query string) {
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

// loadSettings reads the settings file, and returns the defaults for one that
// is missing or unreadable.
func loadSettings(path string) settings {
	s := defaultSettings()
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
	read.CacheSongs = validAudioCacheLimit(read.CacheSongs)
	if _, ok := parseSeed(read.Seed); !ok {
		read.Seed = s.Seed
	}
	if read.Mode == "" {
		read.Mode = s.Mode
	}
	return read
}

// save writes the settings atomically: see writeFileAtomic.
func (s *settings) save(path string) error {
	if path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

// settingsWriter saves settings off the main thread, one write at a time and
// in the order they were asked for. A save asked for while another runs
// replaces any waiting one, so a slider dragged across the screen costs a
// few writes, not one for each frame.
type settingsWriter struct {
	mu      sync.Mutex
	next    *settings
	path    string
	running bool
	idle    sync.WaitGroup
	write   func(*settings, string) error
}

// save writes s to path soon. It keeps its own copy of s.
func (w *settingsWriter) save(s *settings, path string) {
	c := s.clone()
	w.mu.Lock()
	w.next, w.path = &c, path
	if w.running {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.idle.Add(1)
	w.mu.Unlock()
	go w.drain()
}

func (w *settingsWriter) drain() {
	defer w.idle.Done()
	for {
		w.mu.Lock()
		s, path := w.next, w.path
		w.next = nil
		if s == nil {
			w.running = false
			w.mu.Unlock()
			return
		}
		write := w.write
		w.mu.Unlock()
		if write == nil {
			write = (*settings).save
		}
		if err := write(s, path); err != nil {
			log.Printf("saving settings: %v", err)
		}
	}
}

// wait returns once every save asked for so far has been written.
func (w *settingsWriter) wait() { w.idle.Wait() }
