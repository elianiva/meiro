package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// These tests drive the whole app through its window, the way a user does:
// real pages, real clicks, and a fake YouTube at the network edge.

// signedInApp is an app whose account client speaks for a saved session.
func signedInApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp()
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: a.account.newClient(auth), cookie: "SAPISID=test"}
	return a
}

func TestEveryPageLoadsAndDraws(t *testing.T) {
	pages := []struct {
		path string
		want []string
	}{
		{"/home", []string{greeting(time.Now()), "Quick picks", "Ambient One"}},
		{"/explore", []string{"Moods", "Night Drive"}},
		{"/library", []string{"Library", "Your library lives here"}},
		{"/search", []string{"Find your next favourite"}},
		{"/settings", []string{"Settings", "Appearance"}},
		{"/album/MPREb_test", []string{"Album Track One"}},
		{"/playlist/VLPL_video", []string{"Playlist video"}},
		{"/artist/UCartist", []string{"Aurora Vale", "Artist Track"}},
		{"/recap", []string{"Your recap lives here"}},
		{"/nowhere", []string{"That page does not exist."}},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			a := newTestApp()
			a.router.Push(page.path)
			tt := ui.NewTester(a.view, 1000, 700)
			tt.Frame()
			for _, want := range page.want {
				if !tt.HasText(want) {
					t.Errorf("%s did not show %q: %q", page.path, want, tt.Texts())
				}
			}
		})
	}
}

func TestSearchFlow(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if !tt.HasText("Yorushika songs") {
		t.Fatalf("suggestions are missing: %q", tt.Texts())
	}
	if !tt.HasText("16M monthly audience") {
		t.Fatalf("the items the query matches are missing: %q", tt.Texts())
	}
	if searches.Load() != 0 {
		t.Fatal("typing ran a search")
	}

	if err := tt.Click("Yorushika songs"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Search Result Song") || searches.Load() != 1 {
		t.Fatalf("choosing a suggestion did not show results: %q", tt.Texts())
	}
	if len(a.settings.Recent) == 0 || a.settings.Recent[0] != "Yorushika songs" {
		t.Errorf("the search was not remembered: %v", a.settings.Recent)
	}

	// The items the query matches are chosen the same way: the panel opens
	// what it names.
	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if err := tt.Click("16M monthly audience"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.path(), "/artist/UCyorushika") {
		t.Fatalf("choosing an item opened %q", a.path())
	}
}

// An item of the panel under the search bar offers its menu on a right click,
// as it does wherever else it appears: a song offers the queue and its link,
// and anything that opens a page offers that. The panel stays open, and a
// right click must not also choose the item under the menu.
func TestRightClickingASuggestionOffersItsMenu(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if !tt.HasText("Suggested Song") || !tt.HasText("16M monthly audience") {
		t.Fatalf("the items the query matches are missing: %q", tt.Texts())
	}

	// A song is the song it is anywhere: the queue actions, and its link.
	if err := tt.RightClick("Suggested Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Play next", "Add to queue", "Add to playlist", "Copy link"} {
		if !tt.HasText(want) {
			t.Errorf("right-clicking a song among the suggestions offered no %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("Suggested Song") {
		t.Errorf("the right click closed the panel: %q", tt.Texts())
	}
	a.menu.close()
	tt.Frame()

	// An artist among the suggestions opens its page, or its link.
	if err := tt.RightClick("16M monthly audience"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Go to artist") || !tt.HasText("Copy link") {
		t.Fatalf("right-clicking an artist among the suggestions offered %q", tt.Texts())
	}
	if err := tt.Click("Copy link"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "https://music.youtube.com/browse/UCyorushika" {
		t.Errorf("Copy link copied %q, want the artist's link", got)
	}
	if a.menu.open {
		t.Error("the menu stayed open after one of its items was chosen")
	}

	// The panel stayed for the next item, and nothing was searched, opened or
	// played under the menu.
	if !tt.HasText("16M monthly audience") {
		t.Errorf("the panel closed: %q", tt.Texts())
	}
	if searches.Load() != 0 {
		t.Errorf("a right click ran %d searches", searches.Load())
	}
	if r, _ := a.route(); r.id != routeSearch {
		t.Errorf("a right click left the search page for %q", r.id)
	}
	if a.current.VideoID != "" {
		t.Errorf("a right click played %q", a.current.VideoID)
	}
}

// The search field is in the top bar of every page, and submitting it there
// shows the search page with what it ran.
func TestSearchingFromAnotherPage(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yorushika")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if r, _ := a.route(); r.id != routeSearch {
		t.Fatalf("a search from settings left the app on %q", a.path())
	}
	if !tt.HasText("Search Result Song") || searches.Load() != 1 {
		t.Fatalf("the search did not run: %q", tt.Texts())
	}
	if a.search.query != "yorushika" {
		t.Errorf("the field reads %q", a.search.query)
	}
}

// A search shows the filters YouTube offers for it, without one of the app's
// own, and each one searches the query again with the parameters it carries.
func TestSearchShowsYouTubesFilters(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if !tt.HasText("Songs") || !tt.HasText("Albums") || tt.HasText("All") {
		t.Fatalf("the filters are wrong: %q", tt.Texts())
	}

	if err := tt.Click("Songs"); err != nil {
		t.Fatal(err)
	}
	if searches.Load() != 1 || !strings.Contains(searchBody(), "EgWKAQIIAWoSEAUQAxAJ") {
		t.Fatalf("the filter did not search with its parameters: %q", searchBody())
	}
	if !tt.HasText("Filtered Song") || a.search.params != "EgWKAQIIAWoSEAUQAxAJ" {
		t.Fatalf("the filter did not show its own results: %q", tt.Texts())
	}

	if err := tt.Click("Clear filters"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Search Result Song") || a.search.params != "" || searches.Load() != 2 {
		t.Fatalf("clearing the filter did not search again: %q", tt.Texts())
	}
}

// The card a search leads with names what the search found, and selecting it
// opens that page.
func TestSearchLeadsWithItsTopResult(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if len(a.page.rows) == 0 || a.page.rows[0].kind != rowTopResult {
		t.Fatalf("the results do not lead with a card: %#v", a.page.rows)
	}
	if !tt.HasText("16M monthly audience") {
		t.Fatalf("the card shows no subtitle: %q", tt.Texts())
	}
	if err := tt.Click("Yorushika"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.path(), "/artist/") {
		t.Fatalf("the card opened %q", a.path())
	}
}

// A search shows the pages of its results itself as its list reaches the end of
// what it has, rather than offering the button that asks.
func TestSearchLoadsTheNextPageItself(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	searchesMore.Store(0)
	// The list reaching the end of what it has is what asks for the page
	// after it: the frame that lays the results out is enough.
	tt := ui.NewTester(a.view, 1000, 700)
	if searchesMore.Load() == 0 {
		t.Fatal("the search did not ask for the page after its results")
	}
	tt.Frame()
	if !tt.HasText("Later Song") {
		t.Fatalf("the next page did not arrive: %q", tt.Texts())
	}
	for _, r := range a.page.rows {
		if r.kind == rowMore {
			t.Fatalf("a search offers the button that asks: %q", tt.Texts())
		}
	}
}

// Right-clicking a search result opens its menu, whatever the result is: the
// top result and a row of an album, an artist or a playlist open the page
// they name, and a song offers the queue. A right click must not also open or
// play what it opened the menu for.
func TestRightClickingASearchResultOffersItsMenu(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1180, 850)
	tt.Frame()
	if err := tt.Click("Search songs, albums, artists, podcasts"); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor mixed")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	for _, want := range []string{"Mixed Album", "Row Album", "Row Artist", "Row Playlist", "Row Song"} {
		if !tt.HasText(want) {
			t.Fatalf("the search did not show %q: %q", want, tt.Texts())
		}
	}

	for _, c := range []struct {
		result string
		offer  string
	}{
		{"Mixed Album", "Open album"},  // the top result
		{"Row Album", "Open album"},    // an album among the rows
		{"Row Artist", "Go to artist"}, // an artist among the rows
		{"Row Playlist", "Open playlist"},
		{"Row Song", "Add to queue"}, // a song among the rows
	} {
		if tt.HasText(c.offer) {
			t.Fatalf("%q is on screen before right-clicking %q: %q", c.offer, c.result, tt.Texts())
		}
		if err := tt.RightClick(c.result); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !tt.HasText(c.offer) {
			t.Errorf("right-clicking %q offered no %q: %q", c.result, c.offer, tt.Texts())
		}
		if !tt.HasText("Copy link") {
			t.Errorf("the menu on %q has no Copy link: %q", c.result, tt.Texts())
		}
		a.menu.close()
		tt.Frame()
	}

	// Opening a menu is not opening the page, or playing the song, under it.
	if r, _ := a.route(); r.id != routeSearch {
		t.Errorf("a right click left the search page for %q", r.id)
	}
	if a.current.VideoID != "" {
		t.Errorf("a right click played %q", a.current.VideoID)
	}
}

func TestRightClickingTheHeroArtOffersItsMenu(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/album/MPREb_test")
	mustShow(t, tt, "Deep Focus")
	if tt.HasText("Start radio") {
		t.Fatalf("Start radio is on screen before right-clicking: %q", tt.Texts())
	}
	if err := tt.RightClick("More options for Deep Focus"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Start radio", "Shuffle play", "Copy link"} {
		if !tt.HasText(want) {
			t.Errorf("right-clicking the artwork offered no %q: %q", want, tt.Texts())
		}
	}
	a.menu.close()
	tt.Frame()
	if !tt.HasText("Album Track One") {
		t.Errorf("the menu left the album page: %q", tt.Texts())
	}
	if a.current.VideoID != "" {
		t.Errorf("a right click played %q", a.current.VideoID)
	}
}

// A playlist page can sort the songs that Play, Shuffle and activation use.
func TestAPlaylistPageCanBeSorted(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/playlist/VLPL_songs")
	// The control closes the menu it opened when it is pressed again, as the
	// account menu's does.
	if err := tt.Click("Sort: Original order"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.detailSortOpen {
		t.Fatal("the sort control did not open its menu")
	}
	if err := tt.Click("Sort: Original order"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.detailSortOpen {
		t.Error("the sort control did not close its own menu")
	}
	if tt.HasText("Duration, shortest first") {
		t.Errorf("the closed menu is still on screen: %q", tt.Texts())
	}

	if err := tt.Click("Sort: Original order"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Title, A–Z"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-one", "song-three", "song-two"}) {
		t.Fatalf("title sort queued %v, want song-one, song-three, song-two", got)
	}
	if err := tt.Click("Sort: Title · A–Z"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Sort descending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-two", "song-three", "song-one"}) {
		t.Fatalf("title descending sort queued %v, want song-two, song-three, song-one", got)
	}
	if err := tt.Click("Sort: Title · Z–A"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Sort ascending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.detailSort.field != playlistSortTitle || a.detailSort.desc {
		t.Fatalf("direction toggle = %#v, want ascending title", a.detailSort)
	}
}

// Closing the search shrinks the bar back into the button that opened it. The
// control's size is a cap the animation moves: a width would only be the flex
// base, which the control's Grow fills, so the bar stayed at its full size and
// then jumped into the button.
func TestAPlaylistSearchShrinksIntoItsButton(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/playlist/VLPL_songs")
	tt := ui.NewTester(a.view, 1400, 900)
	tt.Frame()
	button, ok := tt.Find("Search in this playlist")
	if !ok || button.W < 1 {
		t.Fatalf("the playlist page has no search button: %q", tt.Texts())
	}
	// The bar opens at once: this test watches it close.
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("Search in this playlist"); err != nil {
		t.Fatal(err)
	}
	tt.SetPreferences(ui.Preferences{})
	open, ok := tt.Find("Search in this playlist")
	if !ok || open.W <= button.W {
		t.Fatalf("the open bar is %.0f wide, no wider than its button's %.0f", open.W, button.W)
	}
	// The closing runs slower than the usual motion, so that a frame sees the
	// bar part-way: MyGo's clock is the real one, and settling a frame builds
	// many of them, so the usual 0.3 s animation can pass between two frames
	// unseen.
	fast := m3.SpatialFast
	m3.SpatialFast = m3.Spring{Stiffness: 10, Damping: 1}
	t.Cleanup(func() { m3.SpatialFast = fast })

	if err := tt.Click("Close the search"); err != nil {
		t.Fatal(err)
	}
	// The bar goes back into the button, taking widths between the two.
	between, closed := false, false
	for i := 0; i < 400 && !closed; i++ {
		time.Sleep(10 * time.Millisecond)
		tt.Frame()
		bar, ok := tt.Find("Search in this playlist")
		if !ok {
			t.Fatalf("the search control went away: %q", tt.Texts())
		}
		between = between || bar.W > button.W+2 && bar.W < open.W-2
		closed = bar.W <= button.W+2
	}
	if !between {
		t.Errorf("the bar never took a width between the button's %.0f and its own %.0f: closing skipped the animation", button.W, open.W)
	}
	if !closed {
		t.Error("the search control never came back to its button")
	}
}

func TestAPlaylistPageCanBeSearched(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/playlist/VLPL_songs")
	tt := ui.NewTester(a.view, 1000, 700)
	for i := 0; i < 20 && len(a.page.playable) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	if len(a.page.playable) != 3 {
		t.Fatalf("the playlist page holds %d songs, want 3", len(a.page.playable))
	}
	// The search is a button that opens, not a bar that is always there.
	if _, ok := tt.Find("Search in this playlist"); !ok {
		t.Fatalf("the playlist page has no search button: %q", tt.Texts())
	}
	if a.detailSearchOpen {
		t.Fatal("the search opened by itself")
	}
	shown := func() int {
		n := 0
		for _, r := range a.page.rows {
			if r.kind == rowTrack {
				n++
			}
		}
		return n
	}
	// search opens the bar and types a query into it, emptying whatever the
	// last one left.
	search := func(query string) {
		t.Helper()
		if !a.detailSearchOpen {
			if err := tt.Click("Search in this playlist"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()

		}
		if _, ok := tt.Find("Clear search"); ok {
			if err := tt.Click("Clear search"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
		}
		a.detailFilter, a.detailSearchHadFocus = query, false
		tt.Frame()
		tt.Frame()
	}

	search("ameiro")
	// The bar grows in place, on the row of the buttons that play the page:
	// opening it must not move it to a row of its own.
	play, ok := tt.Find("Play")
	if !ok {
		t.Fatalf("the page has no Play button: %q", tt.Texts())
	}
	bar, ok := tt.Find("Search in this playlist")
	if !ok {
		t.Fatalf("the open search has no bar: %q", tt.Texts())
	}
	if bar.Y >= play.Y+play.H || bar.Y+bar.H <= play.Y {
		t.Fatalf("the search bar is not on the Play row: bar %v, Play %v", bar, play)
	}
	if !slices.ContainsFunc(a.page.rows, func(r row) bool { return r.shelf == "playlist-sort-header" }) {
		t.Errorf("filtering hid the playlist sort header: %#v", a.page.rows)
	}
	if !tt.HasText("Sort: Original order") {
		t.Errorf("filtering hid the Sort control: %q", tt.Texts())
	}
	if shown() != 2 {
		t.Errorf("the search shows %d songs, want the 2 whose title matches", shown())
	}
	if tt.HasText("Meteorite") {
		t.Errorf("the search left a song that does not match: %q", tt.Texts())
	}
	if len(a.page.playable) != 3 {
		t.Errorf("the search left %d songs to play, want the whole playlist's 3", len(a.page.playable))
	}

	// A matching song plays from its own place in the whole playlist.
	if err := tt.Click("Ameiro Two"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for range 2 {
		tt.Frame()
	}
	// The search field loses focus when activating its filtered row; closing
	// it verifies the playback path leaves the playlist page usable.
	if err := tt.Click("Close the search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.queue.items); !slices.Equal(got, []string{"song-one", "song-two", "song-three"}) {
		t.Errorf("playing a match queued %v, want the whole playlist", got)
	}
	if a.queue.index != 2 || a.current.VideoID != "song-three" {
		t.Errorf("the match played at %d as %q, want song-three in its own place", a.queue.index, a.current.VideoID)
	}

	// The artist beside a song is searched too.
	search("kaori")
	if shown() != 1 || !tt.HasText("Meteorite") {
		t.Errorf("the artist search shows %d songs (%q), want the one that matches", shown(), tt.Texts())
	}

	search("nothing here")
	if shown() != 0 {
		t.Errorf("a search that matches nothing shows %d songs", shown())
	}
	if !slices.ContainsFunc(a.page.rows, func(r row) bool { return r.kind == rowEmpty && strings.Contains(r.title, "nothing here") }) {
		t.Errorf("a search that matches nothing has no empty-state row: %#v", a.page.rows)
	}

	// Closing the bar puts the page back to its whole list, and the button
	// back in its place.
	if a.detailSearchOpen {
		if err := tt.Click("Close the search"); err != nil {
			t.Fatal(err)
		}
		for range 3 {
			tt.Frame()
		}
	} else {
		a.detailFilter = ""
		tt.Frame()
	}
	if a.detailSearchOpen || a.detailFilter != "" {
		t.Errorf("closing left the search open %v on %q", a.detailSearchOpen, a.detailFilter)
	}
	if shown() != 3 {
		t.Errorf("closing the search left %d songs, want the whole playlist's 3", shown())
	}
	if _, ok := tt.Find("Search in this playlist"); !ok {
		t.Errorf("the search button did not come back: %q", tt.Texts())
	}
}

func TestBrowsingFromHomeIntoAnAlbum(t *testing.T) {
	a := newTestApp()
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.router.Path(), "/album/") || !tt.HasText("Album Track One") {
		t.Fatalf("the album did not open: %q at %q", tt.Texts(), a.router.Path())
	}
}

func TestSettingsPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp()
	a.saver.Path = path
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Rose"); err != nil {
		t.Fatal(err)
	}
	a.saver.Save(&a.settings)
	a.saver.Wait()

	restarted := newTestApp()
	restarted.settings = settings.Load(path)
	if restarted.settings.Mode != "dark" || restarted.settings.Seed != "#d81b78" {
		t.Errorf("settings came back as %+v", restarted.settings)
	}
}

func TestMakingAPlaylistFromTheLibrary(t *testing.T) {
	libraryLoads.Store(0)
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if err := tt.Click("New playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.newPlaylist.name = "Late night"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if playlistCreates.Load() != 1 || a.newPlaylist != nil {
		t.Fatalf("playlist creates = %d, dialog %#v", playlistCreates.Load(), a.newPlaylist)
	}
	tt.Frame()
	if !tt.HasText("Created “Late night”") {
		t.Errorf("the playlist was not announced: %q", tt.Texts())
	}
}

func TestAddingASongToAPlaylist(t *testing.T) {
	a := signedInApp(t)
	tt := ui.NewTester(a.view, 1180, 850)
	card, ok := tt.Find("Ambient One")
	if !ok {
		t.Fatalf("the home page has no song: %q", tt.Texts())
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for Ambient One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Add to playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Add to Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	write := written()
	if !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") || write.body["playlistId"] != "PLhype" {
		t.Fatalf("write = %#v", write)
	}
	tt.Frame()
	if !tt.HasText("Added “Ambient One” to “Hype”") {
		t.Errorf("the song was not announced: %q", tt.Texts())
	}
}

func TestSessionRestoreAndSignOut(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=fresh"}
	a.account.creds.store = store
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}
	a.restoreAccount()
	if !a.signedIn() {
		t.Fatalf("the saved session did not sign in; notice %q", a.notice)
	}
	a.signOut()
	if a.signedIn() || store.cookie != "" {
		t.Errorf("sign-out left signedIn=%v, saved cookie %q", a.signedIn(), store.cookie)
	}
}

func TestRefusedSessionStaysSignedOut(t *testing.T) {
	a := newTestApp()
	a.account.creds.store = &memoryStore{cookie: "SAPISID=stale"}
	a.settings.SignInSource = "Browser"
	a.account.sources = func() []cookies.Source {
		return []cookies.Source{{Label: "Browser", Read: func(context.Context) (string, error) {
			return "SAPISID=stale", nil
		}}}
	}
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}
	a.restoreAccount()
	if a.signedIn() {
		t.Fatal("a session YouTube refuses signed the user in")
	}
	if !strings.Contains(a.notice, "session expired") {
		t.Errorf("the refusal was not surfaced: %q", a.notice)
	}
}

// Plays recorded while the history is still being read are kept, in front of
// nothing lost and behind what the file held.
func TestHistoryLoadMergesPendingPlays(t *testing.T) {
	a := newTestApp()
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	t.Cleanup(a.historyWriter.Wait) // the merged history is written in the background
	var finish func()
	a.run = func(work func()) { finish = work } // the read is held back
	a.loadHistory()
	a.recordHistory(youtube.MusicItem{VideoID: "new", Title: "New"})
	if err := os.WriteFile(a.historyPath, []byte(`[{"videoId":"old","playedAt":"2026-01-01T00:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.run = func(work func()) { work() }
	finish()
	if len(a.history) != 2 || a.history[0].VideoID != "old" || a.history[1].VideoID != "new" {
		t.Fatalf("history = %+v", a.history)
	}
}

// Clear history asks again, then empties the history in memory and on disk,
// and the stats page shows nothing played.
func TestClearHistoryFromSettings(t *testing.T) {
	a := newTestApp()
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	a.loadHistory()
	a.recordHistory(youtube.MusicItem{VideoID: "a", Title: "A", Subtitle: "X"})
	a.recordHistory(youtube.MusicItem{VideoID: "b", Title: "B", Subtitle: "Y"})
	a.historyWriter.Wait()
	if got := history.Read(a.historyPath); len(got) != 2 {
		t.Fatalf("saved %d plays, want 2", len(got))
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4000)
	tt.Frame()
	if !tt.HasText("2 plays are kept on this device") {
		t.Fatalf("settings do not count the plays: %q", tt.Texts())
	}
	if err := tt.Click("Clear history"); err != nil {
		t.Fatal(err)
	}
	if len(a.history) != 2 {
		t.Fatal("the first click cleared the history without asking")
	}
	if err := tt.Click("Keep"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Clear history"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Delete all plays"); err != nil {
		t.Fatal(err)
	}
	a.historyWriter.Wait()
	if len(a.history) != 0 || len(history.Read(a.historyPath)) != 0 {
		t.Errorf("history left: %d in memory, %d on disk", len(a.history), len(history.Read(a.historyPath)))
	}
	tt.Frame()
	if !tt.HasText("No plays are kept on this device.") {
		t.Errorf("settings still show plays: %q", tt.Texts())
	}
	a.router.Push("/stats")
	tt.Frame()
	if a.statsSummary(time.Now(), 14).Plays != 0 {
		t.Error("the stats still count cleared plays")
	}
}

// Clearing while the history is still being read must not let the read bring
// the old plays back.
func TestClearHistoryWhileItLoads(t *testing.T) {
	a := newTestApp()
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(a.historyPath, []byte(`[{"videoId":"old","playedAt":"2026-01-01T00:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var finish func()
	a.run = func(work func()) { finish = work } // the read is held back
	a.loadHistory()
	a.clearHistory()
	a.run = func(work func()) { work() }
	finish()
	a.historyWriter.Wait()
	if len(a.history) != 0 || len(history.Read(a.historyPath)) != 0 {
		t.Errorf("the read brought plays back: %d in memory, %d on disk", len(a.history), len(history.Read(a.historyPath)))
	}
}

func TestOpeningTheLogsFolderFromSettings(t *testing.T) {
	a := newTestApp()
	a.logsDir = "/logs"
	var opened []string
	a.openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4000)
	// The sound card made the page taller than this window, and a list
	// builds only the cards in view: scroll the logs card in before
	// clicking it.
	tt.Scroll(500, 2000, 0, 2500)
	tt.Frame()
	if err := tt.Click("Open logs folder"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "/logs" {
		t.Errorf("opened %v, want the logs folder", opened)
	}
}

// Report a problem opens the issue form with the versions filled in, and
// never carries the account.
func TestReportProblemOpensPrefilledIssue(t *testing.T) {
	a := signedInApp(t)
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()

	if err := tt.Click("Report a problem"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened %d links, want 1", len(opened))
	}
	u, err := url.Parse(opened[0])
	if err != nil || u.Host != "github.com" || u.Path != "/elianiva/meiro/issues/new" {
		t.Fatalf("unexpected link %s (%v)", opened[0], err)
	}
	if strings.Contains(opened[0], "SAPISID") {
		t.Error("the link carries the session cookie")
	}
	q := u.Query()
	if q.Get("template") != "bug_report.yml" || !strings.HasPrefix(q.Get("os-version"), runtime.GOOS+" ") {
		t.Errorf("form fields = %v", q)
	}
	for _, want := range []string{"yt-dlp: ", "ffmpeg: "} {
		if !strings.Contains(q.Get("tools"), want) {
			t.Errorf("tools lack %q: %q", want, q.Get("tools"))
		}
	}
}

// rejectingMusic answers every request the way YouTube answers a session it
// no longer accepts.
type rejectingMusic struct{}

func (rejectingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

func TestRejectedSessionAsksToSignInAgain(t *testing.T) {
	a := newTestApp()
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: rejectingMusic{}},
		})
	}
	auth, err := youtube.NewCookieAuth("SAPISID=stale", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: a.account.newClient(auth), cookie: "SAPISID=stale"}
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	tt.Frame()
	if a.signedIn() {
		t.Fatal("a rejected session is still in use")
	}
	if !tt.HasText("Your session expired") || !tt.HasText("Sign in again to see your songs, albums and playlists.") {
		t.Fatalf("the library does not ask to sign in again: %q", tt.Texts())
	}
	if err := tt.Click("Sign in"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.account.signIn.open {
		t.Error("Sign in did not open the dialog")
	}
}

// A browser that cannot be read leaves the sign-in dialog pointing at the
// Cookie header, with where to copy it from, and a pasted one signs in.
func TestFailedBrowserImportOffersPastingTheCookieHeader(t *testing.T) {
	a := newTestApp()
	a.account.sources = func() []cookies.Source {
		return []cookies.Source{{Label: "Fakefox", Read: func(context.Context) (string, error) {
			return "", errors.New("no profile found")
		}}}
	}
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=pasted"}},
		})
	}
	a.openSignIn()
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if tt.HasText("To paste it:") || tt.HasText("Import from a browser") {
		t.Fatalf("the advanced ways to sign in show before they are asked for: %q", tt.Texts())
	}
	if err := tt.Click("Advanced"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("To paste it:") {
		t.Fatalf("the dialog does not say where to copy the header from: %q", tt.Texts())
	}
	if tt.HasText("Importing from Fakefox did not work. Paste the Cookie header instead.") {
		t.Fatal("the fallback is announced before any import failed")
	}
	if err := tt.Click("Import from a browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Fakefox"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Importing from Fakefox did not work. Paste the Cookie header instead.") || !tt.HasText("no profile found") {
		t.Fatalf("the failed import did not point at the Cookie header: %q", tt.Texts())
	}
	a.account.signIn.cookie = "SAPISID=pasted; other=1"
	a.submitSignIn()
	tt.Frame()
	if !a.signedIn() || a.account.signIn.open {
		t.Errorf("the pasted header did not sign in: signedIn=%v open=%v err=%q", a.signedIn(), a.account.signIn.open, a.account.signIn.err)
	}
}

// The browser window signs in without naming a browser, so nothing re-reads
// the user's own browser for it later, and a window that fails says why.
func TestSignInWithABrowserWindow(t *testing.T) {
	a := newTestApp()
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=window"}},
		})
	}
	a.account.window = func(context.Context) (string, error) { return "", cookies.ErrWindowClosed }
	// An earlier import from a browser must not outlive a window sign-in.
	a.settings.SignInSource = "Fakefox"
	a.openSignIn()
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Sign in with a browser window"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.signedIn() || !tt.HasText(cookies.ErrWindowClosed.Error()) {
		t.Fatalf("a closed window did not say so: signedIn=%v %q", a.signedIn(), tt.Texts())
	}
	if tt.HasText("Importing from") {
		t.Error("a window that failed points at importing from a browser")
	}

	a.account.window = func(context.Context) (string, error) { return "SAPISID=window; SID=1", nil }
	if err := tt.Click("Sign in with a browser window"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.signedIn() || a.account.signIn.open {
		t.Fatalf("the window did not sign in: signedIn=%v open=%v err=%q", a.signedIn(), a.account.signIn.open, a.account.signIn.err)
	}
	if got := a.settings.SignInSource; got != "" {
		t.Errorf("the session names the browser %q, which would be read again when it goes stale", got)
	}
}

func TestSettingsShowPlaybackToolVersions(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()
	tt.Frame()

	if !tt.HasText("Playback tools") {
		t.Fatalf("settings lack the tools card: %q", tt.Texts())
	}
	if tt.HasText("yt-dlp Looking up…") {
		t.Errorf("the versions were never looked up: %q", tt.Texts())
	}
	if !a.toolsLoaded {
		t.Error("opening settings did not read the tool versions")
	}
}

func TestGoingBackRestoresWhereAPageWasScrolled(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 500)
	tt.Frame()
	tt.Scroll(500, 300, 0, 1500)
	tt.Frame()
	if first, _ := a.list.Visible(); first == 0 {
		t.Fatal("settings did not scroll")
	}
	a.navigate(routeStats)
	tt.Frame()
	a.router.Back()
	tt.Frame()
	if first, _ := a.list.Visible(); a.router.Path() != "/settings" || first == 0 {
		t.Fatalf("settings did not come back where it was left: first row %d at %q", first, a.router.Path())
	}
}
