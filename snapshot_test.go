package main

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// Snapshots render the app's pages to PNG files, for looking at them. They
// run only when MEIRO_SNAPSHOTS names a directory:
//
//	MEIRO_SNAPSHOTS=/tmp/meiro-shots go test -run TestSnapshots .

var artCache sync.Map

// synthArt makes artwork out of a URL: a soft two-colour gradient with a
// disc, different for every URL.
func synthArt(url string, size int) *ui.Bitmap {
	if b, ok := artCache.Load(url); ok {
		return b.(*ui.Bitmap)
	}
	h := fnv.New32a()
	h.Write([]byte(url))
	seed := h.Sum32()
	hue := float64(seed%360) + 0
	a := m3.Palette{Hue: hue, Chroma: 0.13}.Tone(62)
	b := m3.Palette{Hue: hue + 70, Chroma: 0.12}.Tone(38)
	const n = 160
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	cx, cy := float64(30+seed%100), float64(30+(seed/7)%100)
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			t := (float64(x) + float64(y)) / (2 * n)
			c := mixColor(a, b, t)
			if d := math.Hypot(float64(x)-cx, float64(y)-cy); d < 38 {
				c = mixColor(c, ui.RGB(255, 255, 255), 0.25)
			}
			img.Set(x, y, color.RGBA{c.R, c.G, c.B, 255})
		}
	}
	bm := ui.NewBitmap(img)
	artCache.Store(url, bm)
	return bm
}

func mixColor(a, b ui.Color, t float64) ui.Color { return a.Mix(b, float32(t)) }

func songs(prefix string, names ...string) []youtube.MusicItem {
	var items []youtube.MusicItem
	for i, name := range names {
		items = append(items, youtube.MusicItem{
			ID: fmt.Sprintf("%s-%d", prefix, i), VideoID: fmt.Sprintf("%s-%d", prefix, i), Title: name,
			Subtitle: []string{"Aurora Vale", "Night Transit", "Low Tide", "Hollow Pines", "Mira Kessler"}[i%5],
			Kind:     "track", Duration: fmt.Sprintf("%d:%02d", 2+i%4, 7+(i*13)%50),
			Thumbnail: fmt.Sprintf("https://art.test/%s/%d", prefix, i),
		})
	}
	return items
}

func albums(prefix string, names ...string) []youtube.MusicItem {
	var items []youtube.MusicItem
	for i, name := range names {
		kind := "MPREb_" + fmt.Sprint(prefix, i)
		items = append(items, youtube.MusicItem{
			ID: kind, BrowseID: kind, Title: name, Kind: "music_item",
			Subtitle:  []string{"Album • Aurora Vale", "Single • Low Tide", "Album • Hollow Pines", "EP • Mira Kessler"}[i%4],
			Thumbnail: fmt.Sprintf("https://art.test/%s/a%d", prefix, i),
		})
	}
	return items
}

func artists(names ...string) []youtube.MusicItem {
	var items []youtube.MusicItem
	for i, name := range names {
		id := fmt.Sprintf("UCartist%d", i)
		items = append(items, youtube.MusicItem{
			ID: id, BrowseID: id, Title: name, Kind: "music_item",
			Subtitle: "2.4M subscribers", Thumbnail: "https://art.test/artist/" + name,
		})
	}
	return items
}

func homeSections() []youtube.MusicSection {
	return []youtube.MusicSection{
		{Title: "Quick picks", Items: songs("qp", "Glass Hours", "Slow Burn", "Paper Lanterns", "Static Bloom", "Midnight Cartography", "Salt & Honey", "Afterimage", "Low Orbit")},
		{Title: "Listen again", Items: albums("la", "Deep Focus", "Tidal", "Evergreen", "Soft Machine", "Northern Lights", "Dust & Gold")},
		{Title: "Artists you might like", Items: artists("Aurora Vale", "Night Transit", "Low Tide", "Hollow Pines", "Mira Kessler", "Pale Harbor")},
		{Title: "Albums for you", Items: albums("fy", "Quiet Machines", "The Long Way Home", "Orchid", "Slow Currents", "Halcyon", "Mirrors")},
	}
}

func newShotApp(path string, dark bool) (*app, *ui.Tester) {
	a := newTestApp()
	a.thumbs.synth = synthArt
	a.router = ui.NewRouter(path)
	a.location = path
	a.feed = pageState{sections: homeSections()}
	tt := ui.NewTester(a.view, 1180, 760)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.SetDark(dark)
	return a, tt
}

func save(t *testing.T, tt *ui.Tester, name string) {
	dir := os.Getenv("MEIRO_SNAPSHOTS")
	f, err := os.Create(filepath.Join(dir, name+".png"))
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
		t.Skip("set MEIRO_SNAPSHOTS to a directory to write snapshots")
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
