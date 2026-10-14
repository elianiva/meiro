package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// libraryWrite is the last write the fake YouTube was asked for to a playlist's
// like: its endpoint, and the playlist its target names.
func libraryWrite() (endpoint, playlistID string) {
	write := written()
	return write.endpoint[strings.LastIndex(write.endpoint, "/like/"):], writtenPlaylist()
}

func TestSavingAnAlbumToTheLibraryAndTakingItOut(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if !tt.HasText("Save to library") || tt.HasText("Remove from library") {
		t.Fatalf("an album not in the library should offer to save it: %q", tt.Texts())
	}
	if err := tt.Click("Save to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The write names the album's audio playlist, which the page carried, and
	// not the browse ID the page was opened with.
	if endpoint, id := libraryWrite(); endpoint != "/like/like" || id != "OLAK5uy_test" || likeWrites.Load() != 1 {
		t.Fatalf("saving wrote %s for %q (%d writes)", endpoint, id, likeWrites.Load())
	}
	if !tt.HasText("Remove from library") || !tt.HasText("Saved “Deep Focus” to your library") {
		t.Fatalf("the page did not show the album saved: %q", tt.Texts())
	}

	// The library is read again, and holds the album.
	libraryLoads.Store(0)
	a.router.Push("/library")
	tt.Frame()
	if libraryLoads.Load() != 1 || !tt.HasText("Deep Focus") {
		t.Fatalf("the library was read %d times, and shows: %q", libraryLoads.Load(), tt.Texts())
	}

	// The album page says what YouTube now says, and takes the album out.
	a.router.Push("/album/MPREb_test")
	tt.Frame()
	if !tt.HasText("Remove from library") || a.page.library != (youtube.LibraryState{ID: "OLAK5uy_test", Saved: true}) {
		t.Fatalf("the album page did not read its state back: %+v %q", a.page.library, tt.Texts())
	}
	if err := tt.Click("Remove from library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := libraryWrite(); endpoint != "/like/removelike" || id != "OLAK5uy_test" || likeWrites.Load() != 2 {
		t.Fatalf("removing wrote %s for %q (%d writes)", endpoint, id, likeWrites.Load())
	}
	if !tt.HasText("Save to library") || fakeSaved(fakeAlbumPlaylist) {
		t.Fatalf("the album was not taken out: %q", tt.Texts())
	}
}

func TestSavingAPlaylist(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.router.Push("/playlist/VLPL_video")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Save to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := libraryWrite(); endpoint != "/like/like" || id != "PL_video" {
		t.Fatalf("saving wrote %s for %q", endpoint, id)
	}
	if !tt.HasText("Remove from library") {
		t.Fatalf("the page did not show the playlist saved: %q", tt.Texts())
	}
}

func TestARejectedSaveIsTakenBack(t *testing.T) {
	resetFakes()
	refuseWrites.Store(true)
	t.Cleanup(resetFakes)
	a := signedInApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	var held []func()
	run := a.run
	a.run = func(work func()) { held = append(held, work) }
	if err := tt.Click("Save to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Remove from library") {
		t.Fatalf("the button did not flip before YouTube answered: %q", tt.Texts())
	}
	if err := tt.Click("Remove from library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(held) != 1 {
		t.Fatalf("a press while the write was out wrote again: %d held", len(held))
	}
	a.run = run
	for _, work := range held {
		work()
	}
	tt.Frame()
	if !tt.HasText("Save to library") || tt.HasText("Remove from library") || a.page.library.Saved {
		t.Fatalf("the button stayed flipped after a rejection: %q", tt.Texts())
	}
	if !tt.HasText("Could not save “Deep Focus”") {
		t.Fatalf("the failure was not announced: %q", tt.Texts())
	}
	if fakeSaved(fakeAlbumPlaylist) {
		t.Error("a refused write changed the library")
	}
}

func TestSignedOutHasNoLibraryButtons(t *testing.T) {
	resetFakes()
	a := newTestApp()
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if tt.HasText("Save to library") || tt.HasText("Remove from library") {
		t.Fatalf("a signed-out album page offers the library: %q", tt.Texts())
	}
	a.toggleLibrary(youtube.LibraryState{ID: fakeAlbumPlaylist}, "x") // nothing may reach YouTube
	if likeWrites.Load() != 0 {
		t.Fatalf("a signed-out save wrote %d times", likeWrites.Load())
	}
}

func TestTakingAPlaylistOutOfTheLibraryFromItsCard(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1180, 850)
	tt.Frame()
	card, ok := tt.Find("Hype")
	if !ok {
		t.Fatalf("the library has no Hype: %q", tt.Texts())
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Remove from library") {
		t.Fatalf("a saved playlist's menu should offer to remove it: %q", tt.Texts())
	}
	if err := tt.Click("Remove from library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := libraryWrite(); endpoint != "/like/removelike" || id != "PLhype" {
		t.Fatalf("removing wrote %s for %q", endpoint, id)
	}
	tt.Frame()
	if slices.Contains(tt.Texts(), "Hype") || !slices.Contains(tt.Texts(), "car radio") {
		t.Fatalf("the library page still shows the playlist taken out, or lost another: %q", tt.Texts())
	}
	if !tt.HasText("Removed “Hype” from your library") {
		t.Fatalf("the removal was not announced: %q", tt.Texts())
	}
	// The card with no way to save, as a playlist the account made, offers none.
	card, _ = tt.Find("car radio")
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for car radio"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Remove from library") || tt.HasText("Save to library") {
		t.Fatalf("a card that carried no toggle offers the library: %q", tt.Texts())
	}
}
