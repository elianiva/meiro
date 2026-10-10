//go:build snapshot

// Snapshots render the app's pages to PNG files, for looking at them. They are
// a build-tagged check, run explicitly so they never slow or flake the default
// suite:
//
//	go test -tags snapshot -run TestSnapshots .
//
// The justfile's `snapshots` recipe does this. Each snapshot also asserts that
// the window drew something, so a broken frame fails instead of writing a
// blank PNG.

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// drawn reports whether the window shows more than a handful of colours, so a
// snapshot of an empty frame fails rather than being written silently.
func drawn(img *image.RGBA) bool {
	seen := map[color.RGBA]struct{}{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y += 4 {
		for x := b.Min.X; x < b.Max.X; x += 4 {
			seen[img.RGBAAt(x, y)] = struct{}{}
			if len(seen) > 8 {
				return true
			}
		}
	}
	return false
}

func save(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	if !drawn(tt.Image()) {
		t.Errorf("snapshot %s drew a blank window", name)
	}
	f, err := os.Create(filepath.Join(os.Getenv("MEIRO_SNAPSHOTS"), name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshots(t *testing.T) {
	if os.Getenv("MEIRO_SNAPSHOTS") == "" {
		t.Fatal("set MEIRO_SNAPSHOTS to a directory to write snapshots")
	}
	play := func(a *app) {
		a.current = songs("qp", "Glass Hours")[0]
		a.queue = songs("qp", "Glass Hours", "Slow Burn", "Paper Lanterns", "Static Bloom", "Midnight Cartography")
		a.total = 3*time.Minute + 24*time.Second
		a.scrub = 74
	}

	a, tt := newShotApp("/home", false)
	save(t, tt, "home-light")
	track := a.feed.sections[0].Items[0]
	track.BrowseID = "UCartist"
	a.feed.sections[0].Items[0] = track
	a.menu.key = songKey(track, songOptions{list: "/home#0"})
	a.menu.open = true
	tt.Frame()
	save(t, tt, "track-menu")
	a.menu = itemMenu{}
	tt.Frame()
	album := a.feed.sections[1].Items[0]
	if r, ok := tt.Find(album.Title); ok {
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
		save(t, tt, "grid-card-hover")
		if err := tt.Click("More options for " + album.Title); err != nil {
			t.Fatal(err)
		}
		save(t, tt, "grid-card-menu")
	} else {
		t.Fatalf("listen-again card %q is not visible for its snapshot", album.Title)
	}
	a.menu = itemMenu{}
	tt.Frame()
	play(a)
	tt.Frame()
	save(t, tt, "home-light-player")
	a.current.Kind = "video"
	tt.Frame()
	save(t, tt, "home-light-video-player")

	a, tt = newShotApp("/home", true)
	play(a)
	tt.Frame()
	save(t, tt, "home-dark-player")

	a, tt = newShotApp("/album/MPREb_x", false)
	a.details["/album/MPREb_x"] = detail{title: "Deep Focus", subtitle: "Album • Aurora Vale • 2024 • 12 songs", art: "https://art.test/la/a0", kind: pageAlbum}
	a.detail = a.details["/album/MPREb_x"]
	a.feed = pageState{items: songs("al", "Opening", "Glass Hours", "Slow Burn", "Paper Lanterns", "Static Bloom", "Midnight Cartography", "Salt & Honey", "Afterimage")}
	for i := range a.feed.items {
		a.feed.items[i].Thumbnail = ""
	}
	play(a)
	a.current = a.feed.items[1]
	tt.Frame()
	save(t, tt, "album-light")

	a, tt = newShotApp("/playlist/VLPL_video", false)
	a.detail = detail{title: "Video playlist", subtitle: "A playlist with videos", kind: pagePlaylist}
	a.feed = pageState{sections: []youtube.MusicSection{{
		Kind: "playlistVideoListRenderer",
		Items: []youtube.MusicItem{{
			ID: "playlist-video", VideoID: "playlist-video", Kind: "video", Title: "Playlist video",
			Subtitle: "Aurora Vale • Live", Duration: "5:21", Thumbnail: "https://art.test/video/playlist",
		}},
	}}}
	tt.SetSize(1600, 1000)
	tt.Frame()
	save(t, tt, "playlist-video")

	a, tt = newShotApp("/search", false)
	a.settings.Recent = []string{"ambient focus", "lofi beats", "aurora vale"}
	tt.Frame()
	save(t, tt, "search-landing")
	a.search.query = "aurora"
	a.search.suggestions = []string{"aurora vale", "aurora borealis", "aurora vale live"}
	tt.Frame()
	save(t, tt, "search-suggestions")
	a.search.query, a.search.submitted = "aurora", "aurora"
	a.search.items = append(songs("sr", "Aurora", "Aurora Borealis", "Northern Aurora"), albums("sr", "Aurora Vale Live")...)
	tt.Frame()
	save(t, tt, "search-results")
	a.search.kind = len(searchKinds) - 1
	a.search.items = []youtube.MusicItem{{
		ID: "video-1", VideoID: "video-1", Kind: "video", Title: "Aurora Vale — Live Session",
		Subtitle: "Aurora Vale • Live", Duration: "12:34", Thumbnail: "https://art.test/video/live",
	}}
	tt.Frame()
	save(t, tt, "search-video-result")

	a, tt = newShotApp("/settings", false)
	play(a)
	tt.Frame()
	save(t, tt, "settings-light")
	a.settings.Seed, a.settings.Style = "#e0620d", int(m3.Expressive)
	a.themeAt = time.Time{}
	tt.SetDark(true)
	save(t, tt, "settings-dark-orange")

	a, tt = newShotApp("/home", false)
	play(a)
	a.current.BrowseID = "UCartist"
	a.npOpen = true
	tt.Frame()
	save(t, tt, "now-playing")
	a.menu.key = itemKey("now-playing-menu", a.current)
	a.menu.open = true
	tt.Frame()
	save(t, tt, "now-playing-menu")
	a.menu = itemMenu{}
	tt.Frame()
	a.current.Kind = "video"
	tt.Frame()
	save(t, tt, "now-playing-video")
	a.npTab = 1
	a.lyrics = lyricsState{videoID: "qp-0", text: "Verse one\nSomething in the glass hours\nLight comes through the window\n\nChorus\nHold on to the slow burn"}
	tt.Frame()
	save(t, tt, "now-playing-lyrics")

	a, tt = newShotApp("/home", false)
	play(a)
	a.queue = songs("qp", "Glass Hours", "Slow Burn", "Paper Lanterns", "Static Bloom", "Midnight Cartography")
	a.recommendationStart, a.queueSource = 3, "zoo / Ilios"
	a.npOpen = true
	tt.Frame()
	save(t, tt, "now-playing-autoplay")
	a.npTab = 2
	a.related = relatedState{videoID: a.current.VideoID, items: songs("related", "Paper Lanterns", "Salt & Honey", "Afterimage")}
	tt.Frame()
	save(t, tt, "now-playing-related")

	a, tt = newShotApp("/home", false)
	tt.SetSize(860, 560)
	play(a)
	tt.Frame()
	save(t, tt, "narrow-home")
	a.npOpen = true
	tt.Frame()
	save(t, tt, "narrow-now-playing")
	a.npQueueOnly = true
	tt.Frame()
	save(t, tt, "narrow-queue")

	_, tt = newShotApp("/home", false)
	tt.Move(380, 560)
	tt.Frame()
	save(t, tt, "home-hover")

	_, tt = newShotApp("/library", true)
	tt.Frame()
	save(t, tt, "library-signed-out")

	a, tt = newShotApp("/recap", true)
	a.signedIn, a.account = true, youtube.AccountDetails{Name: "Me"}
	a.feed = pageState{sections: homeSections()}
	play(a)
	tt.Frame()
	save(t, tt, "recap-signed-in")

	a, tt = newShotApp("/home", true)
	a.signedIn, a.account = true, youtube.AccountDetails{Name: "Me", Email: "me@example.com"}
	a.accounts = []youtube.AccountChannel{
		{Name: "Main channel", ChannelID: "UC-main", Selected: true},
		{Name: "Brand channel", ChannelID: "UC-brand"},
	}
	a.settings.Channel = "UC-main"
	a.menuOpen = true
	play(a)
	tt.Frame()
	save(t, tt, "account-channels")
}
