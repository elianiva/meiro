package applog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRollsOverKeepingTheNewestFiles(t *testing.T) {
	dir := t.TempDir()
	f, err := open(dir, 100, 2, func() string { return "HEADER" })
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := range 8 {
		fmt.Fprintf(f, "line %d %s\n", i, strings.Repeat("x", 30))
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := "meiro.log meiro.log.1 meiro.log.2"; strings.Join(names, " ") != want {
		t.Fatalf("files = %v, want %s", names, want)
	}
	if got := read(t, filepath.Join(dir, "meiro.log")); !strings.Contains(got, "line 7") {
		t.Errorf("newest line is not in the current file: %q", got)
	}
	for _, name := range names {
		path := filepath.Join(dir, name)
		got := read(t, path)
		if !strings.HasPrefix(got, "HEADER\n") {
			t.Errorf("%s does not start with the header: %q", name, got)
		}
		if info, _ := os.Stat(path); info.Size() > 100 {
			t.Errorf("%s is %d bytes, over the cap", name, info.Size())
		}
	}
	if old := read(t, filepath.Join(dir, "meiro.log.2")); strings.Contains(old, "line 0") {
		t.Errorf("the oldest line should have been dropped: %q", old)
	}
}

func TestReopeningContinuesTheFileWithoutAnotherHeader(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"first\n", "second\n"} {
		f, err := Open(dir, func() string { return "HEADER" })
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprint(f, line)
		f.Close()
	}
	if got, want := read(t, filepath.Join(dir, FileName)), "HEADER\nfirst\nsecond\n"; got != want {
		t.Errorf("log = %q, want %q", got, want)
	}
}

func TestRedactsSecrets(t *testing.T) {
	cases := []struct{ in, kept string }{
		{"request Cookie: SAPISID=abc123; HSID=zzz9 done", "request Cookie: " + Redacted},
		{"authorization=SAPISIDHASH 1_deadbeef", "authorization=" + Redacted},
		{"cookie file has __Secure-3PSID=g.a000secret; path=/", "__Secure-3PSID=" + Redacted},
		{"refresh_token=tok-123 ok", "refresh_token=" + Redacted + " ok"},
		{"playing track vid=abc, cookie jar empty", "playing track vid=abc, cookie jar empty"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if !strings.Contains(got, c.kept) {
			t.Errorf("Redact(%q) = %q, want it to contain %q", c.in, got, c.kept)
		}
		for _, secret := range []string{"abc123", "zzz9", "deadbeef", "a000secret", "tok-123"} {
			if strings.Contains(got, secret) {
				t.Errorf("Redact(%q) = %q still has %q", c.in, got, secret)
			}
		}
	}
}

func TestWriteRedactsBeforeItReachesTheFile(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, "Cookie: SAPISID=hunter2")
	if got := read(t, f.Path()); strings.Contains(got, "hunter2") {
		t.Errorf("the log has the secret: %q", got)
	}
}
