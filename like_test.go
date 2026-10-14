package main

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// afterAnimations draws frames until the player's entry animation is over,
// which a click would otherwise land in the middle of.
func afterAnimations(tt *ui.Tester) {
	for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); {
		tt.Frame()
	}
}

// openTrackMenu opens the menu of the row of a track on the page shown.
func openTrackMenu(t *testing.T, tt *ui.Tester, title string) {
	t.Helper()
	row, ok := tt.Find(title)
	if !ok {
		t.Fatalf("no row %q: %q", title, tt.Texts())
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	if err := tt.Click("More options for " + title); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

// likeWrite is the last like write the fake YouTube was asked for: its
// endpoint, and the video its target names.
func likeWrite() (endpoint, videoID string) {
	write := written()
	target, _ := write.body["target"].(map[string]any)
	videoID, _ = target["videoId"].(string)
	return write.endpoint[strings.LastIndex(write.endpoint, "/like/"):], videoID
}

func TestLikingATrackFromItsMenu(t *testing.T) {
	resetFakes()
	a := signedInApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	openTrackMenu(t, tt, "Album Track One")
	if !tt.HasText("Like") || tt.HasText("Unlike") {
		t.Fatalf("a track not known to be liked should offer Like: %q", tt.Texts())
	}
	if err := tt.Click("Like"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := likeWrite(); endpoint != "/like/like" || id != "vid-3" || likeWrites.Load() != 1 {
		t.Fatalf("like wrote %s for %q (%d writes)", endpoint, id, likeWrites.Load())
	}
	if got := a.page.feed.sections[0].Items[0].Like; got != youtube.Liked {
		t.Fatalf("the page's track says %q after liking", got)
	}

	openTrackMenu(t, tt, "Album Track One")
	if !tt.HasText("Unlike") {
		t.Fatalf("a liked track should offer Unlike: %q", tt.Texts())
	}
	if err := tt.Click("Unlike"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := likeWrite(); endpoint != "/like/removelike" || id != "vid-3" || likeWrites.Load() != 2 {
		t.Fatalf("unlike wrote %s for %q (%d writes)", endpoint, id, likeWrites.Load())
	}
	if got := a.page.feed.sections[0].Items[0].Like; got != youtube.Indifferent {
		t.Fatalf("the page's track says %q after unliking", got)
	}
}

func TestSignedOutHasNoLike(t *testing.T) {
	resetFakes()
	a, _ := playbackApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	openTrackMenu(t, tt, "Album Track One")
	if tt.HasText("Like") || tt.HasText("Add to playlist") == false {
		t.Fatalf("the signed-out menu should have no Like, and its other items: %q", tt.Texts())
	}
	tt.CloseMenu()
	if err := tt.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Like ") || strings.HasPrefix(text, "Unlike ") {
			t.Fatalf("the signed-out player has a heart: %q", text)
		}
	}
	a.toggleLike(a.current) // a shortcut that reached it would write nothing
	if likeWrites.Load() != 0 {
		t.Fatalf("a signed-out like wrote %d times", likeWrites.Load())
	}
}

func TestThePlayerHeartShowsWhatYouTubeSaid(t *testing.T) {
	resetFakes()
	fakeLikeState.Store("LIKE")
	t.Cleanup(resetFakes)
	a, _ := playbackApp(t)
	a.settings.AutoPlay = true
	a.account.session = signedInApp(t).account.session
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 800)
	tt.Frame()
	if err := tt.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	if !tt.HasText("Unlike Album Track One") {
		t.Fatalf("the player did not show the like /next reported: %q", tt.Texts())
	}
	tt.Frame()
	if err := tt.Click("Unlike Album Track One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if endpoint, id := likeWrite(); endpoint != "/like/removelike" || id != "vid-3" {
		t.Fatalf("the heart wrote %s for %q", endpoint, id)
	}
	if !tt.HasText("Like Album Track One") || a.current.Like != youtube.Indifferent {
		t.Fatalf("the heart did not follow the write: %q, %q", a.current.Like, tt.Texts())
	}
}

func TestARejectedLikeIsTakenBack(t *testing.T) {
	resetFakes()
	refuseWrites.Store(true)
	t.Cleanup(resetFakes)
	a, _ := playbackApp(t)
	a.account.session = signedInApp(t).account.session
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 800)
	tt.Frame()
	if err := tt.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)

	// Hold the write back, to see the heart before YouTube answers.
	var held []func()
	run := a.run
	a.run = func(work func()) { held = append(held, work) }
	if err := tt.Click("Like Album Track One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Unlike Album Track One") || a.current.Like != youtube.Liked {
		t.Fatalf("the heart did not fill at once: %q %q", a.current.Like, tt.Texts())
	}
	// A press while the write is out writes nothing more.
	if err := tt.Click("Unlike Album Track One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(held) != 1 || a.current.Like != youtube.Liked {
		t.Fatalf("a second press while the first was out: %d writes held, like %d", len(held), a.current.Like)
	}
	a.run = run
	for _, work := range held {
		work()
	}
	tt.Frame()
	if likeWrites.Load() != 1 {
		t.Fatalf("writes = %d", likeWrites.Load())
	}
	if !tt.HasText("Like Album Track One") || tt.HasText("Unlike Album Track One") || a.current.Like != youtube.LikeUnknown {
		t.Fatalf("the heart stayed filled after a rejection: %q %q", a.current.Like, tt.Texts())
	}
	if !tt.HasText("Could not like “Album Track One”") {
		t.Fatalf("the failure was not announced: %q", tt.Texts())
	}
}
