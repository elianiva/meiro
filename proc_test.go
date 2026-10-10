//go:build !windows

package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fakeYtDlp installs a yt-dlp stand-in on PATH that runs script.
func fakeYtDlp(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "yt-dlp"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMPDIR", t.TempDir())
}

func TestDownloadAudioKeepsPercentPathsOutOfTheTemplate(t *testing.T) {
	// The fake saves the file where -P and -o say, as yt-dlp does, and fails
	// when the template carries the directory.
	fakeYtDlp(t, `
while [ $# -gt 0 ]; do
  case "$1" in
    -P) dir="$2"; shift ;;
    -o) tmpl="$2"; shift ;;
  esac
  shift
done
[ "$tmpl" = 'audio.%(ext)s' ] || { echo "ERROR: bad template $tmpl" >&2; exit 1; }
echo data > "$dir/audio.webm"
`)
	cacheDir := filepath.Join(t.TempDir(), "100%(id)s%%")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := downloadAudio(context.Background(), "vid", cacheDir, "")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(filepath.Dir(path)) != cacheDir || filepath.Base(path) != "audio.webm" {
		t.Errorf("audio saved at %s, want inside a temp dir of %s", path, cacheDir)
	}
}

func TestDownloadAudioCancellationKillsTreeAndRemovesCookies(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	fakeYtDlp(t, `
sleep 300 &
echo $! > `+pidFile+`
wait
`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	cacheDir := t.TempDir()
	go func() {
		_, err := downloadAudio(ctx, "vid", cacheDir, "SID=secret")
		done <- err
	}()
	var pid int
	for range 100 {
		if data, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(data)) != "" {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("fake yt-dlp never started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("cancelled download succeeded")
		}
	case <-time.After(commandWaitDelay + 3*time.Second):
		t.Fatal("downloadAudio hung after cancellation")
	}
	dead := false
	for range 100 {
		if err := syscall.Kill(pid, 0); err != nil {
			dead = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !dead {
		t.Error("yt-dlp child process survived cancellation")
	}
	root, _ := tempRoot()
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Errorf("temporary files remain after cancellation: %v", left)
	}
	if left, _ := os.ReadDir(cacheDir); len(left) != 0 {
		t.Errorf("cache directory not cleaned: %v", left)
	}
}

func TestYtDlpCookieImportRemovesItsDirectory(t *testing.T) {
	fakeYtDlp(t, `
while [ $# -gt 0 ]; do
  [ "$1" = --cookies ] && file="$2"
  shift
done
printf '.youtube.com\tTRUE\t/\tTRUE\t0\tSID\tabc\n' > "$file"
`)
	if cookie, err := ytDlpCookie(context.Background(), "chrome"); err != nil || cookie != "SID=abc" {
		t.Fatalf("ytDlpCookie = %q, %v", cookie, err)
	}
	root, _ := tempRoot()
	if left, _ := os.ReadDir(root); len(left) != 0 {
		t.Errorf("cookie directory remains: %v", left)
	}
}

func TestSweepStaleTempOnlyRemovesOldMatchingEntries(t *testing.T) {
	dir := t.TempDir()
	old := time.Now().Add(-3 * time.Hour)
	for _, name := range []string{".audio-old", ".audio-new", "keep-old"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{".audio-old", "keep-old"} {
		if err := os.Chtimes(filepath.Join(dir, name), old, old); err != nil {
			t.Fatal(err)
		}
	}
	sweepStaleTemp(dir, staleTempAge, ".audio-")
	for name, want := range map[string]bool{".audio-old": false, ".audio-new": true, "keep-old": true} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
}
