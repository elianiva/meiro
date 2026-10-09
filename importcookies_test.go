package main

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestChromiumSpecNamesTheKeyring(t *testing.T) {
	// yt-dlp does not find a Chromium browser's cookie key under a keyring it
	// searches by default on Linux, so the name of one goes with the browser.
	if got := chromiumSpec("chrome", "gnomekeyring"); got != "chrome+gnomekeyring" {
		t.Errorf("chromiumSpec = %q, want %q", got, "chrome+gnomekeyring")
	}
	if got := chromiumSpec("chrome", ""); got != "chrome" {
		t.Errorf("chromiumSpec = %q, want %q", got, "chrome")
	}
}

func TestYtDlpKeyringNamesTheSecretServiceOffKnownDesktops(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	// yt-dlp reads the desktop environment itself and settles on plain text
	// for a session it cannot place, where a browser keeps its cookie key in
	// the Secret Service, so that is what to name.
	for _, session := range []map[string]string{
		{"XDG_CURRENT_DESKTOP": "niri"},
		{},
		{"XDG_CURRENT_DESKTOP": "niri:GNOME"},
		{"DESKTOP_SESSION": "gnome"},
		{"DESKTOP_SESSION": "xfce"},
	} {
		if got := ytDlpKeyring(env(session)); got != "gnomekeyring" {
			t.Errorf("ytDlpKeyring(%v) = %q, want gnomekeyring", session, got)
		}
	}
	// A KDE session keeps a wallet of its own, which yt-dlp does know how to
	// open, whatever the variable that says so.
	for _, session := range []map[string]string{
		{"XDG_CURRENT_DESKTOP": "KDE"},
		{"XDG_CURRENT_DESKTOP": "niri:KDE"},
		{"KDE_FULL_SESSION": "true"},
		{"DESKTOP_SESSION": "plasma"},
		{"DESKTOP_SESSION": "kde-plasma"},
		{"DESKTOP_SESSION": "kde4"},
		{"DESKTOP_SESSION": "kde", "KDE_SESSION_VERSION": "5"},
		{"KDE_SESSION_VERSION": "6"},
	} {
		if got := ytDlpKeyring(env(session)); got != "" {
			t.Errorf("ytDlpKeyring(%v) = %q, want it left to yt-dlp", session, got)
		}
	}
}

func TestImportSourcesOfferHeliumFromItsProfile(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Helium's profiles live in the XDG configuration directory on Linux")
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)

	if _, ok := heliumSource(); ok {
		t.Error("Helium was offered without a profile on the machine")
	}

	profile := filepath.Join(config, "net.imput.helium", "Default")
	if err := os.MkdirAll(profile, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "Cookies"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	source, ok := heliumSource()
	if !ok || source.label != "Helium" {
		t.Fatalf("heliumSource = %+v, %v; want the Helium browser", source, ok)
	}
	var labels []string
	for _, source := range importSources() {
		labels = append(labels, source.label)
	}
	if !slices.Contains(labels, "Helium") {
		t.Errorf("the browsers to import from are %q, without Helium", labels)
	}
}
