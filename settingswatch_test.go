package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
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

func TestSettingsFileChangesSwitchTheMacDesktopPlayer(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS desktop player selection")
	}
	a := notchTestApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	writeSettingsFile(t, a.saver.Path, a.settings)
	a.settingsSeen, _ = os.ReadFile(a.saver.Path)
	panel := ui.NewTester(a.notchView, 284, 38)
	if err := panel.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	next := a.settings
	next.ShowNotch = false
	writeSettingsFile(t, a.saver.Path, next)
	a.checkSettings()
	panel.Frame()
	if !a.trayEnabled() || a.notch.expanded || a.notch.transitioning || panel.HasText("Notch panel") {
		t.Fatal("editing showNotch did not immediately replace the expanded notch")
	}
	next.ShowNotch = true
	writeSettingsFile(t, a.saver.Path, next)
	a.checkSettings()
	panel.Frame()
	if a.trayEnabled() || !panel.HasText("Open notch lyrics") || a.notch.expanded {
		t.Fatal("editing showNotch did not restore the compact notch")
	}
	if a.current.VideoID != "a" || a.player.Playing() || a.settings.NotifyTrack || a.saver.Pending() {
		t.Fatal("reloading the desktop choice changed playback, banners or wrote the file")
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
