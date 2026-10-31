// Package settings is what the user chose, kept between runs.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"runtime"
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
	// Matugen takes the colours from colors.json, which matugen writes, in
	// place of the seed. Dynamic and Matugen are never both on.
	Matugen bool `json:"matugen,omitempty"`
	// RailExpanded keeps the navigation rail open, with labels beside icons.
	RailExpanded bool `json:"railExpanded"`
	// Zoom is how large the interface is, as a percentage: 100 shows it at
	// its own size.
	Zoom int `json:"zoom,omitempty"`
	// Volume is the player's, from 0 to 100.
	Volume float64 `json:"volume"`
	// LogVolume makes the volume slider follow a logarithmic curve, with finer
	// steps at low volumes. Off keeps the slider linear.
	LogVolume bool `json:"logVolume,omitempty"`
	// AutoPlay adds YouTube Music's generated queue after the selected tracks.
	AutoPlay bool `json:"autoPlay"`
	// AutoSkipDisliked skips tracks known to have a thumbs-down.
	AutoSkipDisliked bool `json:"autoSkipDisliked,omitempty"`
	// NormalizeLoudness applies YouTube's per-track loudness correction when signed in.
	NormalizeLoudness bool `json:"normalizeLoudness"`
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
	// StaticLyrics shows lyrics as plain text even when a track has them timed.
	StaticLyrics bool `json:"staticLyrics,omitempty"`
	// NotifyTrack shows a desktop notification when a new track starts,
	// except on macOS, where playback is presented by the notch or menu bar.
	NotifyTrack bool `json:"notifyTrack"`
	// GlobalShortcut plays or pauses from anywhere with
	// CmdOrCtrl+Shift+Space.
	GlobalShortcut bool `json:"globalShortcut"`
	// ShowTray keeps an icon with transport controls on platforms with a tray.
	// On macOS the menu bar follows ShowNotch and display availability instead.
	ShowTray bool `json:"showTray"`
	// ShowNotch prefers the compact player around a MacBook's camera housing;
	// macOS uses the menu bar mini player when it is off or no notch is available.
	ShowNotch bool `json:"showNotch"`
	// TrayMark is the menu bar icon style on macOS and Linux.
	TrayMark TrayMark `json:"trayMark"`
	// DiscordPresence shares the current track with the running Discord client.
	DiscordPresence bool `json:"discordPresence,omitempty"`
	// CloseToTray hides the window instead of quitting when the user closes
	// it. macOS reopens from the Dock, notch or menu bar; other platforms keep
	// a tray icon.
	// Empty means the user has not chosen yet, so closing asks.
	CloseToTray *bool `json:"closeToTray,omitempty"`
	// OpenAtLogin starts Meiro when the user logs in.
	OpenAtLogin bool `json:"openAtLogin"`
	// LastFMUser is the Last.fm account plays are scrobbled to, or empty when
	// none is connected. Its session key is not kept here: see
	// secretfile.File.
	LastFMUser string `json:"lastfmUser,omitempty"`
	// LastFMPaused keeps the account connected and stops scrobbling.
	LastFMPaused bool `json:"lastfmPaused,omitempty"`
	// Shortcuts holds the keyboard shortcuts the user changed, by action ID
	// (see package keymap), written as "Shift+Cmd+K". An action not listed
	// keeps its default.
	Shortcuts map[string]string `json:"shortcuts,omitempty"`
	// EQOn enables the equaliser's bands.
	EQOn bool `json:"eqOn"`
	// EQBands holds each band's gain in dB, low to high, one per the player's
	// equaliser frequencies.
	EQBands []float64 `json:"eqBands,omitempty"`
	// Crossfade fades the end of a track into the start of the next, for
	// CrossfadeSeconds.
	Crossfade        bool `json:"crossfade,omitempty"`
	CrossfadeSeconds int  `json:"crossfadeSeconds,omitempty"`
	// PodcastSpeed is how fast podcast episodes play, and MusicSpeed how fast
	// everything else does, 1 or 0 for their own pace.
	PodcastSpeed float64 `json:"podcastSpeed,omitempty"`
	MusicSpeed   float64 `json:"musicSpeed,omitempty"`
}

// TrayMark names one of the menu bar icon styles.
type TrayMark string

const (
	// TrayMarkWordmark uses the optically centered Meiro wordmark.
	TrayMarkWordmark TrayMark = "wordmark"
	// TrayMarkOMark uses the smaller standalone O mark.
	TrayMarkOMark TrayMark = "o"
)

// Defaults is what a first run starts with.
func Defaults() Settings {
	return Settings{
		Seed: "#6750a4", Mode: "system", Volume: 70, AutoPlay: true, CacheSongs: audio.DefaultLimit, NotifyTrack: runtime.GOOS != "darwin", ShowTray: runtime.GOOS != "darwin", TrayMark: TrayMarkWordmark,
		Zoom: DefaultZoom, ShowNotch: true, CrossfadeSeconds: DefaultCrossfade,
	}
}

// The crossfade lengths the settings offer, in seconds.
const (
	MinCrossfade     = 1
	MaxCrossfade     = 12
	DefaultCrossfade = 5
)

// ClampCrossfade keeps a crossfade length between the ones the settings
// offer, and reads the zero value of a settings file that has none as the
// default.
func ClampCrossfade(seconds int) int {
	if seconds == 0 {
		return DefaultCrossfade
	}
	return min(max(seconds, MinCrossfade), MaxCrossfade)
}

// The interface zooms the app offers, as percentages, and the step between
// them.
const (
	// MinZoom is the smallest the interface gets, and MaxZoom the largest.
	MinZoom = 80
	MaxZoom = 200
	// ZoomStep is how much one shortcut or button moves the zoom.
	ZoomStep = 10
	// DefaultZoom is the interface at its own size.
	DefaultZoom = 100
)

// ClampZoom keeps a zoom between the ones the app offers, and reads the zero
// value of a settings file that has none as the default.
func ClampZoom(percent int) int {
	if percent == 0 {
		return DefaultZoom
	}
	return min(max(percent, MinZoom), MaxZoom)
}

// clone returns a copy that shares no memory with s, for work that outlives
// the caller's use of the settings.
func (s *Settings) clone() Settings {
	c := *s
	c.Recent = slices.Clone(s.Recent)
	c.Shortcuts = maps.Clone(s.Shortcuts)
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
	if path == "" {
		return Defaults()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("reading settings: %v", err)
		}
		return Defaults()
	}
	s, err := Parse(data)
	if err != nil {
		// The next save replaces the file, so say what was lost.
		log.Printf("settings in %s are not readable, starting from the defaults: %v", path, err)
		return Defaults()
	}
	return s
}

// Parse reads settings from data and repairs values a hand-edited file may
// hold. It reports an error for data that is not settings JSON, so a watcher
// can wait for the rest of a file that is still being written.
func Parse(data []byte) (Settings, error) {
	// Start from defaults so settings added later get their default when an
	// older settings file does not contain the new field yet.
	defaults := Defaults()
	read := defaults
	if err := json.Unmarshal(data, &read); err != nil {
		return defaults, err
	}
	if len(read.Recent) > maxRecent {
		read.Recent = read.Recent[:maxRecent]
	}
	if read.Volume < 0 || read.Volume > 100 {
		read.Volume = defaults.Volume
	}
	read.CacheSongs = audio.ValidLimit(read.CacheSongs)
	if _, ok := ParseSeed(read.Seed); !ok {
		read.Seed = defaults.Seed
	}
	if read.Mode == "" {
		read.Mode = defaults.Mode
	}
	if read.TrayMark != TrayMarkWordmark && read.TrayMark != TrayMarkOMark {
		read.TrayMark = defaults.TrayMark
	}
	read.Zoom = ClampZoom(read.Zoom)
	if runtime.GOOS == "darwin" {
		// macOS has no track banners; its menu bar follows the notch choice and
		// available display, not the legacy ShowTray preference.
		read.NotifyTrack, read.ShowTray = false, false
	}
	read.CrossfadeSeconds = ClampCrossfade(read.CrossfadeSeconds)
	return read, nil
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

// Pending reports whether a save is waiting or on its way to disk.
func (w *Writer) Pending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
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

// ParseColors reads the colours file: an object with "light" and "dark"
// objects that map role names, such as "primary" or "surface_container_high",
// to "#rrggbb". It says no to a file that is not that, or that names a colour
// it cannot read or a role that does not exist, or one with no colours at all, so a typo is not lost quietly.
func ParseColors(data []byte) (*m3.Colors, error) {
	var file struct {
		Light, Dark map[string]string
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	if len(file.Light) == 0 && len(file.Dark) == 0 {
		return nil, errors.New("no colours: expected \"light\" or \"dark\" objects")
	}
	read := func(appearance string, in map[string]string) (map[string]ui.Color, error) {
		out := make(map[string]ui.Color, len(in))
		var scratch m3.Scheme
		for role, text := range in {
			if scratch.Role(role) == nil {
				return nil, fmt.Errorf("%s: %q is not a colour role", appearance, role)
			}
			colour, ok := ParseSeed(text)
			if !ok {
				return nil, fmt.Errorf("%s.%s: %q is not #rrggbb", appearance, role, text)
			}
			out[role] = colour
		}
		return out, nil
	}
	light, err := read("light", file.Light)
	if err != nil {
		return nil, err
	}
	dark, err := read("dark", file.Dark)
	if err != nil {
		return nil, err
	}
	return &m3.Colors{Light: light, Dark: dark}, nil
}
