package main

import (
	"image"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/youtube"
)

func TestItemKeysUseIdentityAndDistinguishRepeatedItems(t *testing.T) {
	first := youtube.MusicItem{ID: "album-one", BrowseID: "MPREb_one", Title: "Same title"}
	second := youtube.MusicItem{ID: "album-two", BrowseID: "MPREb_two", Title: "Same title"}
	if cardKey(first, "shelf", 0) == cardKey(second, "shelf", 0) {
		t.Fatal("same-title albums with different identities share a key")
	}
	if got, want := cardKey(first, "shelf", 0), cardKey(first, "shelf", 1); got == want {
		t.Fatal("repeated identical cards share a key")
	}
	if got, want := cardKey(first, "shelf-one", 0), cardKey(first, "shelf-two", 0); got == want {
		t.Fatal("repeated cards in different shelves share a key")
	}
	if got, want := songKey(first, songOptions{list: "shelf", position: 0}), songKey(first, songOptions{list: "shelf", position: 1}); got == want {
		t.Fatal("repeated identical items in one shelf share a key")
	}
	firstRow, secondRow := row{kind: rowTrack, item: first, shelf: "/home#0"}, row{kind: rowTrack, item: first, shelf: "/home#1"}
	if firstRow.key() == secondRow.key() {
		t.Fatal("the same item repeated across sections shares a row key")
	}
}

func TestHomeListsSections(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	for _, text := range []string{"Home", "Search", "Explore", "Library", "Settings", "Quick picks", "Ambient One"} {
		if !tt.HasText(text) {
			t.Fatalf("the home page is missing %q: %q", text, tt.Texts())
		}
	}
	if _, ok := tt.Find("Account"); !ok {
		t.Errorf("the account button is missing: %q", tt.Texts())
	}
}

func TestBackRestoresTheHomePageWithoutRefetching(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 300)
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	// A Home fetch would fail now. Returning through router history must use
	// the fresh route entry instead.
	failed := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: failingMusic{}}})
	a.public, a.authed = failed, failed
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Quick picks") || tt.HasText("Something went wrong") {
		t.Fatalf("Back did not restore cached Home data: %q", tt.Texts())
	}
}

func TestCardShelfFetchesVisibleArtworkAndLoadsAsItScrolls(t *testing.T) {
	a := newTestApp()
	a.location = "/home"
	items := make([]youtube.MusicItem, 20)
	for i := range items {
		id := strconv.Itoa(i)
		items[i] = youtube.MusicItem{
			ID: id, BrowseID: "MPREb_" + id, Kind: "music_item",
			Title: "Album " + id, Thumbnail: "https://art.test/albums/" + id,
		}
	}
	a.feed = pageState{sections: []youtube.MusicSection{{Title: "Albums", Items: items}}}
	requested := make(map[string]bool)
	a.thumbs.synth = func(url string, _ int) *ui.Bitmap {
		requested[url] = true
		return ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	}

	tt := ui.NewTester(a.view, 1000, 700)
	if len(requested) == 0 || len(requested) == len(items) {
		t.Fatalf("initial artwork requests = %d of %d items", len(requested), len(items))
	}

	state := a.carousels["/home#0"]
	if state == nil || state.MaxX <= 0 {
		t.Fatal("the card shelf did not create a scrollable carousel")
	}
	state.X = state.MaxX
	tt.Frame()
	if !requested[items[len(items)-1].Thumbnail] {
		t.Error("scrolling to the end did not request the newly visible artwork")
	}
}

func TestOpeningAnAlbumFromTheHomePage(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/album/MPREb_test" {
		t.Fatalf("clicking an album went to %q", a.router.Path())
	}
	if a.detail.title != "Deep Focus" || a.detail.kind != pageAlbum {
		t.Errorf("the album page heading is %+v", a.detail)
	}
	if !tt.HasText("Album Track One") || !tt.HasText("ALBUM") {
		t.Fatalf("the album page did not list its tracks: %q", tt.Texts())
	}
	if len(a.playable) != 1 || a.playable[0].VideoID != "vid-3" {
		t.Errorf("the album's play queue is %v", a.playable)
	}
	foundAlbumTrack := false
	for _, row := range a.rows {
		if row.kind == rowTrack && row.item.VideoID == "vid-3" {
			foundAlbumTrack = true
			if row.item.Thumbnail != a.detail.art {
				t.Errorf("album row artwork = %q, want album artwork %q", row.item.Thumbnail, a.detail.art)
			}
		}
	}
	if !foundAlbumTrack {
		t.Error("the album track row is missing")
	}
	if a.playable[0].Duration != "4:12" {
		t.Errorf("the track length is %q", a.playable[0].Duration)
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if a.router.Path() != "/home" {
		t.Errorf("going back led to %q", a.router.Path())
	}
}

func TestAlbumCardMenuCopiesItsLinkWithoutOpeningTheCard(t *testing.T) {
	a := newTestApp()
	a.location = "/home"
	a.feed = pageState{sections: []youtube.MusicSection{{
		Title: "Listen again",
		Items: []youtube.MusicItem{{
			ID: "MPREb_focus", BrowseID: "MPREb_focus", Title: "Deep Focus", Kind: "music_item",
			Subtitle: "Album • Aurora Vale",
		}},
	}}}
	tt := ui.NewTester(a.view, 1180, 850)
	if _, ok := tt.Find("More options for Deep Focus"); ok {
		t.Fatal("album card menu is visible before hover")
	}
	r, ok := tt.Find("Deep Focus")
	if !ok {
		t.Fatalf("album card is not visible: %q", tt.Texts())
	}
	tt.Move(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if err := tt.Click("More options for Deep Focus"); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Open album", "Copy link"} {
		if !tt.HasText(label) {
			t.Errorf("album card menu is missing %q: %q", label, tt.Texts())
		}
	}
	if tt.HasText("Add to playlist") {
		t.Fatal("album card menu unexpectedly offers playlist editing")
	}
	if got := a.router.Path(); got != "/home" {
		t.Fatalf("opening the card menu activated the album: route = %q", got)
	}
	if err := tt.Click("Copy link"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Clipboard(), "https://music.youtube.com/browse/MPREb_focus"; got != want {
		t.Fatalf("copied album link = %q, want %q", got, want)
	}
}

// The secondary button opens the menu of a card and of a song row, at the
// pointer, without activating what was right-clicked.
func TestRightClickOpensTheItemMenuAtThePointer(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1180, 850)
	card, ok := tt.Find("Deep Focus")
	if !ok {
		t.Fatalf("the home page has no card: %q", tt.Texts())
	}
	x, y := card.X+card.W/2, card.Y+card.H/2
	tt.Move(x, y)
	tt.Frame()
	tt.RightClickAt(x, y)
	tt.Frame()
	if !a.menu.open {
		t.Fatal("a right-click on a card did not open its menu")
	}
	if got := a.router.Path(); got != "/home" {
		t.Fatalf("a right-click on a card activated it: route = %q", got)
	}
	checkMenuAt(t, tt, "Open album", x, y)

	b := newTestApp()
	tb := ui.NewTester(b.view, 1180, 850)
	if err := tb.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	row, ok := tb.Find("Album Track One")
	if !ok {
		t.Fatalf("the album has no track row: %q", tb.Texts())
	}
	// Where the row is, away from the menu button at its end.
	rx, ry := row.X+200, row.Y+row.H/2
	tb.Move(rx, ry)
	tb.Frame()
	tb.RightClickAt(rx, ry)
	tb.Frame()
	if !b.menu.open {
		t.Fatal("a right-click on a song row did not open its menu")
	}
	for _, label := range []string{"Play next", "Add to queue"} {
		if !tb.HasText(label) {
			t.Errorf("the song row menu is missing %q: %q", label, tb.Texts())
		}
	}
	if b.current.VideoID != "" {
		t.Errorf("a right-click on a song row started playback: %q", b.current.VideoID)
	}
	checkMenuAt(t, tb, "Play next", rx, ry)
}

// checkMenuAt fails unless the menu's item named opened at the pointer, which
// was at (x, y).
func checkMenuAt(t *testing.T, tt *ui.Tester, item string, x, y float32) {
	t.Helper()
	r, ok := tt.Find(item)
	if !ok {
		t.Fatalf("the menu has no %q: %q", item, tt.Texts())
	}
	if r.X < x-24 || r.X > x+24 {
		t.Errorf("the menu opened at x=%.0f, not at the pointer at x=%.0f", r.X, x)
	}
	if r.Y < y || r.Y > y+40 {
		t.Errorf("the menu opened at y=%.0f, not below the pointer at y=%.0f", r.Y, y)
	}
}

// The full-screen player leaves the page behind it built, and its queue lists
// the same songs as the page's shelves. A row must key its menu to its own
// list, or the hidden page's row opens a menu of its own, over the player.
func TestQueueRowOpensOneMenuOverThePageBehind(t *testing.T) {
	a, tt := newShotApp("/home", false)
	a.current = songs("qp", "Glass Hours")[0]
	a.queue = songs("qp", "Glass Hours", "Slow Burn", "Paper Lanterns", "Static Bloom")
	a.total = 3 * time.Minute
	a.npOpen = true
	tt.Frame()

	// Hover the queue row of Slow Burn, in the side panel, until its menu
	// button shows. The page behind lists the same song, so a shared key
	// would open that row's menu too.
	hovered := false
	for probe := float32(920); probe < 1140 && !hovered; probe += 20 {
		for py := float32(280); py < 740; py += 16 {
			tt.Move(probe, py)
			tt.Frame()
			if tt.HasText("More options for Slow Burn") {
				hovered = true
				break
			}
		}
	}
	if !hovered {
		t.Fatalf("no queue row to right-click: %q", tt.Texts())
	}
	// Right-click the row itself, away from its menu button at the panel's
	// right edge, where the menu would flip back over the pointer.
	r, ok := tt.Find("More options for Slow Burn")
	if !ok {
		t.Fatalf("the queue row shows no menu button: %q", tt.Texts())
	}
	x, y := r.X-200, r.Y+r.H/2
	tt.RightClickAt(x, y)
	tt.Frame()
	if !a.menu.open {
		t.Fatal("a right-click on a queue row did not open its menu")
	}
	// One menu draws its items once as the item's label and once as its text,
	// so a second menu over the page behind would double that count.
	if n := strings.Count(strings.Join(tt.Texts(), "\n"), "Play next"); n != 2 {
		t.Errorf("the queue row's menu opened %d times", n/2)
	}
	checkMenuAt(t, tt, "Play next", x, y)
}

func TestPlaylistListsAndQueuesVideoEntries(t *testing.T) {
	a := newTestApp()
	path := "/playlist/VLPL_video"
	a.details[path] = detail{title: "Video playlist", kind: pagePlaylist}
	a.router.Push(path)
	tt := ui.NewTester(a.view, 1000, 700)

	if !tt.HasText("Playlist video") {
		t.Fatalf("the playlist did not list its video: %q", tt.Texts())
	}
	if len(a.playable) != 1 || a.playable[0].VideoID != "playlist-video" || !isVideo(a.playable[0]) {
		t.Errorf("playlist queue = %#v, want its video entry", a.playable)
	}
}

func TestTrackMenuQueuesCopiesAndOpensTheArtist(t *testing.T) {
	a := newTestApp()
	track := youtube.MusicItem{
		ID: "picked", VideoID: "picked", BrowseID: "UCartist",
		Title: "Picked track", Subtitle: "Aurora Vale", Kind: "track", Duration: "3:20",
	}
	a.feed.sections = []youtube.MusicSection{{Title: "Songs", Items: []youtube.MusicItem{
		track,
		{ID: "second", VideoID: "second", Title: "Second track", Kind: "track", Duration: "3:10"},
		{ID: "third", VideoID: "third", Title: "Third track", Kind: "track", Duration: "2:50"},
	}}}
	a.location = "/home"
	a.current = youtube.MusicItem{ID: "current", VideoID: "current", Title: "Current track"}
	a.queue = []youtube.MusicItem{a.current, {ID: "later", VideoID: "later", Title: "Later track"}}
	tt := ui.NewTester(a.view, 1000, 700)
	menuButton := "More options for Picked track"
	hoverTrack := func() {
		r, ok := tt.Find("Picked track")
		if !ok {
			t.Fatal("track row is not visible")
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		tt.Frame()
	}
	hoverTrack()
	if err := tt.Click(menuButton); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Go to artist", "Play next", "Add to queue", "Copy link"} {
		if !tt.HasText(label) {
			t.Errorf("track menu is missing %q: %q", label, tt.Texts())
		}
	}
	if tt.HasText("Add to playlist") {
		t.Fatal("track menu unexpectedly offers playlist editing")
	}
	if err := tt.Click("Play next"); err != nil {
		t.Fatal(err)
	}
	if len(a.queue) != 3 || a.queue[1].VideoID != track.VideoID || a.index != 0 {
		t.Fatalf("Play next changed queue to %#v at index %d", a.queue, a.index)
	}
	hoverTrack()
	if err := tt.Click(menuButton); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add to queue"); err != nil {
		t.Fatal(err)
	}
	if len(a.queue) != 4 || a.queue[3].VideoID != track.VideoID || a.index != 0 {
		t.Fatalf("Add to queue changed queue to %#v at index %d", a.queue, a.index)
	}
	hoverTrack()
	if err := tt.Click(menuButton); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Copy link"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Clipboard(), "https://music.youtube.com/watch?v=picked"; got != want {
		t.Fatalf("copied link = %q, want %q", got, want)
	}
	hoverTrack()
	if err := tt.Click(menuButton); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Go to artist"); err != nil {
		t.Fatal(err)
	}
	if got, want := a.router.Path(), "/artist/UCartist"; got != want {
		t.Fatalf("Go to artist opened %q, want %q", got, want)
	}
}

// A track's menu opens an artist page knowing only the ID, so the page must
// take the name and the picture it puts over its content from what it loads.
func TestArtistPageTakesItsHeadingFromWhatItLoads(t *testing.T) {
	a := newTestApp()
	// What "Go to artist" seeds: the kind, and nothing else.
	a.details["/artist/UCartist"] = detail{kind: pageArtist}
	a.router.Push("/artist/UCartist")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if got := a.detail.title; got != "Aurora Vale" {
		t.Errorf("artist name = %q, want the name its page carries", got)
	}
	if got := a.detail.art; got != "https://img.example/aurora" {
		t.Errorf("artist picture = %q, want the one its page carries", got)
	}
	if !tt.HasText("Aurora Vale") {
		t.Errorf("the artist does not show over the tracks: %q", tt.Texts())
	}
	// The heading the page loaded also names the queue it plays into.
	if _, source := a.playbackQueueOptions(0); source != "Aurora Vale" {
		t.Errorf("queue source = %q, want the artist's name", source)
	}
}

func TestTargetOfRoutesItemsToPages(t *testing.T) {
	cases := []struct {
		name string
		item youtube.MusicItem
		kind string
		id   string
	}{
		{"song", youtube.MusicItem{VideoID: "abc"}, pageTrack, "abc"},
		{"video", youtube.MusicItem{VideoID: "video-1", Kind: "video"}, pageTrack, "video-1"},
		{"album", youtube.MusicItem{BrowseID: "MPREb_1"}, pageAlbum, "MPREb_1"},
		{"artist", youtube.MusicItem{BrowseID: "UCabc"}, pageArtist, "UCabc"},
		{"playlist", youtube.MusicItem{BrowseID: "VLPLabc"}, pagePlaylist, "VLPLabc"},
		{"library playlist", youtube.MusicItem{PlaylistID: "PLabc"}, pagePlaylist, "PLabc"},
		{"video with a mix queue", youtube.MusicItem{VideoID: "video-1", BrowseID: "RDAMVMvideo-1", Kind: "video"}, pageTrack, "video-1"},
		{"video naming its artist", youtube.MusicItem{VideoID: "video-1", BrowseID: "UCartist", Kind: "video"}, pageTrack, "video-1"},
		{"library album", youtube.MusicItem{BrowseID: "FEmusic_library_privately_owned_release1"}, pageAlbum, "FEmusic_library_privately_owned_release1"},
		{"nothing", youtube.MusicItem{Title: "empty"}, "", ""},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			kind, id := targetOf(test.item)
			if kind != test.kind || id != test.id {
				t.Errorf("targetOf = (%q, %q), want (%q, %q)", kind, id, test.kind, test.id)
			}
		})
	}
}

// A failed request for more must not throw away the page, and must leave the
// button to ask again.
func TestFailedLoadMoreKeepsThePage(t *testing.T) {
	a := newTestApp()
	client := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: failingMusic{}}})
	a.public, a.authed = client, client
	a.feed.items = []youtube.MusicItem{{VideoID: "a", ID: "a", Title: "Kept song"}}
	a.feed.more = "token"

	a.loadMore()
	if a.feed.err != "" || a.feed.moreErr == "" || a.feed.more != "token" {
		t.Fatalf("after a failed load: err=%q moreErr=%q more=%q", a.feed.err, a.feed.moreErr, a.feed.more)
	}
	a.setRows()
	kinds := ""
	for _, r := range a.rows {
		if r.kind == rowCards {
			kinds += "c"
		}
		if r.kind == rowMore {
			kinds += "m"
		}
		if r.kind == rowError {
			t.Fatalf("the page was replaced by an error: %q", r.title)
		}
	}
	if kinds != "cm" {
		t.Errorf("rows after a failed load more = %q, want the shelf and the button", kinds)
	}
}
