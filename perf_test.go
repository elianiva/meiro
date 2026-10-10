package main

import (
	"image"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// shelfApp shows n album cards in one shelf, with artwork that needs no
// network. It returns the app and the URLs of the cards it built.
func shelfApp(n int) (*app, map[string]bool) {
	a := newTestApp()
	a.location = "/home"
	items := make([]youtube.MusicItem, n)
	for i := range items {
		id := strconv.Itoa(i)
		items[i] = youtube.MusicItem{
			ID: "MPREb_" + id, BrowseID: "MPREb_" + id, Kind: "music_item",
			Title: "Album " + id, Thumbnail: "https://art.test/albums/" + id,
		}
	}
	a.feed = pageState{sections: []youtube.MusicSection{{Title: "Albums", Items: items}}}
	built := make(map[string]bool)
	art := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	a.thumbs.synth = func(url string, _ int) *ui.Bitmap {
		built[url] = true
		return art
	}
	ui.NewTester(a.view, 1000, 700)
	return a, built
}

// A shelf builds only the cards in view, yet keeps the width all of them
// would take, so it scrolls exactly as far as it did.
func TestCardShelfBuildsOnlyVisibleCardsAndKeepsItsScroll(t *testing.T) {
	const step = float32(cardWidth + 4)
	small, builtSmall := shelfApp(20)
	large, builtLarge := shelfApp(40)
	if len(builtSmall) >= 20 || len(builtLarge) >= 40 {
		t.Fatalf("built %d of 20 and %d of 40 cards; only the cards in view should be built", len(builtSmall), len(builtLarge))
	}
	smallShelf, largeShelf := small.carousels["/home#0"], large.carousels["/home#0"]
	if smallShelf == nil || largeShelf == nil {
		t.Fatal("the shelves did not create their carousels")
	}
	if want := float32(20) * step; largeShelf.MaxX-smallShelf.MaxX != want {
		t.Errorf("a shelf of 40 scrolls %v past one of 20, want %v: off-view cards lost their width", largeShelf.MaxX-smallShelf.MaxX, want)
	}
}

// The flattened rows are reused until the page's data changes.
func TestRowsAreReusedUntilThePageChanges(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.location = "/search"
	a.search.submitted = "x"
	a.search.items = benchTracks(3)
	a.syncRows()
	if len(a.rows) == 0 {
		t.Fatal("the page built no rows")
	}
	a.rows[0].title = "sentinel"
	a.rowsDirty = false
	a.syncRows()
	if a.rows[0].title != "sentinel" {
		t.Error("syncRows rebuilt the rows with no change")
	}
	a.rowsDirty = true
	a.syncRows()
	if a.rows[0].title == "sentinel" {
		t.Error("syncRows did not rebuild after a change")
	}
}

// A search that lands rebuilds the rows, through the flag every page load
// sets.
func TestSearchResultsRebuildTheRows(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	a.search.query = "ambient"
	a.runSearch("ambient")
	tt.Frame()
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the results of the search did not reach the page: %q", tt.Texts())
	}
}

// The theme is resolved once while the settings and appearance hold still,
// and again when they change.
func TestThemeIsResolvedOnceUntilItsInputsChange(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	var seen []*m3.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		seen = append(seen, m3.Of(c))
	}, 1000, 900)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	seen = nil
	tt.Frame()
	tt.Frame()
	if len(seen) != 2 || seen[0] != seen[1] {
		t.Fatalf("an unchanged theme was resolved again: %v then %v", seen[0], seen[1])
	}
	a.settings.Seed, a.settings.Dynamic = "#d81b78", false
	tt.Frame()
	if len(seen) != 3 || seen[2] == seen[1] {
		t.Error("a changed seed did not resolve a new theme")
	}
}

// A settled page builds the view once per frame, as ReduceMotion expects.
func TestIdlePageBuildsOncePerFrame(t *testing.T) {
	a := newTestApp()
	builds := 0
	tt := ui.NewTester(func(c *ui.Context) {
		builds++
		a.view(c)
	}, 1000, 700)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	for i := 0; i < 2; i++ {
		builds = 0
		tt.Frame()
		if builds != 1 {
			t.Errorf("idle frame %d built the view %d times, want 1", i, builds)
		}
	}
}
