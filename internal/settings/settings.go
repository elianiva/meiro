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

type Settings struct {
	MCPControl         bool              `json:"mcpControl,omitempty"`
	Seed               string            `json:"seed"`
	Mode               string            `json:"mode"`
	Style              int               `json:"style"`
	Dynamic            bool              `json:"dynamic"`
	Matugen            bool              `json:"matugen,omitempty"`
	RailExpanded       bool              `json:"railExpanded"`
	Zoom               int               `json:"zoom,omitempty"`
	Volume             float64           `json:"volume"`
	LogVolume          bool              `json:"logVolume,omitempty"`
	AutoPlay           bool              `json:"autoPlay"`
	AutoSkipDisliked   bool              `json:"autoSkipDisliked,omitempty"`
	AccountAudio       bool              `json:"accountAudio,omitempty"`
	PremiumOffered     bool              `json:"premiumOffered,omitempty"`
	ExcludeAIGenerated bool              `json:"excludeAIGenerated,omitempty"`
	NormalizeLoudness  bool              `json:"normalizeLoudness"`
	CacheSongs         int               `json:"cacheSongs"`
	CacheDirectory     string            `json:"cacheDirectory,omitempty"`
	Recent             []string          `json:"recent,omitempty"`
	Channel            string            `json:"channel,omitempty"`
	AccountIndex       int               `json:"accountIndex,omitempty"`
	AccountEmail       string            `json:"accountEmail,omitempty"`
	SignInSource       string            `json:"signInSource,omitempty"`
	StaticLyrics       bool              `json:"staticLyrics,omitempty"`
	NotifyTrack        bool              `json:"notifyTrack"`
	GlobalShortcut     bool              `json:"globalShortcut"`
	ShowTray           bool              `json:"showTray"`
	ShowNotch          bool              `json:"showNotch"`
	TrayMark           TrayMark          `json:"trayMark"`
	DiscordPresence    bool              `json:"discordPresence,omitempty"`
	CloseToTray        *bool             `json:"closeToTray,omitempty"`
	OpenAtLogin        bool              `json:"openAtLogin"`
	LastFMUser         string            `json:"lastfmUser,omitempty"`
	LastFMPaused       bool              `json:"lastfmPaused,omitempty"`
	Shortcuts          map[string]string `json:"shortcuts,omitempty"`
	EQOn               bool              `json:"eqOn"`
	EQBands            []float64         `json:"eqBands,omitempty"`
	Crossfade          bool              `json:"crossfade,omitempty"`
	CrossfadeSeconds   int               `json:"crossfadeSeconds,omitempty"`
	PodcastSpeed       float64           `json:"podcastSpeed,omitempty"`
	MusicSpeed         float64           `json:"musicSpeed,omitempty"`
	UsagePing          *bool             `json:"usagePing,omitempty"`
	UsagePingDetails   bool              `json:"usagePingDetails,omitempty"`
	UsagePingSent      bool              `json:"usagePingSent,omitempty"`
	CrashReports       bool              `json:"crashReports,omitempty"`
}

type TrayMark string

const (
	TrayMarkWordmark TrayMark = "wordmark"
	TrayMarkOMark    TrayMark = "o"
)

func Defaults() Settings {
	return Settings{
		Seed: "#6750a4", Mode: "system", Volume: 70, AutoPlay: true, CacheSongs: audio.DefaultLimit, NotifyTrack: runtime.GOOS != "darwin", ShowTray: runtime.GOOS != "darwin", TrayMark: TrayMarkWordmark,
		Zoom: DefaultZoom, ShowNotch: true, CrossfadeSeconds: DefaultCrossfade,
	}
}

const (
	MinCrossfade     = 1
	MaxCrossfade     = 12
	DefaultCrossfade = 5
)

func ClampCrossfade(seconds int) int {
	if seconds == 0 {
		return DefaultCrossfade
	}
	return min(max(seconds, MinCrossfade), MaxCrossfade)
}

const (
	MinZoom     = 80
	MaxZoom     = 200
	ZoomStep    = 10
	DefaultZoom = 100
)

func ClampZoom(percent int) int {
	if percent == 0 {
		return DefaultZoom
	}
	return min(max(percent, MinZoom), MaxZoom)
}

func (s *Settings) clone() Settings {
	c := *s
	c.Recent = slices.Clone(s.Recent)
	c.Shortcuts = maps.Clone(s.Shortcuts)
	return c
}

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

func (s *Settings) SetMode(mode m3.Mode) {
	s.Mode = strings.ToLower(mode.String())
}

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

func SeedHex(c ui.Color) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

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

const maxRecent = 8

func (s *Settings) Forget(query string) {
	s.Recent = slices.DeleteFunc(s.Recent, func(q string) bool { return q == query })
}

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
		log.Printf("settings in %s are not readable, starting from the defaults: %v", path, err)
		return Defaults()
	}
	return s
}

func Parse(data []byte) (Settings, error) {
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
	macOSPresentsPlaybackItself := runtime.GOOS == "darwin"
	if macOSPresentsPlaybackItself {
		read.NotifyTrack, read.ShowTray = false, false
	}
	read.CrossfadeSeconds = ClampCrossfade(read.CrossfadeSeconds)
	return read, nil
}

func (s *Settings) save(path string) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(path, data, 0o600)
}

const writeDelay = 400 * time.Millisecond

type Writer struct {
	Path string

	mu      sync.Mutex
	next    *Settings
	running bool
	done    chan struct{}
	flush   chan struct{}
}

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

func (w *Writer) Pending() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.running
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
