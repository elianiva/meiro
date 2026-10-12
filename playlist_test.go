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
func signedInApp(t *testing.T) *app { return signedInWith(t, nil) }

// signedInWith is signedInApp with a transport of its own, for the pages and
// the writes the fake YouTube does not answer the way a test needs.
func signedInWith(t *testing.T, transport http.RoundTripper) *app {
	t.Helper()
	a := newTestApp()
	a.signedIn = true
	if transport != nil {
		a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
			return youtube.NewClient(youtube.Options{
				APIKey:     "test",
				CookieAuth: auth,
				HTTPClient: &http.Client{Transport: transport},
			})
		}
	}
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.authed = a.newClient(auth)
	return a
}

// openSongPicker opens the menu of the first song on the page and asks to put
// it in a playlist, which leaves the picker open.
func openSongPicker(t *testing.T, tt *ui.Tester) {
	t.Helper()
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
	if !tt.HasText("Add to playlist") {
		t.Fatalf("the song menu is missing Add to playlist: %q", tt.Texts())
	}
	if err := tt.Click("Add to playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
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
	// YouTube answers the write before its library holds the playlist, so the
	// app shows the one it made instead of reading the library back without
	// it: no read, and the playlist leads the library's shelf.
	if got := libraryLoads.Load(); got != 1 {
		t.Errorf("the library loaded %d times, want the playlist shown rather than read back", got)
	}
	first, ok := firstCardRow(a)
	if !ok || len(first.items) < 2 {
		t.Fatalf("the library has no shelf of cards: %#v", a.rows)
	}
	if first.items[0].ID != newPlaylistID || first.items[1].Title != "Late night" || first.items[1].BrowseID != "VLPLnew" {
		t.Errorf("the library leads with %#v, want the tile and the playlist just made", first.items[:2])
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

// The menu of a song offers putting it in a playlist.
func TestASongMenuOffersAddingToAPlaylist(t *testing.T) {
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
	if !tt.HasText("Add to playlist") {
		t.Errorf("the song menu is missing Add to playlist: %q", tt.Texts())
	}
}

// The picker lists the playlists the library holds, leaving out the one a
// thumb fills, and adds the song to the one chosen.
func TestAddingASongToAPlaylistFromItsMenu(t *testing.T) {
	a := signedInApp(t)
	tt := ui.NewTester(a.view, 1180, 850)
	openSongPicker(t, tt)

	for _, name := range []string{"Hype", "car radio"} {
		if !tt.HasText(name) {
			t.Errorf("the picker does not offer %q: %q", name, tt.Texts())
		}
	}
	for _, name := range []string{"Liked Music", "Aurora Vale"} {
		if tt.HasText(name) {
			t.Errorf("the picker offers %q, which a song cannot go into: %q", name, tt.Texts())
		}
	}

	if err := tt.Click("Add to Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	write := written()
	if !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") {
		t.Errorf("write endpoint = %q", write.endpoint)
	}
	if write.body["playlistId"] != "PLhype" {
		t.Errorf("playlistId = %#v, want the id without the VL prefix a browse ID carries", write.body["playlistId"])
	}
	actions, _ := write.body["actions"].([]any)
	if len(actions) != 1 {
		t.Fatalf("actions = %#v, want one", write.body["actions"])
	}
	action, _ := actions[0].(map[string]any)
	if action["action"] != "ACTION_ADD_VIDEO" || action["addedVideoId"] != "vid-1" {
		t.Errorf("action = %#v, want the song added", action)
	}
	if a.addToPlaylist.open || a.addToPlaylist.err != "" {
		t.Errorf("the picker did not close: %#v", a.addToPlaylist)
	}
	tt.Frame()
	if !tt.HasText("Added “Ambient One” to “Hype”") {
		t.Errorf("the song was not announced: %q", tt.Texts())
	}
}

// A library page lists a playlist in more than one shelf, as the real one does
// when it holds both a shelf and a grid. The picker offers it once: two rows
// with one key would share one state, and the click would land on the other.
func TestThePickerOffersAPlaylistOnceWhenTheLibraryListsItTwice(t *testing.T) {
	a := signedInWith(t, repeatingLibrary{})
	tt := ui.NewTester(a.view, 1180, 850)
	openSongPicker(t, tt)

	offered := 0
	for _, text := range tt.Texts() {
		if text == "Add to Hype" {
			offered++
		}
	}
	if offered != 1 {
		t.Errorf("the picker offers Hype %d times, want once: %q", offered, tt.Texts())
	}
	if err := tt.Click("Add to Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.addToPlaylist.open || a.addToPlaylist.err != "" {
		t.Errorf("the picker did not close: %#v", a.addToPlaylist)
	}
	if write := written(); !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") {
		t.Errorf("write endpoint = %q, want the edit the click asked for", write.endpoint)
	}
}

// A playlist made from the picker is made holding the song, in one write.
func TestCreatingAPlaylistFromThePickerHoldsTheSong(t *testing.T) {
	a := signedInApp(t)
	tt := ui.NewTester(a.view, 1180, 850)
	openSongPicker(t, tt)

	if err := tt.Click("New playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.newPlaylist.open || a.newPlaylist.song.VideoID != "vid-1" {
		t.Fatalf("the create dialog does not hold the song: %#v", a.newPlaylist)
	}
	if a.addToPlaylist.open {
		t.Error("the picker stayed open behind the create dialog")
	}
	a.newPlaylist.name = "Late night"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	write := written()
	if !strings.HasSuffix(write.endpoint, "/playlist/create") {
		t.Errorf("write endpoint = %q", write.endpoint)
	}
	seeded, _ := write.body["videoIds"].([]any)
	if len(seeded) != 1 || seeded[0] != "vid-1" {
		t.Errorf("videoIds = %#v, want the song it was made with", write.body["videoIds"])
	}
	if a.newPlaylist.open || a.newPlaylist.err != "" {
		t.Errorf("the dialog did not close: %#v", a.newPlaylist)
	}
	tt.Frame()
	if !tt.HasText("Added “Ambient One” to “Late night”") {
		t.Errorf("the playlist was not announced: %q", tt.Texts())
	}
	// The picker is reached from any page, and only the library holds a list a
	// new playlist belongs in.
	if got := a.feed.sections[0].Items[0].Title; got != "Ambient One" {
		t.Errorf("a playlist made from the picker was put on another page: %q, want the page left alone", got)
	}
}

// An edit YouTube will not make says why, and keeps the picker open.
func TestAnAddToPlaylistYouTubeWillNotMakeSaysWhy(t *testing.T) {
	a := signedInWith(t, refusingEdits{status: "STATUS_FAILED"})
	tt := ui.NewTester(a.view, 1180, 850)
	openSongPicker(t, tt)

	if err := tt.Click("Add to Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.addToPlaylist.open || a.addToPlaylist.busy {
		t.Errorf("a refused edit closed the picker: %#v", a.addToPlaylist)
	}
	if !strings.Contains(a.addToPlaylist.err, "STATUS_FAILED") {
		t.Errorf("the refusal was not shown: %q", a.addToPlaylist.err)
	}
	if a.notice != "" {
		t.Errorf("a refused edit claimed %q", a.notice)
	}
}

// A library with no playlists still offers to make one.
func TestThePickerWithNoPlaylistsOffersToMakeOne(t *testing.T) {
	a := signedInWith(t, noPlaylistLibrary{})
	tt := ui.NewTester(a.view, 1180, 850)
	openSongPicker(t, tt)

	if !tt.HasText("You have no playlists yet.") {
		t.Errorf("the picker says nothing about an empty library: %q", tt.Texts())
	}
	if !tt.HasText("New playlist") {
		t.Errorf("the picker does not offer to make a playlist: %q", tt.Texts())
	}
}
