package main

import (
	"context"
	"errors"
	"image"
	"io"
	"math"
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
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// These tests drive the whole app through its window, the way a user does:
// real pages, real clicks, and a fake YouTube at the network edge.

func TestAlbumCoverLoadsFromPlayerMetadata(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(strconv.FormatBool(full), func(t *testing.T) {
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
			if full {
				if err := tt.Click("Open the player"); err != nil {
					t.Fatal(err)
				}
				tt.Advance(time.Second)
			}
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
		})
	}
}

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

func TestDiscordPresenceShowsTheBylineWhenNoArtistIsLinked(t *testing.T) {
	// A saved queue's row can link no artist, so the item credits none. The
	// byline names Ado, and the player shows it.
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
	// An episode's second line names the show and its length, not an artist.
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

func blurSearchBar(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for range 8 {
		if !tt.Focused(label) && !tt.Focused("Close the search") && !tt.Focused("Clear search") {
			return
		}
		tt.Key(0, ui.KeyTab)
	}
	t.Fatalf("Tab did not move focus out of the %q search bar", label)
}

func assertSearchInputKeepsBoundsOnFocus(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	before, ok := tt.Find(label)
	if !ok {
		t.Fatalf("search input %q is missing: %q", label, tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Move(0, 0)
	tt.Frame()
	if !tt.Focused(label) {
		t.Fatalf("clicking %q did not focus the search input", label)
	}
	after, ok := tt.Find(label)
	if !ok {
		t.Fatalf("focused search input %q is missing: %q", label, tt.Texts())
	}
	if before != after {
		t.Errorf("search input %q moved on focus: before %v, after %v", label, before, after)
	}
}

func TestExpandedNavigationMetricsAndFullWidthSelection(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 720)
	if err := tt.Click("Expand navigation"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)

	home, ok := tt.Find("Home")
	if !ok {
		t.Fatalf("expanded navigation has no Home destination: %q", tt.Texts())
	}
	search, ok := tt.Find("Search")
	if !ok {
		t.Fatalf("expanded navigation has no Search destination: %q", tt.Texts())
	}
	if home.W < 230 || home.W > 240 {
		t.Errorf("expanded navigation width is %g DIPs, want to stay near 236", home.W)
	}
	if pitch := search.Y - home.Y; pitch < 67 || pitch > 69 {
		t.Errorf("expanded navigation row pitch is %g DIPs, want 68", pitch)
	}
	image := tt.Image()
	x, y := int(home.X+100), int(home.Y)
	selected := image.RGBAAt(x, y+1)
	outside := image.RGBAAt(x, y-5)
	if selected == outside {
		t.Errorf("the selected 56-DIP indicator is missing: selected=%v outside=%v", selected, outside)
	}
	activeLeft := image.RGBAAt(int(home.X+25), int(home.Y+28))
	leftMargin := image.RGBAAt(int(home.X+5), int(home.Y+28))
	if activeLeft == leftMargin {
		t.Errorf("the selected background does not reach the left rail inset: row=%v margin=%v", activeLeft, leftMargin)
	}
	activeRight := image.RGBAAt(int(home.X+200), int(home.Y+28))
	activeMargin := image.RGBAAt(int(home.X+220), int(home.Y+28))
	if activeRight == activeMargin {
		t.Errorf("the selected background escapes the rail's horizontal insets: right=%v margin=%v", activeRight, activeMargin)
	}
	for _, label := range []string{"Search", "Explore", "Library", "Settings"} {
		row, ok := tt.Find(label)
		if !ok {
			t.Errorf("expanded navigation has no %s destination: %q", label, tt.Texts())
			continue
		}
		inside := image.RGBAAt(int(row.X+200), int(row.Y+28))
		outside := image.RGBAAt(int(row.X+5), int(row.Y+28))
		if inside != outside {
			t.Errorf("%s has an unintended idle indicator: inside=%v outside=%v", label, inside, outside)
		}
	}
}

func TestNavigationRailAndPageAnimateTogether(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1280, 720)
	menu, ok := tt.Find("Expand navigation")
	if !ok {
		t.Fatalf("compact navigation has no menu button: %q", tt.Texts())
	}
	if angle := navigationGlyphAngle(tt.Image(), menu); math.Abs(angle) > 10 {
		t.Fatalf("compact menu glyph is not horizontal: angle %.1f°", angle)
	}
	compact, ok := tt.Find("Search")
	if !ok {
		t.Fatalf("compact navigation has no Search destination: %q", tt.Texts())
	}
	if center := navigationCompactLabelCenterX(tt.Image(), compact); math.Abs(center-float64(compact.X+m3.RailCollapsed/2)) > 8 {
		t.Fatalf("compact label is not centered below its icon: center %.1f, row %v", center, compact)
	}
	compactRows := make(map[string]ui.Rect)
	compactIconCenters := make(map[string]float64)
	for _, label := range []string{"Home", "Search", "Explore", "Library", "Settings"} {
		row, ok := tt.Find(label)
		if !ok {
			t.Fatalf("compact navigation has no %s destination: %q", label, tt.Texts())
		}
		center, ok := navigationItemIconCenterY(tt.Image(), row)
		if !ok {
			t.Fatalf("compact navigation has no visible icon for %s", label)
		}
		compactRows[label] = row
		compactIconCenters[label] = center
	}
	compactPage, ok := tt.Find("Quick picks")
	if !ok {
		t.Fatalf("home page is missing: %q", tt.Texts())
	}

	if err := tt.Click("Expand navigation"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(80 * time.Millisecond)
	if angle := navigationGlyphAngle(tt.Image(), menu); math.Abs(angle) < 20 {
		t.Fatalf("menu glyph did not rotate during the rail transition: angle %.1f°", angle)
	}
	openingLabel, ok := tt.Find("Search")
	if !ok {
		t.Fatalf("Search disappeared during the rail transition: %q", tt.Texts())
	}
	if center := navigationCompactLabelCenterX(tt.Image(), openingLabel); math.Abs(center-float64(openingLabel.X+m3.RailCollapsed/2)) > 8 {
		t.Fatalf("compact label slid before switching layouts: center %.1f, row %v", center, openingLabel)
	}
	tt.Advance(70 * time.Millisecond)
	opening, ok := tt.Find("Search")
	if !ok || opening.W <= compact.W || opening.W >= 230 {
		t.Fatalf("rail did not pass smoothly through an intermediate width: compact %g, opening %v", compact.W, opening)
	}
	openingPage, ok := tt.Find("Quick picks")
	if !ok || openingPage.X <= compactPage.X {
		t.Fatalf("page did not move with the opening rail: compact %v, opening %v", compactPage, openingPage)
	}
	tt.Advance(time.Second)
	expanded, ok := tt.Find("Search")
	if !ok || expanded.W < 230 || expanded.W > 240 {
		t.Fatalf("rail did not settle at its expanded width: %v", expanded)
	}
	expandedPage, ok := tt.Find("Quick picks")
	if !ok || expandedPage.X <= openingPage.X {
		t.Fatalf("page stopped following the rail: opening %v, expanded %v", openingPage, expandedPage)
	}
	for label, compactRow := range compactRows {
		row, ok := tt.Find(label)
		if !ok {
			t.Fatalf("expanded navigation has no %s destination: %q", label, tt.Texts())
		}
		if row.Y != compactRow.Y {
			t.Errorf("%s lost its vertical anchor: compact row %v, expanded row %v", label, compactRow, row)
		}
		center, ok := navigationItemIconCenterY(tt.Image(), row)
		if !ok {
			t.Errorf("expanded navigation has no visible icon for %s", label)
			continue
		}
		if delta := center - compactIconCenters[label]; delta < 4 || delta > 8 {
			t.Errorf("%s icon did not move down within its own anchor: compact %.1f, expanded %.1f (delta %.1f)", label, compactIconCenters[label], center, delta)
		}
	}

	if err := tt.Click("Collapse navigation"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(150 * time.Millisecond)
	closing, ok := tt.Find("Search")
	if !ok || closing.W >= expanded.W || closing.W <= compact.W {
		t.Fatalf("rail did not pass smoothly through an intermediate width while closing: expanded %g, closing %v", expanded.W, closing)
	}
	closingPage, ok := tt.Find("Quick picks")
	if !ok || closingPage.X >= expandedPage.X {
		t.Fatalf("page did not follow the closing rail: expanded %v, closing %v", expandedPage, closingPage)
	}
	tt.Advance(time.Second)
	collapsed, ok := tt.Find("Search")
	if !ok || collapsed.W != compact.W {
		t.Fatalf("rail did not return to its compact width: start %g, end %v", compact.W, collapsed)
	}
}

func navigationGlyphAngle(img *image.RGBA, button ui.Rect) float64 {
	left := int(button.X + (button.W-24)/2)
	top := int(button.Y + (button.H-24)/2)
	var points [][2]float64
	for y := top; y < top+24; y++ {
		for x := left; x < left+24; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.R < 110 && pixel.G < 110 && pixel.B < 110 {
				points = append(points, [2]float64{float64(x - left), float64(y - top)})
			}
		}
	}
	if len(points) == 0 {
		return 0
	}
	var meanX, meanY float64
	for _, point := range points {
		meanX += point[0]
		meanY += point[1]
	}
	meanX /= float64(len(points))
	meanY /= float64(len(points))
	var varianceX, varianceY, covariance float64
	for _, point := range points {
		dx, dy := point[0]-meanX, point[1]-meanY
		varianceX += dx * dx
		varianceY += dy * dy
		covariance += dx * dy
	}
	return 0.5 * math.Atan2(2*covariance, varianceX-varianceY) * 180 / math.Pi
}

func navigationCompactLabelCenterX(img *image.RGBA, row ui.Rect) float64 {
	minX, maxX := int(row.X+row.W), int(row.X)
	left, right := int(row.X), int(min(row.X+row.W, row.X+m3.RailCollapsed))
	top, bottom := int(row.Y+row.H-24), int(row.Y+row.H)
	for y := top; y < bottom; y++ {
		for x := left; x < right; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.R < 110 && pixel.G < 110 && pixel.B < 110 {
				minX = min(minX, x)
				maxX = max(maxX, x)
			}
		}
	}
	if maxX < minX {
		return 0
	}
	return float64(minX+maxX) / 2
}

func navigationItemIconCenterY(img *image.RGBA, row ui.Rect) (float64, bool) {
	left := int(row.X + m3.RailCollapsed/2 - 12)
	top, bottom := int(row.Y), min(int(row.Y+40), img.Bounds().Max.Y)
	minY, maxY := bottom, top
	for y := top; y < bottom; y++ {
		for x := left; x < left+24; x++ {
			pixel := img.RGBAAt(x, y)
			if pixel.R < 110 && pixel.G < 110 && pixel.B < 110 {
				minY = min(minY, y)
				maxY = max(maxY, y)
			}
		}
	}
	if maxY < minY {
		return 0, false
	}
	return float64(minY+maxY) / 2, true
}

func TestSearchInputsKeepBoundsWhenFocused(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		a := newTestApp(t)
		a.router.Push("/home")
		tt := ui.NewTester(a.view, 1000, 700)
		tt.Frame()
		if tt.Focused(searchPlaceholder) {
			t.Fatal("the home page's search input unexpectedly starts focused")
		}
		assertSearchInputKeepsBoundsOnFocus(t, tt, searchPlaceholder)
	})

	t.Run("settings", func(t *testing.T) {
		a := newTestApp(t)
		a.router.Push("/settings")
		tt := ui.NewTester(a.view, 1000, 700)
		tt.Frame()
		if err := tt.Click("Search settings"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(300 * time.Millisecond)
		tt.Type("discord")
		tt.Frame()
		blurSearchBar(t, tt, "Search settings")
		if !a.settingsSearch.open {
			t.Fatal("the settings search closed when its field lost focus")
		}
		assertSearchInputKeepsBoundsOnFocus(t, tt, "Search settings")
	})

	t.Run("playlist", func(t *testing.T) {
		a, _ := playbackApp(t)
		a.router.Push("/playlist/VLPL_songs")
		tt := ui.NewTester(a.view, 1000, 700)
		for i := 0; i < 20 && len(a.page.playable) == 0; i++ {
			tt.Frame()
		}
		if len(a.page.playable) == 0 {
			t.Fatal("the test playlist did not load")
		}
		if err := tt.Click("Search in this playlist"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(300 * time.Millisecond)
		tt.Type("ameiro")
		tt.Frame()
		blurSearchBar(t, tt, "Search in this playlist")
		if !a.detailSearchOpen {
			t.Fatal("the playlist search closed when its field lost focus")
		}
		assertSearchInputKeepsBoundsOnFocus(t, tt, "Search in this playlist")
	})
}

// Settings' search button opens a local filter without changing pages.
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
		{query: "artwork", want: "Colour from artwork"},
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

	// The same compact control appears nowhere on Stats; global music search
	// remains available on a music page.
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

func TestSettingsDoNotOfferWavyProgressPreference(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if tt.HasText("Wavy progress bar") {
		t.Fatalf("settings still offer the wavy progress preference: %q", tt.Texts())
	}
}

func TestPaletteTileContentDoesNotShiftOnHover(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 1400)
	tt.Frame()

	idle, ok := tt.Find("No colour at all")
	if !ok {
		t.Fatalf("the palette tile is not visible: %q", tt.Texts())
	}
	tt.Move(idle.X+idle.W/2, idle.Y+idle.H/2)
	tt.Frame()

	hovered, ok := tt.Find("No colour at all")
	if !ok {
		t.Fatalf("the palette tile disappeared on hover: %q", tt.Texts())
	}
	if idle != hovered {
		t.Errorf("palette description bounds changed on hover: before %v, after %v", idle, hovered)
	}
}

func TestPaletteTilesHaveUniformSize(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 1400)
	tt.Frame()

	var size ui.Rect
	for i, name := range []string{"Tonal spot", "Vibrant", "Expressive", "Neutral", "Monochrome"} {
		got, ok := tt.Find(name)
		if !ok {
			t.Fatalf("palette tile %q is not visible: %q", name, tt.Texts())
		}
		if i == 0 {
			size = got
			continue
		}
		if got.W != size.W || got.H != size.H {
			t.Errorf("palette tile %q size = %vx%v, want %vx%v", name, got.W, got.H, size.W, size.H)
		}
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

// A suggested query is chosen to see what it searches, from whichever page the
// field was typed in: choosing one shows the search page, as Enter does.
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

// An episode top result exposes Play and resolves the video ID carried by its
// episode-page browse ID.
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

// A menu the hero's artwork opened opens again once it has closed: the mark
// it leaves where the pointer pressed must not take the next right click.
func TestTheHeroMenuOpensAgainAfterItCloses(t *testing.T) {
	a, _ := playbackApp(t)
	tt := open(a, "/playlist/VLPL_songs")
	tt.Frame()
	for round := range 2 {
		if err := tt.RightClick("More options for "); err != nil {
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
	tt := ui.NewTester(a.view, 1000, 4400)
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

// Report a problem opens the issue form with the versions filled in, and
// never carries the account.
func TestReportProblemOpensPrefilledIssue(t *testing.T) {
	a := signedInApp(t)
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5400)
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

// A window shorter than the dialog used to clip it, with the actions past
// the bottom and no way to reach them. The dialog scrolls instead.
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
	// A text the panel clips keeps its label and loses its box. A window
	// tall enough for the dialog would show the note already, and the test
	// would be passing for lack of anything to scroll.
	const note = "Meiro saves the session in a file in its data folder that only your user can read. Sign out to remove Meiro's saved copy."
	if r, ok := tt.Find(note); ok && r.H > 0 {
		t.Fatal("the end of the dialog is already in view, so the window is not shorter than it")
	}
	// Cancel is past the panel too. A click uses the element's own box, so
	// one that lands while the button is clipped says the panel did not
	// scroll it into reach.
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

// Escape closes the dialog and leaves the player. The dialog's Escape is an
// overlay shortcut, which must take the key rather than let the player's
// own Escape run.
func TestEscapeClosesTheDialogAndLeavesThePlayer(t *testing.T) {
	a := newTestApp(t)
	a.current.VideoID = "vid-3"
	a.account.signIn.open = true
	tt := ui.NewTester(a.view, 800, 700)
	tt.Frame()
	// Opening the player before the first frame does not last: that frame
	// navigates, and a navigation closes the player.
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

// Escape closes the browser menu the dialog opened, and leaves the dialog.
// The menu is the overlay on top, so it has to take the key.
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
	if after, _ := a.route(); after.nav != routeHome {
		t.Errorf("the tab moved the page to %q", after.title)
	}
}
