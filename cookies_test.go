package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCookieHeader(t *testing.T) {
	const want = "SAPISID=abc; SID=def"
	for name, in := range map[string]string{
		"value":        want,
		"padded":       "  " + want + "\n",
		"header":       "Cookie: " + want,
		"lowercase":    "cookie: " + want,
		"curl":         "curl 'https://music.youtube.com/youtubei/v1/browse' -H 'cookie: " + want + "' -H 'origin: x'",
		"curl doubled": `curl "https://music.youtube.com" -H "cookie: ` + want + `" --compressed`,
	} {
		if got := cookieHeader(in); got != want {
			t.Errorf("%s: cookieHeader = %q, want %q", name, got, want)
		}
	}
}

func TestCookieFromNetscape(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	text := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tabc",
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t2100000000\t__Secure-3PSID\tdef",
		"music.youtube.com\tFALSE\t/\tTRUE\t0\tYSC\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t1000000000\tOLD\texpired",
		".google.com\tTRUE\t/\tTRUE\t2100000000\tSID\tgoogle",
		".notyoutube.com\tTRUE\t/\tTRUE\t2100000000\tEVIL\tx",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tnewer",
		"broken line",
	}, "\n")
	got := cookieFromNetscape(text, now)
	want := "SAPISID=newer; __Secure-3PSID=def; YSC=session"
	if got != want {
		t.Errorf("cookieFromNetscape = %q, want %q", got, want)
	}
	if got := cookieFromNetscape("# nothing\n", now); got != "" {
		t.Errorf("an empty file gave %q", got)
	}
}

func TestChromiumProfiles(t *testing.T) {
	dir := t.TempDir()
	for _, database := range []string{"Default/Cookies", "Profile 3/Network/Cookies", "Profile 1/Preferences", "Crashpad/Cookies"} {
		path := filepath.Join(dir, filepath.FromSlash(database))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := chromiumProfiles(dir)
	want := []string{filepath.Join(dir, "Default"), filepath.Join(dir, "Profile 3")}
	if !slices.Equal(got, want) {
		t.Errorf("chromiumProfiles = %q, want %q", got, want)
	}
	if got := chromiumProfiles(filepath.Join(dir, "missing")); got != nil {
		t.Errorf("a missing directory gave %q", got)
	}
	if got, want := profileSelectors("chrome", chromiumProfiles(dir), true), []string{
		"chrome:" + filepath.Join(dir, "Default"),
		"chrome:" + filepath.Join(dir, "Profile 3"),
		"chrome",
	}; !slices.Equal(got, want) {
		t.Errorf("Chrome selectors = %q, want %q", got, want)
	}
}

func TestFirefoxProfiles(t *testing.T) {
	dir := t.TempDir()
	for _, database := range []string{"primary.default/cookies.sqlite", "secondary.default-release/cookies.sqlite"} {
		path := filepath.Join(dir, filepath.FromSlash(database))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profiles := firefoxProfiles([]string{dir})
	want := []string{filepath.Join(dir, "primary.default"), filepath.Join(dir, "secondary.default-release")}
	if !slices.Equal(profiles, want) {
		t.Errorf("firefoxProfiles() = %q, want %q", profiles, want)
	}
	if got, want := profileSelectors("firefox", profiles, true), []string{
		"firefox:" + filepath.Join(dir, "primary.default"),
		"firefox:" + filepath.Join(dir, "secondary.default-release"),
		"firefox",
	}; !slices.Equal(got, want) {
		t.Errorf("Firefox selectors = %q, want %q", got, want)
	}
}

func TestZenProfileRoots(t *testing.T) {
	home, config := "/home/mei", "/home/mei/.config"
	got := zenProfileRoots(home, config, "linux")
	want := []string{
		filepath.Join(home, ".zen"),
		filepath.Join(home, ".var", "app", "app.zen_browser.zen", "zen"),
		filepath.Join(config, "zen"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("zenProfileRoots() = %q, want %q", got, want)
	}
}

func TestCookieHeaderSurvivesNonASCIIText(t *testing.T) {
	// "İ" is two bytes, and three once lowercased.
	if got := cookieHeader("İİİİİİİİİİ Cookie: SAPISID=abc"); got != "SAPISID=abc" {
		t.Errorf("cookieHeader = %q", got)
	}
}
