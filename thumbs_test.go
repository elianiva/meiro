package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestThumbCacheRetriesAfterFailure(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 200
	}
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			http.Error(w, "busy", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	landed := make(chan struct{}, 4)
	cache := newThumbCache(func() { landed <- struct{}{} })
	url := server.URL + "/cover=w60-h60-l90-rj"

	if cache.bitmap(url, 640) != nil {
		t.Fatal("bitmap returned before it downloaded")
	}
	waitFor(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.failed) == 1 })

	// Inside the retry wait the failure stands.
	cache.bitmap(url, 640)
	if hits.Load() != 1 {
		t.Fatalf("retried too soon: %d requests", hits.Load())
	}

	// After it, the next draw asks again.
	cache.mu.Lock()
	for key := range cache.failed {
		failure := cache.failed[key]
		failure.at = time.Now().Add(-2 * thumbRetry)
		cache.failed[key] = failure
	}
	cache.mu.Unlock()
	cache.bitmap(url, 640)
	waitFor(t, func() bool { return cache.bitmap(url, 640) != nil })
}

func TestThumbCacheStandsInWithSmallerSize(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "w128-h128-l90-rj") {
			_, _ = w.Write(buf.Bytes())
			return
		}
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60-l90-rj"
	small := func() bool { return cache.bitmap(url, 128) != nil }
	cache.bitmap(url, 128)
	waitFor(t, small)

	// The large size is not there, and may fail, yet the cover shows.
	if cache.bitmap(url, 640) == nil {
		t.Fatal("the small copy did not stand in for the large one")
	}
}

func TestThumbCacheRebuildsEvictedBitmapWithoutDownloading(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60"
	cache.bitmap(url, 60)
	waitFor(t, func() bool { return cache.bitmap(url, 60) != nil })
	if hits.Load() != 1 {
		t.Fatalf("initial downloads = %d, want 1", hits.Load())
	}

	cache.mu.Lock()
	cache.evictLeastRecent("")
	if len(cache.bitmaps) != 0 || len(cache.sources) != 1 {
		cache.mu.Unlock()
		t.Fatalf("after eviction: bitmaps = %d, sources = %d; want 0 and 1", len(cache.bitmaps), len(cache.sources))
	}
	cache.mu.Unlock()

	if got := cache.bitmap(url, 60); got != nil {
		t.Fatal("evicted bitmap was returned before it was rebuilt")
	}
	waitFor(t, func() bool { return cache.bitmap(url, 60) != nil })
	if hits.Load() != 1 {
		t.Errorf("rebuilding the bitmap made %d downloads, want 1 total", hits.Load())
	}
}

func TestThumbCacheDoesNotFetchOutsideCarouselRange(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	if got := cache.bitmapIf(server.URL+"/cover=w60-h60", 60, false); got != nil {
		t.Fatal("off-screen request returned a bitmap")
	}
	cache.mu.Lock()
	pending := len(cache.pending)
	cache.mu.Unlock()
	if pending != 0 || hits.Load() != 0 {
		t.Errorf("off-screen request: pending = %d, downloads = %d; want 0 and 0", pending, hits.Load())
	}
}

func TestThumbnailURLChoosesSmallerYouTubeVariants(t *testing.T) {
	cases := []struct {
		name string
		url  string
		size int
		want string
	}{
		{
			name: "card",
			url:  "https://i.ytimg.com/vi/video/hq720.jpg?sqp=token",
			size: 320,
			want: "https://i.ytimg.com/vi/video/mqdefault.jpg?sqp=token",
		},
		{
			name: "row",
			url:  "https://i.ytimg.com/vi/video/hq720.jpg",
			size: 128,
			want: "https://i.ytimg.com/vi/video/default.jpg",
		},
		{
			name: "hero",
			url:  "https://i.ytimg.com/vi/video/maxresdefault.jpg",
			size: 512,
			want: "https://i.ytimg.com/vi/video/hqdefault.jpg",
		},
		{
			name: "do not upscale",
			url:  "https://i.ytimg.com/vi/video/default.jpg",
			size: 320,
			want: "https://i.ytimg.com/vi/video/default.jpg",
		},
		{
			name: "unrecognized host",
			url:  "https://images.example/video/hq720.jpg",
			size: 320,
			want: "https://images.example/video/hq720.jpg",
		},
		{
			name: "sized url gets the asked-for size",
			url:  "https://img.test/a=w544-h544-l90-rj",
			size: 96,
			want: "https://img.test/a=w96-h96-l90-rj",
		},
		{
			name: "scaled url gets the asked-for scale",
			url:  "https://img.test/a=s96-c-k-c0",
			size: 64,
			want: "https://img.test/a=s64-c-k-c0",
		},
		{
			name: "a url without a size is left alone",
			url:  "https://img.test/a",
			size: 64,
			want: "https://img.test/a",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := thumbnailURL(test.url, test.size); got != test.want {
				t.Errorf("thumbnailURL(%q, %d) = %q, want %q", test.url, test.size, got, test.want)
			}
		})
	}
	if got, want := sizeless("https://i.ytimg.com/vi/video/mqdefault.jpg?sqp=token"), "https://i.ytimg.com/vi/video/thumbnail.jpg?sqp=token"; got != want {
		t.Errorf("sizeless YouTube URL = %q, want %q", got, want)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

// waitForFailure waits until the cache has recorded how a picture failed, and
// returns that record.
func waitForFailure(t *testing.T, cache *thumbCache, url string) thumbFailure {
	t.Helper()
	var failure thumbFailure
	waitFor(t, func() bool {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		got, ok := cache.failed[url]
		failure = got
		return ok
	})
	return failure
}

// smallPicture is a picture the cache can decode and take a colour from.
func smallPicture(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 60, B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A 404 will keep answering 404; the cache must take it at its word instead of
// downloading it again on every retry.
func TestThumbCacheDoesNotRetryClientErrors(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w320-h320"
	sized := thumbnailURL(url, 320)
	cache.bitmap(url, 320)
	if failure := waitForFailure(t, cache, sized); !failure.permanent {
		t.Fatalf("a 404 was not held as permanent: %+v", failure)
	}

	// However long the session runs and however often the cover is drawn, a
	// 404 is not downloaded again.
	cache.mu.Lock()
	failure := cache.failed[sized]
	failure.at = time.Now().Add(-100 * thumbRetryMax)
	failure.attempts = 50
	cache.failed[sized] = failure
	cache.mu.Unlock()
	for range 5 {
		cache.bitmap(url, 320)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("a 404 was downloaded %d times, want 1", got)
	}
}

// A picture larger than the limit will not shrink on the next try.
func TestThumbCacheRejectsOversizedPictures(t *testing.T) {
	var hits atomic.Int32
	body := make([]byte, thumbLimit+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60"
	cache.bitmap(url, 60)
	if failure := waitForFailure(t, cache, thumbnailURL(url, 60)); !failure.permanent {
		t.Fatalf("an oversized picture was not held as permanent: %+v", failure)
	}
	cache.bitmap(url, 60)
	if got := hits.Load(); got != 1 {
		t.Errorf("an oversized picture was downloaded %d times, want 1", got)
	}
}

// Bytes that are not a picture will not decode on the next try either.
func TestThumbCacheDoesNotRetryUndecodablePictures(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("this is not a picture"))
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60"
	cache.bitmap(url, 60)
	if failure := waitForFailure(t, cache, thumbnailURL(url, 60)); !failure.permanent {
		t.Fatalf("an undecodable picture was not held as permanent: %+v", failure)
	}
	for range 5 {
		cache.bitmap(url, 60)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("an undecodable picture was downloaded %d times, want 1", got)
	}
}

// A picture that keeps failing for a reason that may pass waits longer each
// time, up to the cap.
func TestThumbFailureBacksOffExponentially(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, thumbRetry},
		{1, thumbRetry},
		{2, 2 * thumbRetry},
		{3, 4 * thumbRetry},
		{4, 8 * thumbRetry},
		{5, 16 * thumbRetry},
		{6, thumbRetryMax},
		{100, thumbRetryMax},
	}
	for _, test := range cases {
		if got := (thumbFailure{attempts: test.attempts}).wait(); got != test.want {
			t.Errorf("wait after %d attempts = %v, want %v", test.attempts, got, test.want)
		}
	}
}

// Covers never pay for a colour; the picture only gives one up when the
// player asks for a theme seed.
func TestThumbCacheTakesColourOnlyOnRequest(t *testing.T) {
	body := smallPicture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	url := server.URL + "/cover=w60-h60"
	sized := thumbnailURL(url, 60)
	cache.bitmap(url, 60)
	waitFor(t, func() bool { return cache.bitmap(url, 60) != nil })

	cache.mu.Lock()
	entry := cache.bitmaps[sized]
	ready := entry != nil && entry.colourReady
	cache.mu.Unlock()
	if entry == nil {
		t.Fatal("the cover did not land")
	}
	if ready {
		t.Fatal("a cover paid for a colour nothing asked for")
	}

	colour, ok := cache.colour(url, 60)
	if !ok || colour.B < 150 || colour.R > 80 {
		t.Fatalf("colour = %v, %v; want the blue", colour, ok)
	}
	cache.mu.Lock()
	ready = cache.bitmaps[sized].colourReady
	cache.mu.Unlock()
	if !ready {
		t.Error("the colour was not remembered")
	}
}

// The most recently wanted wait is served first, so a fast scroll does not
// bury the covers on screen behind the ones it left behind.
func TestThumbCacheServesVisibleWaitsBeforeStaleOnes(t *testing.T) {
	body := smallPicture(t)
	gate := make(chan struct{})
	var gateOnce sync.Once
	release := func() { gateOnce.Do(func() { close(gate) }) }
	var mu sync.Mutex
	var arrived []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived = append(arrived, r.URL.Path)
		mu.Unlock()
		<-gate
		_, _ = w.Write(body)
	}))
	defer func() { release(); server.Close() }()
	saw := func(path string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, got := range arrived {
			if got == path {
				return true
			}
		}
		return false
	}

	cache := newThumbCache(func() {})
	const busy = thumbFetches
	for i := range busy {
		cache.bitmap(server.URL+"/busy"+strconv.Itoa(i)+"=w60-h60", 60)
	}
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(arrived) == busy
	})

	// With every worker busy, ask for a stale cover and then for one on
	// screen. Only one worker frees; it must take the visible one.
	cache.bitmap(server.URL+"/stale=w60-h60", 60)
	cache.bitmap(server.URL+"/visible=w60-h60", 60)
	gate <- struct{}{}
	waitFor(t, func() bool { return saw("/visible=w60-h60") })
	if saw("/stale=w60-h60") {
		t.Error("a stale wait was served before the visible one")
	}
}

// The failure record is bounded, and expired transient failures are pruned.
func TestThumbCacheBoundsFailures(t *testing.T) {
	t.Run("prunes expired", func(t *testing.T) {
		cache := newThumbCache(func() {})
		cache.mu.Lock()
		for i := range thumbFailures + 1 {
			cache.failed[strconv.Itoa(i)] = thumbFailure{at: time.Now().Add(-2 * thumbRetryMax), attempts: 1}
		}
		cache.pruneFailedLocked()
		left := len(cache.failed)
		cache.mu.Unlock()
		if left != 0 {
			t.Errorf("expired failures were kept: %d left", left)
		}
	})
	t.Run("caps the record", func(t *testing.T) {
		cache := newThumbCache(func() {})
		cache.mu.Lock()
		for i := range thumbFailures * 2 {
			cache.failed[strconv.Itoa(i)] = thumbFailure{at: time.Now(), attempts: 1, permanent: true}
		}
		cache.pruneFailedLocked()
		left := len(cache.failed)
		cache.mu.Unlock()
		if left > thumbFailures {
			t.Errorf("failure record grew to %d entries, want at most %d", left, thumbFailures)
		}
	})
}

// The compressed sources are dropped least-recently-used, so a picture that
// is still on screen keeps the bytes that rebuild it.
func TestThumbCacheDropsTheSourceUsedLeastRecently(t *testing.T) {
	cache := newThumbCache(func() {})
	chunk := thumbSourceBudget / 4
	cache.mu.Lock()
	cache.storeSource("a", make([]byte, chunk))
	cache.storeSource("b", make([]byte, chunk))
	cache.source("a") // a is read, so it is no longer the oldest
	cache.storeSource("c", make([]byte, chunk))
	cache.storeSource("d", make([]byte, chunk))
	cache.storeSource("e", make([]byte, chunk))
	_, kept := cache.sources["a"]
	_, dropped := cache.sources["b"]
	cache.mu.Unlock()
	if !kept {
		t.Error("a was dropped although it was read again")
	}
	if dropped {
		t.Error("b was kept although it went unused longest")
	}
}

func BenchmarkThumbCacheLookup(b *testing.B) {
	url := "https://lh3.googleusercontent.com/cover=w544-h544-l90-rj"
	b.Run("hit", func(b *testing.B) {
		cache := newThumbCache(func() {})
		cache.mu.Lock()
		cache.store(thumbnailURL(url, 512), &thumb{bitmap: &ui.Bitmap{}, bytes: 1})
		cache.mu.Unlock()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if cache.bitmap(url, 512) == nil {
				b.Fatal("cache hit returned nothing")
			}
		}
	})
	b.Run("known-bad", func(b *testing.B) {
		cache := newThumbCache(func() {})
		cache.mu.Lock()
		cache.failed[thumbnailURL(url, 512)] = thumbFailure{at: time.Now(), permanent: true}
		cache.mu.Unlock()
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			if cache.bitmap(url, 512) != nil {
				b.Fatal("a known-bad picture was returned")
			}
		}
	})
	b.Run("derive", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = thumbnailURL(url, 512)
		}
	})
}

func TestThumbCacheDropsTheBitmapDrawnLeastRecently(t *testing.T) {
	cache := newThumbCache(func() {})
	cover := func(n string) string { return "https://covers.example/" + n + "=w320-h320" }
	size := thumbBudget/3 + 1
	for _, name := range []string{"a", "b", "c"} {
		cache.mu.Lock()
		cache.store(cover(name), &thumb{bitmap: &ui.Bitmap{}, bytes: size})
		cache.mu.Unlock()
		if name == "b" {
			// a is drawn again, so b is the one that has gone unseen longest.
			cache.mu.Lock()
			cache.touch(cache.bitmaps[cover("a")])
			cache.mu.Unlock()
		}
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if _, ok := cache.bitmaps[cover("b")]; ok {
		t.Error("b was kept although it was drawn least recently")
	}
	if _, ok := cache.bitmaps[cover("a")]; !ok {
		t.Error("a was dropped although it was drawn again")
	}
	if _, ok := cache.bitmaps[cover("c")]; !ok {
		t.Error("c was dropped on landing")
	}
	if cache.held != 2*size || len(cache.sizes) != 2 {
		t.Errorf("held = %d (want %d), sizes = %d (want 2)", cache.held, 2*size, len(cache.sizes))
	}
}

// A page asks for dozens of covers at once; only a few may download together.
func TestThumbCacheLimitsConcurrentDownloads(t *testing.T) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil); err != nil {
		t.Fatal(err)
	}
	var active, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write(buf.Bytes())
	}))
	defer server.Close()

	cache := newThumbCache(func() {})
	const covers = 30
	for i := range covers {
		cache.bitmap(server.URL+"/cover"+strconv.Itoa(i)+"=w60-h60", 60)
	}
	waitFor(t, func() bool { cache.mu.Lock(); defer cache.mu.Unlock(); return len(cache.bitmaps) == covers })
	if got := peak.Load(); got > thumbFetches {
		t.Errorf("%d downloads ran together, want at most %d", got, thumbFetches)
	}
}

func TestThumbEvictionKeepsIndexesConsistent(t *testing.T) {
	cache := newThumbCache(func() {})
	cache.synth = nil
	for i := range 5 {
		cache.store(fmt.Sprintf("https://x/%d=w64-h64", i), &thumb{bitmap: &ui.Bitmap{}, bytes: thumbBudget / 3})
	}
	if cache.recent.Len() != len(cache.bitmaps) || len(cache.bitmaps) != 3 {
		t.Fatalf("list = %d, bitmaps = %d; want 3 each", cache.recent.Len(), len(cache.bitmaps))
	}
	if cache.held != 3*(thumbBudget/3) {
		t.Errorf("held = %d, want %d", cache.held, 3*(thumbBudget/3))
	}
	if _, ok := cache.bitmaps["https://x/0=w64-h64"]; ok {
		t.Error("oldest bitmap survived")
	}
	n := 0
	for _, urls := range cache.sizes {
		n += len(urls)
	}
	if n != 3 {
		t.Errorf("size index has %d entries, want 3", n)
	}
}
