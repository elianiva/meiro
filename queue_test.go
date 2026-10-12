package main

import (
	"encoding/json"
	"errors"
	"image"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/systemmedia"
	"github.com/elianiva/meiro/youtube"
)

type captureSystemMedia struct {
	state  systemmedia.State
	err    error
	closed bool
}

func (s *captureSystemMedia) Update(state systemmedia.State) error {
	s.state = state
	return s.err
}

func (s *captureSystemMedia) Close() error {
	s.closed = true
	return nil
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
	controls.Seek(100 * time.Millisecond)
	if media.state.SeekVersion != 1 {
		t.Errorf("explicit seek version = %d, want 1", media.state.SeekVersion)
	}
	a.syncSystemMedia()
	if media.state.SeekVersion != 1 {
		t.Error("ordinary state publication was reported as a seek")
	}
	a.previous()
	if media.state.SeekVersion != 2 {
		t.Errorf("restart seek version = %d, want 2", media.state.SeekVersion)
	}
	a.resolving = true
	controls.Stop()
	if a.resolving || media.state.Status != systemmedia.Stopped {
		t.Errorf("media Stop left playback state resolving=%v status=%q", a.resolving, media.state.Status)
	}
}

func TestSystemMediaFailureClosesAndDisablesTheSession(t *testing.T) {
	a := newTestApp()
	media := &captureSystemMedia{err: errors.New("D-Bus connection closed")}
	a.systemMedia = media

	a.syncSystemMedia()

	if !media.closed || a.systemMedia != nil {
		t.Fatalf("failed system media session remained active: closed=%t session=%T", media.closed, a.systemMedia)
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
	a.repeat, a.index = repeatOff, 0
	a.setShuffle(true)
	if i, ok := a.nextIndex(); !ok || i != 1 {
		t.Errorf("a shuffled queue went to %d, %v instead of the track after it", i, ok)
	}
	a.index = 2
	if _, ok := a.nextIndex(); ok {
		t.Errorf("a shuffled queue did not end at its last track")
	}
	a.repeat = repeatQueue
	if i, ok := a.nextIndex(); !ok || i != 0 {
		t.Errorf("repeating a shuffled queue went to %d, %v", i, ok)
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

// The end of the queue asks YouTube for more even when the recommendations
// pulled before it have been played out. It used to stop there, leaving
// shuffle and auto-play with nothing to play after the queue.
func TestTheQueueEndAsksAgainWhenItsRecommendationsArePlayedOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio one"}}}]}}}`))
	}))
	defer server.Close()
	client := youtube.NewClient(youtube.Options{BaseURL: server.URL, APIKey: "test"})
	a := newTestApp()
	a.public, a.authed = client, client
	a.settings.AutoPlay = true
	current := youtube.MusicItem{VideoID: "seed", Title: "Seed"}
	a.current, a.queue = current, []youtube.MusicItem{current}
	a.index = 0
	a.resetUpNext(youtube.UpNextOptions{VideoID: current.VideoID}, "")
	// What is left once the recommendations pulled earlier have been played.
	a.upNextFetched, a.upNextToken = true, ""

	a.advance()
	if len(a.queue) != 2 || a.queue[1].VideoID != "radio-1" {
		t.Fatalf("the queue end pulled %#v, want the recommendation after it", a.queue)
	}
	if a.index != 1 || a.current.VideoID != "radio-1" {
		t.Errorf("the queue end left current=%q at index %d", a.current.VideoID, a.index)
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

// Shuffle arranges what is still to play, once: the queue then moves from each
// track to the next and ends, which is where auto-play takes over. Drawing a
// track at random as it played never reached the end.
func TestShuffleArrangesTheQueueOnceAndThenPlaysItInOrder(t *testing.T) {
	a := newTestApp()
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		a.queue = append(a.queue, youtube.MusicItem{VideoID: id})
	}
	a.current, a.index = a.queue[0], 0
	original := make([]string, 0, len(a.queue))
	for _, item := range a.queue {
		original = append(original, item.VideoID)
	}
	a.setShuffle(true)

	var played []string
	for {
		next, ok := a.nextIndex()
		if !ok {
			break
		}
		if next != a.index+1 {
			t.Fatalf("the shuffled queue moved from %d to %d", a.index, next)
		}
		a.index = next
		played = append(played, a.queue[next].VideoID)
	}
	if len(played) != len(a.queue)-1 {
		t.Fatalf("shuffle played %d tracks, want the %d still to play", len(played), len(a.queue)-1)
	}
	seen := map[string]bool{}
	for _, id := range played {
		if seen[id] {
			t.Errorf("shuffle played %q twice", id)
		}
		seen[id] = true
	}
	if slices.Equal(played, original[1:]) {
		t.Errorf("shuffle left the queue in the order it was given: %v", played)
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

// Shuffle plays the page from a track drawn at random, with the whole page in
// the queue behind it, arranged in an order of its own.
func TestShufflePlaysThePageFromARandomTrack(t *testing.T) {
	a := newTestApp()
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		a.playable = append(a.playable, youtube.MusicItem{VideoID: id})
	}
	opened, orders := map[string]bool{}, map[string]bool{}
	for range 200 {
		a.shufflePage()
		if len(a.queue) < len(a.playable) {
			t.Fatalf("the queue holds %d of the page's %d tracks", len(a.queue), len(a.playable))
		}
		held := make([]string, 0, len(a.playable))
		for _, item := range a.queue[:len(a.playable)] {
			held = append(held, item.VideoID)
		}
		orders[strings.Join(held, ",")] = true
		opened[a.queue[a.index].VideoID] = true
	}
	if len(opened) < 2 {
		t.Errorf("shuffle always opened on %v", opened)
	}
	if len(orders) < 2 {
		t.Errorf("shuffle left the page in one order: %v", orders)
	}
}
