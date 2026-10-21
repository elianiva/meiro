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
	a := newTestApp(t)
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: a.account.newClient(auth), cookie: "SAPISID=test"}
	return a
}

func TestDiscordPresenceNamesEveryCreditedArtist(t *testing.T) {
	item := youtube.MusicItem{
		VideoID:   "track-1",
		Title:     "Song",
		Subtitle:  "Song • Artist, Guest • Album • 3:42",
		Artist:    "Artist",
		Artists:   "Artist, Guest",
		Thumbnail: "https://i.ytimg.com/cover.jpg",
	}
	got := discordPresenceState(item, 42*time.Second, 222*time.Second, 3)
	if got.Artist != "Artist, Guest" {
		t.Errorf("Discord artist = %q, want every credited artist", got.Artist)
	}
	if got.VideoID != item.VideoID || got.Title != item.Title || got.ArtworkURL != item.Thumbnail {
		t.Fatalf("Discord state = %+v, want fields from the track", got)
	}
	if got.Position != 42*time.Second || got.Duration != 222*time.Second || got.SeekVersion != 3 || !got.Playing {
		t.Errorf("Discord playback state = %+v, want position 42s, length 3:42, seek version 3, playing", got)
	}
}

func TestDiscordPresenceFallsBackToTheFirstArtistOfASavedQueue(t *testing.T) {
	// A queue saved before the item named every artist has only the first.
	item := youtube.MusicItem{
		VideoID:  "track-2",
		Title:    "Song",
		Subtitle: "Song • Artist",
		Artist:   "Artist",
	}
	got := discordPresenceState(item, 0, 200*time.Second, 1)
	if got.Artist != "Artist" {
		t.Errorf("Discord artist = %q, want the first artist of a saved queue item", got.Artist)
	}
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
		{"/podcast/MPSPPLshow", []string{"PODCAST", "Slow Talk", "Two people, talking slowly.", "Episode One"}},
		{"/recap", []string{"Your recap lives here"}},
		{"/nowhere", []string{"That page does not exist."}},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			a := newTestApp(t)
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

// The top bar searches YouTube Music and holds the account, and neither finds
// anything on the settings pages: they show the account as their first card,
// so their bar names the page alone.
func TestSettingsPagesLeaveTheMusicSearchOutOfTheTopBar(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	// A page of YouTube Music has both, and the account is at the bar's end.
	account, ok := tt.Find("Account")
	if !ok {
		t.Fatalf("the home page has no account button: %q", tt.Texts())
	}
	if _, ok := tt.Find(searchPlaceholder); !ok {
		t.Fatalf("the home page has no music search: %q", tt.Texts())
	}

	// The settings and their stats are the app's own pages, not YouTube
	// Music's: their bar names the page and stops there.
	for _, path := range []string{"/settings", "/stats"} {
		a.router.Push(path)
		tt.Frame()
		if _, ok := tt.Find(searchPlaceholder); ok {
			t.Errorf("%s shows the music search in its top bar: %q", path, tt.Texts())
		}
		if r, ok := tt.Find("Account"); ok && r == account {
			t.Errorf("%s shows the account in its top bar, at %v", path, r)
		}
	}

	// The settings show the account among their rows instead.
	a.router.Push("/settings")
	tt.Frame()
	if !tt.HasText("Sign in") {
		t.Errorf("the settings have no account card: %q", tt.Texts())
	}
}

func TestHomeShowMoreAppendsContinuationSections(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	a.page.feed.more = "home-more"
	a.page.rowsDirty = true
	tt.Frame()
	if !tt.HasText("Show more") {
		t.Fatalf("home page has no continuation control: %q", tt.Texts())
	}
	if err := tt.Click("Show more"); err != nil {
		t.Fatal(err)
	}
	if homeMoreRequests.Load() != 1 {
		t.Fatalf("home continuation requests = %d, want 1", homeMoreRequests.Load())
	}
	if got := len(a.page.feed.sections); got != 2 {
		t.Fatalf("home sections = %d, want original and continuation sections", got)
	}
	if section := a.page.feed.sections[1]; section.Title != "More picks" || len(section.Items) != 1 || section.Items[0].Title != "More Ambient" {
		t.Errorf("continuation section = %+v", section)
	}
}

func TestSearchFlow(t *testing.T) {
	a := newTestApp(t)
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
	a := newTestApp(t)
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

// The search field is in the top bar of every page of YouTube Music, and
// submitting it there shows the search page with what it ran.
func TestSearchingFromAnotherPage(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/explore")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yorushika")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if r, _ := a.route(); r.id != routeSearch {
		t.Fatalf("a search from explore left the app on %q", a.path())
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	if tt.HasText("Artist") {
		t.Errorf("the closed menu is still on screen: %q", tt.Texts())
	}

	// The direction is the page's, not a field's, so it works before a field
	// is chosen: descending reads the playlist backwards.
	if err := tt.Click("Sort descending"); err != nil {
		t.Fatalf("the original order has no direction button: %v", err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-three", "song-two", "song-one"}) {
		t.Fatalf("the reversed original order queued %v, want song-three, song-two, song-one", got)
	}
	if err := tt.Click("Sort ascending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-one", "song-two", "song-three"}) {
		t.Fatalf("the original order queued %v, want the playlist's own order", got)
	}

	// A field sorts the page, and the button reverses it.
	if err := tt.Click("Sort: Original order"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Title"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-one", "song-three", "song-two"}) {
		t.Fatalf("title sort queued %v, want song-one, song-three, song-two", got)
	}
	if !tt.HasText("Sort: Title") {
		t.Errorf("the control does not name the field: %q", tt.Texts())
	}
	if err := tt.Click("Sort descending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-two", "song-three", "song-one"}) {
		t.Fatalf("title descending sort queued %v, want song-two, song-three, song-one", got)
	}

	// Choosing another field keeps the direction it was given.
	if err := tt.Click("Sort: Title"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Artist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.detailSort.field != playlistSortArtist || !a.detailSort.desc {
		t.Fatalf("a new field = %#v, want descending artist", a.detailSort)
	}
	if !tt.HasText("Sort: Artist") {
		t.Errorf("the control does not name the field: %q", tt.Texts())
	}
	if err := tt.Click("Sort ascending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.detailSort.field != playlistSortArtist || a.detailSort.desc {
		t.Fatalf("direction toggle = %#v, want ascending artist", a.detailSort)
	}

	// The original order comes back with the direction the page holds, so a
	// page read backwards keeps reading backwards.
	if err := tt.Click("Sort descending"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Sort: Artist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Original order"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.detailSort.field != playlistSortOriginal || !a.detailSort.desc {
		t.Fatalf("original choice = %#v, want the reversed original order", a.detailSort)
	}
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-three", "song-two", "song-one"}) {
		t.Fatalf("the original order queued %v, want the direction it kept", got)
	}
	if !tt.HasText("Sort ascending") {
		t.Errorf("the direction button did not follow the original order: %q", tt.Texts())
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

// A playlist page shows what YouTube suggests under it as it opens, under a
// heading of its own and five at a time. Those songs are not the playlist: they
// never join its songs or its queue, and the button under them shows the next
// ones.
func TestAPlaylistShowsItsSuggestionsFiveAtATime(t *testing.T) {
	resetFakes()
	a, _ := playbackApp(t)
	a.router.Push("/playlist/VLPL_songs")
	tt := ui.NewTester(a.view, 1000, 1200)
	for i := 0; i < 20 && a.page.feed.similarShown == 0; i++ {
		tt.Frame()
	}
	if !tt.HasText("Similar songs") {
		t.Fatalf("the suggestions have no heading of their own: %q", tt.Texts())
	}
	if got := len(a.page.feed.similar); got != 7 {
		t.Fatalf("the page holds %d suggestions, want 7", got)
	}
	if got := a.page.feed.similarShown; got != 5 {
		t.Fatalf("the page shows %d suggestions, want 5", got)
	}
	if !tt.HasText("Suggestion One") || !tt.HasText("Suggestion Four") {
		t.Errorf("the first suggestions are not on the page: %q", tt.Texts())
	}
	if got := ids(a.page.playable); !slices.Equal(got, []string{"song-one", "song-two", "song-three"}) {
		t.Errorf("the playlist holds %v, want only its own songs", got)
	}
	// A suggestion plays from the suggestions alone, never with the playlist.
	if err := tt.Click("Suggestion One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "suggestion-one" {
		t.Fatalf("playing the suggestion left %q current", a.current.VideoID)
	}
	for _, id := range ids(a.queue.items) {
		if id == "song-one" || id == "song-two" || id == "song-three" {
			t.Fatalf("the queue holds the playlist's %q: %v", id, ids(a.queue.items))
		}
	}
	// The button shows more of them.
	if err := tt.Click("Show more"); err != nil {
		t.Fatalf("the suggestions have no Show more button: %v", err)
	}
	tt.Frame()
	if got := a.page.feed.similarShown; got != 7 {
		t.Errorf("after Show more the page shows %d suggestions, want 7", got)
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
	// shown counts the playlist's own rows: a row of the suggestions that
	// YouTube keeps under the page plays from its own list.
	shown := func() int {
		n := 0
		for _, r := range a.page.rows {
			if r.kind == rowTrack && r.queue == nil {
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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

	restarted := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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
	a := newTestApp(t)
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

// holdsPlaylist says whether the page's feed still lists the playlist with
// this ID.
func holdsPlaylist(a *app, id string) bool {
	held := func(items []youtube.MusicItem) bool {
		return slices.ContainsFunc(items, func(item youtube.MusicItem) bool {
			return strings.TrimPrefix(item.Target().ID, "VL") == id
		})
	}
	if held(a.page.feed.items) {
		return true
	}
	return slices.ContainsFunc(a.page.feed.sections, func(section youtube.MusicSection) bool {
		return held(section.Items)
	})
}

// Deleting a playlist asks first: the question names the playlist, a cancel
// writes nothing, and only the answer deletes it and takes it off the page.
func TestDeletingAPlaylistAsksFirst(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	tt := open(a, "/library")
	mustShow(t, tt, "Hype")
	if err := tt.RightClick("Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Delete playlist") {
		t.Fatalf("the playlist's menu offers no Delete playlist: %q", tt.Texts())
	}
	if err := tt.Click("Delete playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.deletePlaylist == nil || !tt.HasText("“Hype” is deleted from your account") {
		t.Fatalf("the menu item asked no question: %v %q", a.deletePlaylist, tt.Texts())
	}
	if playlistDeletes.Load() != 0 {
		t.Fatal("the question deleted the playlist before it was answered")
	}

	// A cancel leaves the playlist where it was, and writes nothing.
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.deletePlaylist != nil || playlistDeletes.Load() != 0 || !holdsPlaylist(a, "PLhype") {
		t.Fatalf("Cancel wrote %d deletes, and the page holds the playlist: %v", playlistDeletes.Load(), holdsPlaylist(a, "PLhype"))
	}

	// The answer deletes the playlist, and takes it off the page.
	if err := tt.RightClick("Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Delete playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if playlistDeletes.Load() != 1 {
		t.Fatalf("the answer wrote %d deletes", playlistDeletes.Load())
	}
	if write := written(); !strings.HasSuffix(write.endpoint, "/playlist/delete") || write.body["playlistId"] != "PLhype" {
		t.Fatalf("the delete wrote %s with %v", write.endpoint, write.body)
	}
	if a.deletePlaylist != nil || holdsPlaylist(a, "PLhype") || !tt.HasText("Deleted “Hype”") {
		t.Fatalf("the deleted playlist stayed: %v %q", a.deletePlaylist, tt.Texts())
	}
}

// A delete YouTube refuses keeps its question up, with what YouTube said, and
// leaves the playlist where it was.
func TestADeclinedPlaylistDeleteKeepsItsQuestion(t *testing.T) {
	resetFakes()
	refuseWrites.Store(true)
	a := signedInApp(t)
	tt := open(a, "/library")
	mustShow(t, tt, "Hype")
	if err := tt.RightClick("Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Delete playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Delete"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.deletePlaylist == nil || a.deletePlaylist.err == "" || a.deletePlaylist.busy {
		t.Fatalf("a refused delete closed its question: %+v", a.deletePlaylist)
	}
	if !holdsPlaylist(a, "PLhype") {
		t.Fatal("a refused delete took the playlist off the page")
	}
}

// The tab of the page already showing has nothing to open, so with the player
// over the page it puts the player away instead of doing nothing.
func TestTheCurrentTabPutsThePlayerAway(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/home")
	a.nowPlaying.open, a.nowPlaying.playerShown = true, true
	tt.Frame()
	if before, _ := a.route(); before.nav != routeHome {
		t.Fatalf("the window shows %q", before.title)
	}
	if err := tt.Click("Home"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.nowPlaying.open && !a.nowPlaying.closing {
		t.Fatal("the current tab left the player open")
	}
	// The sink ends by taking the player away.
	deadline := time.Now().Add(3 * time.Second)
	for a.nowPlaying.open && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		tt.Frame()
	}
	if a.nowPlaying.open {
		t.Fatal("the player did not close")
	}
	if after, _ := a.route(); after.nav != routeHome {
		t.Errorf("the tab moved the page to %q", after.title)
	}
}
