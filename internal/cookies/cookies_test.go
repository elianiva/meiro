package cookies

import (
	"context"
	"errors"
	"path/filepath"
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

func TestFileStoreRoundTrip(t *testing.T) {
	store := &FileStore{Path: filepath.Join(t.TempDir(), "nested", "cookie.txt")}
	ctx := context.Background()
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotSaved) {
		t.Fatalf("an empty store loaded %v", err)
	}
	if err := store.Save(ctx, "SAPISID=a"); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(ctx); err != nil || got != "SAPISID=a" {
		t.Fatalf("Load = %q, %v", got, err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotSaved) {
		t.Errorf("a deleted store loaded %v", err)
	}
}
