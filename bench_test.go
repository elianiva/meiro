package main

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/youtube"
)

// benchApp is a signed-in app whose pages are the large fixtures of
// benchfixture_test.go, and whose tracks never resolve, so no ffmpeg runs.
func benchApp(b *testing.B) *app {
	b.Helper()
	a := newTestApp()
	client := func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: benchMusic{}}, CookieAuth: auth})
	}
	a.account.newClient, a.account.public = client, client(nil)
	auth, err := youtube.NewCookieAuth("SAPISID=bench", youtube.CookieOptions{})
	if err != nil {
		b.Fatal(err)
	}
	a.account.session = &session{client: client(auth), cookie: "SAPISID=bench"}
	a.thumbs = artwork.New(&http.Client{Transport: benchNoArtwork{}}, func() {})
	a.settings.AutoPlay = false
	a.streams = audio.NewURLs(func(context.Context, string, string) (string, time.Duration, error) {
		return "", 0, errors.New("bench")
	})
	b.Cleanup(a.player.Stop)
	return a
}

// BenchmarkFrame builds, lays out and paints one whole frame of a page that
// has loaded, as the window does whenever anything on it moves.
func BenchmarkFrame(b *testing.B) {
	for _, bench := range []struct {
		name, path, want string
		setup            func(a *app)
	}{
		{"home", "/home", "Shelf0 0", nil},
		{"album-500", "/album/MPREb_big", "Album Track 0", nil},
		{"library-600", "/library", "Playlist 0", nil},
		{"settings", "/settings", "Appearance", nil},
		{"now-playing-queue-500", "/album/MPREb_big", "Playing from", func(a *app) {
			a.playAll()
			a.openPlayer(tabQueue, false)
		}},
	} {
		b.Run(bench.name, func(b *testing.B) {
			a := benchApp(b)
			a.router.Push(bench.path)
			tt := ui.NewTester(a.view, 1180, 760)
			tt.Frame()
			if bench.setup != nil {
				bench.setup(a)
			}
			tt.Frame()
			tt.Frame()
			settle(tt)
			if !tt.HasText(bench.want) {
				b.Fatalf("%s does not show %q: %q", bench.path, bench.want, tt.Texts())
			}
			b.ReportAllocs()
			for b.Loop() {
				tt.Frame()
			}
		})
	}
}

// settle draws frames until a page's entry animations are over, so a
// benchmark measures the frames of a page at rest.
func settle(tt *ui.Tester) {
	for end := time.Now().Add(time.Second); time.Now().Before(end); {
		tt.Frame()
	}
}

// BenchmarkRows rebuilds the rows of a loaded 500-track album, as a change of
// the page's data does.
func BenchmarkRows(b *testing.B) {
	a := benchApp(b)
	a.router.Push("/album/MPREb_big")
	tt := ui.NewTester(a.view, 1180, 760)
	tt.Frame()
	if len(a.page.playable) != benchAlbumTracks {
		b.Fatalf("the album has %d tracks", len(a.page.playable))
	}
	b.ReportAllocs()
	for b.Loop() {
		a.page.rowsDirty = true
		a.syncRows()
	}
}

// BenchmarkNavigateCached goes back and forth between two loaded pages, which
// the page cache keeps.
func BenchmarkNavigateCached(b *testing.B) {
	a := benchApp(b)
	tt := ui.NewTester(a.view, 1180, 760)
	a.router.Push("/home")
	tt.Frame()
	a.router.Push("/album/MPREb_big")
	tt.Frame()
	b.ReportAllocs()
	for b.Loop() {
		a.router.Back()
		a.syncLocation()
		a.router.Forward()
		a.syncLocation()
	}
}
