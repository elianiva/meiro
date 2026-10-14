package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/youtube"
)

// The soak test plays a long session in a short time: each step is a minute
// of use, which opens pages the size of a real account's with fresh
// artwork, scrolls them, and plays a track. It is skipped unless
// MEIRO_SOAK_STEPS says how many minutes to simulate:
//
//	MEIRO_SOAK_STEPS=600 go test -run TestSoak -v .
//
// Every sample is taken after a garbage collection, so it is what the app
// holds, not what it has not yet freed.

// soakMusic answers like benchMusic, but every response names pictures that
// no earlier response named, as the real service's recommendations do.
type soakMusic struct{ generation atomic.Int64 }

func (m *soakMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	resp, err := benchMusic{}.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	fresh := fmt.Sprintf("https://img.test/g%d/", m.generation.Add(1))
	resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(body), "https://img.test/", fresh)))
	return resp, nil
}

// soakArtwork answers every picture with a 544 by 544 JPEG, one of a few
// dozen distinct ones, so downloading, decoding and the budgets all run.
type soakArtwork struct{ pictures [][]byte }

func newSoakArtwork() *soakArtwork {
	s := &soakArtwork{}
	for seed := range 24 {
		img := image.NewRGBA(image.Rect(0, 0, 544, 544))
		for y := range 544 {
			for x := range 544 {
				img.Set(x, y, color.RGBA{uint8(x/3 + seed*9), uint8(y/3 + seed*5), uint8((x*y)>>7 + seed), 255})
			}
		}
		var buf bytes.Buffer
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80})
		s.pictures = append(s.pictures, buf.Bytes())
	}
	return s
}

func (s *soakArtwork) RoundTrip(request *http.Request) (*http.Response, error) {
	n := 0
	for _, c := range request.URL.Path {
		n = n*31 + int(c)
	}
	body := s.pictures[(n&0x7fffffff)%len(s.pictures)]
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: request}, nil
}

// soakApp is benchApp with fresh pictures, the history and the playback
// state going to files, and the ticking work the app starts at launch.
func soakApp(t testing.TB, dir string) (*app, *ui.Tester) {
	a := newTestApp()
	music := &soakMusic{}
	client := func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: music}, CookieAuth: auth})
	}
	a.account.newClient, a.account.public = client, client(nil)
	auth, err := youtube.NewCookieAuth("SAPISID=soak", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: client(auth), cookie: "SAPISID=soak"}
	a.thumbs = artwork.New(&http.Client{Transport: newSoakArtwork()}, a.refresh)
	a.settings.AutoPlay = false
	a.streams = audio.NewURLs(func(context.Context, string, string) (string, time.Duration, error) {
		return "", 0, errors.New("soak")
	})
	a.historyPath = filepath.Join(dir, "history.json")
	a.playSave.writer.Path = filepath.Join(dir, "playback.json")
	t.Cleanup(a.player.Stop)
	tt := ui.NewTester(a.view, 1180, 760)
	return a, tt
}

// soakSample is one line of the table the soak prints.
type soakSample struct {
	step                        int
	heapMB, rssMB               float64
	goroutines                  int
	art                         artwork.Stats
	history, queue, pages, cpuS int
}

func rssMB() float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			kb, _ := strconv.Atoi(strings.Fields(rest)[0])
			return float64(kb) / 1024
		}
	}
	return 0
}

func TestSoak(t *testing.T) {
	steps, _ := strconv.Atoi(os.Getenv("MEIRO_SOAK_STEPS"))
	if steps == 0 {
		t.Skip("set MEIRO_SOAK_STEPS to run the soak")
	}
	// MEIRO_SOAK_LEAK adds a deliberate leak of that many kilobytes a step: the
	// control that shows the check below notices growth.
	leakKB, _ := strconv.Atoi(os.Getenv("MEIRO_SOAK_LEAK_KB"))
	var leaked [][]byte
	if os.Getenv("MEIRO_SOAK_HEAP") != "" {
		runtime.MemProfileRate = 4 << 10
	}
	a, tt := soakApp(t, t.TempDir())
	every := max(steps/20, 1)
	var samples []soakSample
	sample := func(step int) {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		s := soakSample{
			step: step, heapMB: float64(m.HeapAlloc) / (1 << 20), rssMB: rssMB(), goroutines: runtime.NumGoroutine(),
			art: a.thumbs.Stats(), history: len(a.history), queue: a.queue.len(), pages: a.pages.Len(), cpuS: int(cpuSeconds()),
		}
		samples = append(samples, s)
		t.Logf("step %4d heap=%6.1fMB rss=%6.1fMB goroutines=%3d art=%d/%.1fMB src=%d/%.1fMB failed=%d keys=%d colours=%d history=%d queue=%d pages=%d cpu=%ds",
			s.step, s.heapMB, s.rssMB, s.goroutines, s.art.Bitmaps, float64(s.art.Bytes)/(1<<20), s.art.Sources,
			float64(s.art.SourceBytes)/(1<<20), s.art.Failed, s.art.Keys, s.art.Colours, s.history, s.queue, s.pages, s.cpuS)
	}
	scroll := func(frames int) {
		for range frames {
			tt.Scroll(600, 400, 0, -900)
			tt.Frame()
			time.Sleep(3 * time.Millisecond) // pictures land
		}
	}
	for step := 0; step <= steps; step++ {
		if step%every == 0 {
			sample(step)
		}
		if leakKB > 0 {
			leaked = append(leaked, make([]byte, leakKB<<10))
		}
		// A minute of use: browse the home page, an album and the library,
		// play from the album, look at the queue and the stats.
		a.pages.Clear() // the home and library pages show fresh pictures each visit
		a.router.Push("/home")
		tt.Frame()
		scroll(8)
		a.router.Push("/album/MPREb_big" + strconv.Itoa(step%30))
		tt.Frame()
		scroll(12)
		a.playAll()
		a.openPlayer(tabQueue, false)
		scroll(3)
		a.router.Push("/library")
		tt.Frame()
		scroll(10)
		a.router.Push("/stats")
		tt.Frame()
		a.persistPlayback()
		a.syncSystemMedia()
	}
	sample(steps + 1)
	runtime.KeepAlive(leaked)
	if path := os.Getenv("MEIRO_SOAK_HEAP"); path != "" {
		if f, err := os.Create(path); err == nil {
			_ = pprof.Lookup("heap").WriteTo(f, 0)
			_ = f.Close()
		}
	}

	// The first quarter is the cache filling; growth after it is a leak.
	var warm, last soakSample
	for _, s := range samples {
		if s.step*4 >= steps && warm.step == 0 {
			warm = s
		}
		last = s
	}
	if grew := last.heapMB - warm.heapMB; grew > 8 {
		t.Errorf("heap grew %.1f MB between step %d and %d (%.1f -> %.1f MB)", grew, warm.step, last.step, warm.heapMB, last.heapMB)
	}
	if grew := last.rssMB - warm.rssMB; grew > 64 {
		t.Errorf("RSS grew %.1f MB between step %d and %d", grew, warm.step, last.step)
	}
	if last.goroutines > warm.goroutines+4 {
		t.Errorf("goroutines grew from %d to %d", warm.goroutines, last.goroutines)
	}
	if last.history > history.MaxEntries {
		t.Errorf("history holds %d entries, over its cap", last.history)
	}
	if info, err := os.Stat(a.historyPath); err == nil {
		t.Logf("history file: %d entries, %d bytes", last.history, info.Size())
	}
}
