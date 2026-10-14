package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/youtube"
)

// resolved records the tracks the app asked its stream resolver for, in place
// of yt-dlp.
type resolved struct {
	mu  sync.Mutex
	ids []string
	err map[string]error
	// file, when set, is the audio every track resolves to.
	file string
	// url, when set, chooses the URL of a track's nth resolve (from 1), in
	// place of file, and may fail it.
	url func(videoID string, n int) (string, error)
}

func (r *resolved) resolve(_ context.Context, videoID, _ string) (string, time.Duration, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, videoID)
	if err := r.err[videoID]; err != nil {
		return "", 0, err
	}
	if r.url != nil {
		n := 0
		for _, id := range r.ids {
			if id == videoID {
				n++
			}
		}
		url, err := r.url(videoID, n)
		return url, 3 * time.Minute, err
	}
	if r.file != "" {
		return r.file, 3 * time.Second, nil
	}
	return "/nonexistent/" + videoID + ".m4a", 3 * time.Minute, nil
}

// asked reports whether the resolver was asked for videoID. The app also
// resolves the next track ahead of time, so the last ask is not the track
// playing.
func (r *resolved) count(videoID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, id := range r.ids {
		if id == videoID {
			n++
		}
	}
	return n
}

func (r *resolved) asked(videoID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.ids {
		if id == videoID {
			return true
		}
	}
	return false
}

// playbackApp is an app whose tracks resolve to fake files, for the tests of
// what the app decides. TestPlayingATrackThroughToTheEnd plays real audio.
func playbackApp(t *testing.T) (*app, *resolved) {
	t.Helper()
	a := newTestApp()
	a.settings.AutoPlay = false
	r := &resolved{err: map[string]error{}}
	a.streams = audio.NewURLs(r.resolve)
	a.replaceAudioCache(nil)
	t.Cleanup(a.player.Stop)
	return a, r
}

func tracks(ids ...string) []youtube.MusicItem {
	items := make([]youtube.MusicItem, len(ids))
	for i, id := range ids {
		items[i] = youtube.MusicItem{VideoID: id, Title: "Track " + id}
	}
	return items
}

func ids(items []youtube.MusicItem) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = item.VideoID
	}
	return out
}

func TestClickingATrackPlaysIt(t *testing.T) {
	a, r := playbackApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "vid-3" || !r.asked("vid-3") {
		t.Fatalf("current %q, resolver asked for vid-3: %v", a.current.VideoID, r.asked("vid-3"))
	}
	if a.total != 3*time.Minute {
		t.Errorf("the track length was %v, want the resolver's 3m", a.total)
	}
	if !tt.HasText("Track") && !tt.HasText("Album Track One") {
		t.Errorf("the player bar does not show the track: %q", tt.Texts())
	}
}

func TestQueueAdvancesPreviousAndStops(t *testing.T) {
	a, r := playbackApp(t)
	q := tracks("a", "b", "c")
	a.play(q[0], q)

	a.trackEnded()
	if a.current.VideoID != "b" || !r.asked("b") {
		t.Fatalf("after one track current=%q", a.current.VideoID)
	}
	a.trackEnded()
	a.previous() // just started: goes back a track
	if a.current.VideoID != "b" || a.queue.index != 1 {
		t.Fatalf("previous left index %d on %q", a.queue.index, a.current.VideoID)
	}
	a.trackEnded()
	if a.current.VideoID != "c" {
		t.Fatalf("current = %q", a.current.VideoID)
	}
	a.trackEnded() // the end of the queue, nothing repeats
	if a.current.VideoID != "c" || a.loading() {
		t.Errorf("the queue end: current=%q loading=%v", a.current.VideoID, a.loading())
	}
}

func TestRepeatModes(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b")
	a.play(q[0], q)

	a.cycleRepeat() // queue
	a.trackEnded()
	a.trackEnded()
	if a.current.VideoID != "a" {
		t.Errorf("repeating the queue ended on %q, want a", a.current.VideoID)
	}
	a.cycleRepeat() // track
	before := a.stream.gen
	a.trackEnded()
	if a.current.VideoID != "a" || a.stream.gen == before {
		t.Errorf("repeating the track: current=%q, restarted=%v", a.current.VideoID, a.stream.gen != before)
	}
	a.cycleRepeat() // off
	if a.repeat != repeatOff {
		t.Errorf("a third press left repeat %d", a.repeat)
	}
}

func TestShuffleKeepsTheTrackPlayingAndEveryTrack(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d", "e", "f", "g", "h")
	a.play(q[0], q)
	a.setShuffle(true)
	if a.queue.items[0].VideoID != "a" || a.queue.index != 0 {
		t.Errorf("shuffle moved the track playing: index %d", a.queue.index)
	}
	if a.queue.len() != len(q) {
		t.Fatalf("shuffle changed the queue length: %v", ids(a.queue.items))
	}
	seen := map[string]bool{}
	for _, id := range ids(a.queue.items) {
		seen[id] = true
	}
	if len(seen) != len(q) {
		t.Errorf("shuffle lost or repeated tracks: %v", ids(a.queue.items))
	}
}

func TestQueueingATrack(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	extra := tracks("x", "y")

	a.enqueue(extra[0], false)
	a.enqueue(extra[1], true)
	if got := ids(a.queue.items); len(got) != 5 || got[1] != "y" || got[4] != "x" {
		t.Fatalf("queue = %v, want y next and x last", got)
	}
	a.trackEnded()
	if a.current.VideoID != "y" {
		t.Errorf("the track queued to play next played %q", a.current.VideoID)
	}
}

func TestEnqueueOnAnIdleAppStartsPlaying(t *testing.T) {
	a, r := playbackApp(t)
	a.enqueue(tracks("solo")[0], false)
	if a.current.VideoID != "solo" || !r.asked("solo") {
		t.Errorf("current=%q, asked=%v", a.current.VideoID, r.asked("solo"))
	}
}

func TestAnUnplayableTrackSaysWhyAndRetries(t *testing.T) {
	a, r := playbackApp(t)
	r.err["bad"] = errors.New("yt-dlp could not resolve the audio: Video unavailable")
	a.play(tracks("bad")[0], nil)
	if a.playErr != "This track is no longer available." || a.loading() {
		t.Fatalf("playErr=%q loading=%v", a.playErr, a.loading())
	}
	// Pressing play asks for the same track again.
	delete(r.err, "bad")
	a.togglePlay()
	if r.count("bad") != 2 || a.playErr != "" {
		t.Errorf("play did not resolve the track again: %d resolves, playErr %q", r.count("bad"), a.playErr)
	}
}

// Each way a track fails says why in the player bar, in words, not as what
// yt-dlp printed.
func TestAnUnplayableTrackShowsAReadableReason(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{"yt-dlp could not resolve the audio: Video unavailable. This video is not available in your country", "This track is not available in your region."},
		{"yt-dlp could not resolve the audio: Sign in to confirm your age. This video may be inappropriate for some users.", "This track is age-restricted. Sign in to play it."},
		{"yt-dlp could not resolve the audio: Sign in to confirm you\u2019re not a bot", "YouTube wants you to sign in before it plays this track."},
		{"yt-dlp could not resolve the audio: Private video. Sign in if you've been granted access", "This track is private."},
		{"yt-dlp could not resolve the audio: This video has been removed by the uploader", "This track is no longer available."},
		{"yt-dlp could not resolve the audio: Unable to download webpage: Temporary failure in name resolution", "Could not reach YouTube. Check your connection."},
		{"context deadline exceeded", "Could not reach YouTube. Check your connection."},
		{"yt-dlp could not resolve the audio: something new", "This track could not be played: yt-dlp could not resolve the audio: something new"},
	} {
		a, r := playbackApp(t)
		r.err["bad"] = errors.New(tc.raw)
		tt := ui.NewTester(a.view, 1000, 700)
		a.play(tracks("bad")[0], nil)
		tt.Frame()
		if a.playErr != tc.want {
			t.Errorf("%q: playErr = %q, want %q", tc.raw, a.playErr, tc.want)
		}
		if !tt.HasText(tc.want) {
			t.Errorf("%q: the player bar does not show %q: %q", tc.raw, tc.want, tt.Texts())
		}
		if a.rawPlayErr != tc.raw {
			t.Errorf("the raw error was not kept for the log: %q", a.rawPlayErr)
		}
	}
}

func TestAnUnplayableTrackIsSkipped(t *testing.T) {
	a, r := playbackApp(t)
	r.err["b"] = errors.New("Video unavailable")
	q := tracks("a", "b", "c")
	a.play(q[1], q)
	if a.current.VideoID != "c" || a.playErr != "" {
		t.Fatalf("current %q, playErr %q: want the track after the unplayable one playing", a.current.VideoID, a.playErr)
	}
	if !strings.Contains(a.notice, "Skipped Track b") {
		t.Errorf("notice = %q, want it to say what was skipped", a.notice)
	}
}

// A run of failures, as a network that is down makes, stops at the limit with
// the error shown, and a track the listener chooses starts the count again.
func TestSkippingStopsAfterAFewFailuresInARow(t *testing.T) {
	a, r := playbackApp(t)
	q := tracks("a", "b", "c", "d", "e", "f", "g")
	for _, id := range ids(q)[:6] {
		r.err[id] = errors.New("Temporary failure in name resolution")
	}
	a.play(q[0], q)
	if want := q[maxSkips].VideoID; a.current.VideoID != want {
		t.Fatalf("current = %q, want %q: the first %d failures are skipped and the next stays", a.current.VideoID, want, maxSkips)
	}
	if a.playErr != "Could not reach YouTube. Check your connection." {
		t.Errorf("playErr = %q, want the error shown once the limit is reached", a.playErr)
	}
	if r.asked("e") {
		t.Error("the queue went on past the limit")
	}
	a.playVideoID("e") // chosen by the listener
	if a.current.VideoID != "g" {
		t.Errorf("current = %q, want a fresh run of skips to reach g", a.current.VideoID)
	}
}

func TestAPlayedTrackEndsTheRunOfSkips(t *testing.T) {
	a, r := playbackApp(t)
	q := tracks("a", "b", "c", "d", "e", "f")
	for _, id := range []string{"a", "b", "d", "e"} {
		r.err[id] = errors.New("Video unavailable")
	}
	a.play(q[0], q)
	// a, b skipped, c plays; the two failures after it are a new run.
	a.advance()
	if a.current.VideoID != "f" || a.playErr != "" {
		t.Errorf("current %q, playErr %q: want f playing after c", a.current.VideoID, a.playErr)
	}
}

func TestVolumeAndMutePersistToSettings(t *testing.T) {
	a, _ := playbackApp(t)
	a.setVolume(30)
	if a.settings.Volume != 30 || a.player.Volume() != 0.3 {
		t.Errorf("volume: settings %v, player %v", a.settings.Volume, a.player.Volume())
	}
	a.toggleMute()
	if a.player.Volume() != 0 {
		t.Errorf("mute left the player at %v", a.player.Volume())
	}
	a.toggleMute()
	if a.settings.Volume != 40 { // unmuting never comes back quieter than 40
		t.Errorf("unmuting restored %v, want 40", a.settings.Volume)
	}
	a.setVolume(500)
	if a.settings.Volume != 100 {
		t.Errorf("volume is not clamped: %v", a.settings.Volume)
	}
}

type playbackRoundTrip func(*http.Request) (*http.Response, error)

func (roundTrip playbackRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestAutoPlayFetchesAndQueuesRecommendations(t *testing.T) {
	var requests atomic.Int32
	client := youtube.NewClient(youtube.Options{
		HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"recommended","title":{"runs":[{"text":"Recommended track"}]}}}]}}}`)),
				Request:    request,
			}, nil
		})},
	})
	a, _ := playbackApp(t)
	a.account.public = client
	a.settings.AutoPlay = true
	q := tracks("a")
	a.play(q[0], q)

	if requests.Load() != 1 {
		t.Fatalf("up-next requests = %d, want one", requests.Load())
	}
	if a.upNext.load.busy() {
		t.Fatal("up-next task remained busy after a successful response")
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "recommended"}) {
		t.Fatalf("queue = %v, want chosen and recommended tracks", got)
	}
	if a.queue.recStart != 1 || a.upNext.err != "" {
		t.Errorf("recommendation boundary = %d, error = %q", a.queue.recStart, a.upNext.err)
	}
}

func TestAutoPlayRetriesRecommendationsWhenChosenQueueRunsOut(t *testing.T) {
	var requests atomic.Int32
	client := youtube.NewClient(youtube.Options{
		HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
			count := requests.Add(1)
			response := `{"contents":{"playlistPanelRenderer":{"contents":[]}}}`
			if count == 2 {
				response = `{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"recommended","title":{"runs":[{"text":"Recommended track"}]}}}]}}}`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(response)),
				Request:    request,
			}, nil
		})},
	})
	a, _ := playbackApp(t)
	a.account.public = client
	a.settings.AutoPlay = true
	q := tracks("first", "last")
	a.play(q[0], q)
	if requests.Load() != 1 || a.queue.len() != 2 {
		t.Fatalf("initial fetch: requests=%d queue=%v", requests.Load(), ids(a.queue.items))
	}

	a.trackEnded()
	if a.current.VideoID != "last" {
		t.Fatalf("current track = %q, want last chosen track", a.current.VideoID)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests after final chosen track started = %d, want retry", requests.Load())
	}
	if a.upNext.load.busy() {
		t.Fatal("recommendation retry remained busy after successful response")
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"first", "last", "recommended"}) {
		t.Fatalf("queue after retry = %v, want recommendation appended", got)
	}
}

func TestQueueDoesNotMoveWhileRecommendationsLoad(t *testing.T) {
	a, _ := playbackApp(t)
	a.current = tracks("a")[0]
	a.queue.replace(a.current, tracks("a", "b"), 0, "Test queue")
	a.settings.AutoPlay = true
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)
	rowWithoutStatus, ok := tt.Find("Track a")
	if !ok {
		t.Fatalf("queue row missing from UI: %q", tt.Texts())
	}

	_, cancel := context.WithCancel(context.Background())
	a.upNext.load.cancel = cancel
	a.refresh()
	tt.Frame()
	rowWithStatus, ok := tt.Find("Track a")
	if !ok {
		t.Fatalf("queue row missing while fetching: %q", tt.Texts())
	}
	if tt.HasText("Finding recommendations…") {
		t.Fatal("transient loading status appeared while fetching recommendations")
	}
	if rowWithStatus.Y != rowWithoutStatus.Y {
		t.Errorf("queue row moved from y=%v to y=%v while status appeared", rowWithoutStatus.Y, rowWithStatus.Y)
	}
	a.upNext.load.stop()
	cancel()
}

func TestAutoPlayCompletesWhenNoRecommendationsAreReturned(t *testing.T) {
	var requests atomic.Int32
	client := youtube.NewClient(youtube.Options{
		HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"contents":{"playlistPanelRenderer":{"contents":[]}}}`)),
				Request:    request,
			}, nil
		})},
	})
	a, _ := playbackApp(t)
	a.account.public = client
	a.settings.AutoPlay = true
	q := tracks("a")
	a.play(q[0], q)

	if requests.Load() != 1 {
		t.Fatalf("up-next requests = %d, want one", requests.Load())
	}
	if a.upNext.load.busy() {
		t.Fatal("up-next task remained busy after an empty successful response")
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("queue = %v, want only the chosen track", got)
	}
}

// A queue played the way the app runs it: real ffmpeg decoding real audio,
// the ticker's poll moving on from each track to the next.
func TestPlayingATrackThroughToTheEnd(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=1", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.file = tone
	q := tracks("a", "b")
	a.play(q[0], q)

	deadline := time.Now().Add(15 * time.Second)
	heard := map[string]bool{}
	for time.Now().Before(deadline) {
		a.poll()
		if a.player.Position() > 0 {
			heard[a.current.VideoID] = true
		}
		if a.queue.index == 1 && heard["b"] && !a.player.Active() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if a.queue.index != 1 || !heard["a"] || !heard["b"] || a.player.Active() {
		t.Fatalf("heard %v, index %d, active %v: want both tracks played and the queue stopped", heard, a.queue.index, a.player.Active())
	}
	if a.playErr != "" {
		t.Errorf("playErr = %q", a.playErr)
	}

	a.togglePlay() // pressing play on an ended track starts it again
	waitPlaying := time.Now().Add(5 * time.Second)
	for !a.player.Playing() && time.Now().Before(waitPlaying) {
		a.poll()
		time.Sleep(10 * time.Millisecond)
	}
	if !a.player.Playing() {
		t.Error("play did not restart the ended track")
	}
	a.togglePlay()
	if !a.player.Paused() {
		t.Error("play did not pause a playing track")
	}
}

// deferredRunner replaces the app's background runner with a queue the test
// drains by hand, so the work the app hands off can be held in flight while
// the test asks what the main thread did with it.
func deferredRunner(a *app) (drain func()) {
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }
	return func() {
		queued := len(pending)
		for i := range queued {
			pending[i]()
		}
		pending = pending[queued:]
	}
}

// Moving the player to a position restarts ffmpeg there and waits for the
// audio device, so it happens off the main thread, and a position asked for
// while one is on its way is the one that plays.
func TestSeekingMovesThePlayerOffTheMainThread(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=15", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.file = tone
	drain := deferredRunner(a)
	q := tracks("a", "b")
	a.play(q[0], q)
	drain()
	if !a.player.Active() {
		t.Fatal("the track never reached the player")
	}

	a.seek(3 * time.Second)
	if got := a.player.Position(); got > 2*time.Second {
		t.Fatalf("the player moved to %v on the main thread, want the seek queued", got)
	}
	a.seek(9 * time.Second) // asked for while the first seek is on its way
	drain()
	if got := a.player.Position(); got < 2*time.Second || got >= 8*time.Second {
		t.Fatalf("position after the first seek = %v, want about 3s", got)
	}
	drain() // the position asked for second, which replaces the first
	if got := a.player.Position(); got < 8*time.Second {
		t.Fatalf("position after the second seek = %v, want about 9s", got)
	}
}

// A track the audio cache holds plays from the cache, without asking the
// resolver.
func TestACachedTrackPlaysWithoutResolving(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mp4")
		_, _ = w.Write([]byte("audio"))
	}))
	defer media.Close()
	cache, _, err := audio.Open(t.TempDir(), 2)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	cache.Add("three", media.URL+"/three")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := cache.Get("three"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the track was never cached")
		}
		time.Sleep(5 * time.Millisecond)
	}

	a, r := playbackApp(t)
	a.replaceAudioCache(cache)
	a.play(tracks("three")[0], nil)
	if r.asked("three") {
		t.Error("a cached track was resolved anyway")
	}
	if a.current.VideoID != "three" || a.stream.busy() {
		t.Errorf("current=%q resolving=%v", a.current.VideoID, a.stream.busy())
	}
}

func TestEditingTheQueueKeepsPlayingAndFollowsTheEdits(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d")
	a.play(q[0], q)

	a.removeFromQueue(1)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "c", "d"}) || a.current.VideoID != "a" {
		t.Fatalf("after the removal: %v, playing %q", got, a.current.VideoID)
	}
	a.removeFromQueue(0) // the track playing stays
	a.shiftInQueue(2, -1)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "d", "c"}) {
		t.Fatalf("after the move: %v", got)
	}
	a.trackEnded()
	if a.current.VideoID != "d" {
		t.Errorf("played %q after the move, want d", a.current.VideoID)
	}
	a.clearUpcoming()
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "d"}) || a.current.VideoID != "d" {
		t.Errorf("after clearing: %v, playing %q", got, a.current.VideoID)
	}
}

// A stream that breaks off mid-song, as the network dropping or an audio URL
// that expired while the machine slept makes it, is found again and played
// on from where it stopped.
func TestABrokenStreamResumesFromItsPosition(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=30", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer expired.Close()

	a, r := playbackApp(t)
	// The first URL is dead; resolving again gives one that plays.
	r.url = func(_ string, n int) (string, error) {
		if n == 1 {
			return expired.URL + "/a.m4a", nil
		}
		return tone, nil
	}
	a.play(tracks("a")[0], nil)
	a.player.Seek(12 * time.Second) // the track had played to here
	waitFor(t, func() bool { return a.player.Failure() != "" })

	waitFor(t, func() bool {
		a.poll()
		return a.player.Playing() && a.player.Failure() == ""
	})
	if got := r.count("a"); got != 2 {
		t.Errorf("resolved %d times, want 2: the expired URL must not be served from memory", got)
	}
	if pos := a.player.Position(); pos < 12*time.Second {
		t.Errorf("position %v, want playback to go on from 12s", pos)
	}
	if a.playErr != "" || a.current.VideoID != "a" {
		t.Errorf("playErr %q, current %q", a.playErr, a.current.VideoID)
	}
}

// A stream that stays broken is tried a bounded number of times, then the
// reason is shown; a resolve that fails while the network is down counts as
// a try, and one that works afterwards ends the trouble.
func TestAStreamThatStaysBrokenGivesUpAfterTheRetries(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is not installed")
	}
	expired := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "expired", http.StatusForbidden)
	}))
	defer expired.Close()

	a, r := playbackApp(t)
	r.url = func(videoID string, n int) (string, error) {
		if n%2 == 0 {
			return "", errors.New("Temporary failure in name resolution")
		}
		return expired.URL + "/" + videoID + ".m4a", nil
	}
	q := tracks("a")
	a.play(q[0], q)
	waitFor(t, func() bool {
		a.poll()
		return a.playErr != "" && !a.stream.busy()
	})
	if got, want := r.count("a"), 1+maxRecoveries; got != want {
		t.Errorf("resolved %d times, want the first play and %d retries", got, maxRecoveries)
	}
	if a.playErr == "" || a.current.VideoID != "a" {
		t.Errorf("playErr %q, current %q: want the failure shown on the track", a.playErr, a.current.VideoID)
	}
}

func waitFor(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestEditingTheQueueFromItsPanel(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d")
	a.play(q[0], q)
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)

	if err := tt.Click("Remove Track b from the queue"); err != nil {
		t.Fatal(err)
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("after remove: %v", got)
	}
	if tt.HasText("Remove Track a from the queue") {
		t.Error("the track playing offers a remove button")
	}

	// Drag the last row above the one before it.
	from, ok1 := tt.Find("Track d")
	to, ok2 := tt.Find("Track c")
	if !ok1 || !ok2 {
		t.Fatalf("rows missing: %q", tt.Texts())
	}
	fx, fy := from.X+from.W/2, from.Y+from.H/2
	tt.Press(fx, fy)
	for y := fy; y > to.Y; y -= 4 {
		tt.Move(fx, y)
	}
	tt.Move(fx, to.Y+2)
	tt.Release(fx, to.Y+2)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "d", "c"}) {
		t.Fatalf("after drag: %v", got)
	}

	if err := tt.Click("Clear queue"); err != nil {
		t.Fatal(err)
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a"}) || a.current.VideoID != "a" {
		t.Errorf("after clear: %v, playing %q", got, a.current.VideoID)
	}
}

func TestQueueEditsAtTheEdges(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d")
	a.play(q[1], q) // a [b] c d
	a.settings.AutoPlay = true
	a.queue.appendGenerated(tracks("r1", "r2"))
	a.enqueue(tracks("n")[0], true) // a [b] n c d r1 r2

	a.removeFromQueue(a.queue.index) // the track playing cannot be removed
	if a.current.VideoID != "b" || a.queue.len() != 7 {
		t.Fatalf("removed the current track: %v", ids(a.queue.items))
	}
	a.removeFromQueue(0) // before the current place
	if a.queue.current().VideoID != "b" || a.queue.index != 0 {
		t.Errorf("place after removing before: %d", a.queue.index)
	}
	a.removeFromQueue(a.queue.find("d")) // after it
	if a.queue.current().VideoID != "b" || a.queue.at(a.queue.recStart).VideoID != "r1" {
		t.Errorf("after removing after: %v recStart %d", ids(a.queue.items), a.queue.recStart)
	}

	// b [n] c r1 r2: drag c above the play-next track, then n below the
	// divider; play-next still plays first and the boundary stays on r1.
	a.moveQueue([]int{a.queue.rowOf(a.queue.find("c"))}, a.queue.rowOf(1))
	a.moveQueue([]int{a.queue.rowOf(a.queue.find("n"))}, a.queue.rows())
	if a.queue.playNext != "n" || a.queue.at(a.queue.recStart).VideoID != "r1" && a.queue.at(a.queue.recStart).VideoID != "n" {
		t.Errorf("after drags: %v, recStart %d, playNext %q", ids(a.queue.items), a.queue.recStart, a.queue.playNext)
	}
	if next, ok := a.queue.next(true, repeatOff); !ok || a.queue.at(next).VideoID != "n" {
		t.Errorf("play-next lost: next %d %v", next, ok)
	}

	a.settings.AutoPlay = false // clearing with auto-play on asks for new recommendations
	a.clearUpcoming()           // while playing
	if got := ids(a.queue.items); !slices.Equal(got, []string{"b"}) || a.current.VideoID != "b" {
		t.Errorf("after clear: %v", got)
	}
	a.clearUpcoming() // nothing left to clear
	a.removeFromQueue(5)
	a.moveQueue([]int{9}, 0)
	if a.queue.len() != 1 {
		t.Errorf("out-of-range edits changed the queue: %v", ids(a.queue.items))
	}

	e := newQueue()
	e.clearUpcoming()
	if e.remove(0) || e.shift(0, 1) || e.moveRows([]int{0}, 0) || e.len() != 0 {
		t.Error("edited an empty queue")
	}
}
