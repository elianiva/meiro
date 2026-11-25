package main

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func artistTracks(artists map[string]string, ids ...string) []youtube.MusicItem {
	items := tracks(ids...)
	for i := range items {
		items[i].BrowseID = artists[items[i].VideoID]
		items[i].Artist = "Artist of " + items[i].VideoID
	}
	return items
}

func TestBlockedArtistsAreSkippedInTheQueue(t *testing.T) {
	a, _ := playbackApp(t)
	q := artistTracks(map[string]string{"a": "UCgood", "b": "UCbad", "c": "UCgood"}, "a", "b", "c")
	a.play(q[0], q)
	a.blockArtist("UCbad", "Bad Artist")

	if a.queue.find("b") >= 0 {
		t.Errorf("a song by a blocked artist stayed in the queue: %v", ids(a.queue.items))
	}
	a.advance()
	if a.current.VideoID != "c" {
		t.Errorf("current=%s; want c", a.current.VideoID)
	}
}

func TestPlayingABlockedSongIsSkipped(t *testing.T) {
	a, r := playbackApp(t)
	a.settings.BlockedArtists = []settings.BlockedArtist{{ID: "UCbad", Name: "Bad Artist"}}
	q := artistTracks(map[string]string{"a": "UCbad", "b": "UCgood"}, "a", "b")
	a.play(q[0], q)
	if a.current.VideoID != "b" || r.asked("a") || !r.asked("b") {
		t.Errorf("current=%s resolved=%v; want b, with a skipped before it was requested", a.current.VideoID, r.ids)
	}
	if a.notice != "Skipped Track a: you blocked this artist" {
		t.Errorf("notice = %q", a.notice)
	}
}

func TestASongThatOnlyFeaturesABlockedArtistStillPlays(t *testing.T) {
	a, r := playbackApp(t)
	a.settings.BlockedArtists = []settings.BlockedArtist{{ID: "UCbad", Name: "Bad Artist"}}
	feature := youtube.MusicItem{
		VideoID: "feature", Title: "Feature", Artist: "Good Artist", BrowseID: "UCgood",
		Byline: &youtube.TrackByline{Parts: []youtube.BylinePart{
			{Text: "Good Artist", BrowseID: "UCgood"}, {Text: " & "}, {Text: "Bad Artist", BrowseID: "UCbad"},
		}},
	}
	if a.excludesItem(feature) {
		t.Fatal("a song by another artist was excluded for a featured artist")
	}
	a.play(feature, []youtube.MusicItem{feature})
	if a.current.VideoID != "feature" || !r.asked("feature") {
		t.Errorf("current=%s resolved=%v; want the song to play", a.current.VideoID, r.ids)
	}
}

func TestBlockingTheArtistOfTheCurrentSongSkipsIt(t *testing.T) {
	a, r := playbackApp(t)
	q := artistTracks(map[string]string{"a": "UCbad", "b": "UCgood"}, "a", "b")
	a.play(q[0], q)
	a.blockArtist("UCbad", "Bad Artist")
	if a.current.VideoID != "b" || !r.asked("b") {
		t.Errorf("current=%s resolved=%v; want playback to move on to b", a.current.VideoID, r.ids)
	}
	if a.notice != "Blocked Bad Artist" {
		t.Errorf("notice = %q", a.notice)
	}
}

func TestUnblockingAnArtistPlaysTheirSongsAgain(t *testing.T) {
	a, _ := playbackApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	song := artistTracks(map[string]string{"a": "UCbad"}, "a")[0]
	a.blockArtist("UCbad", "Bad Artist")
	if !a.excludesItem(song) || len(a.visibleItems([]youtube.MusicItem{song})) != 0 {
		t.Fatal("a blocked artist's song is neither excluded nor hidden")
	}
	a.saver.Wait()
	if got := settings.Load(a.saver.Path).BlockedArtists; len(got) != 1 || got[0].ID != "UCbad" || got[0].Name != "Bad Artist" {
		t.Errorf("saved blocked artists = %v", got)
	}
	a.unblockArtist("UCbad")
	if a.excludesItem(song) || len(a.visibleItems([]youtube.MusicItem{song})) != 1 {
		t.Error("an unblocked artist's song is still excluded")
	}
	a.saver.Wait()
	if got := settings.Load(a.saver.Path).BlockedArtists; len(got) != 0 {
		t.Errorf("the unblock was not saved: %v", got)
	}
}

func TestBlockedSongsStayGreyedOnAnAlbumAndPlaybackSkipsThem(t *testing.T) {
	resetFakes()
	a, r := playbackApp(t)
	a.settings.BlockedArtists = []settings.BlockedArtist{{ID: "UCbad", Name: "Bad Artist"}}
	a.router.Push("/album/MPREb_blocked")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()

	for _, title := range []string{"Blocked Song", "Feature Song", "Good Song"} {
		if !tt.HasText(title) {
			t.Fatalf("%q is missing from the album: %q", title, tt.Texts())
		}
	}
	if err := tt.Click("Blocked Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "feature-song" || r.asked("bad-song") || !r.asked("feature-song") {
		t.Errorf("current=%s resolved=%v; want the blocked song skipped for the one that follows", a.current.VideoID, r.ids)
	}
}

func TestBlockArtistFromASongMenu(t *testing.T) {
	resetFakes()
	a, _ := playbackApp(t)
	a.router.Push("/album/MPREb_blocked")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("More options for Good Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Block artist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.ArtistBlocked("UCgood") {
		t.Fatalf("the menu did not block the song's artist: %v", a.settings.BlockedArtists)
	}
	if err := tt.Click("More options for Good Song"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Unblock artist") {
		t.Errorf("a blocked artist's song menu does not offer to unblock: %q", tt.Texts())
	}
}

func TestBlockedArtistsAreListedAndCanBeUnblockedInSettings(t *testing.T) {
	a := newTestApp(t)
	a.settings.BlockedArtists = []settings.BlockedArtist{{ID: "UCbad", Name: "Bad Artist"}}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 1400)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("blocked artists")
	tt.Frame()
	if !tt.HasText("Bad Artist") {
		t.Fatalf("the blocked artist is not listed: %q", tt.Texts())
	}
	if err := tt.Click("Unblock"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.settings.BlockedArtists) != 0 || !tt.HasText("No blocked artists.") {
		t.Errorf("the artist was not unblocked: %v %q", a.settings.BlockedArtists, tt.Texts())
	}
}

func TestBlockedArtistsPageKeepsOrHidesSongs(t *testing.T) {
	for _, tc := range []struct {
		path string
		keep bool
	}{{"/album/MPREb_blocked", true}, {"/playlist/PL_songs", true}, {"/artist/UCartist", true}, {"/search", false}, {"/", false}} {
		r, _ := matchRoute(tc.path)
		if got := r.keepsBlockedSongs(); got != tc.keep {
			t.Errorf("%s: keepsBlockedSongs = %v, want %v", tc.path, got, tc.keep)
		}
	}
}
