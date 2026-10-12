package audio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The cache downloads real bytes from a media server, keeps the most recent
// tracks within its limit, and survives a restart.
func TestCacheFromDownloadToRestart(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			http.Error(w, "expired", http.StatusForbidden)
		default:
			w.Header().Set("Content-Type", "audio/mp4")
			_, _ = w.Write([]byte("audio for " + r.URL.Path))
		}
	}))
	defer media.Close()

	root := t.TempDir()
	cache, _, err := Open(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	cached := func(id string) bool { _, ok := cache.Get(id); return ok }
	waitCached := func(id string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !cached(id) {
			if time.Now().After(deadline) {
				t.Fatalf("%s was never cached", id)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	cache.Add("one", media.URL+"/one")
	waitCached("one")
	cache.Add("two", media.URL+"/two")
	waitCached("two")
	path, _ := cache.Get("two")
	if !strings.HasSuffix(path, ".m4a") {
		t.Errorf("a track served as audio/mp4 was kept as %q", path)
	}
	if data, _ := os.ReadFile(path); string(data) != "audio for /two" {
		t.Errorf("cached bytes = %q", data)
	}

	// A third track pushes out the least recently used one, which is "one"
	// now that "two" was just read.
	cache.Get("two")
	cache.Add("three", media.URL+"/three")
	waitCached("three")
	if cached("one") || !cached("two") {
		t.Errorf("eviction kept one=%v two=%v", cached("one"), cached("two"))
	}

	// An expired URL caches nothing and leaves nothing half written.
	cache.Add("gone", media.URL+"/gone")
	cache.Close()
	if cached("gone") {
		t.Error("a refused download was cached")
	}

	// A restart finds the files that were kept.
	again, _, err := Open(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, ok := again.Get("three"); !ok {
		t.Error("the cache lost its files across a restart")
	}
}

// Requests for one track share a single resolve, and a failed resolve is
// asked again.
func TestURLsShareOneResolve(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	urls := NewURLs(func(_ context.Context, videoID, _ string) (string, time.Duration, error) {
		calls.Add(1)
		<-release
		if videoID == "bad" {
			return "", 0, context.DeadlineExceeded
		}
		return "https://media.test/" + videoID, time.Minute, nil
	})
	done := make(chan string, 2)
	for range 2 {
		go func() {
			url, _, _ := urls.Get(context.Background(), "a", "")
			done <- url
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	if first, second := <-done, <-done; first != "https://media.test/a" || first != second {
		t.Fatalf("got %q and %q", first, second)
	}
	if calls.Load() != 1 {
		t.Errorf("two requests resolved %d times", calls.Load())
	}
	_, _, _ = urls.Get(context.Background(), "bad", "")
	_, _, _ = urls.Get(context.Background(), "bad", "")
	if calls.Load() != 3 {
		t.Errorf("a failed resolve was remembered: %d resolves", calls.Load())
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	output := "Input #0, mov,mp4,m4a, from 'a.m4a':\n  Duration: 00:03:00.50, start: 0.000000, bitrate: 128 kb/s\n"
	if got := parseFFmpegDuration(output); got != 3*time.Minute+500*time.Millisecond {
		t.Errorf("duration = %v", got)
	}
}
