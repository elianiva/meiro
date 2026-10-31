package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
)

func TestSettingsWatcherNoticesAnAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 10)
	watcher, err := newSettingsWatcher(dir, "settings.json", 20*time.Millisecond, func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- watcher.run(stop) }()

	// A tool writes a temporary file and renames it over settings.json.
	tmp := filepath.Join(dir, "settings.json.tmp")
	if err := os.WriteFile(tmp, []byte(`{"seed":"#d81b78"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher did not notice the settings file")
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSettingsWatcherIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{}, 10)
	watcher, err := newSettingsWatcher(dir, "settings.json", 20*time.Millisecond, func() { changed <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- watcher.run(stop) }()

	if err := os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		t.Fatal("a different file triggered the settings watcher")
	case <-time.After(150 * time.Millisecond):
	}

	if err := os.WriteFile(path, []byte(`{"seed":"#d81b78"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(2 * time.Second):
		t.Fatal("the watcher did not notice the settings file")
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSettingsFileChangesApplyWithoutARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.settings = settings.Defaults()
	a.settings.Seed, a.settings.Mode = "#6750a4", "light"
	a.settings.Channel, a.settings.SignInSource = "channel-1", "firefox"
	a.settings.LastFMUser = "neo"
	cacheDir := filepath.Join(t.TempDir(), "cache")
	a.settings.CacheDirectory = cacheDir
	writeSettingsFile(t, path, a.settings)
	a.settingsSeen, _ = os.ReadFile(path)
	a.keyMap() // build the shortcuts before the file changes

	var shown m3.Scheme
	tt := ui.NewTester(func(c *ui.Context) {
		m3.Provide(c, a.resolveTheme(c))
		shown = m3.Of(c).Scheme
		ui.Box(c).Size(10, 10).Background(shown.Primary)
	}, 100, 100)
	tt.Frame()
	before := shown.Primary

	next := a.settings
	next.Seed, next.Mode = "#d81b78", "dark"
	next.Volume = 35
	next.EQOn = true
	next.EQBands = []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	next.Crossfade, next.CrossfadeSeconds = true, 8
	next.Shortcuts = map[string]string{"playPause": "Shift+Cmd+K"}
	next.Channel, next.SignInSource = "channel-2", "chrome"
	next.LastFMUser = "someone-else"
	next.CacheDirectory = filepath.Join(t.TempDir(), "other-cache")
	writeSettingsFile(t, path, next)

	a.checkSettings()

	if a.settings.Seed != "#d81b78" || a.settings.Mode != "dark" {
		t.Fatalf("theme settings did not reload: %+v", a.settings)
	}
	if a.settings.Channel != "channel-1" || a.settings.SignInSource != "firefox" || a.settings.LastFMUser != "neo" {
		t.Fatalf("account settings changed from the file: %+v", a.settings)
	}
	if a.settings.CacheDirectory != cacheDir {
		t.Fatalf("cache folder changed from the file: %q", a.settings.CacheDirectory)
	}
	if a.volume != 35 {
		t.Errorf("volume = %v, want 35", a.volume)
	}
	if got := a.player.Volume(); math.Abs(got-0.35) > 0.001 {
		t.Errorf("player volume = %v, want 0.35", got)
	}
	if !slices.Equal(a.settings.EQBands, next.EQBands) {
		t.Errorf("eq bands = %v, want %v", a.settings.EQBands, next.EQBands)
	}
	if got := a.player.Crossfade(); got != 8*time.Second {
		t.Errorf("crossfade = %v, want 8s", got)
	}
	if a.keyboard.keys != nil {
		t.Error("shortcuts were not rebuilt")
	}

	tt.Frame()
	tt.Advance(themeGlide + 20*time.Millisecond)
	want := m3.New(a.settings.Theme(uiFont), true).Scheme.Primary
	if shown.Primary != want {
		t.Errorf("primary = %v, want %v", shown.Primary, want)
	}
	if shown.Primary == before {
		t.Error("the window kept its old colour")
	}
}

func TestSettingsFileChangesWaitForAWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.settings = settings.Defaults()
	a.settings.Seed = "#6750a4"
	writeSettingsFile(t, path, a.settings)
	a.settingsSeen, _ = os.ReadFile(path)

	if err := os.WriteFile(path, []byte(`{"seed":`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkSettings()
	if a.settings.Seed != "#6750a4" {
		t.Fatalf("a half-written file changed the settings: %+v", a.settings)
	}

	next := a.settings
	next.Seed = "#d81b78"
	writeSettingsFile(t, path, next)
	a.checkSettings()
	if a.settings.Seed != "#d81b78" {
		t.Fatalf("the whole file did not apply: %+v", a.settings)
	}
}

func TestSettingsFileChangesLeaveAPendingAppWriteAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.settings = settings.Defaults()
	writeSettingsFile(t, path, a.settings)
	a.settingsSeen, _ = os.ReadFile(path)

	a.settings.Seed = "#123456"
	a.saveSettings()
	if !a.saver.Pending() {
		t.Fatal("the save is not pending")
	}
	next := a.settings
	next.Seed = "#abcdef"
	writeSettingsFile(t, path, next)
	a.checkSettings()
	if a.settings.Seed != "#123456" {
		t.Fatalf("the file overrode a pending app write: %q", a.settings.Seed)
	}
	a.saver.Wait()
}

func writeSettingsFile(t *testing.T, path string, s settings.Settings) {
	t.Helper()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestColorsFileThemesTheWindow(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp(t)
	a.saver.Path = filepath.Join(dir, "settings.json")
	a.colorsPath = filepath.Join(dir, "colors.json")
	a.settings = settings.Defaults()
	a.settings.Seed, a.settings.Mode = "#6750a4", "dark"
	writeSettingsFile(t, a.saver.Path, a.settings)
	a.settingsSeen, _ = os.ReadFile(a.saver.Path)
	derived := m3.New(a.settings.Theme(uiFont), true).Scheme

	// The light set is left out: a mode with no colours keeps the seed's own.
	file := `{"dark": {"primary": "#00e63a", "on_primary": "#003910", "surface": "#101510"}}`
	if err := os.WriteFile(a.colorsPath, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkColors()
	shown := func(mode string) m3.Scheme {
		a.settings.Mode = mode
		var s m3.Scheme
		tt := ui.NewTester(func(c *ui.Context) { s = a.resolveTheme(c).Scheme; ui.Box(c).Size(10, 10) }, 100, 100)
		tt.Frame()
		tt.Advance(themeGlide + 20*time.Millisecond)
		return s
	}
	// Until matugen is the colour source, the file is read and ignored.
	if got := shown("dark"); got.Primary != derived.Primary {
		t.Errorf("primary = %v with the source not matugen, want the seed's %v", got.Primary, derived.Primary)
	}
	a.settings.Matugen = true
	got := shown("dark")
	if got.Primary != ui.Hex("#00e63a") || got.Surface != ui.Hex("#101510") {
		t.Errorf("colours did not apply: primary %v, surface %v", got.Primary, got.Surface)
	}
	if got.Secondary != derived.Secondary {
		t.Errorf("a role the file leaves out changed: %v, want %v", got.Secondary, derived.Secondary)
	}

	if got, want := shown("light").Primary, m3.New(a.settings.Theme(uiFont), false).Scheme.Primary; got != want {
		t.Errorf("light primary = %v, want the seed's %v", got, want)
	}
	if err := os.WriteFile(a.colorsPath, []byte(`{"dark": {"primary": "#00e`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.checkColors()
	if got := shown("dark"); got.Primary != ui.Hex("#00e63a") {
		t.Errorf("a half-written file changed the colours: %v", got.Primary)
	}

	if err := os.Remove(a.colorsPath); err != nil {
		t.Fatal(err)
	}
	a.checkColors()
	if got := shown("dark"); got.Primary != derived.Primary {
		t.Errorf("primary = %v after the file went, want the seed's %v", got.Primary, derived.Primary)
	}
}

func TestColourSourceSwitchesBetweenFixedArtworkAndMatugen(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if !tt.HasText("Violet") || !tt.HasText("Palette") {
		t.Fatal("the fixed source did not show the colour and palette pickers")
	}

	for _, tc := range []struct {
		click            string
		dynamic, matugen bool
	}{
		{"Matugen", false, true},
		{"Artwork", true, false},
		{"Matugen", false, true},
		{"Fixed", false, false},
	} {
		if err := tt.Click(tc.click); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if a.settings.Dynamic != tc.dynamic || a.settings.Matugen != tc.matugen {
			t.Fatalf("after %q: dynamic=%v matugen=%v", tc.click, a.settings.Dynamic, a.settings.Matugen)
		}
		if got := tt.HasText("Palette"); got == tc.matugen {
			t.Errorf("after %q: palette picker shown = %v", tc.click, got)
		}
		if got := tt.HasText("Violet"); got == (tc.dynamic || tc.matugen) {
			t.Errorf("after %q: colour swatches shown = %v", tc.click, got)
		}
	}
}
