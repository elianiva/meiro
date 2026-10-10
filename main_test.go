package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/systemmedia"
	"github.com/elianiva/meiro/youtube"
)

type captureSystemMedia struct{ state systemmedia.State }

func (s *captureSystemMedia) Update(state systemmedia.State) { s.state = state }
func (*captureSystemMedia) Close() error                     { return nil }

// searches counts the search requests the fake YouTube has answered, and
// fakePageID records the channel a request acted as.
var (
	searches   atomic.Int32
	fakePageID atomic.Value
)

// fakeMusic answers the InnerTube endpoints with canned responses, so the
// app's tests need no network.
type fakeMusic struct{}

func (fakeMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	asked := ""
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		asked = string(body)
	}
	if id := request.Header.Get("X-Goog-PageId"); id != "" {
		fakePageID.Store(id)
	}
	reply := homeResponse
	switch {
	case strings.HasSuffix(request.URL.Path, "/account/accounts_list"):
		reply = accountsResponse
	case strings.HasSuffix(request.URL.Path, "/music/get_search_suggestions"):
		reply = suggestionsResponse
	case strings.Contains(asked, "FEmusic_explore"):
		reply = exploreResponse
	case strings.Contains(asked, "FEmusic_listening_review"):
		reply = recapResponse
	case strings.Contains(asked, "VLPL_video"):
		reply = playlistResponse
	case strings.Contains(asked, "MPREb_test"):
		reply = albumResponse
	case strings.Contains(asked, `"query"`):
		searches.Add(1)
		reply = searchResponse
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

// newTestApp builds an app whose loads run inline and whose YouTube requests
// are answered by fakeMusic.
func newTestApp() *app {
	a := newApp()
	a.volume = a.settings.Volume
	a.player.SetVolume(a.volume / 100)
	a.run = func(work func()) { work() }
	// Debounced work runs at once, so a frame sees the suggestion request's
	// answer without waiting.
	a.schedule = func(_ time.Duration, work func()) func() {
		work()
		return func() {}
	}
	newFakeClient := func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			HTTPClient: &http.Client{Transport: fakeMusic{}},
			CookieAuth: auth,
		})
	}
	a.newClient = newFakeClient
	a.public, a.authed = newFakeClient(nil), newFakeClient(nil)
	return a
}

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

// Typing in the search field must not search: only Enter does.
func TestSearchRunsOnSubmitOnly(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if err := tt.Click("Search songs, albums, artists"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ambient")
	time.Sleep(50 * time.Millisecond)
	tt.Frame()
	if n := searches.Load(); n != 0 || tt.HasText("Search Result Song") {
		t.Fatalf("typing ran %d searches: %q", n, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if n := searches.Load(); n != 1 {
		t.Fatalf("Enter ran %d searches, want 1", n)
	}
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the search results are missing: %q", tt.Texts())
	}
	if len(a.settings.Recent) != 1 || a.settings.Recent[0] != "ambient" {
		t.Errorf("the search was not remembered: %v", a.settings.Recent)
	}
	// Picking another filter searches again for what was submitted, not for
	// whatever is half typed.
	tt.Type("zzz")
	if err := tt.Click("Songs"); err != nil {
		t.Fatal(err)
	}
	if n := searches.Load(); n != 2 || a.search.submitted != "ambient" {
		t.Errorf("a filter ran %d searches for %q", n, a.search.submitted)
	}
}

// Typing shows the completions YouTube Music offers; choosing one searches it.
func TestSearchSuggestionsRunTheChosenSearch(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if err := tt.Click("Search songs, albums, artists"); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if !tt.HasText("Yorushika songs") {
		t.Fatalf("suggestions are missing: %q", tt.Texts())
	}
	if n := searches.Load(); n != 0 {
		t.Fatalf("showing suggestions ran %d searches, want 0", n)
	}
	if err := tt.Click("Yorushika songs"); err != nil {
		t.Fatal(err)
	}
	if a.search.submitted != "Yorushika songs" || searches.Load() != 1 {
		t.Fatalf("choosing a suggestion submitted %q after %d searches", a.search.submitted, searches.Load())
	}
	if tt.HasText("Yorushika songs") {
		t.Errorf("suggestions stayed after the search ran: %q", tt.Texts())
	}
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the chosen search has no results: %q", tt.Texts())
	}
}

// The recap is offered only to a signed-in account, and shows its review.
func TestRecapPageFollowsTheSignIn(t *testing.T) {
	a := newTestApp()
	a.router.Push("/recap")
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("Your recap lives here") {
		t.Fatalf("signed-out recap page = %q", tt.Texts())
	}

	a.signedIn, a.account = true, youtube.AccountDetails{Name: "Me"}
	a.router.Push("/home")
	tt.Frame()
	if err := tt.Click("Recap"); err != nil {
		t.Fatalf("the signed-in rail has no Recap entry: %v", err)
	}
	if a.router.Path() != "/recap" {
		t.Fatalf("the Recap entry led to %q", a.router.Path())
	}
	tt.Frame()
	if !tt.HasText("Top song of the year") {
		t.Fatalf("the recap page did not load: %q", tt.Texts())
	}
}

// The account menu lists the channels the session can act as, and switching
// rebuilds the client around the chosen one.
func TestAccountMenuSwitchesChannel(t *testing.T) {
	a := newTestApp()
	a.router.Push("/home")
	a.signedIn = true
	a.account = youtube.AccountDetails{Name: "Me"}
	a.ytDlpCookie = "SAPISID=secret"
	a.accounts = []youtube.AccountChannel{
		{Name: "Main channel", ChannelID: "UC-main", Selected: true},
		{Name: "Brand channel", ChannelID: "UC-brand"},
	}
	a.settings.Channel = "UC-main"
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Account"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Brand channel") {
		t.Fatalf("the account menu does not list the channels: %q", tt.Texts())
	}
	fakePageID.Store("")
	if err := tt.Click("Brand channel"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Channel != "UC-brand" {
		t.Fatalf("the chosen channel is %q", a.settings.Channel)
	}
	if got, _ := fakePageID.Load().(string); got != "UC-brand" {
		t.Fatalf("the switched client sent page ID %q, want UC-brand", got)
	}
	if !a.signedIn || a.authed == nil {
		t.Fatal("switching channel dropped the sign-in")
	}
}

func TestPlayerBarShowsTheCurrentTrack(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One", Subtitle: "Someone"}
	a.total = 3*time.Minute + 33*time.Second
	tt := ui.NewTester(a.view, 1000, 700)
	if !tt.HasText("Ambient One") || !tt.HasText("3:33") || !tt.HasText("0:00") {
		t.Fatalf("the player bar does not show the track: %q", tt.Texts())
	}
	for _, control := range []string{"Play", "Previous", "Next", "Shuffle", "Repeat", "Position", "Up next"} {
		if _, ok := tt.Find(control); !ok {
			t.Errorf("the %s control is missing: %q", control, tt.Texts())
		}
	}
	if err := tt.Click("Open the player"); err != nil {
		t.Fatal(err)
	}
	if !a.npOpen || !tt.HasText("Now playing") {
		t.Errorf("the full-screen player did not open: %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	if a.npOpen {
		t.Errorf("Escape did not close the full-screen player")
	}
}

func TestSystemMediaCommandsUpdatePlaybackState(t *testing.T) {
	a := newApp()
	a.current = youtube.MusicItem{VideoID: "current", Title: "Current song", Subtitle: "An artist"}
	a.queue = []youtube.MusicItem{a.current, {VideoID: "next", Title: "Next song"}}
	a.total = 3 * time.Minute
	a.volume = 60
	media := &captureSystemMedia{}
	a.systemMedia = media

	a.syncSystemMedia()
	if media.state.Title != "Current song" || media.state.Artist != "An artist" || !media.state.CanNext || media.state.Volume != 0.6 {
		t.Fatalf("initial system media state = %+v", media.state)
	}

	controls := a.systemMediaControls()
	controls.SetVolume(0.35)
	controls.SetShuffle(true)
	controls.SetRepeat("Playlist")
	if a.volume != 35 || a.player.Volume() != 0.35 || !media.state.Shuffle || media.state.Volume != 0.35 || a.repeat != repeatQueue || media.state.LoopStatus != "Playlist" {
		t.Errorf("media volume/shuffle/repeat did not reach the app and system state: app volume=%v player volume=%v repeat=%v state=%+v", a.volume, a.player.Volume(), a.repeat, media.state)
	}
	a.resolving = true
	controls.Stop()
	if a.resolving || media.state.Status != systemmedia.Stopped {
		t.Errorf("media Stop left playback state resolving=%v status=%q", a.resolving, media.state.Status)
	}
}

func TestNowPlayingArtworkOpensTrackActions(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{
		ID: "now-playing", VideoID: "now-playing", BrowseID: "UCartist",
		Title: "Now playing song", Subtitle: "Aurora Vale", Kind: "track",
	}
	a.queue = []youtube.MusicItem{a.current, {VideoID: "later", Title: "Later song"}}
	a.location = "/home"
	a.npOpen = true
	tt := ui.NewTester(a.view, 1180, 760)
	menuButton := "More options for Now playing song"
	if err := tt.Click(menuButton); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"Go to artist", "Play next", "Add to queue", "Copy link"} {
		if !tt.HasText(label) {
			t.Errorf("playback menu is missing %q: %q", label, tt.Texts())
		}
	}
	if err := tt.Click("Copy link"); err != nil {
		t.Fatal(err)
	}
	if got, want := tt.Clipboard(), "https://music.youtube.com/watch?v=now-playing"; got != want {
		t.Fatalf("copied playback link = %q, want %q", got, want)
	}
}

func TestVideoPlaybackShowsBadgeOnPlayerArtwork(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "video-1", Title: "Video track", Kind: "video"}
	tt := ui.NewTester(a.view, 1000, 700)
	if _, ok := tt.Find("Video"); !ok || !tt.HasText("VIDEO") {
		t.Fatalf("video playback has no badge: %q", tt.Texts())
	}
	a.current.Kind = "track"
	tt.Frame()
	if _, ok := tt.Find("Video"); ok {
		t.Errorf("audio-only track retained the video badge: %q", tt.Texts())
	}
}

func TestUpNextShowsAutoplayStateAndRecommendationSection(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "current", Title: "Current song"}
	a.queue = []youtube.MusicItem{
		{VideoID: "current", Title: "Current song", Thumbnail: "https://art.test/queue/current"},
		{VideoID: "chosen-next", Title: "Chosen next", Thumbnail: "https://art.test/queue/next"},
		{VideoID: "recommended", Title: "Recommended song", Thumbnail: "https://art.test/queue/recommended"},
	}
	a.index, a.recommendationStart, a.queueSource = 1, 2, "My playlist"
	a.location, a.npOpen = "/home", true
	requested := make(map[string]bool)
	a.thumbs.synth = func(url string, _ int) *ui.Bitmap {
		requested[url] = true
		return ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	}
	tt := ui.NewTester(a.view, 1180, 760)
	for _, item := range a.queue {
		if !requested[item.Thumbnail] {
			t.Errorf("the queue row %q did not request its artwork: %v", item.Title, requested)
		}
	}
	for _, label := range []string{"Playing from", "My playlist", "Auto-play", "Add similar music when this queue ends.", "Recommended", "Recommended song"} {
		if !tt.HasText(label) {
			t.Errorf("Up next panel is missing %q: %q", label, tt.Texts())
		}
	}
	if err := tt.Click("Toggle auto-play"); err != nil {
		t.Fatal(err)
	}
	if a.settings.AutoPlay {
		t.Fatal("the Auto-play switch did not turn autoplay off")
	}
	if _, ok := a.nextIndex(); ok {
		t.Errorf("turning autoplay off left a generated recommendation playable")
	}
}

func TestRelatedTabShowsRelatedTracks(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "current", Title: "Current song"}
	a.related = relatedState{
		videoID: "current",
		items:   []youtube.MusicItem{{VideoID: "related", Title: "Related song", Kind: "track", Thumbnail: "https://art.test/related/0"}},
	}
	a.location, a.npOpen, a.npTab = "/home", true, 2
	requested := make(map[string]bool)
	a.thumbs.synth = func(url string, _ int) *ui.Bitmap {
		requested[url] = true
		return ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 8)))
	}
	tt := ui.NewTester(a.view, 1180, 760)
	if !tt.HasText("Related") || !tt.HasText("Related song") {
		t.Fatalf("related tab did not show its tracks: %q", tt.Texts())
	}
	if !requested["https://art.test/related/0"] {
		t.Errorf("the related row did not request its artwork: %v", requested)
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

func TestEnqueueKeepsManualTracksBeforeRecommendations(t *testing.T) {
	a := newTestApp()
	a.current = youtube.MusicItem{VideoID: "current"}
	a.queue = []youtube.MusicItem{
		{VideoID: "current"}, {VideoID: "selected"}, {VideoID: "recommendation"},
	}
	a.index, a.recommendationStart = 0, 2
	a.enqueue(youtube.MusicItem{VideoID: "added", Title: "Added"}, false)
	if len(a.queue) != 4 || a.queue[2].VideoID != "added" || a.queue[3].VideoID != "recommendation" {
		t.Fatalf("Add to queue placed items in %#v", a.queue)
	}
	if a.recommendationStart != 3 || a.index != 0 {
		t.Errorf("queue position changed recommendation start to %d or current index to %d", a.recommendationStart, a.index)
	}
}

func TestPlayNextIsRespectedWithShuffleAndAutoplayOff(t *testing.T) {
	a := newTestApp()
	a.settings.AutoPlay = false
	a.current = youtube.MusicItem{VideoID: "current"}
	a.queue = []youtube.MusicItem{{VideoID: "current"}, {VideoID: "recommendation"}}
	a.index, a.recommendationStart, a.shuffle = 0, 1, true
	a.enqueue(youtube.MusicItem{VideoID: "picked-next"}, true)
	if a.recommendationStart != 2 {
		t.Fatalf("Play next left the autoplay boundary at %d", a.recommendationStart)
	}
	if next, ok := a.nextIndex(); !ok || next != 1 || a.queue[next].VideoID != "picked-next" {
		t.Fatalf("Play next returned index %d, %v in queue %#v", next, ok, a.queue)
	}
}

func TestShuffleAndRepeatChooseTheNextTrack(t *testing.T) {
	a := newTestApp()
	a.queue = []youtube.MusicItem{{VideoID: "a"}, {VideoID: "b"}, {VideoID: "c"}}
	if i, ok := a.nextIndex(); !ok || i != 1 {
		t.Errorf("next = %d, %v", i, ok)
	}
	a.index = 2
	if _, ok := a.nextIndex(); ok {
		t.Errorf("the queue should end after its last track")
	}
	a.repeat = 1
	if i, ok := a.nextIndex(); !ok || i != 0 {
		t.Errorf("repeating the queue went to %d, %v", i, ok)
	}
	a.shuffle = true
	for range 50 {
		if i, ok := a.nextIndex(); !ok || i == a.index {
			t.Fatalf("shuffle chose %d, %v for the current track", i, ok)
		}
	}
}

// Every page draws, in both appearances, with and without a track playing.
func TestEveryPageDraws(t *testing.T) {
	for _, path := range []string{"/home", "/explore", "/library", "/search", "/settings", "/album/MPREb_test", "/nowhere"} {
		for _, dark := range []bool{false, true} {
			t.Run(path, func(t *testing.T) {
				a := newTestApp()
				a.router.Push(path)
				tt := ui.NewTester(a.view, 1000, 700)
				tt.SetDark(dark)
				a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One"}
				a.npOpen = path == "/home"
				tt.Frame()
				if len(tt.Texts()) == 0 {
					t.Fatalf("%s drew nothing", path)
				}
			})
		}
	}
}

// The settings restyle the window: a new seed, palette style or mode changes
// the colours every component draws with.
func TestSettingsChangeTheTheme(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	var shown *m3.Theme
	tt := ui.NewTester(func(c *ui.Context) {
		a.view(c)
		shown = m3.Of(c)
	}, 1000, 900)
	before := shown.Scheme.Primary
	if err := tt.Click("Rose"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Seed != "#d81b78" {
		t.Fatalf("clicking Rose chose %q", a.settings.Seed)
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	if after := shown.Scheme.Primary; after == before {
		t.Errorf("the primary colour stayed %v after choosing a new seed", after)
	}
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !shown.Dark || a.settings.Mode != "dark" {
		t.Errorf("choosing Dark left dark=%v, mode=%q", shown.Dark, a.settings.Mode)
	}
	if err := tt.Click("Vibrant"); err != nil {
		t.Fatal(err)
	}
	if a.settings.Style != int(m3.Vibrant) {
		t.Errorf("choosing Vibrant left style %d", a.settings.Style)
	}
}

func TestSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "settings.json")
	s := defaultSettings()
	s.Seed, s.Mode, s.Style, s.Dynamic, s.AutoPlay = "#00897b", "dark", int(m3.Expressive), true, false
	s.CacheSongs = 20
	s.CacheDirectory = filepath.Join(t.TempDir(), "audio files")
	s.remember("one")
	s.remember("two")
	s.remember("One")
	if err := s.save(path); err != nil {
		t.Fatal(err)
	}
	got := loadSettings(path)
	if got.Seed != s.Seed || got.Mode != "dark" || got.Style != int(m3.Expressive) || !got.Dynamic || got.AutoPlay || got.CacheSongs != s.CacheSongs || got.CacheDirectory != s.CacheDirectory {
		t.Errorf("settings came back as %+v", got)
	}
	if len(got.Recent) != 2 || got.Recent[0] != "One" {
		t.Errorf("recent searches came back as %v", got.Recent)
	}
	cfg := got.config()
	if cfg.Mode != m3.Dark || cfg.Style != m3.Expressive {
		t.Errorf("config = %+v", cfg)
	}
	if bad := loadSettings(filepath.Join(t.TempDir(), "missing.json")); bad.Seed != defaultSettings().Seed {
		t.Errorf("a missing file gave %+v", bad)
	}
	if err := os.WriteFile(path, []byte("{nonsense"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bad := loadSettings(path); bad.Seed != defaultSettings().Seed {
		t.Errorf("a broken file gave %+v", bad)
	}
}

func TestSettingsCacheLimitDefaultsAndAllowsAnyNonNegativeValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	for _, test := range []struct {
		name string
		json string
		want int
	}{
		{name: "old settings", json: `{"volume":70}`, want: defaultAudioCacheLimit},
		{name: "custom count", json: `{"cacheSongs":37}`, want: 37},
		{name: "disabled", json: `{"cacheSongs":0}`, want: 0},
		{name: "negative count", json: `{"cacheSongs":-1}`, want: defaultAudioCacheLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(test.json), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := loadSettings(path).CacheSongs; got != test.want {
				t.Errorf("cache limit = %d, want %d", got, test.want)
			}
		})
	}
}

func TestSettingsChoosePlaybackCacheLimit(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	a.cacheSongsText = ""
	a.cacheDirectoryText = "/music/cache"
	tt := ui.NewTester(a.view, 1000, 1800)
	if !tt.HasText("Playback cache") || !tt.HasText("Keep recent songs") || !tt.HasText("Songs to cache") || !tt.HasText("Cache folder") || !tt.HasText("Choose cache folder") || !tt.HasText("/music/cache") || !tt.HasText("MP3") {
		t.Fatalf("the cache setting is missing: %q", tt.Texts())
	}
	if err := tt.Click("Songs to cache"); err != nil {
		t.Fatal(err)
	}
	tt.Type("37")
	tt.Key(0, ui.KeyEnter)
	if a.settings.CacheSongs != 37 {
		t.Fatalf("entering 37 set the cache size to %d (text %q, error %q)", a.settings.CacheSongs, a.cacheSongsText, a.cacheLimitError)
	}
	a.cacheSongsText = "0"
	tt.Frame()
	if err := tt.Click("Songs to cache"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	if a.settings.CacheSongs != 0 {
		t.Errorf("setting the limit to 0 set the cache size to %d", a.settings.CacheSongs)
	}
}

func TestReplacingAudioCacheClosesCurrentAndRetiredCachesAtShutdown(t *testing.T) {
	a := newTestApp()
	first, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAudioCache(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	a.replaceAudioCache(first)
	if got := a.replaceAudioCache(second); got != first {
		t.Fatal("replacing the cache did not return the previous manager")
	}
	a.closeAudioCaches()
	for name, cache := range map[string]*audioCache{"retired": first, "current": second} {
		cache.mu.Lock()
		closed := cache.closed
		cache.mu.Unlock()
		if !closed {
			t.Errorf("%s audio cache was left open", name)
		}
	}
}

func TestOlderSettingsDefaultAutoplayToOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"seed":"#6750a4","mode":"system","volume":70}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(path); !got.AutoPlay {
		t.Errorf("older settings disabled autoplay: %+v", got)
	}
}

func TestArtworkColour(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 60, B: 200, A: 255})
		}
	}
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	got, ok := dominantColour(decoded(t, data.Bytes()))
	if !ok || got.B < 150 || got.R > 80 {
		t.Errorf("dominantColour = %v, %v; want the blue", got, ok)
	}
	grey := image.NewRGBA(image.Rect(0, 0, 8, 8))
	data.Reset()
	if err := png.Encode(&data, grey); err != nil {
		t.Fatal(err)
	}
	if _, ok := dominantColour(decoded(t, data.Bytes())); ok {
		t.Errorf("a grey picture should have no dominant colour")
	}
}

// decoded reads a picture the way the thumbnail cache does.
func decoded(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
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

func TestClockAndDuration(t *testing.T) {
	if got := clock(3*time.Minute + 42*time.Second); got != "3:42" {
		t.Errorf("clock = %q", got)
	}
	if got := clock(time.Hour + 2*time.Minute + 3*time.Second); got != "1:02:03" {
		t.Errorf("clock = %q", got)
	}
	if got := parseDuration("3:42"); got != 3*time.Minute+42*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("1:02:03"); got != time.Hour+2*time.Minute+3*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("2023"); got != 0 {
		t.Errorf("parseDuration of a year = %v", got)
	}
}

const homeResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Quick picks"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Ambient One"}]},
				"subtitle":{"runs":[{"text":"Someone"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/a=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-1"}}
			}},
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Deep Focus"}]},
				"subtitle":{"runs":[{"text":"Album"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/b=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_test"}}
			}}
		]
	}}
]}}}`

const exploreResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Moods"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Night Drive"}]},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-2"}}
			}}
		]
	}}
]}}}`

const albumResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-3"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track One"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"4:12"}]}}}
		]
	}}
]}}}`

const playlistResponse = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"playlist-video","title":{"simpleText":"Playlist video"},"lengthText":{"simpleText":"5:21"}}}
]}}}`

const searchResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-4"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Search Result Song"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
		]
	}}
]}}}`

const recapResponse = `{"contents":{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Your recap"}]}}},
	"contents":[
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Top song of the year"}]},
			"subtitle":{"runs":[{"text":"Someone"}]},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-9"}}
		}}
	]
}}}`

const accountsResponse = `{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
	{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"channelId":"UC-main","isSelected":true,"hasChannel":true}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Brand channel"},"channelId":"UC-brand","hasChannel":true}}
]}}]}}`

const suggestionsResponse = `{"contents":[{"searchSuggestionsSectionRenderer":{"contents":[
	{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika"}]}}},
	{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika songs"}]}}}
]}}]}`

func TestCookieHeader(t *testing.T) {
	const want = "SAPISID=abc; SID=def"
	for name, in := range map[string]string{
		"value":        want,
		"padded":       "  " + want + "\n",
		"header":       "Cookie: " + want,
		"lowercase":    "cookie: " + want,
		"curl":         "curl 'https://music.youtube.com/youtubei/v1/browse' -H 'cookie: " + want + "' -H 'origin: x'",
		"curl doubled": `curl "https://music.youtube.com" -H "cookie: ` + want + `" --compressed`,
	} {
		if got := cookieHeader(in); got != want {
			t.Errorf("%s: cookieHeader = %q, want %q", name, got, want)
		}
	}
}

func TestCookieFromNetscape(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	text := strings.Join([]string{
		"# Netscape HTTP Cookie File",
		"",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tabc",
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t2100000000\t__Secure-3PSID\tdef",
		"music.youtube.com\tFALSE\t/\tTRUE\t0\tYSC\tsession",
		".youtube.com\tTRUE\t/\tTRUE\t1000000000\tOLD\texpired",
		".google.com\tTRUE\t/\tTRUE\t2100000000\tSID\tgoogle",
		".notyoutube.com\tTRUE\t/\tTRUE\t2100000000\tEVIL\tx",
		".youtube.com\tTRUE\t/\tTRUE\t2100000000\tSAPISID\tnewer",
		"broken line",
	}, "\n")
	got := cookieFromNetscape(text, now)
	want := "SAPISID=newer; __Secure-3PSID=def; YSC=session"
	if got != want {
		t.Errorf("cookieFromNetscape = %q, want %q", got, want)
	}
	if got := cookieFromNetscape("# nothing\n", now); got != "" {
		t.Errorf("an empty file gave %q", got)
	}
}

func TestChromiumProfiles(t *testing.T) {
	dir := t.TempDir()
	for _, database := range []string{"Default/Cookies", "Profile 3/Network/Cookies", "Profile 1/Preferences", "Crashpad/Cookies"} {
		path := filepath.Join(dir, filepath.FromSlash(database))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := chromiumProfiles(dir)
	want := []string{filepath.Join(dir, "Default"), filepath.Join(dir, "Profile 3")}
	if !slices.Equal(got, want) {
		t.Errorf("chromiumProfiles = %q, want %q", got, want)
	}
	if got := chromiumProfiles(filepath.Join(dir, "missing")); got != nil {
		t.Errorf("a missing directory gave %q", got)
	}
	if got, want := profileSelectors("chrome", chromiumProfiles(dir), true), []string{
		"chrome:" + filepath.Join(dir, "Default"),
		"chrome:" + filepath.Join(dir, "Profile 3"),
		"chrome",
	}; !slices.Equal(got, want) {
		t.Errorf("Chrome selectors = %q, want %q", got, want)
	}
}

func TestFirefoxProfiles(t *testing.T) {
	dir := t.TempDir()
	for _, database := range []string{"primary.default/cookies.sqlite", "secondary.default-release/cookies.sqlite"} {
		path := filepath.Join(dir, filepath.FromSlash(database))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	profiles := firefoxProfiles([]string{dir})
	want := []string{filepath.Join(dir, "primary.default"), filepath.Join(dir, "secondary.default-release")}
	if !slices.Equal(profiles, want) {
		t.Errorf("firefoxProfiles() = %q, want %q", profiles, want)
	}
	if got, want := profileSelectors("firefox", profiles, true), []string{
		"firefox:" + filepath.Join(dir, "primary.default"),
		"firefox:" + filepath.Join(dir, "secondary.default-release"),
		"firefox",
	}; !slices.Equal(got, want) {
		t.Errorf("Firefox selectors = %q, want %q", got, want)
	}
}

func TestZenProfileRoots(t *testing.T) {
	home, config := "/home/mei", "/home/mei/.config"
	got := zenProfileRoots(home, config, "linux")
	want := []string{
		filepath.Join(home, ".zen"),
		filepath.Join(home, ".var", "app", "app.zen_browser.zen", "zen"),
		filepath.Join(config, "zen"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("zenProfileRoots() = %q, want %q", got, want)
	}
}

func TestSignInDialogOffersBrowsersInADropdown(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	a.signInWithGoogle()
	tt.Frame()
	if !tt.HasText("Import from a browser") {
		t.Fatalf("the sign-in dialog has no import button: %q", tt.Texts())
	}
	if tt.HasText("Firefox") {
		t.Errorf("the browsers are listed before the dropdown opens: %q", tt.Texts())
	}
	if err := tt.Click("Import from a browser"); err != nil {
		t.Fatal(err)
	}
	for _, browser := range []string{"Chrome", "Safari", "Firefox", "Brave", "Edge", "Zen"} {
		if !tt.HasText(browser) {
			t.Errorf("the dropdown is missing %s: %q", browser, tt.Texts())
		}
	}
}

func TestPlayingShowsLoadingAndIgnoresASecondPress(t *testing.T) {
	a := newTestApp()
	a.run = func(work func()) {} // the audio never resolves
	item := youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One", Duration: "3:33"}

	a.play(item, []youtube.MusicItem{item}, 0)
	if !a.loading() || a.streamGen != 1 {
		t.Fatalf("playing did not start loading: loading=%v, gen=%d", a.loading(), a.streamGen)
	}
	tt := ui.NewTester(a.view, 1000, 700)
	if _, ok := tt.Find("Loading"); !ok {
		t.Errorf("the play button does not show it is loading: %q", tt.Texts())
	}

	// Pressing play again, in a row or on the button, must not ask twice.
	a.play(item, []youtube.MusicItem{item}, 0)
	a.togglePlay()
	if a.streamGen != 1 {
		t.Errorf("a second press started another request: gen=%d", a.streamGen)
	}

	// Choosing another track supersedes the first one.
	other := youtube.MusicItem{VideoID: "vid-2", Title: "Ambient Two"}
	a.play(other, []youtube.MusicItem{item, other}, 1)
	if a.streamGen != 2 || !a.loading() {
		t.Errorf("the second track did not start: gen=%d, loading=%v", a.streamGen, a.loading())
	}
}

// The queue is the tracks the user chose to play from, not the rows of
// whatever page is drawn next.
func TestQueueSurvivesTheNextPageOfRows(t *testing.T) {
	a := newTestApp()
	a.run = func(work func()) {}
	song := func(id string) youtube.MusicItem { return youtube.MusicItem{VideoID: id, ID: id, Title: id} }
	a.router.Push("/search")
	a.search.submitted = "x"
	a.search.items = []youtube.MusicItem{song("A"), song("B"), song("C")}
	a.setRows()
	a.play(a.playable[0], a.playable, 0)

	a.search.items = []youtube.MusicItem{song("X"), song("Y"), song("Z")}
	a.setRows()
	if got := a.queue[0].VideoID + a.queue[1].VideoID + a.queue[2].VideoID; got != "ABC" {
		t.Errorf("the queue became %q after the next page was drawn", got)
	}
}

func TestAutoplayAppendsAndContinuesWithoutDuplicatingTheSelectedQueue(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Errorf("request path = %q, want /next", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "seed" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(1) {
				t.Errorf("initial up-next request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"RDAMVMseed","contents":[{"playlistPanelVideoRenderer":{"videoId":"playlist-next","title":{"simpleText":"Playlist next"}}},{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		case 2:
			if request["videoId"] != "seed" || request["playlistId"] != "RDAMVMseed" || request["playlistIndex"] != nil || request["continuation"] != "RADIO_MORE" {
				t.Errorf("radio continuation request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"playlistId":"RDAMVMseed","contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}},{"playlistPanelVideoRenderer":{"videoId":"radio-2","title":{"simpleText":"Radio two"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_END"}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := youtube.NewClient(youtube.Options{BaseURL: server.URL, APIKey: "test"})
	a := newTestApp()
	a.public, a.authed = client, client
	a.run = func(work func()) { work() }
	index := 1
	a.current = youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.queue = []youtube.MusicItem{{VideoID: "seed"}, {VideoID: "playlist-next"}}
	a.index = 0
	a.resetUpNext(youtube.UpNextOptions{VideoID: "seed", PlaylistID: "PL123", PlaylistIndex: &index}, "Playlist")

	a.ensureUpNext()
	if len(a.queue) != 3 || a.recommendationStart != 2 || a.queue[2].VideoID != "radio-1" || a.upNextToken != "RADIO_MORE" {
		t.Fatalf("initial up-next queue = %#v, recommendation start %d, token %q", a.queue, a.recommendationStart, a.upNextToken)
	}
	if a.queueSource != "Playlist" || a.upNextOptions.PlaylistID != "RDAMVMseed" || a.upNextOptions.PlaylistIndex != nil {
		t.Errorf("resolved queue context = source %q, options %+v", a.queueSource, a.upNextOptions)
	}

	a.index = 1 // two tracks remain, so prefetch the radio continuation.
	a.ensureUpNext()
	if len(a.queue) != 4 || a.queue[3].VideoID != "radio-2" || a.upNextToken != "RADIO_END" || nextRequests.Load() != 2 {
		t.Fatalf("continued queue = %#v with token %q after %d requests", a.queue, a.upNextToken, nextRequests.Load())
	}
	a.setAutoPlay(false)
	if _, ok := a.nextIndex(); ok {
		t.Errorf("autoplay-off advanced into generated recommendations")
	}
}

func TestQueueEndWaitsForInFlightRecommendation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}}]}}}`))
	}))
	defer server.Close()
	client := youtube.NewClient(youtube.Options{BaseURL: server.URL, APIKey: "test"})
	a := newTestApp()
	a.public, a.authed = client, client
	var pending func()
	a.run = func(work func()) { pending = work }
	current := youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.current, a.queue = current, []youtube.MusicItem{current}
	a.resetUpNext(youtube.UpNextOptions{VideoID: current.VideoID}, "")
	a.ensureUpNext()
	if pending == nil || !a.upNextLoading {
		t.Fatal("the initial recommendation request did not start")
	}
	a.advance()
	if !a.waitingForAuto {
		t.Fatal("playback did not wait for the in-flight recommendation request")
	}
	pending()
	if a.current.VideoID != "radio-1" || a.index != 1 || a.waitingForAuto {
		t.Errorf("queue end left current=%q, index=%d, waiting=%v", a.current.VideoID, a.index, a.waitingForAuto)
	}
}

func TestUpNextFailureKeepsTheSelectedQueue(t *testing.T) {
	client := youtube.NewClient(youtube.Options{
		APIKey:     "test",
		HTTPClient: &http.Client{Transport: failingMusic{}},
	})
	a := newTestApp()
	a.public, a.authed = client, client
	a.run = func(work func()) { work() }
	a.current = youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.queue = []youtube.MusicItem{{VideoID: "seed"}, {VideoID: "chosen", Title: "Chosen next"}}
	a.resetUpNext(youtube.UpNextOptions{VideoID: "seed"}, "")
	a.ensureUpNext()
	if len(a.queue) != 2 || a.queue[1].VideoID != "chosen" || a.upNextErr == "" || a.playErr != "" {
		t.Errorf("failed recommendation request changed playback state: queue=%#v error=%q play error=%q", a.queue, a.upNextErr, a.playErr)
	}
}

// failingMusic answers every request with a server error.
type failingMusic struct{}

func (failingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("down")),
		Request:    request,
	}, nil
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

// Saves asked for in a burst, while the list they hold is edited, must not
// race, and the last one must be what ends up on disk.
func TestSettingsSavesAreOrderedAndIndependent(t *testing.T) {
	dir := t.TempDir()
	a := newTestApp()
	a.settingsPath = filepath.Join(dir, "settings.json")
	for i := range 200 {
		a.settings.Recent = []string{"a", "b", "c", "d", "e"}
		a.settings.Volume = float64(i % 100)
		a.saveSettings()
		a.forget("a")
		a.settings.remember("last")
	}
	a.settings.Volume = 42
	a.saveSettings()
	a.saver.wait()

	got := loadSettings(a.settingsPath)
	if got.Volume != 42 || len(got.Recent) == 0 || got.Recent[0] != "last" {
		t.Errorf("the last save was not the one kept: volume %v, recent %v", got.Volume, got.Recent)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("saving left %d files behind, want only the settings", len(entries))
	}
}

func TestShutdownFlushesPendingSettings(t *testing.T) {
	a := newTestApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	started := make(chan struct{})
	release := make(chan struct{})
	a.saver.write = func(s *settings, path string) error {
		close(started)
		<-release
		return s.save(path)
	}
	a.settings.Volume = 13
	a.saveSettings()
	<-started

	done := make(chan struct{})
	go func() {
		a.shutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("shutdown returned before the pending settings save completed")
	case <-time.After(time.Second):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after the settings save completed")
	}

	if got := loadSettings(a.settingsPath); got.Volume != 13 {
		t.Errorf("saved volume = %v, want 13", got.Volume)
	}
}

func TestShutdownReportsSettingsSaveErrorsWithoutBlocking(t *testing.T) {
	a := newTestApp()
	a.settingsPath = filepath.Join(t.TempDir(), "settings.json")
	writeErr := errors.New("disk is read-only")
	var logs bytes.Buffer
	oldOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(oldOutput) })
	a.saver.write = func(*settings, string) error { return writeErr }
	a.saveSettings()
	done := make(chan struct{})
	go func() {
		a.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked after the settings save failed")
	}
	if !strings.Contains(logs.String(), writeErr.Error()) {
		t.Errorf("settings save failure was not logged: %q", logs.String())
	}
}

func TestParseSeedIsStrict(t *testing.T) {
	if c, ok := parseSeed("#6750a4"); !ok || c != ui.RGB(0x67, 0x50, 0xa4) {
		t.Errorf("a good seed gave %v, %v", c, ok)
	}
	for _, bad := range []string{"", "6750a4", "#6750a", "#6750a4f", "#1 2 3 ", "#+1+1+1", "#0x1234", "#gggggg"} {
		if _, ok := parseSeed(bad); ok {
			t.Errorf("%q was taken as a seed", bad)
		}
	}
}

// memoryStore is a cookie store held in memory.
type memoryStore struct {
	cookie    string
	deleteErr error
	onSave    func()
}

func (m *memoryStore) Load(context.Context) (string, error) {
	if m.cookie == "" {
		return "", errNotSignedIn
	}
	return m.cookie, nil
}

func (m *memoryStore) Save(_ context.Context, cookie string) error {
	m.cookie = cookie
	if m.onSave != nil {
		m.onSave()
	}
	return nil
}

func (m *memoryStore) Delete(context.Context) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.cookie = ""
	return nil
}

// The session saved by an earlier run is read in the background. A sign-out
// that happens meanwhile must not be undone when it lands.
func TestSignOutIsNotUndoneByARestoreInFlight(t *testing.T) {
	a := newTestApp()
	a.newClient = func(*youtube.CookieAuth) *youtube.Client { return a.public }
	a.store = &memoryStore{cookie: "SAPISID=abc"}
	a.signedIn, a.authed = false, nil
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }

	a.restoreAccount()
	a.signOut()
	for _, work := range pending[:1] {
		work()
	}
	if a.signedIn {
		t.Error("a restore that began before the sign-out signed the user back in")
	}
}

func TestRestoreDoesNotMarkAnInvalidSessionSignedIn(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=abc"}
	a.store = store
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: failingMusic{}},
		})
	}
	a.run = func(work func()) { work() }

	a.restoreAccount()
	if a.signedIn || a.authed != nil {
		t.Fatalf("failed restore left signedIn=%v client=%v", a.signedIn, a.authed)
	}
	if !strings.Contains(a.signIn.err, "Could not restore") || !strings.Contains(a.notice, "HTTP 500: down") {
		t.Errorf("restore failure was not surfaced: sign-in error %q, notice %q", a.signIn.err, a.notice)
	}
	if store.cookie == "" {
		t.Fatal("a transient restore failure discarded the saved cookie")
	}
}

func TestRestoreSurfacesAnInvalidSavedCookie(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SID=not-enough"}
	a.store = store
	a.run = func(work func()) { work() }

	a.restoreAccount()
	if a.signedIn || a.authed != nil {
		t.Fatalf("invalid saved cookie left signedIn=%v client=%v", a.signedIn, a.authed)
	}
	if !strings.Contains(a.notice, "Could not restore") || !strings.Contains(a.notice, "SAPISID") {
		t.Errorf("invalid saved cookie failure was not surfaced: %q", a.notice)
	}
	if store.cookie == "" {
		t.Fatal("failed restore discarded the saved cookie")
	}
}

func TestCancelledSignInDoesNotLeaveItsCookieSaved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"actions":[{"openPopupAction":{"popup":{"multiPageMenuRenderer":{"header":{"activeAccountHeaderRenderer":{"accountName":{"runs":[{"text":"Me"}]}}}}}}}]}`))
	}))
	defer server.Close()

	a := newTestApp()
	store := &memoryStore{}
	a.store = store
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			BaseURL:                 server.URL,
			APIKey:                  "test",
			CookieAuth:              auth,
			AllowInsecureCookieAuth: true,
		})
	}
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }
	a.signInWithGoogle()
	a.signIn.cookie = "SAPISID=abc"
	attempt := a.signIn.generation
	store.onSave = a.dismissSignIn

	a.finishSignIn(context.Background(), "SAPISID=abc", attempt, "")
	if a.signedIn || store.cookie != "" || a.signIn.cookie != "" {
		t.Fatalf("cancelled sign-in was retained: signedIn=%v cookieSaved=%v dialogCookie=%q", a.signedIn, store.cookie != "", a.signIn.cookie)
	}
	for _, work := range pending {
		work()
	}
}

func TestSignOutSaysWhenTheSessionStays(t *testing.T) {
	a := newTestApp()
	a.store = &memoryStore{cookie: "SAPISID=abc", deleteErr: errors.New("the keychain is locked")}
	a.signedIn = true
	a.ytDlpCookie = "SAPISID=abc"
	a.signOut()
	if a.signedIn || a.ytDlpCookie != "" {
		t.Errorf("sign-out retained authentication: signedIn=%v yt-dlp cookie present=%v", a.signedIn, a.ytDlpCookie != "")
	}
	if !strings.Contains(a.notice, "keychain is locked") {
		t.Errorf("the failure was not reported: %q", a.notice)
	}
}

func TestSignInWaitsForSignOutStorageDeletion(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	a.store = &memoryStore{cookie: "SAPISID=abc"}
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }

	a.signOut()
	if !a.credentialDeletePending {
		t.Fatal("sign-out did not mark credential deletion pending")
	}
	a.signInWithGoogle()
	if a.signIn.open {
		t.Fatal("a new sign-in opened before sign-out deleted the old credential")
	}
	if len(pending) != 1 {
		t.Fatalf("queued %d jobs, want only credential deletion", len(pending))
	}
	pending[0]()
	if a.credentialDeletePending {
		t.Fatal("credential deletion remained pending after it finished")
	}
	a.signInWithGoogle()
	if !a.signIn.open {
		t.Fatal("sign-in did not open after credential deletion finished")
	}
}

func TestDeletingReportsWhatWouldNotGo(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cookie.txt")
	system := &fakeKeychain{usable: true, holds: true, removeErr: errors.New("access denied")}
	store := &keychainStore{system: system, file: newFileStore(path)}
	if err := store.file.Save(ctx, testCookie); err != nil {
		t.Fatal(err)
	}
	err := store.Delete(ctx)
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Errorf("Delete = %v, want the keychain's failure", err)
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Error("the file was left behind because the keychain failed")
	}
}

func TestCookieHeaderSurvivesNonASCIIText(t *testing.T) {
	// "İ" is two bytes, and three once lowercased.
	if got := cookieHeader("İİİİİİİİİİ Cookie: SAPISID=abc"); got != "SAPISID=abc" {
		t.Errorf("cookieHeader = %q", got)
	}
}

func TestShuffleOfTwoTracksAlwaysPicksTheOther(t *testing.T) {
	a := newTestApp()
	a.shuffle = true
	a.queue = []youtube.MusicItem{{VideoID: "a"}, {VideoID: "b"}}
	for _, index := range []int{0, 1} {
		a.index = index
		for range 50 {
			if i, ok := a.nextIndex(); !ok || i != 1-index {
				t.Fatalf("from %d, shuffle chose %d, %v", index, i, ok)
			}
		}
	}
	// A stale index past the end still yields a track in the queue.
	a.index = 7
	for range 50 {
		if i, ok := a.nextIndex(); !ok || i < 0 || i >= len(a.queue) {
			t.Fatalf("from a stale index, shuffle chose %d, %v", i, ok)
		}
	}
}

func TestRepeatCyclesThroughItsModes(t *testing.T) {
	a := newTestApp()
	var seen []repeatMode
	for range 4 {
		a.cycleRepeat()
		seen = append(seen, a.repeat)
	}
	if want := []repeatMode{repeatQueue, repeatTrack, repeatOff, repeatQueue}; !slices.Equal(seen, want) {
		t.Errorf("repeat went %v, want %v", seen, want)
	}
}
