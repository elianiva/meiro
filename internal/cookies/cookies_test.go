package cookies

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestHeaderTakesTheCookieFromWhatWasPasted(t *testing.T) {
	for _, pasted := range []string{
		"SAPISID=a; SID=b",
		"Cookie: SAPISID=a; SID=b",
		`curl 'https://music.youtube.com/' -H 'cookie: SAPISID=a; SID=b' -H 'x: y'`,
	} {
		if got := Header(pasted); got != "SAPISID=a; SID=b" {
			t.Errorf("Header(%q) = %q", pasted, got)
		}
	}
}

func TestFromNetscapeKeepsLiveYouTubeCookies(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	file := "# Netscape HTTP Cookie File\n" +
		".youtube.com\tTRUE\t/\tTRUE\t2000000\tSAPISID\tlive\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tsession\n" +
		".youtube.com\tTRUE\t/\tTRUE\t10\tOLD\texpired\n" +
		".example.com\tTRUE\t/\tTRUE\t0\tOTHER\tnope\n"
	got, err := fromNetscape(file, now)
	if err != nil || got != "SAPISID=live; SID=session" {
		t.Errorf("fromNetscape = %q, %v", got, err)
	}
}

func TestZenProfileRootsPointAtTheFlatpakDataDirectory(t *testing.T) {
	home := t.TempDir()
	roots := zenProfileRoots(home, "", "linux")
	want := filepath.Join(home, ".var", "app", zenFlatpak, ".zen")
	if !slices.Contains(roots, want) {
		t.Fatalf("zenProfileRoots = %v, want %s", roots, want)
	}
	wrong := filepath.Join(home, ".var", "app", zenFlatpak, "zen")
	if slices.Contains(roots, wrong) {
		t.Errorf("zenProfileRoots still holds %s", wrong)
	}
	profile := filepath.Join(want, "abc123.default-release")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "cookies.sqlite"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if profiles := firefoxProfiles(roots); !slices.Contains(profiles, profile) {
		t.Errorf("firefoxProfiles = %v, want %s", profiles, profile)
	}
}
