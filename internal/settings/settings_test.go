package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMacLoadsLegacyDesktopSettingsWithoutTrayOrBanners(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"showTray":true,"notifyTrack":true,"closeToTray":true,"volume":42}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := runtime.GOOS != "darwin"
	for _, s := range []Settings{Defaults(), Load(path)} {
		if s.ShowTray != want || s.NotifyTrack != want {
			t.Fatalf("desktop options on %s: tray=%v, notifications=%v", runtime.GOOS, s.ShowTray, s.NotifyTrack)
		}
	}
	s := Load(path)
	if s.CloseToTray == nil || !*s.CloseToTray || s.Volume != 42 {
		t.Fatal("unrelated playback preferences changed")
	}
}

func TestWriterKeepsTheLastStateAndLoadRepairsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	w := &Writer{Path: path}
	s := Defaults()
	s.Mode = "dark"
	s.DiscordPresence = true
	s.TrayMark = TrayMarkOMark
	w.Save(&s)
	s.Seed = "#d81b78"
	w.Save(&s)
	s.Seed = "not a colour" // a change after the last save is not written
	w.Wait()

	got := Load(path)
	if got.Mode != "dark" || got.Seed != "#d81b78" || !got.DiscordPresence || got.TrayMark != TrayMarkOMark {
		t.Errorf("loaded %+v", got)
	}

	// A file with values out of range comes back with the defaults for them,
	// and a field it lacks takes its default.
	if err := os.WriteFile(path, []byte(`{"volume":500,"seed":"red","cacheSongs":-1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, want := Load(path), Defaults()
	if got.Volume != want.Volume || got.Seed != want.Seed || got.CacheSongs != want.CacheSongs || got.TrayMark != want.TrayMark || !got.AutoPlay {
		t.Errorf("repaired %+v", got)
	}
	if err := os.WriteFile(path, []byte(`{"trayMark":"unexpected"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path).TrayMark; got != want.TrayMark {
		t.Errorf("invalid tray mark = %q, want default %q", got, want.TrayMark)
	}
}

func TestParseRejectsAFileStillBeingWritten(t *testing.T) {
	if _, err := Parse([]byte(`{"seed":`)); err == nil {
		t.Fatal("Parse accepted a half-written file")
	}
}

func TestClampZoomKeepsTheInterfaceReadable(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, DefaultZoom}, // a settings file from before the field
		{50, MinZoom},
		{120, 120},
		{500, MaxZoom},
	} {
		if got := ClampZoom(tc.in); got != tc.want {
			t.Errorf("ClampZoom(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}

	// A file's zoom comes back within the range, and one without the field
	// takes the default.
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"zoom":900}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path).Zoom; got != MaxZoom {
		t.Errorf("loaded zoom = %d, want %d", got, MaxZoom)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Load(path).Zoom; got != DefaultZoom {
		t.Errorf("zoom of a file without one = %d, want %d", got, DefaultZoom)
	}
}

func TestRememberKeepsRecentSearchesUniqueAndBounded(t *testing.T) {
	var s Settings
	for _, q := range []string{"a", "b", "A", "c", "d", "e", "f", "g", "h", "i"} {
		s.Remember(q)
	}
	if len(s.Recent) != maxRecent || s.Recent[0] != "i" {
		t.Errorf("recent = %v", s.Recent)
	}
	s.Forget("i")
	if s.Recent[0] != "h" {
		t.Errorf("forget left %v", s.Recent)
	}
}

func TestParseColorsReadsMatugenOutputAndRefusesTypos(t *testing.T) {
	// A trimmed colors.json as matugen writes it from the README's template.
	colors, err := ParseColors([]byte(`{
  "light": {"primary": "#006a60", "on_primary": "#ffffff", "surface_container_high": "#e4e9e6"},
  "dark": {"primary": "#82d5c8", "inverse_primary": "#006a60"}
}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := SeedHex(colors.Light["primary"]); got != "#006a60" {
		t.Errorf("light primary = %s", got)
	}
	if got := SeedHex(colors.Dark["inverse_primary"]); got != "#006a60" {
		t.Errorf("dark inverse_primary = %s", got)
	}
	if len(colors.Light) != 3 || len(colors.Dark) != 2 {
		t.Errorf("roles read: %d light, %d dark", len(colors.Light), len(colors.Dark))
	}

	for name, file := range map[string]string{
		"unknown role": `{"dark": {"primry": "#006a60"}}`,
		"bad colour":   `{"light": {"primary": "teal"}}`,
		"cut off":      `{"light": {"primary": "#00`,
	} {
		if _, err := ParseColors([]byte(file)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
