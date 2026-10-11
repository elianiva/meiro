package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// signedInApp is an app whose account client speaks for a saved session, so a
// write reaches the fake YouTube.
func signedInApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp()
	a.signedIn = true
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.authed = a.newClient(auth)
	return a
}

// The library leads with a tile that makes a playlist, before the playlists
// and artists it holds, and no other page offers one.
func TestTheLibraryLeadsWithMakingAPlaylist(t *testing.T) {
	a := signedInApp(t)
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if !tt.HasText("New playlist") {
		t.Fatalf("the library has no way to make a playlist: %q", tt.Texts())
	}
	first, ok := firstCardRow(a)
	if !ok || len(first.items) < 2 {
		t.Fatalf("the library has no shelf of cards: %#v", a.rows)
	}
	if first.items[0].ID != newPlaylistID {
		t.Errorf("the library leads with %q, want the tile that makes a playlist", first.items[0].Title)
	}
	if first.items[1].ID == newPlaylistID {
		t.Errorf("the tile replaced the item the library leads with: %#v", first.items)
	}
	if a.newPlaylist.open {
		t.Error("the library opened the dialog without being asked")
	}

	a.router.Push("/home")
	tt.Frame()
	if tt.HasText("New playlist") {
		t.Errorf("Home offers to make a playlist: %q", tt.Texts())
	}
}

// firstCardRow is the shelf or grid of cards a page shows first.
func firstCardRow(a *app) (row, bool) {
	a.syncRows()
	for _, r := range a.rows {
		if r.kind == rowCards || r.kind == rowGrid {
			return r, true
		}
	}
	return row{}, false
}

// The tile asks for a name, and making the playlist puts it in the library.
func TestMakingAPlaylistFromTheLibraryTile(t *testing.T) {
	libraryLoads.Store(0)
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if err := tt.Click("New playlist"); err != nil {
		t.Fatalf("the tile is not clickable: %v", err)
	}
	tt.Frame()
	if !a.newPlaylist.open {
		t.Fatal("the tile did not ask for a name")
	}
	a.newPlaylist.name = "Late night"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("the dialog has no Create button: %v", err)
	}
	tt.Frame()

	if a.newPlaylist.open || a.newPlaylist.busy || a.newPlaylist.err != "" {
		t.Errorf("the dialog did not close: %#v", a.newPlaylist)
	}
	if got := playlistCreates.Load(); got != 1 {
		t.Errorf("YouTube was asked for %d playlists, want one", got)
	}
	if got := libraryLoads.Load(); got < 2 {
		t.Errorf("the library loaded %d times, want it loaded again to show the new playlist", got)
	}
	// The toast is what the user is told; the frame that showed it may already
	// have taken the notice, so look at the window rather than the field.
	tt.Frame()
	if !tt.HasText("Created “Late night”") {
		t.Errorf("the playlist was not announced: %q", tt.Texts())
	}
}

// A name YouTube will not take is refused before the dialog closes, and the
// name stays for the user to fix.
func TestANameYouTubeRefusesKeepsTheDialog(t *testing.T) {
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.newPlaylist = newPlaylistState{open: true, name: "Late <night>"}
	a.createNewPlaylist()
	if !a.newPlaylist.open || a.newPlaylist.busy {
		t.Errorf("the dialog closed on a name YouTube will not take: %#v", a.newPlaylist)
	}
	if !strings.Contains(a.newPlaylist.err, "cannot contain") {
		t.Errorf("the refusal was not shown: %q", a.newPlaylist.err)
	}
	if a.newPlaylist.name != "Late <night>" {
		t.Errorf("the name was dropped: %q", a.newPlaylist.name)
	}
	if playlistCreates.Load() != 0 {
		t.Error("a name YouTube will not take was sent anyway")
	}
}

// A write that fails says so, and keeps what was typed.
func TestAFailedPlaylistCreateSaysWhy(t *testing.T) {
	a := newTestApp()
	a.signedIn = true
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: failingMusic{}},
		})
	}
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.authed = a.newClient(auth)
	a.newPlaylist = newPlaylistState{open: true, name: "Late night"}
	a.createNewPlaylist()
	if !a.newPlaylist.open || a.newPlaylist.busy {
		t.Errorf("the dialog did not stay open on a failure: %#v", a.newPlaylist)
	}
	if !strings.Contains(a.newPlaylist.err, "HTTP 500") {
		t.Errorf("the failure was not shown: %q", a.newPlaylist.err)
	}
	if a.notice != "" {
		t.Errorf("a failed create claimed %q", a.notice)
	}
}

// An empty name makes no playlist: the dialog keeps asking.
func TestAnEmptyPlaylistNameMakesNothing(t *testing.T) {
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.newPlaylist = newPlaylistState{open: true, name: "   "}
	a.createNewPlaylist()
	if playlistCreates.Load() != 0 {
		t.Error("an empty name was sent to YouTube")
	}
	if !a.newPlaylist.open || a.newPlaylist.busy || a.newPlaylist.err != "" {
		t.Errorf("an empty name changed the dialog: %#v", a.newPlaylist)
	}
}

// An answer that lands after the dialog was dismissed changes nothing.
func TestAPlaylistAnswerAfterDismissalIsDropped(t *testing.T) {
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.router.Push("/library")
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }
	a.newPlaylist = newPlaylistState{open: true, name: "Late night"}
	a.createNewPlaylist()
	a.dismissNewPlaylist()
	for _, work := range pending {
		work()
	}
	if a.newPlaylist.open || a.newPlaylist.busy || a.newPlaylist.err != "" || a.notice != "" {
		t.Errorf("a dismissed dialog came back: %#v, notice %q", a.newPlaylist, a.notice)
	}
	if got := playlistCreates.Load(); got != 1 {
		t.Errorf("YouTube was asked for %d playlists, want the one already sent", got)
	}
}
