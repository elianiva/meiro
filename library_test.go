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
	a := newTestApp(t)
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

func TestAddingASongRefreshesThePlaylist(t *testing.T) {
	for _, scenario := range []string{"open", "cached", "refused", "duplicate"} {
		t.Run(scenario, func(t *testing.T) {
			resetFakes()
			t.Cleanup(resetFakes)
			a := signedInApp(t)
			a.router.Push("/playlist/VLPLhype")
			tt := ui.NewTester(a.view, 1000, 850)
			tt.Frame()
			if len(a.page.playable) != 0 {
				t.Fatal("the playlist should start empty")
			}
			if scenario == "cached" {
				a.router.Push("/playlist/VLPL_songs")
				tt.Frame()
			}
			refuseWrites.Store(scenario == "refused")
			duplicateWrites.Store(scenario == "duplicate")
			card, ok := tt.Find("Suggestion One")
			if !ok {
				t.Fatalf("no suggested song: %q", tt.Texts())
			}
			tt.Move(card.X+card.W/2, card.Y+card.H/2)
			tt.Frame()
			for _, label := range []string{"More options for Suggestion One", "Add to playlist", "Add to Hype"} {
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			write := written()
			if !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") || write.body["playlistId"] != "PLhype" {
				t.Fatalf("write = %#v", write)
			}
			if scenario == "cached" {
				if len(a.page.playable) != 3 {
					t.Fatal("adding to Hype changed the other playlist")
				}
				a.router.Push("/playlist/VLPLhype")
				tt.Frame()
			}
			if scenario == "refused" || scenario == "duplicate" {
				if hypeSongAdded.Load() || len(a.page.playable) != 0 {
					t.Fatal("an unsuccessful add changed the playlist")
				}
				return
			}
			if !hypeSongAdded.Load() || len(a.page.playable) != 1 || a.page.playable[0].VideoID != "suggestion-one" {
				t.Fatalf("the added song is missing from the playlist: %+v", a.page.playable)
			}
		})
	}
}

// A control that looks small still takes a press 48 DIPs wide and high.

// songLibraryToken is the token the last feedback write sent to YouTube.
func songLibraryToken() string {
	tokens, _ := written().body["feedbackTokens"].([]any)
	if len(tokens) != 1 {
		return ""
	}
	token, _ := tokens[0].(string)
	return token
}

func TestAddingASongToTheLibraryWithoutLikingIt(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)
	a := signedInApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	openTrackMenu(t, tt, "Album Track One")
	if !tt.HasText("Add to library") || tt.HasText("Remove from library") {
		t.Fatalf("a song not in the library should offer to add it: %q", tt.Texts())
	}
	if err := tt.Click("Add to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if token := songLibraryToken(); token != "add-vid-3" || songLibraryWrites.Load() != 1 || likeWrites.Load() != 0 {
		t.Fatalf("adding wrote %q (%d library writes, %d like writes)", token, songLibraryWrites.Load(), likeWrites.Load())
	}
	song := a.page.feed.sections[0].Items[0]
	if song.SongLibrary == nil || !song.SongLibrary.Saved || song.Like == youtube.Liked {
		t.Fatalf("the song says library %+v, like %v after adding", song.SongLibrary, song.Like)
	}

	// Liking a saved song, and unliking it, leave the library alone.
	openTrackMenu(t, tt, "Album Track One")
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	openTrackMenu(t, tt, "Album Track One")
	if err := tt.Click("Unlike"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if songLibraryWrites.Load() != 1 || !fakeSaved("song:vid-3") {
		t.Fatalf("liking touched the library: %d writes, saved %t", songLibraryWrites.Load(), fakeSaved("song:vid-3"))
	}

	openTrackMenu(t, tt, "Album Track One")
	if !tt.HasText("Remove from library") {
		t.Fatalf("a saved song should offer to remove it: %q", tt.Texts())
	}
	if err := tt.Click("Remove from library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if token := songLibraryToken(); token != "remove-vid-3" || songLibraryWrites.Load() != 2 || fakeSaved("song:vid-3") {
		t.Fatalf("removing wrote %q (%d writes)", token, songLibraryWrites.Load())
	}

	// A refused add is taken back.
	refuseWrites.Store(true)
	openTrackMenu(t, tt, "Album Track One")
	if err := tt.Click("Add to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if library := a.page.feed.sections[0].Items[0].SongLibrary; library == nil || library.Saved || !tt.HasText("Could not add “Album Track One” to your library") {
		t.Fatalf("a refused add was not taken back: %+v %q", library, tt.Texts())
	}
}

func TestLikingASongSavesItToTheLibrary(t *testing.T) {
	resetFakes()
	t.Cleanup(resetFakes)
	a := signedInApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	click := func(label string) {
		t.Helper()
		openTrackMenu(t, tt, "Album Track One")
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		tt.Frame()
	}

	click("Like")
	if token := songLibraryToken(); token != "add-vid-3" || songLibraryWrites.Load() != 1 || !fakeSaved("song:vid-3") {
		t.Fatalf("liking saved %q (%d library writes)", token, songLibraryWrites.Load())
	}
	if library := a.page.feed.sections[0].Items[0].SongLibrary; library == nil || !library.Saved {
		t.Fatalf("the liked song does not show saved: %+v", library)
	}

	// Taking the like back, and disliking, keep the song in the library.
	click("Unlike")
	click("Dislike")
	if songLibraryWrites.Load() != 1 || !fakeSaved("song:vid-3") {
		t.Fatalf("unliking or disliking touched the library: %d writes, saved %t", songLibraryWrites.Load(), fakeSaved("song:vid-3"))
	}

	// A like YouTube refuses saves nothing.
	click("Remove from library")
	refuseWrites.Store(true)
	click("Like")
	if songLibraryWrites.Load() != 2 || fakeSaved("song:vid-3") {
		t.Fatalf("a refused like wrote to the library: %d writes", songLibraryWrites.Load())
	}
}

func TestSignedOutSongsOfferNoLibrary(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	openTrackMenu(t, tt, "Album Track One")
	if tt.HasText("Add to library") {
		t.Fatalf("a signed-out menu offers the library: %q", tt.Texts())
	}
}
