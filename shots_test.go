package main

import (
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// The helpers here build an app whose pages are full of realistic, synthetic
// data, for the screenshot writer and the tests that want a populated page
// without a live session.

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
