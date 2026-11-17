package audio

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheFromDownloadToRestart(t *testing.T) {
	long := make([]byte, 2*downloadRange+3)
	for i := range long {
		long[i] = byte(i % 251)
	}
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gone":
			http.Error(w, "expired", http.StatusForbidden)
		case "/long":
			if ranged := r.Header.Get("Range"); ranged == "" || strings.HasSuffix(ranged, "-") {
				http.Error(w, "ask for a range", http.StatusTooManyRequests)
				return
			}
			http.ServeContent(w, r, "long.webm", time.Time{}, bytes.NewReader(long))
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

	cache.Get("two")
	cache.Add("three", media.URL+"/three")
	waitCached("three")
	if cached("one") || !cached("two") {
		t.Errorf("eviction kept one=%v two=%v", cached("one"), cached("two"))
	}

	cache.Add("gone", media.URL+"/gone")
	cache.Close()
	if cached("gone") {
		t.Error("a refused download was cached")
	}

	again, _, err := Open(root, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if _, ok := again.Get("three"); !ok {
		t.Error("the cache lost its files across a restart")
	}

	cache = again
	cache.Add("long", media.URL+"/long")
	waitCached("long")
	path, _ = cache.Get("long")
	if data, _ := os.ReadFile(path); !bytes.Equal(data, long) {
		t.Error("a track downloaded by ranges was not cached byte for byte")
	}
}

func TestURLsShareOneResolve(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	urls := NewURLs(func(_ context.Context, videoID string) (string, time.Duration, error) {
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
			url, _, _ := urls.Get(context.Background(), "a")
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
	_, _, _ = urls.Get(context.Background(), "bad")
	_, _, _ = urls.Get(context.Background(), "bad")
	if calls.Load() != 3 {
		t.Errorf("a failed resolve was remembered: %d resolves", calls.Load())
	}
}

func TestURLsCancelAbandonedResolve(t *testing.T) {
	stopped := make(chan string, 2)
	urls := NewURLs(func(ctx context.Context, videoID string) (string, time.Duration, error) {
		<-ctx.Done()
		stopped <- videoID
		return "", 0, ctx.Err()
	})
	first, cancelFirst := context.WithCancel(context.Background())
	second, cancelSecond := context.WithCancel(context.Background())
	for _, ctx := range []context.Context{first, second} {
		go func() { _, _, _ = urls.Get(ctx, "a") }()
	}
	time.Sleep(20 * time.Millisecond)
	cancelFirst()
	select {
	case <-stopped:
		t.Fatal("a resolve with a request still waiting was stopped")
	case <-time.After(50 * time.Millisecond):
	}
	cancelSecond()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("an abandoned resolve was not stopped")
	}
}
