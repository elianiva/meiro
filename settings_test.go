package main

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
)

// The settings restyle the window: a new seed, palette style or mode changes
// the colours every component draws with.
func TestSettingsChangeTheTheme(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	var shown *m3.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		shown = m3.Of(c)
	}, 1000, 900)
	before := shown.Scheme.Primary
	if err := tt.Click("Rose"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Seed != "#d81b78" {
		t.Fatalf("clicking Rose chose %q", a.settings.Seed)
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	if after := shown.Scheme.Primary; after == before {
		t.Errorf("the primary colour stayed %v after choosing a new seed", after)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !shown.Dark || a.settings.Mode != "dark" {
		t.Errorf("choosing Dark left dark=%v, mode=%q", shown.Dark, a.settings.Mode)
	}
	if err := tt.Click("Vibrant"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Style != int(m3.Vibrant) {
		t.Errorf("choosing Vibrant left style %d", a.settings.Style)
	}
}

func TestSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	s := defaultSettings()
	s.Seed, s.Mode, s.Style, s.Dynamic, s.AutoPlay = "#00897b", "dark", int(m3.Expressive), true, false
	s.CacheSongs = 20
	s.CacheDirectory = filepath.Join(t.TempDir(), "audio files")
	s.remember("one")
	s.remember("two")
	s.remember("One")
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	got := loadSettings(path)
	if got.Seed != s.Seed || got.Mode != "dark" || got.Style != int(m3.Expressive) || !got.Dynamic || got.AutoPlay || got.CacheSongs != s.CacheSongs || got.CacheDirectory != s.CacheDirectory {
		t.Errorf("settings came back as %+v", got)
	}
	if len(got.Recent) != 2 || got.Recent[0] != "One" {
		t.Errorf("recent searches came back as %v", got.Recent)
	}
	cfg := got.config()
	if cfg.Mode != m3.Dark || cfg.Style != m3.Expressive {
		t.Errorf("config = %+v", cfg)
	}
	if bad := loadSettings(filepath.Join(t.TempDir(), "missing.json")); bad.Seed != defaultSettings().Seed {
		t.Errorf("a missing file gave %+v", bad)
	}
	if err := os.WriteFile(path, []byte("{nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bad := loadSettings(path); bad.Seed != defaultSettings().Seed {
		t.Errorf("a broken file gave %+v", bad)
	}
}

func TestSettingsCacheLimitDefaultsAndAllowsAnyNonNegativeValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, test := range []struct {
		name string
		json string
		want int
	}{
		{name: "old settings", json: `{"volume":70}`, want: defaultAudioCacheLimit},
		{name: "custom count", json: `{"cacheSongs":37}`, want: 37},
		{name: "disabled", json: `{"cacheSongs":0}`, want: 0},
		{name: "negative count", json: `{"cacheSongs":-1}`, want: defaultAudioCacheLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := loadSettings(path).CacheSongs; got != test.want {
				t.Errorf("cache limit = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSettingsChoosePlaybackCacheLimit(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	a.cacheSongsText = ""
	a.cacheDirectoryText = "/music/cache"
	tt := ui.NewTester(a.view, 1000, 1800)
	if !tt.HasText("Playback cache") || !tt.HasText("Keep recent songs") || !tt.HasText("Songs to cache") || !tt.HasText("Cache folder") || !tt.HasText("Choose cache folder") || !tt.HasText("/music/cache") || !tt.HasText("MP3") {
		t.Fatalf("the cache setting is missing: %q", tt.Texts())
	}
	if err := tt.Click("Songs to cache"); err != nil {
		t.Fatal(err)
	}
	tt.Type("37")
	tt.Key(0, ui.KeyEnter)
	if a.settings.CacheSongs != 37 {
		t.Fatalf("entering 37 set the cache size to %d (text %q, error %q)", a.settings.CacheSongs, a.cacheSongsText, a.cacheLimitError)
	}
	a.cacheSongsText = "0"
	tt.Frame()
	if err := tt.Click("Songs to cache"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if a.settings.CacheSongs != 0 {
		t.Errorf("setting the limit to 0 set the cache size to %d", a.settings.CacheSongs)
	}
}

func TestReplacingAudioCacheClosesCurrentAndRetiredCachesAtShutdown(t *testing.T) {
	a := newTestApp()
	first, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	a.replaceAudioCache(first)
	if got := a.replaceAudioCache(second); got != first {
		t.Fatal("replacing the cache did not return the previous manager")
	}
	a.closeAudioCaches()
	for name, cache := range map[string]*audioCache{"retired": first, "current": second} {
		cache.mu.Lock()
		closed := cache.closed
		cache.mu.Unlock()
		if !closed {
			t.Errorf("%s audio cache was left open", name)
		}
	}
}

func TestOlderSettingsDefaultAutoplayToOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"seed":"#6750a4","mode":"system","volume":70}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path); !got.AutoPlay {
		t.Errorf("older settings disabled autoplay: %+v", got)
	}
}

// Saves asked for in a burst, while the list they hold is edited, must not
// race, and the last one must be what ends up on disk.
func TestSettingsSavesAreOrderedAndIndependent(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	for i := range 200 {
		a.settings.Recent = []string{"a", "b", "c", "d", "e"}
		a.settings.Volume = float64(i % 100)
		a.saveSettings()
		a.forget("a")
		a.settings.remember("last")
	}
	a.settings.Volume = 42
	a.saveSettings()
	a.saver.wait()

	got := loadSettings(a.settingsPath)
	if got.Volume != 42 || len(got.Recent) == 0 || got.Recent[0] != "last" {
		t.Errorf("the last save was not the one kept: volume %v, recent %v", got.Volume, got.Recent)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("saving left %d files behind, want only the settings", len(entries))
	}
}

func TestShutdownFlushesPendingSettings(t *testing.T) {
	a := newTestApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	started := make(chan struct{})
	release := make(chan struct{})
	a.saver.write = func(s *settings, path string) error {
		close(started)
		<-release
		return s.save(path)
	}
	a.settings.Volume = 13
	a.saveSettings()
	<-started

	done := make(chan struct{})
	go func() {
		a.shutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("shutdown returned before the pending settings save completed")
	case <-time.After(time.Second):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the settings save completed")
	}

	if got := loadSettings(a.settingsPath); got.Volume != 13 {
		t.Errorf("saved volume = %v, want 13", got.Volume)
	}
}

func TestShutdownReportsSettingsSaveErrorsWithoutBlocking(t *testing.T) {
	a := newTestApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	writeErr := errors.New("disk is read-only")
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	a.saver.write = func(*settings, string) error { return writeErr }
	a.saveSettings()
	done := make(chan struct{})
	go func() {
		a.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked after the settings save failed")
	}
	if !strings.Contains(logs.String(), writeErr.Error()) {
		t.Errorf("settings save failure was not logged: %q", logs.String())
	}
}

func TestParseSeedIsStrict(t *testing.T) {
	if c, ok := parseSeed("#6750a4"); !ok || c != ui.RGB(0x67, 0x50, 0xa4) {
		t.Errorf("a good seed gave %v, %v", c, ok)
	}
	for _, bad := range []string{"", "6750a4", "#6750a", "#6750a4f", "#1 2 3 ", "#+1+1+1", "#0x1234", "#gggggg"} {
		if _, ok := parseSeed(bad); ok {
			t.Errorf("%q was taken as a seed", bad)
		}
	}
}
