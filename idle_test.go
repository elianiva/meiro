package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/youtube"
)

// TestIdleCPU measures the CPU the app uses with no one touching it: with
// nothing running (the control), with a restored queue paused, and with a
// track playing. It runs the periodic work main starts, at its real
// intervals, so it is skipped unless MEIRO_IDLE_SECONDS says how long each
// phase lasts. ffmpeg, a child process, is not counted:
//
//	MEIRO_IDLE_SECONDS=60 go test -run TestIdleCPU -v .
//
// The window and the frames it draws are not part of it: a frame costs what
// BenchmarkFrame says, and a playing track asks for one every pollInterval.
func TestIdleCPU(t *testing.T) {
	seconds, _ := strconv.Atoi(os.Getenv("MEIRO_IDLE_SECONDS"))
	if seconds == 0 {
		t.Skip("set MEIRO_IDLE_SECONDS to run the idle measurement")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("playing needs ffmpeg")
	}
	dir := t.TempDir()
	tone := filepath.Join(dir, "tone.wav")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=3600", "-ar", "44100", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	a, _ := soakApp(t, dir)
	a.streams = audio.NewURLs(func(context.Context, string, string) (string, time.Duration, error) {
		return tone, time.Hour, nil
	})
	window := time.Duration(seconds) * time.Second
	measure := func(name string) {
		cpu0 := cpuSeconds()
		start := time.Now()
		for elapsed := time.Duration(0); elapsed < window; {
			step := min(10*time.Second, window-elapsed)
			time.Sleep(step)
			elapsed = time.Since(start)
			t.Logf("%-8s t=%3ds app cpu %.3fs (%.2f%% of one core)", name, int(elapsed.Seconds()), cpuSeconds()-cpu0, 100*(cpuSeconds()-cpu0)/elapsed.Seconds())
		}
		t.Logf("%-8s over %s: app %.2f%% of one core", name, window, 100*(cpuSeconds()-cpu0)/window.Seconds())
	}

	time.Sleep(10 * time.Second) // what building the app left to do settles
	measure("control")           // nothing started: the cost of the test process alone

	var items []youtube.MusicItem
	for i := range playstate.MaxItems {
		items = append(items, youtube.MusicItem{VideoID: "q" + strconv.Itoa(i), Title: "Queue track", Subtitle: "Someone", Duration: "59:00"})
	}
	a.queue.restore(playstate.State{Items: items, Index: 3, RecStart: -1})
	a.current = a.queue.current()
	a.total = time.Hour
	// What main starts once the window is up.
	a.startTicker()
	a.every(persistInterval, func() { a.update(a.persistPlayback) })
	a.watchSession()
	measure("paused")

	a.start(a.current)
	time.Sleep(3 * time.Second) // ffmpeg opens the stream
	if !a.player.Playing() {
		t.Fatalf("the tone is not playing: %q", a.player.Failure())
	}
	measure("playing")
	a.player.Stop()
	a.stopTicking()
}
