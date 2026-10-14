package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/applog"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/youtube"
)

// profile lays out the folders of an installation under root, and points the
// app's folders at them, the way the platform does for a real one. A used
// profile has what a few months of listening leave behind: settings, a saved
// sign-in, a restored queue, a full history, full caches and an old log.
func profile(b *testing.B, root string, used bool) {
	b.Helper()
	b.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	b.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	b.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	if !used {
		return
	}
	data, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		b.Fatal(err)
	}
	cache, _ := mygo.App.Path(mygo.PathCache)
	logs, _ := mygo.App.Path(mygo.PathLogs)
	write := func(path string, content []byte) {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	write(filepath.Join(data, "settings.json"), []byte(`{"volume":60,"cacheSongs":50,"recent":["a","b","c"]}`))
	write(filepath.Join(data, "cookie.txt"), []byte("SAPISID=bench"))
	var items []youtube.MusicItem
	for i := range playstate.MaxItems {
		items = append(items, youtube.MusicItem{VideoID: fmt.Sprintf("q%04d", i), Title: "Queue track", Subtitle: "Someone", Duration: "3:30", Thumbnail: "https://img.test/x"})
	}
	state, _ := json.Marshal(playstate.State{Items: items, Index: 40, RecStart: -1, Position: 90 * time.Second})
	write(filepath.Join(data, "playback.json"), state)
	var entries []history.Entry
	for i := range history.MaxEntries {
		entries = append(entries, history.Entry{VideoID: fmt.Sprintf("h%05d", i), Title: "A played track", Artist: "An artist", PlayedAt: time.Now().Add(-time.Duration(i) * time.Hour)})
	}
	hist, _ := json.Marshal(entries)
	write(filepath.Join(data, "history.json"), hist)
	// The artwork cache full to its limit, in 15 KB covers; the audio cache
	// holding 50 tracks.
	art := make([]byte, 15<<10)
	for i := range artworkCacheLimit / len(art) {
		write(filepath.Join(cache, "Meiro Artwork Cache", fmt.Sprintf("%032x.bin", i)), art)
	}
	for i := range 50 {
		write(filepath.Join(cache, "Meiro Audio Cache", fmt.Sprintf("%064x.webm", i)), []byte("x"))
	}
	write(filepath.Join(logs, applog.FileName), make([]byte, applog.MaxSize/2))
}

// BenchmarkStartup times what a launch does before the first frame, in the
// order main does it, on a fresh profile and on a used, signed-in one. The
// window itself is the toolkit's and is not part of it. Each phase is
// reported in its own metric, so the slow one shows.
func BenchmarkStartup(b *testing.B) {
	for _, used := range []bool{false, true} {
		name := "fresh"
		if used {
			name = "signed-in"
		}
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				profile(b, b.TempDir(), used)
				b.StartTimer()
				var marks []time.Time
				lap := func() { marks = append(marks, time.Now()) }
				lap()
				logs, _ := mygo.App.Path(mygo.PathLogs)
				file, err := applog.Open(logs, func() string { return "header" })
				if err != nil {
					b.Fatal(err)
				}
				lap()
				a := newTestApp()
				lap()
				// Background work runs off the main thread, as it does in the
				// app; newTestApp runs it inline.
				inline := a.run
				a.run = func(work func()) { go work() }
				a.setup()
				a.run = inline
				lap()
				a.restoreAccount()
				a.loadHistory()
				tt := ui.NewTester(a.view, 1180, 760)
				tt.Frame()
				lap()
				for i, phase := range []string{"openlog-ms", "newapp-ms", "setup-ms", "firstframe-ms"} {
					b.ReportMetric(float64(marks[i+1].Sub(marks[i]).Microseconds())/1000, phase)
				}
				b.ReportMetric(float64(marks[4].Sub(marks[0]).Microseconds())/1000, "total-ms")
				b.StopTimer()
				_ = file.Close()
				a.closeAudioCaches()
				a.player.Stop()
				b.StartTimer()
			}
		})
	}
}
