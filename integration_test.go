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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func TestAlbumCoverLoadsFromPlayerMetadata(t *testing.T) {
	a, r := tonesApp(t, 600)
	r.url = func(string, int) (string, error) { return r.file, nil }
	a.thumbs.Close()
	a.thumbs = artwork.New(&http.Client{Transport: redArt{}}, func() {})
	t.Cleanup(a.thumbs.Close)
	a.router.Push("/playlist/VLPL_album_cover")
	tt := ui.NewTester(a.view, 1440, 900)
	if err := tt.Click("Linked Track"); err != nil {
		t.Fatal(err)
	}
	pollUntil(t, a, "playing", a.player.Playing)
	tt.Frame()
	if err := tt.Click("Go to album: After Hours"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if a.router.Path() != "/album/MPREb_nested" || a.page.detail.title != "After Hours" || a.page.detail.art != "https://img.test/album.png" || !tt.HasText("Album Track One") {
		t.Fatalf("album link lost the header or tracks: path %q, detail %+v, texts %q", a.router.Path(), a.page.detail, tt.Texts())
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		tt.Frame()
		cover, ok := tt.Find("More options for After Hours")
		if !ok {
			t.Fatal("album cover is missing")
		}
		pixel := tt.Image().RGBAAt(int(cover.X+cover.W/2), int(cover.Y+cover.H/2))
		if pixel.R > 180 && pixel.G < 30 && pixel.B < 30 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("album artwork never replaced the placeholder")
}

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

func TestDiscordPresenceShowsTheBylineWhenNoArtistIsLinked(t *testing.T) {
	item := youtube.MusicItem{
		VideoID:  "track-3",
		Title:    "きっとコースター - Kitto Coaster",
		Subtitle: "Ado",
		Kind:     youtube.KindTrack,
		Duration: "2:59",
	}
	got := discordPresenceState(item, 0, 179*time.Second, 1)
	if got.Artist != "Ado" {
		t.Errorf("Discord artist = %q, want the artist the byline names", got.Artist)
	}
}

func TestDiscordPresenceKeepsAnEpisodeBylineOutOfTheArtistLine(t *testing.T) {
	item := youtube.MusicItem{
		VideoID:  "episode-1",
		Title:    "Episode 4",
		Subtitle: "Episode • Show • 1 hr 30 min",
		Kind:     youtube.KindEpisode,
	}
	got := discordPresenceState(item, 0, time.Minute, 1)
	if got.Artist != "" {
		t.Errorf("Discord artist = %q, want no artist for an episode", got.Artist)
	}
}

func TestEveryPageLoadsAndDraws(t *testing.T) {
	pages := []struct {
		path string
		want []string
	}{
		{"/library", []string{"Library", "Your library lives here"}},
		{"/search", []string{"Find your next favourite"}},
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

func TestSettingsSearchFiltersSettingsInPlace(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	account, ok := tt.Find("Account")
	if !ok {
		t.Fatalf("the home page has no account button: %q", tt.Texts())
	}
	if _, ok := tt.Find(searchPlaceholder); !ok {
		t.Fatalf("the home page has no global music search: %q", tt.Texts())
	}
	a.router.Push("/settings")
	for range 3 {
		tt.Frame()
	}
	if _, ok := tt.Find(searchPlaceholder); ok {
		t.Fatalf("Settings shows the global music search: %q", tt.Texts())
	}
	if r, ok := tt.Find("Account"); ok && r == account {
		t.Fatalf("Settings shows the account in its top bar at %v", r)
	}
	if _, ok := tt.Find("Search settings"); !ok {
		t.Fatalf("Settings has no local search button: %q", tt.Texts())
	}
	if !tt.HasText("Sign in") {
		t.Fatalf("Settings has no account card: %q", tt.Texts())
	}
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if a.router.Path() != "/settings" {
		t.Fatalf("opening settings search navigated to %q", a.router.Path())
	}
	if _, ok := tt.Find("Close the search"); !ok {
		t.Fatalf("the local search field did not open (open %v): %q", a.settingsSearch.open, tt.Texts())
	}
	if field, ok := tt.Find("Search settings"); !ok || field.W < 300 {
		t.Fatalf("the expanded field is missing or too narrow: %v, %q", field, tt.Texts())
	}
	tt.Type("Discord")
	tt.Frame()
	if !tt.HasText("Discord Rich Presence") {
		t.Errorf("searching Discord hid its setting: %q", tt.Texts())
	}
	if tt.HasText("Normalize loudness") {
		t.Errorf("searching Discord left unrelated sound settings visible: %q", tt.Texts())
	}
	for _, tc := range []struct {
		query string
		want  string
	}{
		{query: "colour", want: "Colour"},
		{query: "color", want: "Colour"},
		{query: "palette", want: "Palette"},
		{query: "hue", want: "Hue"},
		{query: "artwork", want: "Colour source"},
		{query: "matugen", want: "Colour source"},
	} {
		if err := tt.Click("Clear search"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("Search settings"); err != nil {
			t.Fatal(err)
		}
		tt.Type(tc.query)
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Errorf("searching %q did not show %q: %q", tc.query, tc.want, tt.Texts())
		}
		if tt.HasText("Discord Rich Presence") {
			t.Errorf("searching %q left the unrelated Discord setting visible: %q", tc.query, tt.Texts())
		}
	}

	tt.Type(" unmatched")
	tt.Frame()
	if !tt.HasText("No settings found") {
		t.Errorf("an unmatched query has no empty state: %q", tt.Texts())
	}
	if err := tt.Click("Clear search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Sign in") || tt.HasText("No settings found") {
		t.Errorf("clearing the query did not restore Settings: %q", tt.Texts())
	}
	if !a.settingsSearch.open {
		t.Fatalf("clearing the query closed Settings search: %#v; %q", a.settingsSearch, tt.Texts())
	}
	if err := tt.Click("Close the search"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if a.settingsSearch.open || a.settingsSearch.query != "" {
		t.Errorf("closing left settings search open with query %q", a.settingsSearch.query)
	}
	if _, ok := tt.Find("Search settings"); !ok {
		t.Errorf("the settings search button did not return: %q", tt.Texts())
	}

	a.router.Push("/stats")
	for range 3 {
		tt.Frame()
	}
	if _, ok := tt.Find("Search settings"); ok {
		t.Errorf("Stats shows the Settings filter: %q", tt.Texts())
	}
	if r, ok := tt.Find("Account"); ok && r == account {
		t.Errorf("Stats shows the account in its top bar at %v", r)
	}
	a.router.Push("/home")
	for range 3 {
		tt.Frame()
	}
	if _, ok := tt.Find(searchPlaceholder); !ok {
		t.Errorf("the home page lost global music search: %q", tt.Texts())
	}
	if r, ok := tt.Find("Account"); !ok || r != account {
		t.Errorf("the home page account button moved from %v to %v", account, r)
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

	if err := tt.RightClick("Suggested Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, want := range []string{"Play next", "Add to queue", "Add to playlist", "Copy"} {
		if !tt.HasText(want) {
			t.Errorf("right-clicking a song among the suggestions offered no %q: %q", want, tt.Texts())
		}
	}
	if !tt.HasText("Suggested Song") {
		t.Errorf("the right click closed the panel: %q", tt.Texts())
	}
	a.menu.close()
	tt.Frame()

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

func TestASongsMenuCopiesItsTitleAndArtist(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/playlist/VLPL_metadata")
	tt := ui.NewTester(a.view, 1180, 850)
	tt.Frame()

	if err := tt.RightClick("Linked Track"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Copy title") || !tt.HasText("Copy artist") {
		t.Fatalf("the copy page of a song with credits offered %q", tt.Texts())
	}
	if err := tt.Click("Copy title"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "Linked Track" {
		t.Errorf("Copy title copied %q, want the song's title", got)
	}
	if a.menu.open {
		t.Error("the menu stayed open after Copy title")
	}

	row, ok := tt.Find("Linked Track")
	if !ok {
		t.Fatalf("the song row is missing: %q", tt.Texts())
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if err := tt.Click("More options for Linked Track"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Copy artist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != "Aurora Vale, Guest Artist" {
		t.Errorf("Copy artist copied %q, want every credited artist", got)
	}
	if a.menu.open {
		t.Error("the menu stayed open after Copy artist")
	}

	if err := tt.RightClick("Unlinked Track"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Copy title") {
		t.Errorf("the copy page of a song without credits offered %q", tt.Texts())
	}
	if tt.HasText("Copy artist") {
		t.Errorf("the menu on a song without credits offered Copy artist: %q", tt.Texts())
	}
	a.menu.close()
}

func TestChoosingASuggestionFromAnotherPageShowsTheSearch(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/explore")
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
	if err := tt.Click("Yorushika songs"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if r, _ := a.route(); r.id != routeSearch {
		t.Fatalf("choosing a suggestion left the app on %q", a.path())
	}
	if !tt.HasText("Search Result Song") || searches.Load() != 1 {
		t.Fatalf("the search did not run: %q", tt.Texts())
	}
	if a.search.query != "Yorushika songs" {
		t.Errorf("the field reads %q", a.search.query)
	}
}

func TestChoosingASuggestedSongStartsItsRadio(t *testing.T) {
	for _, page := range []string{"/search", "/playlist/VLPL_songs"} {
		t.Run(page, func(t *testing.T) {
			resetFakes()
			a, _ := playbackApp(t)
			a.settings.AutoPlay = true
			a.router.Push(page)
			tt := ui.NewTester(a.view, 1000, 700)
			if page != "/search" {
				for i := 0; i < 20 && !tt.HasText("Meteorite"); i++ {
					tt.Frame()
				}
				if err := tt.Click("Meteorite"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if a.current.VideoID != "song-two" || a.queue.source != "Ameiro Playlist" {
					t.Fatalf("the playlist did not play: current %q, queue %v from %q", a.current.VideoID, ids(a.queue.items), a.queue.source)
				}
			}

			if err := tt.Click(searchPlaceholder); err != nil {
				t.Fatal(err)
			}
			tt.Type("pick")
			tt.Frame()
			if !tt.HasText("Pick One") || !tt.HasText("Pick Two") {
				t.Fatalf("the suggested songs are missing: %q", tt.Texts())
			}
			if err := tt.Click("Pick Two"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()

			if a.current.VideoID != "pick-two" {
				t.Fatalf("choosing the second suggestion played %q", a.current.VideoID)
			}
			if got := ids(a.queue.items); !slices.Equal(got, []string{"pick-two", "radio-one", "radio-two"}) {
				t.Fatalf("the queue is %v, want the song and its radio", got)
			}
			if a.queue.index != 0 || a.queue.recStart != 1 {
				t.Errorf("the queue stands at %d with recommendations from %d, want 0 and 1", a.queue.index, a.queue.recStart)
			}
			if a.queue.source != "Pick Two" || a.queue.playlistMore != "" {
				t.Errorf("the queue kept the page's context: source %q, playlist rest %q", a.queue.source, a.queue.playlistMore)
			}
			asked, _ := pickNextAsked.Load().(string)
			if !strings.Contains(asked, `"playlistId":"RDAMVMpick-two"`) || strings.Contains(asked, `"playlistIndex"`) {
				t.Errorf("the recommendations were asked for with %s, want the song's own mix", asked)
			}
			if a.search.suggestOpen {
				t.Error("the suggestions stayed open after a song was chosen")
			}

			if err := tt.Click(searchPlaceholder); err != nil {
				t.Fatal(err)
			}
			tt.Type(" again")
			tt.Frame()
			if err := tt.RightClick("Pick One"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if err := tt.Click("Add to queue"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if got := ids(a.queue.items); !slices.Contains(got, "pick-one") || !slices.Contains(got, "radio-one") || a.current.VideoID != "pick-two" {
				t.Errorf("Add to queue left the queue %v playing %q", got, a.current.VideoID)
			}
		})
	}
}

func TestSearchEpisodeTopResultPlays(t *testing.T) {
	a, streams := playbackApp(t)
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Search songs, albums, artists, podcasts"); err != nil {
		t.Fatal(err)
	}
	tt.Type("play episode result")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("Find Me feat.Hina from miolly") || !tt.HasText("Play") {
		t.Fatalf("the episode result offers no play action: %q", tt.Texts())
	}
	if err := tt.Click("Play"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "UZ83quirqLA" {
		t.Fatalf("the episode played video %q, want UZ83quirqLA", a.current.VideoID)
	}
	waitFor(t, func() bool { return streams.asked("UZ83quirqLA") })
}

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

func TestSearchLoadsTheNextPageItself(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/search")
	a.runSearch("yor", "")
	searchesMore.Store(0)
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
		{"Mixed Album", "Open album"},
		{"Row Album", "Open album"},
		{"Row Artist", "Go to artist"},
		{"Row Playlist", "Open playlist"},
		{"Row Song", "Add to queue"},
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
		if !tt.HasText("Copy") {
			t.Errorf("the menu on %q has no Copy: %q", c.result, tt.Texts())
		}
		a.menu.close()
		tt.Frame()
	}

	if r, _ := a.route(); r.id != routeSearch {
		t.Errorf("a right click left the search page for %q", r.id)
	}
	if a.current.VideoID != "" {
		t.Errorf("a right click played %q", a.current.VideoID)
	}
}

func TestTheHeroMenuOpensAgainAfterItCloses(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/playlist/VLPL_songs")
	tt.Frame()
	for round := range 2 {
		if err := tt.RightClick("More options for Ameiro Playlist"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if !a.menu.open || !tt.HasText("Delete playlist") {
			t.Fatalf("right click %d opened no menu: %v %q", round+1, a.menu.opened("hero"), tt.Texts())
		}
		a.menu.close()
		tt.Frame()
	}
}

func TestAPlaylistPageCanBeSorted(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/playlist/VLPL_songs")
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
	if err := tt.Click("Show more"); err != nil {
		t.Fatalf("the suggestions have no Show more button: %v", err)
	}
	tt.Frame()
	if got := a.page.feed.similarShown; got != 7 {
		t.Errorf("after Show more the page shows %d suggestions, want 7", got)
	}
}

func TestAPlaylistLoadsEveryContinuationBeforeSuggestions(t *testing.T) {
	calls := 0
	more, err := readPlaylistRest(context.Background(), "more-0", func(_ context.Context, token string) (*youtube.BrowseResult, error) {
		calls++
		if calls <= 25 {
			if want := "more-" + strconv.Itoa(calls-1); token != want {
				t.Fatalf("continuation %d = %q, want %q", calls, token, want)
			}
			items := make([]youtube.MusicItem, 12)
			for i := range items {
				items[i] = youtube.MusicItem{Kind: youtube.KindTrack, VideoID: "song-" + strconv.Itoa(calls) + "-" + strconv.Itoa(i)}
			}
			kind := "playlistVideoListContinuation"
			if calls%2 == 0 {
				kind = "musicPlaylistShelfContinuation"
			}
			return &youtube.BrowseResult{Sections: []youtube.MusicSection{{
				Kind: kind, Items: items, ContinuationToken: "more-" + strconv.Itoa(calls),
			}}}, nil
		}
		if token != "more-25" {
			t.Fatalf("suggestions continuation = %q, want more-25", token)
		}
		return &youtube.BrowseResult{Sections: []youtube.MusicSection{{
			Kind: "musicShelfRenderer", Items: []youtube.MusicItem{{VideoID: "suggested"}}, ContinuationToken: "similar-more",
		}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 26 || len(more.items) != 300 {
		t.Fatalf("read %d pages and %d playlist songs, want 26 pages and all 300 songs", calls, len(more.items))
	}
	if len(more.sections) != 1 || more.sections[0].Kind != "musicShelfRenderer" || more.next != "similar-more" {
		t.Errorf("suggestions = %#v with continuation %q, want the suggestion shelf and its next token", more.sections, more.next)
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
	if _, ok := tt.Find("Search in this playlist"); !ok {
		t.Fatalf("the playlist page has no search button: %q", tt.Texts())
	}
	if a.detailSearchOpen {
		t.Fatal("the search opened by itself")
	}
	searchButton, ok := tt.Find("Search in this playlist")
	if !ok {
		t.Fatalf("the playlist page has no search button: %q", tt.Texts())
	}
	sortControl, ok := tt.Find("Sort: Original order")
	if !ok {
		t.Fatalf("the playlist page has no sort control: %q", tt.Texts())
	}
	if searchButton.Y >= sortControl.Y+sortControl.H || searchButton.Y+searchButton.H <= sortControl.Y {
		t.Fatalf("the search button is not on the Sort row: search %v, Sort %v", searchButton, sortControl)
	}
	shown := func() int {
		n := 0
		for _, r := range a.page.rows {
			if r.kind == rowTrack && r.queue == nil {
				n++
			}
		}
		return n
	}
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
	play, ok := tt.Find("Play")
	if !ok {
		t.Fatalf("the page has no Play button: %q", tt.Texts())
	}
	sortControl, ok = tt.Find("Sort: Original order")
	if !ok {
		t.Fatalf("the page has no Sort control: %q", tt.Texts())
	}
	bar, ok := tt.Find("Search in this playlist")
	if !ok {
		t.Fatalf("the open search has no bar: %q", tt.Texts())
	}
	if bar.Y >= sortControl.Y+sortControl.H || bar.Y+bar.H <= sortControl.Y {
		t.Fatalf("the search bar is not on the Sort row: bar %v, Sort %v", bar, sortControl)
	}
	if bar.Y < play.Y+play.H && bar.Y+bar.H > play.Y {
		t.Fatalf("the search bar is still on the hero row: bar %v, Play %v", bar, play)
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

	if err := tt.Click("Ameiro Two"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for range 2 {
		tt.Frame()
	}
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

func TestTheSongsToolbarStaysOnScreen(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/playlist/VLPL_songs")
	tt := ui.NewTester(a.view, 1000, 700)
	for i := 0; i < 20 && len(a.page.playable) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	if _, ok := tt.Find("Sort: Original order"); !ok {
		t.Fatalf("the page has no sort control: %q", tt.Texts())
	}
	tt.Scroll(700, 500, 0, 900)
	for range 3 {
		tt.Frame()
	}
	first, _ := a.list.Visible()
	if first == 0 {
		t.Fatalf("the list did not scroll: %d", first)
	}
	sortControl, ok := tt.Find("Sort: Original order")
	if !ok {
		t.Fatalf("the songs toolbar left the screen: %q", tt.Texts())
	}
	if sortControl.H == 0 || sortControl.Y < 60 || sortControl.Y > 200 {
		t.Fatalf("the songs toolbar is at %v, not at the top of the list", sortControl.Y)
	}
	next, ok := tt.Find("Similar songs")
	if !ok {
		t.Fatalf("the page has no suggestions section: %q", tt.Texts())
	}
	if next.Y < sortControl.Y {
		t.Fatalf("the suggestions are above the pinned toolbar: %v vs %v", next.Y, sortControl.Y)
	}
	if _, ok := tt.Find("Search in this playlist"); !ok {
		t.Fatalf("the search left the pinned toolbar: %q", tt.Texts())
	}
}

func TestGlobalSearchKeepsItsPlaceAcrossPages(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	a.router.Push("/home")
	for range 5 {
		tt.Frame()
	}
	home, ok := tt.Find(searchPlaceholder)
	if !ok {
		t.Fatalf("the home page has no global search: %q", tt.Texts())
	}
	a.router.Push("/playlist/VLPL_songs")
	for i := 0; i < 20 && len(a.page.playable) == 0; i++ {
		tt.Frame()
	}
	for range 5 {
		tt.Frame()
	}
	playlist, ok := tt.Find(searchPlaceholder)
	if !ok {
		t.Fatalf("the playlist page has no global search: %q", tt.Texts())
	}
	if home != playlist {
		t.Fatalf("the global search moved between pages: home %v, playlist %v", home, playlist)
	}
}

func TestPlaylistHeaderFollowsTheScroll(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/playlist/VLPL_songs")
	tt := ui.NewTester(a.view, 1000, 700)
	for i := 0; i < 20 && len(a.page.playable) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	if a.detailCompact {
		t.Fatal("the compact header showed before the hero was scrolled away")
	}
	if _, ok := tt.Find("Play playlist"); ok {
		t.Fatalf("the compact header showed before scrolling: %q", tt.Texts())
	}
	searchBefore, ok := tt.Find(searchPlaceholder)
	if !ok {
		t.Fatalf("the page has no global search: %q", tt.Texts())
	}
	tt.Scroll(700, 500, 0, 900)
	for range 2 {
		tt.Frame()
	}
	tt.Advance(500 * time.Millisecond)
	for range 5 {
		tt.Frame()
	}
	if !a.detailCompact {
		t.Fatalf("the compact header did not show after scrolling: %q", tt.Texts())
	}
	if _, ok := tt.Find("Play playlist"); !ok {
		t.Fatalf("the compact header has no Play: %q", tt.Texts())
	}
	if _, ok := tt.Find("Shuffle playlist"); !ok {
		t.Fatalf("the compact header has no Shuffle: %q", tt.Texts())
	}
	if searchAfter, ok := tt.Find(searchPlaceholder); !ok || searchAfter != searchBefore {
		t.Fatalf("the global search moved or changed with the compact header: before %v, after %v", searchBefore, searchAfter)
	}
	tt.Scroll(700, 500, 0, -2000)
	for range 2 {
		tt.Frame()
	}
	tt.Advance(500 * time.Millisecond)
	for range 5 {
		tt.Frame()
	}
	if a.detailCompact {
		t.Fatalf("the compact header stayed after scrolling back to the top: %q", tt.Texts())
	}
	if _, ok := tt.Find("Play playlist"); ok {
		t.Fatalf("the compact header did not leave at the top: %q", tt.Texts())
	}
	if searchBack, ok := tt.Find(searchPlaceholder); !ok || searchBack != searchBefore {
		t.Fatalf("the global search moved after scrolling back: before %v, back %v", searchBefore, searchBack)
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

func TestAnAlbumCoverFliesFromItsCard(t *testing.T) {
	a := newTestApp(t)
	a.router.Transition = ui.TransitionNone
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	card, ok := tt.Find("Deep Focus")
	if !ok {
		t.Fatalf("no album card: %q", tt.Texts())
	}
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(2 * time.Second)
	for range 5 {
		tt.Frame()
	}
	rest, ok := tt.Find("More options for Deep Focus")
	if !ok {
		t.Fatalf("no hero cover: %q", tt.Texts())
	}
	a.router.Back()
	tt.Advance(2 * time.Second)
	for range 5 {
		tt.Frame()
	}

	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	flying, ok := tt.Find("More options for Deep Focus")
	if !ok {
		t.Fatalf("no cover while the page opens: %q", tt.Texts())
	}
	if flying.X < card.X-30 || flying.X > card.X+30 {
		t.Fatalf("the cover started at x=%.0f, not at its card's x=%.0f: the hero did not fly it in", flying.X, card.X)
	}
	tt.Advance(2 * time.Second)
	for range 5 {
		tt.Frame()
	}
	landed, ok := tt.Find("More options for Deep Focus")
	if !ok {
		t.Fatalf("the cover left: %q", tt.Texts())
	}
	if dx, dy := landed.X-rest.X, landed.Y-rest.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Fatalf("the cover landed at %.0f,%.0f, not in the hero at %.0f,%.0f", landed.X, landed.Y, rest.X, rest.Y)
	}
}

func TestHomeLeadsWithSongColumns(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1200, 800)
	tt.Frame()
	songs := make([]youtube.MusicItem, 12)
	for i := range songs {
		songs[i] = youtube.MusicItem{
			Kind:    youtube.KindTrack,
			VideoID: "top-" + strconv.Itoa(i),
			Title:   "Top " + strconv.Itoa(i+1),
			Artist:  "Someone",
		}
	}
	a.page.feed.sections = []youtube.MusicSection{{Title: "Trending songs for you", Items: songs}}
	a.page.rowsDirty = true
	tt.Frame()
	first, ok := tt.Find("Top 1")
	if !ok {
		t.Fatalf("the first song is not a row: %q", tt.Texts())
	}
	fifth, ok := tt.Find("Top 5")
	if !ok {
		t.Fatalf("the fifth song is not a row: %q", tt.Texts())
	}
	if fifth.X <= first.X+100 {
		t.Fatalf("the fifth song starts at x=%.0f, not in a second column after x=%.0f", fifth.X, first.X)
	}
	ninth, ok := tt.Find("Top 9")
	if !ok {
		t.Fatalf("the ninth song is not a row: %q", tt.Texts())
	}
	if ninth.X <= fifth.X+100 {
		t.Fatalf("the ninth song starts at x=%.0f, not in a third column after x=%.0f", ninth.X, fifth.X)
	}
	for i := 2; i <= 4; i++ {
		row, ok := tt.Find("Top " + strconv.Itoa(i))
		if !ok || row.X != first.X {
			t.Fatalf("song %d starts at x=%.0f, not in the first column at x=%.0f", i, row.X, first.X)
		}
	}
	if err := tt.Click("Play all"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.queue.source != "Trending songs for you" || a.queue.len() < len(songs) {
		t.Fatalf("Play all queued %d songs from %q, want at least %d from the shelf", a.queue.len(), a.queue.source, len(songs))
	}
	if a.queue.items[0].Title != "Top 1" {
		t.Fatalf("Play all started with %q, not the first song", a.queue.items[0].Title)
	}
}

func TestSearchSuggestionsReadAsAList(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()

	field, ok := tt.Access(searchPlaceholder)
	if !ok || field.AutoComplete != ui.AutoCompleteList {
		t.Fatalf("the search field completes with %v, found %v: %q", field.AutoComplete, ok, tt.Texts())
	}
	if _, ok := tt.AccessByRole(ui.RoleListBox, "Search suggestions"); !ok {
		t.Fatalf("the suggestions are not a list: %q", tt.Texts())
	}
	first, ok := tt.AccessByRole(ui.RoleOption, "Yorushika")
	if !ok {
		t.Fatalf("the first suggestion is not an option: %q", tt.Texts())
	}
	if first.PosInSet != 1 || first.SetSize != 4 {
		t.Fatalf("the first suggestion is %d of %d, want 1 of 4", first.PosInSet, first.SetSize)
	}
	if first.Focused {
		t.Fatal("an option is focused before the arrows move")
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	first, _ = tt.AccessByRole(ui.RoleOption, "Yorushika")
	if !first.Focused {
		t.Fatalf("the first option is not the active descendant after Down: %+v", first)
	}
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	second, ok := tt.AccessByRole(ui.RoleOption, "Yorushika songs")
	if !ok || !second.Focused || second.PosInSet != 2 {
		t.Fatalf("the second option is %+v after another Down", second)
	}
	if first, _ := tt.AccessByRole(ui.RoleOption, "Yorushika"); first.Focused {
		t.Fatal("the first option stayed active after the arrows moved")
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

func TestAddingASongAlreadyInAPlaylist(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)
	duplicateWrites.Store(true)
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
	if write := written(); !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") || write.body["playlistId"] != "PLhype" {
		t.Fatalf("write = %#v", write)
	}
	if a.addToPlaylist != nil {
		t.Errorf("the picker stayed open over a song the playlist holds already")
	}
	if !tt.HasText("“Ambient One” is already in “Hype”") {
		t.Errorf("the duplicate was not announced: %q", tt.Texts())
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

func TestHistoryLoadMergesPendingPlays(t *testing.T) {
	a := newTestApp(t)
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	t.Cleanup(a.historyWriter.Wait)
	var finish func()
	a.run = func(work func()) { finish = work }
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
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("history")
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

func TestClearHistoryWhileItLoads(t *testing.T) {
	a := newTestApp(t)
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(a.historyPath, []byte(`[{"videoId":"old","playedAt":"2026-01-01T00:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	var finish func()
	a.run = func(work func()) { finish = work }
	a.loadHistory()
	a.clearHistory()
	a.run = func(work func()) { work() }
	finish()
	a.historyWriter.Wait()
	if len(a.history) != 0 || len(history.Read(a.historyPath)) != 0 {
		t.Errorf("the read brought plays back: %d in memory, %d on disk", len(a.history), len(history.Read(a.historyPath)))
	}
}

func TestHistoryPageMergesAccountAndLocalPlays(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.settings.AutoPlay = false
	now := time.Now()
	a.historyLoaded = true
	a.history = []history.Entry{
		{VideoID: "shared-today", Title: "Shared Today", Artist: "Local Artist", PlayedAt: now.Add(-time.Minute)},
		{VideoID: "local-yesterday", Title: "Local Yesterday", Artist: "Local Artist", PlayedAt: now.AddDate(0, 0, -1)},
		{VideoID: "local-week", Title: "Local Week", Artist: "Local Artist", PlayedAt: now.AddDate(0, 0, -3)},
	}
	a.router.Push("/history")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()

	if historyLoads.Load() != 1 {
		t.Fatalf("the account history was read %d times, want 1", historyLoads.Load())
	}
	mustShow(t, tt, "Phone Today", "Shared Today", "Phone Yesterday", "Phone Week", "Phone Old", "Local Yesterday", "Local Week")
	texts := tt.Texts()
	shared := 0
	for _, item := range a.page.playable {
		if item.VideoID == "shared-today" {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("the local and account plays of Shared Today queued %d times, want once", shared)
	}
	if !inOrder(texts, "Today", "Shared Today", "Phone Today") {
		t.Errorf("Today does not hold the local play before the account play: %q", texts)
	}
	if !inOrder(texts, "Yesterday", "Local Yesterday", "Phone Yesterday") {
		t.Errorf("Yesterday does not hold the local play before the account play: %q", texts)
	}
	if !inOrder(texts, "This week", "Local Week", "Phone Week") {
		t.Errorf("the local play from this week was not folded into YouTube's shelf: %q", texts)
	}

	if err := tt.Click("Phone Old"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "phone-old" {
		t.Errorf("clicking the account play started %q, want phone-old", a.current.VideoID)
	}
}

func TestHistoryPageStaysLocalWhenSignedOut(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.historyLoaded = true
	a.history = []history.Entry{
		{VideoID: "local-today", Title: "Local Today", Artist: "Local Artist", PlayedAt: time.Now()},
	}
	tt := open(a, "/history")

	if historyLoads.Load() != 0 {
		t.Errorf("a signed-out app asked YouTube for history %d times", historyLoads.Load())
	}
	mustShow(t, tt, "History", "Today", "Local Today")
	mustNotShow(t, tt, "Phone Today", "This week")
}

func TestHistoryPageWithoutPlays(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.historyLoaded = true
	tt := open(a, "/history")

	mustShow(t, tt, "History", "No listening history yet", "Songs you play in Meiro show up here.")
	mustNotShow(t, tt, "Today", "Phone Today")
}

func TestHistoryPageKeepsLocalPlaysWhenYouTubeFails(t *testing.T) {
	resetFakes()
	historyFails.Store(true)
	t.Cleanup(resetFakes)
	a := signedInApp(t)
	a.historyLoaded = true
	a.history = []history.Entry{
		{VideoID: "local-today", Title: "Local Today", Artist: "Local Artist", PlayedAt: time.Now()},
	}
	tt := open(a, "/history")

	if historyLoads.Load() != 1 {
		t.Fatalf("the account history was read %d times, want 1", historyLoads.Load())
	}
	mustShow(t, tt, "Today", "Local Today")
	mustNotShow(t, tt, "Retry", "Could not load this page", "Phone Today")
}

func TestHistoryPageOffersRetryWhenOnlyYouTubeFails(t *testing.T) {
	resetFakes()
	historyFails.Store(true)
	a := signedInApp(t)
	a.historyLoaded = true
	tt := open(a, "/history")

	mustShow(t, tt, "Could not load this page", "Could not load your YouTube Music history.", "Retry")
	historyFails.Store(false)
	if err := tt.Click("Retry"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	mustShow(t, tt, "Phone Today")
	mustNotShow(t, tt, "Retry", "Could not load this page")
	if historyLoads.Load() != 2 {
		t.Errorf("the account history was read %d times, want 2", historyLoads.Load())
	}
}

func TestMergeHistoryFoldsKnownShelvesAndKeepsTheRest(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*60*60)
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, loc)
	local := []history.Entry{
		{VideoID: "dup", Title: "Dup Local", PlayedAt: now.Add(-3 * time.Hour)},
		{VideoID: "local-today", Title: "Local Today", PlayedAt: now.Add(-time.Hour)},
		{VideoID: "local-yesterday", Title: "Local Yesterday", PlayedAt: now.AddDate(0, 0, -1)},
		{VideoID: "local-week", Title: "Local Week", PlayedAt: now.AddDate(0, 0, -3)},
		{VideoID: "local-old", Title: "Local Old", PlayedAt: now.AddDate(0, 0, -40)},
	}
	remote := []remoteShelf{
		{title: "Today", items: []youtube.MusicItem{
			{VideoID: "remote-today", Title: "Remote Today"},
			{VideoID: "dup", Title: "Remote Dup"},
		}},
		{title: "This week", items: []youtube.MusicItem{{VideoID: "remote-week", Title: "Remote Week"}}},
		{title: "Something new", items: []youtube.MusicItem{{VideoID: "remote-unknown", Title: "Remote Unknown"}}},
		{title: "Last month", items: []youtube.MusicItem{{VideoID: "remote-last", Title: "Remote Last"}}},
	}
	sections := mergeHistory(local, remote, now)
	var titles []string
	for _, section := range sections {
		titles = append(titles, section.title)
	}
	wantTitles := []string{"Today", "Yesterday", "This week", "Something new", "Last month", "Thursday, 29 January"}
	if !slices.Equal(titles, wantTitles) {
		t.Fatalf("sections = %q, want %q", titles, wantTitles)
	}
	wantItems := [][]string{
		{"Local Today", "Dup Local", "Remote Today"},
		{"Local Yesterday"},
		{"Local Week", "Remote Week"},
		{"Remote Unknown"},
		{"Remote Last"},
		{"Local Old"},
	}
	for i, section := range sections {
		if got := itemTitles(section.items); !slices.Equal(got, wantItems[i]) {
			t.Errorf("%s holds %q, want %q", section.title, got, wantItems[i])
		}
	}
}

func TestHistoryBucketReadsYouTubesShelfTitles(t *testing.T) {
	loc := time.FixedZone("UTC+7", 7*60*60)
	now := time.Date(2026, 3, 10, 15, 0, 0, 0, loc)
	day := func(month time.Month, date int) time.Time {
		return time.Date(2026, month, date, 0, 0, 0, 0, loc)
	}
	lastYear := func(month time.Month, date int) time.Time {
		return time.Date(2025, month, date, 0, 0, 0, 0, loc)
	}
	cases := []struct {
		label      string
		start, end time.Time
		ok         bool
	}{
		{"Today", day(3, 10), day(3, 10), true},
		{"Yesterday", day(3, 9), day(3, 9), true},
		{"This week", day(3, 4), day(3, 8), true},
		{"Last week", day(2, 25), day(3, 3), true},
		{"This month", day(3, 1), day(3, 8), true},
		{"Last month", day(2, 1), day(2, 28), true},
		{"September 2025", lastYear(9, 1), lastYear(9, 30), true},
		{"Sep 2025", lastYear(9, 1), lastYear(9, 30), true},
		{"April", lastYear(4, 1), lastYear(4, 30), true},
		{"Recently", time.Time{}, time.Time{}, false},
	}
	for _, tc := range cases {
		start, end, ok := historyBucket(tc.label, now)
		if ok != tc.ok || !start.Equal(tc.start) || !end.Equal(tc.end) {
			t.Errorf("%q = %v to %v, %t; want %v to %v, %t", tc.label, start, end, ok, tc.start, tc.end, tc.ok)
		}
	}
}

func textIndex(texts []string, want string) int {
	for i, text := range texts {
		if text == want {
			return i
		}
	}
	return -1
}

func inOrder(texts []string, wants ...string) bool {
	at := -1
	for _, want := range wants {
		next := textIndex(texts, want)
		if next <= at {
			return false
		}
		at = next
	}
	return true
}

func itemTitles(items []youtube.MusicItem) []string {
	titles := make([]string, len(items))
	for i, item := range items {
		titles[i] = item.Title
	}
	return titles
}

func TestReportProblemOpensPrefilledIssue(t *testing.T) {
	a := signedInApp(t)
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("report problem")
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

func TestSupportCardOpensKoFi(t *testing.T) {
	a := newTestApp(t)
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ko-fi")
	tt.Frame()
	if err := tt.Click("Open Ko-fi"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != kofiURL {
		t.Fatalf("opened %v, want %q", opened, kofiURL)
	}
}

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

func TestSignInDialogScrollsInAShortWindow(t *testing.T) {
	a := newTestApp(t)
	a.openSignIn()
	a.account.signIn.advanced = true
	const height = 480
	tt := ui.NewTester(a.view, 800, height)
	tt.Frame()

	panel, ok := tt.Find("dialog-panel")
	if !ok || panel.Y < 0 || panel.Y+panel.H > height {
		t.Fatalf("the dialog is not inside the window: %+v", panel)
	}
	const note = "Meiro saves the session in a file in its data folder that only your user can read. Sign out to remove Meiro's saved copy."
	if r, ok := tt.Find(note); ok && r.H > 0 {
		t.Fatal("the end of the dialog is already in view, so the window is not shorter than it")
	}
	cancel, ok := tt.Find("Cancel")
	if !ok || cancel.H > 0 && cancel.Y+cancel.H <= panel.Y+panel.H {
		t.Fatalf("Cancel is already inside the panel, at %+v; panel %+v", cancel, panel)
	}

	tt.Scroll(panel.X+panel.W/2, panel.Y+panel.H/2, 0, 4000)
	tt.Frame()
	end, ok := tt.Find(note)
	if !ok || end.H == 0 || end.Y < panel.Y || end.Y+end.H > panel.Y+panel.H {
		t.Fatalf("scrolling did not bring the end of the dialog into view: ok=%v %+v panel %+v", ok, end, panel)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("Cancel is in the dialog and still not clickable: %v", err)
	}
	if a.account.signIn.open {
		t.Error("Cancel did not close the scrolled dialog")
	}
}

func TestEscapeClosesTheDialogAndLeavesThePlayer(t *testing.T) {
	a := newTestApp(t)
	a.current.VideoID = "vid-3"
	a.account.signIn.open = true
	tt := ui.NewTester(a.view, 800, 700)
	tt.Frame()
	a.nowPlaying.open = true
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	if a.account.signIn.open {
		t.Error("Escape left the dialog open")
	}
	if !a.nowPlaying.open || a.nowPlaying.closing {
		t.Error("Escape closed the player as well as the dialog")
	}
}

func TestEscapeClosesTheBrowserMenuBeforeTheDialog(t *testing.T) {
	a := newTestApp(t)
	a.account.sources = func() []cookies.Source {
		return []cookies.Source{{Label: "Fakefox"}}
	}
	a.openSignIn()
	a.account.signIn.advanced = true
	tt := ui.NewTester(a.view, 800, 900)
	tt.Frame()
	if err := tt.Click("Import from a browser"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Fakefox") {
		t.Fatal("the browser menu did not open")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Fakefox") {
		t.Error("Escape left the browser menu open")
	}
	if !a.account.signIn.open {
		t.Error("Escape closed the dialog with the menu")
	}
}

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

func TestSignInWindowShowsTheFlatpakOverride(t *testing.T) {
	a := newTestApp(t)
	a.account.window = func(context.Context) (string, error) { return "", cookies.ErrNeedsHostAccess }
	a.openSignIn()
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Sign in with a browser window"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(cookies.HostAccessCommand) {
		t.Fatalf("the dialog does not show the override: %q", tt.Texts())
	}
	if err := tt.Click("Copy command"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tt.Clipboard(); got != cookies.HostAccessCommand {
		t.Errorf("clipboard = %q, want %q", got, cookies.HostAccessCommand)
	}

	a.account.window = func(context.Context) (string, error) { return "", cookies.ErrWindowClosed }
	if err := tt.Click("Sign in with a browser window"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText(cookies.HostAccessCommand) || !tt.HasText(cookies.ErrWindowClosed.Error()) {
		t.Errorf("a later failure still shows the override: %q", tt.Texts())
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

	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.deletePlaylist != nil || playlistDeletes.Load() != 0 || !holdsPlaylist(a, "PLhype") {
		t.Fatalf("Cancel wrote %d deletes, and the page holds the playlist: %v", playlistDeletes.Load(), holdsPlaylist(a, "PLhype"))
	}

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
	if after, _ := a.route(); after.nav != routeHome {
		t.Errorf("the tab moved the page to %q", after.title)
	}
}

func TestAPlaylistLoadsSongsFromItsShelfBeforeSuggestions(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/playlist/VLPL_paginated")
	tt := ui.NewTester(a.view, 1000, 700)
	for range 30 {
		tt.Frame()
	}
	if got := len(a.page.playable); got != 146 {
		t.Fatalf("playlist has %d playable songs, want all 146", got)
	}
	if got := a.page.playable[145].VideoID; got != "track-145" {
		t.Fatalf("last playlist song = %q, want track-145", got)
	}
	if got := len(a.page.feed.similar); got != 7 {
		t.Fatalf("playlist has %d suggestions, want 7 kept separate", got)
	}
}

func TestAPlaylistLoadsEveryAppendedContinuation(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/playlist/VLPL_appended")
	tt := ui.NewTester(a.view, 1000, 700)
	for i := 0; i < 100 && len(a.page.playable) < 246; i++ {
		tt.Frame()
	}
	if got := len(a.page.playable); got != 246 {
		t.Fatalf("playlist has %d playable songs, want all 246", got)
	}
	if got := a.page.playable[245].VideoID; got != "track-245" {
		t.Fatalf("last playlist song = %q, want track-245", got)
	}
}

func TestAPlaylistShowsItsNextPageLoading(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	var pending func()
	a.run = func(work func()) {
		if a.pageSimilar.busy() && pending == nil {
			pending = work
			return
		}
		work()
	}
	a.router.Push("/playlist/VLPL_appended")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if pending == nil {
		t.Fatal("the playlist did not ask for its later pages")
	}
	if !hasRowKind(a.page.rows, rowStreaming) {
		t.Fatalf("the playlist shows no loading row while its later pages arrive: %v", a.page.rows)
	}
	pending()
	tt.Frame()
	if hasRowKind(a.page.rows, rowStreaming) {
		t.Fatalf("the loading row stays after the playlist loaded: %v", a.page.rows)
	}
}

func hasRowKind(rows []row, kind rowKind) bool {
	for _, r := range rows {
		if r.kind == kind {
			return true
		}
	}
	return false
}

func TestPlayingBeforeThePlaylistLoadsKeepsItsRemainingSongs(t *testing.T) {
	for _, scenario := range []struct {
		id       string
		replace  bool
		pickNext bool
	}{
		{"VLPL_paginated", false, false},
		{"VLPL_paginated", true, false},
		{"VLPL_page_token", false, false},
		{"VLPL_paginated", false, true},
	} {
		t.Run(scenario.id+strconv.FormatBool(scenario.replace)+strconv.FormatBool(scenario.pickNext), func(t *testing.T) {
			resetFakes()
			a, _ := playbackApp(t)
			var pending []func()
			var heldPage, heldQueue bool
			a.run = func(work func()) {
				if a.pageSimilar.busy() && !heldPage {
					heldPage = true
					pending = append(pending, work)
					return
				}
				if a.queueRest.busy() && !heldQueue {
					heldQueue = true
					pending = append(pending, work)
					return
				}
				work()
			}
			a.router.Push("/playlist/" + scenario.id)
			tt := ui.NewTester(a.view, 1000, 700)
			tt.Frame()
			if len(a.page.playable) != 100 {
				t.Fatalf("first page has %d songs, want 100", len(a.page.playable))
			}
			if err := tt.Click("Play"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if a.queue.len() != 100 || a.current.VideoID != "track-0" {
				t.Fatalf("early playback: current=%q queue=%d", a.current.VideoID, a.queue.len())
			}
			if scenario.pickNext {
				if err := tt.Click("Up next"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				if err := tt.Click("Track 1"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			a.router.Push("/album/MPREb_test")
			tt.Frame()
			if scenario.replace {
				if err := tt.Click("Album Track One"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			for len(pending) > 0 {
				work := pending[0]
				pending = pending[1:]
				work()
			}
			tt.Frame()
			if scenario.replace {
				if a.queue.len() != 1 || a.current.VideoID != "vid-3" {
					t.Fatalf("old playlist changed the replacement queue: %v", ids(a.queue.items))
				}
				return
			}
			wantCurrent, wantRemaining := "track-0", 145
			if scenario.pickNext {
				wantCurrent, wantRemaining = "track-1", 144
			}
			if a.queue.len() != 146 || a.queue.remaining() != wantRemaining || a.current.VideoID != wantCurrent {
				t.Fatalf("completed playback: current=%q queue=%d remaining=%d", a.current.VideoID, a.queue.len(), a.queue.remaining())
			}
			if got := a.queue.at(145).VideoID; got != "track-145" {
				t.Fatalf("final song = %q, want track-145", got)
			}
		})
	}
}

func TestPlayingAPlaylistCardIncludesEverySong(t *testing.T) {
	for _, onPage := range []bool{false, true} {
		t.Run(strconv.FormatBool(onPage), func(t *testing.T) {
			resetFakes()
			a, _ := playbackApp(t)
			var pending func()
			heldPage := false
			a.run = func(work func()) {
				if onPage && a.pageSimilar.busy() && !heldPage {
					heldPage = true
					return
				}
				if a.queueRest.busy() && pending == nil {
					pending = work
					return
				}
				work()
			}
			if onPage {
				a.router.Push("/playlist/PL_paginated")
				tt := ui.NewTester(a.view, 1000, 700)
				tt.Frame()
			}
			a.playCollection(youtube.MusicItem{PlaylistID: "PL_paginated", Title: "Large playlist"}, "large-playlist", false)
			if a.queue.len() != 100 || a.current.VideoID != "track-0" || pending == nil {
				t.Fatalf("first page did not start before the rest: current=%q queue=%d pending=%t", a.current.VideoID, a.queue.len(), pending != nil)
			}
			pending()
			if a.queue.len() != 146 || a.queue.remaining() != 145 {
				t.Fatalf("card playback: queue=%d remaining=%d, want 146 and 145", a.queue.len(), a.queue.remaining())
			}
			if got := a.queue.at(145).VideoID; got != "track-145" {
				t.Fatalf("final song = %q, want track-145", got)
			}
		})
	}
}

func TestPlaylistRadioOnlyReadsTheFirstPage(t *testing.T) {
	resetFakes()
	a, _ := playbackApp(t)
	var continuations int
	a.account.public = youtube.NewClient(youtube.Options{HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		if strings.Contains(string(body), `"continuation"`) {
			continuations++
		}
		return fakeMusic{}.RoundTrip(request)
	})}})
	a.startRadio(youtube.MusicItem{PlaylistID: "PL_paginated", Title: "Large playlist"}, "large-playlist")
	if a.current.VideoID == "" || a.queue.len() != 1 || continuations != 0 || a.queue.playlistMore != "" {
		t.Fatalf("radio: current=%q queue=%d continuations=%d token=%q", a.current.VideoID, a.queue.len(), continuations, a.queue.playlistMore)
	}
}

func TestPlaylistDownloadsReadEveryPage(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	var songs []youtube.MusicItem
	a.withCollectionTracks(youtube.MusicItem{PlaylistID: "PL_paginated"}, "download-playlist", true, func(items []youtube.MusicItem, token string) {
		songs = items
		if token != "" {
			t.Fatal("download still has unread songs")
		}
	})
	if len(songs) != 146 || songs[145].VideoID != "track-145" {
		t.Fatalf("download has %d songs, want all 146", len(songs))
	}
}

func TestPlaylistCardStillPlaysWhenItsContinuationFails(t *testing.T) {
	resetFakes()
	a, r := playbackApp(t)
	a.account.public = youtube.NewClient(youtube.Options{HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		if strings.Contains(string(body), `"continuation":"playlist-tracks"`) {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
		}
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		return fakeMusic{}.RoundTrip(request)
	})}})
	a.playCollection(youtube.MusicItem{PlaylistID: "PL_paginated", Title: "Large playlist"}, "large-playlist", false)
	if a.current.VideoID != "track-0" || a.queue.len() != 100 || !r.asked("track-0") {
		t.Fatalf("failed continuation prevented playback: current=%q queue=%d", a.current.VideoID, a.queue.len())
	}
	if !strings.Contains(a.notice, "Could not load the rest") || a.queue.playlistMore != "" {
		t.Fatalf("failed continuation: notice=%q token=%q", a.notice, a.queue.playlistMore)
	}
}

func TestLatePlaylistSongsJoinTheShuffle(t *testing.T) {
	resetFakes()
	a, _ := playbackApp(t)
	var pending func()
	heldPage := false
	a.run = func(work func()) {
		if a.pageSimilar.busy() && !heldPage {
			heldPage = true
			return
		}
		if a.queueRest.busy() && pending == nil {
			pending = work
			return
		}
		work()
	}
	a.router.Push("/playlist/VLPL_paginated")
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Shuffle"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	if pending == nil || a.queue.index != 1 {
		t.Fatal("playlist continuation was not held while playback advanced")
	}
	history := slices.Clone(a.queue.items[:a.queue.index+1])
	original := slices.Clone(a.queue.shuffleKept)
	pending()
	if a.queue.len() != 146 || !slices.Equal(ids(a.queue.items[:a.queue.index+1]), ids(history)) {
		t.Fatal("late songs changed playback history or lost playlist songs")
	}
	if len(a.queue.shuffleKept) != 146 {
		t.Fatalf("shuffle remembers %d songs, want the full 146 in original order", len(a.queue.shuffleKept))
	}
	for i := 100; i < 146; i++ {
		original = append(original, youtube.MusicItem{VideoID: "track-" + strconv.Itoa(i)})
	}
	a.router.Push("/search")
	tt.Frame()
	if err := tt.Click("Shuffle"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids(a.queue.items), ids(original)) {
		t.Fatal("turning shuffle off did not restore late songs in playlist order")
	}
}
