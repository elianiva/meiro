package main

import (
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// resolved records the tracks the app asked its stream resolver for, in place
// of yt-dlp.
type resolved struct {
	mu  sync.Mutex
	ids []string
	err map[string]error
	// file, when set, is the audio every track resolves to, and video the
	// picture.
	file, video string
	// url, when set, chooses the URL of a track's nth resolve (from 1), in
	// place of file, and may fail it.
	url func(videoID string, n int) (string, error)
}

func (r *resolved) resolve(_ context.Context, videoID string) (audio.Stream, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, videoID)
	if err := r.err[videoID]; err != nil {
		return audio.Stream{}, err
	}
	if r.url != nil {
		n := 0
		for _, id := range r.ids {
			if id == videoID {
				n++
			}
		}
		url, err := r.url(videoID, n)
		return audio.Stream{URL: url, Total: 3 * time.Minute}, err
	}
	if r.file != "" {
		return audio.Stream{URL: r.file, Video: r.video, Total: 3 * time.Second}, nil
	}
	return audio.Stream{URL: "/nonexistent/" + videoID + ".m4a", Total: 3 * time.Minute}, nil
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
	a := newTestApp(t)
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

func TestTurningShuffleOffPutsTheQueueBack(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d")
	a.play(q[2], q) // c, with the songs before it in the queue
	a.setShuffle(true)
	if a.queue.index != 0 || a.current.VideoID != "c" {
		t.Fatalf("shuffle left %q at %d, want c at the top", a.current.VideoID, a.queue.index)
	}
	a.setShuffle(false)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("unshuffle left %v, want the playlist's order", got)
	}
	if a.queue.index != 2 || a.current.VideoID != "c" {
		t.Errorf("unshuffle left %q at %d, want c where it was", a.current.VideoID, a.queue.index)
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
	if len(a.history) != 0 {
		t.Errorf("the failed track was recorded: %+v", a.history)
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
		{"yt-dlp could not resolve the audio: [youtube] M2cckDmNLMI: Requested format is not available. Use --list-formats for a list of available formats", "YouTube did not offer audio for this track. Try again later, or update Meiro."},
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
	for _, entry := range a.history {
		if entry.VideoID == "b" {
			t.Errorf("the skipped track was recorded: %+v", a.history)
		}
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
	if len(a.history) != 0 {
		t.Errorf("the unplayable tracks were recorded: %+v", a.history)
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

func TestLoudnessGainAdjustsPlaybackVolumeWithoutChangingTheUserVolume(t *testing.T) {
	tests := []struct {
		name      string
		volume    float64
		loudness  float64
		normalize bool
		want      float64
	}{
		{"disabled", 50, -11.7, false, 0.5},
		{"quieter track", 50, -6, true, 0.9976311575},
		{"loud track", 50, 6, true, 0.2505936168},
		{"gain is capped", 90, -11.7, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := playbackVolume(tt.volume, tt.loudness, tt.normalize); math.Abs(got-tt.want) > 1e-9 {
				t.Fatalf("playbackVolume(%v, %v, %t) = %v, want %v", tt.volume, tt.loudness, tt.normalize, got, tt.want)
			}
		})
	}
}

func TestLoudnessStateDoesNotCarryBetweenTracks(t *testing.T) {
	a, _ := playbackApp(t)
	a.loudness, a.loudnessOK = 6, true
	a.enterTrack(tracks("next")[0], true)
	if a.loudness != 0 || a.loudnessOK {
		t.Fatalf("new track inherited loudness %v (known=%t)", a.loudness, a.loudnessOK)
	}
}

func TestLoudnessFailureDoesNotBlockPlayback(t *testing.T) {
	a, r := playbackApp(t)
	a.settings.NormalizeLoudness = true
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: youtube.NewClient(youtube.Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: playbackRoundTrip(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("metadata unavailable")
	})}})}
	a.play(tracks("song")[0], tracks("song"))
	if !r.asked("song") || a.playErr != "" {
		t.Fatalf("loudness lookup blocked playback: asked=%v error=%q", r.asked("song"), a.playErr)
	}
}

func TestLoudnessSurvivesTheStreamResolving(t *testing.T) {
	a, _ := playbackApp(t)
	a.settings.NormalizeLoudness = true
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: youtube.NewClient(youtube.Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: playbackRoundTrip(func(*http.Request) (*http.Response, error) {
		body := `{"playerConfig":{"audioConfig":{"loudnessDb":6}}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})}
	want := playbackVolume(a.volume, 6, true)
	a.play(tracks("song")[0], tracks("song"))
	if !a.loudnessOK || math.Abs(a.player.Volume()-want) > 1e-9 {
		t.Fatalf("first play: loudness known=%t, volume %v, want %v", a.loudnessOK, a.player.Volume(), want)
	}
	// The second play finds the value in the cache and asks for nothing.
	a.play(tracks("song")[0], tracks("song"))
	if !a.loudnessOK || math.Abs(a.player.Volume()-want) > 1e-9 {
		t.Fatalf("replay: loudness known=%t, volume %v, want %v", a.loudnessOK, a.player.Volume(), want)
	}
}

func TestLoudnessIsAppliedBeforeTheAudioStarts(t *testing.T) {
	a, _ := playbackApp(t)
	a.settings.NormalizeLoudness = true
	a.volume = 50
	a.player.SetVolume(a.volume / 100)
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: youtube.NewClient(youtube.Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: playbackRoundTrip(func(*http.Request) (*http.Response, error) {
		body := `{"playerConfig":{"audioConfig":{"loudnessDb":6}}}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})}
	// The stream is asked for after the gain is applied, so the fake resolver
	// sees the volume the player holds when the audio is about to start.
	atResolve := -1.0
	urls := a.streams
	a.streams = audio.NewURLs(func(ctx context.Context, videoID string) (audio.Stream, error) {
		atResolve = a.player.Volume()
		return urls.Get(ctx, videoID)
	})
	a.play(tracks("song")[0], tracks("song"))
	want := playbackVolume(50, 6, true)
	if math.Abs(atResolve-want) > 1e-9 {
		t.Fatalf("the player held volume %v when the stream was asked for, want %v", atResolve, want)
	}
	if !a.loudnessOK || math.Abs(a.player.Volume()-want) > 1e-9 {
		t.Fatalf("after playing: loudness known=%t, volume %v, want %v", a.loudnessOK, a.player.Volume(), want)
	}
}

func TestNormalizationToggleImmediatelyUpdatesPlayerVolume(t *testing.T) {
	a, _ := playbackApp(t)
	a.volume = 50
	a.current = tracks("song")[0]
	a.loudnessMap.Put("song", youtube.Loudness{LoudnessDB: 6})
	a.applyLoudness()
	a.setNormalizeLoudness(true)
	if got, want := a.player.Volume(), playbackVolume(50, 6, true); math.Abs(got-want) > 1e-9 {
		t.Fatalf("enabling normalization set volume to %v, want %v", got, want)
	}
	a.setNormalizeLoudness(false)
	if got, want := a.player.Volume(), 0.5; got != want {
		t.Fatalf("disabling normalization set volume to %v, want %v", got, want)
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

func TestSignedInPlaybackReportsHistoryOnlyAfterAudioStarts(t *testing.T) {
	a, r := tonesApp(t, 2)
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var reports atomic.Int32
	a.account.session = &session{client: youtube.NewClient(youtube.Options{
		CookieAuth: auth,
		HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
			body := `{"playbackTracking":{"videostatsPlaybackUrl":{"baseUrl":"https://s.youtube.com/api/stats/playback?docid=song"}}}`
			if request.Method == http.MethodGet {
				reports.Add(1)
				body = ""
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})},
	})}

	r.err["unavailable"] = errors.New("Requested format is not available")
	a.play(tracks("unavailable")[0], nil)
	if reports.Load() != 0 {
		t.Fatalf("reported failed playback %d times", reports.Load())
	}

	a.play(tracks("song")[0], nil)
	pollUntil(t, a, "signed-in playback report", func() bool {
		return reports.Load() == 1 && a.player.Playing()
	})
	if len(a.history) != 1 || a.history[0].VideoID != "song" {
		t.Errorf("local history = %+v, want the track that started", a.history)
	}
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

func TestQueueRecommendationHeadingAlignsWithArtwork(t *testing.T) {
	a, _ := playbackApp(t)
	a.current = tracks("chosen")[0]
	a.queue.replace(a.current, []youtube.MusicItem{a.current}, 0, "Queue")
	a.queue.appendGenerated([]youtube.MusicItem{{VideoID: "recommended", Title: "Recommended track"}})
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)

	heading, ok := tt.Find("Recommended")
	if !ok {
		t.Fatalf("recommendation heading missing: %q", tt.Texts())
	}
	chosenRow, ok := tt.Find("Track chosen")
	if !ok {
		t.Fatalf("chosen row missing: %q", tt.Texts())
	}
	row, ok := tt.Find("Recommended track")
	if !ok {
		t.Fatalf("recommended row missing: %q", tt.Texts())
	}
	if got := heading.X - row.X; got != 8 {
		t.Errorf("heading-to-row inset = %g, want 8 to align with artwork", got)
	}
	topGap := heading.Y - (chosenRow.Y + chosenRow.H)
	bottomGap := row.Y + 8 - (heading.Y + heading.H) // The song row pads its artwork by 8.
	if difference := topGap - bottomGap; difference < -0.5 || difference > 0.5 {
		t.Errorf("heading gaps are unbalanced: %g above, %g below", topGap, bottomGap)
	}
	if err := tt.Click("Recommended"); err != nil {
		t.Fatal(err)
	}
	if a.nowPlaying.queueSelection.Len() != 0 || a.nowPlaying.queueSelectionCursor == a.queue.recStart {
		t.Errorf("recommendation heading selected a queue row: %d selected, cursor %d", a.nowPlaying.queueSelection.Len(), a.nowPlaying.queueSelectionCursor)
	}
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
	if len(a.history) != 2 || a.history[0].VideoID != "a" || a.history[1].VideoID != "b" {
		t.Errorf("history = %+v, want only the two tracks that started", a.history)
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

	// Keyboard edits must reach the decoder too, not only move the thumb.
	a.player.Pause()
	a.total, a.scrub = 15*time.Second, 9
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tabTo(t, tt, "Position")
	tt.Key(0, ui.KeyHome)
	drain()
	if got := a.player.Position(); got > time.Second {
		t.Fatalf("Home left playback at %v, want the start", got)
	}
	tt.Key(0, ui.KeyRight)
	drain()
	if got := a.player.Position(); got < 100*time.Millisecond || got > time.Second {
		t.Fatalf("Right sought to %v, want 150ms (1%% of 15s)", got)
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

// A podcast show opens as a page of its episodes. An episode left part-way
// through starts where it stopped the next time it plays, and the place is
// saved with the queue, so it outlives the run.
func TestAPodcastEpisodeComesBackWhereItStopped(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=120", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.url = func(string, int) (string, error) { return tone, nil } // three minutes long, as far as the app knows
	saved := filepath.Join(t.TempDir(), "playback.json")
	a.playSave.writer.Path = saved
	a.router.Push("/podcast/MPSPPLshow")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if !tt.HasText("Slow Talk") || !tt.HasText("Episode One") || tt.HasText("Shuffle") {
		t.Fatalf("the show page reads %q", tt.Texts())
	}
	playing := func(id string) func() bool {
		return func() bool { a.poll(); return a.current.VideoID == id && a.player.Playing() }
	}

	if err := tt.Click("Episode One"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, playing("ep-1"))
	a.player.Seek(70 * time.Second)
	waitFor(t, func() bool { return a.player.Position() >= 70*time.Second })
	if err := tt.Click("Episode Two"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, playing("ep-2"))
	a.savePlayback(0)
	a.playSave.writer.Wait()
	if episodes := playstate.Load(saved).Episodes; len(episodes) != 1 || episodes[0].VideoID != "ep-1" || episodes[0].Position < 70*time.Second {
		t.Errorf("the saved episodes are %v, want ep-1 at 70s or past it", episodes)
	}

	if err := tt.Click("Episode One"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, playing("ep-1"))
	if pos := a.player.Position(); pos < 70*time.Second {
		t.Errorf("episode one came back at %v, want where it stopped, 70s or past it", pos)
	}
}

// A signed-in episode can resume from YouTube's account progress when there
// is no local position. A local position wins when both are available, and a
// signed-out session ignores the account hint. Pausing sends a final interval.
func TestPodcastEpisodeSyncsResumeProgressWithTheAccount(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=120", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	for _, test := range []struct {
		name      string
		signedIn  bool
		local     time.Duration
		wantStart time.Duration
	}{
		{name: "account progress", signedIn: true, wantStart: 45 * time.Second},
		{name: "local position wins", signedIn: true, local: 70 * time.Second, wantStart: 70 * time.Second},
		{name: "signed out ignores account hint", wantStart: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, r := playbackApp(t)
			r.url = func(string, int) (string, error) { return tone, nil }
			var tracking []*http.Request
			if test.signedIn {
				auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
				if err != nil {
					t.Fatal(err)
				}
				client := youtube.NewClient(youtube.Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: playbackRoundTrip(func(request *http.Request) (*http.Response, error) {
					body := `{"playbackTracking":{"videostatsPlaybackUrl":{"baseUrl":"https://s.youtube.com/api/stats/playback?docid=ep"},"videostatsWatchtimeUrl":{"baseUrl":"https://s.youtube.com/api/stats/watchtime?docid=ep"}}}`
					if request.Method == http.MethodGet {
						tracking = append(tracking, request)
						body = ""
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				})}})
				a.account.session = &session{client: client}
			}
			item := youtube.MusicItem{VideoID: "episode", Title: "Episode", Kind: youtube.KindEpisode, ProgressPercent: 25}
			if test.local > 0 {
				a.episodes = []playstate.Episode{{VideoID: item.VideoID, Position: test.local}}
			}
			a.play(item, []youtube.MusicItem{item})
			waitFor(t, func() bool { return a.player.Playing() })
			if got := a.player.Position(); got < test.wantStart {
				t.Errorf("episode started at %s, want at least %s", got, test.wantStart)
			}
			if test.signedIn {
				if len(tracking) != 1 || tracking[0].URL.Path != "/api/stats/playback" {
					t.Fatalf("episode start pings = %v, want one playback ping", tracking)
				}
				a.pause()
				if len(tracking) != 2 || tracking[1].URL.Path != "/api/stats/watchtime" || tracking[1].URL.Query().Get("final") != "1" {
					t.Errorf("pause pings = %v, want final watch-time ping", tracking)
				}
			}
		})
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

func TestAStreamThatBreaksOffAtTheEndPlaysTheNextTrack(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=3", tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	data, err := os.ReadFile(tone)
	if err != nil {
		t.Fatal(err)
	}
	// A track whose data stops just before its end: ffmpeg plays almost all
	// of it and then reports the stream broken off.
	broken := filepath.Join(t.TempDir(), "broken.wav")
	if err := os.WriteFile(broken, data[:len(data)-4000], 0o644); err != nil {
		t.Fatal(err)
	}

	a, r := playbackApp(t)
	r.file = broken
	q := tracks("a", "b")
	a.play(q[0], q)

	// The break is the player's, before the app has looked at it, and the
	// sound drains the last moment out while the ticker waits for its next
	// look at the player.
	waitFor(t, func() bool { return a.player.Failure() != "" })
	waitFor(t, func() bool { return a.total-a.player.Position() <= endOfTrackLead })

	a.poll()
	if got := r.count("a"); got != 1 {
		t.Errorf("the track was resolved %d times, want the break to end it rather than find it again", got)
	}
	if a.current.VideoID != "b" || a.queue.index != 1 {
		t.Errorf("after the break: playing %q at %d, want b", a.current.VideoID, a.queue.index)
	}
	if a.playErr != "" {
		t.Errorf("playErr = %q", a.playErr)
	}
}

// A stream that breaks off mid-song, as the network dropping or an audio URL
// that expired while the machine slept makes it, is found again and played
// on from where it stopped.

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

	// A row of the queue has no buttons: a right click opens its menu, which
	// holds the remove.
	rowB, ok := tt.Find("Track b")
	if !ok {
		t.Fatalf("no row: %q", tt.Texts())
	}
	tt.RightClickAt(rowB.X+rowB.W/2, rowB.Y+rowB.H/2)
	tt.Frame()
	if err := tt.Click("Remove from queue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "c", "d"}) {
		t.Fatalf("after remove: %v", got)
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

	// The track playing has nowhere to go, so its menu offers no remove.
	rowA, ok := tt.Find("Track a")
	if !ok {
		t.Fatalf("no row: %q", tt.Texts())
	}
	tt.RightClickAt(rowA.X+rowA.W/2, rowA.Y+rowA.H/2)
	tt.Frame()
	if tt.HasText("Remove from queue") {
		t.Errorf("the track playing offers a remove: %q", tt.Texts())
	}
	a.menu.close()
	tt.Frame()

	if err := tt.Click("Clear queue"); err != nil {
		t.Fatal(err)
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a"}) || a.current.VideoID != "a" {
		t.Errorf("after clear: %v, playing %q", got, a.current.VideoID)
	}
}

func TestQueueSelectionCursorUsesRenderedRowWithRecommendations(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	a.queue.appendGenerated(tracks("recommended"))
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)

	if err := tt.ClickWith(ui.Cmd, "Track recommended"); err != nil {
		t.Fatal(err)
	}
	row := a.queue.rowOf(3)
	if a.nowPlaying.queueSelectionCursor != row || a.current.VideoID != "a" {
		t.Fatalf("selected generated row %d with cursor %d and current %q", row, a.nowPlaying.queueSelectionCursor, a.current.VideoID)
	}

	if err := tt.ClickWith(ui.Shift, "Track b"); err != nil {
		t.Fatal(err)
	}
	if a.nowPlaying.queueSelection.Len() != 3 || a.nowPlaying.queueSelectionCursor != a.queue.rowOf(1) {
		t.Fatalf("shift selection has %d items and cursor %d; want 3 items and row %d", a.nowPlaying.queueSelection.Len(), a.nowPlaying.queueSelectionCursor, a.queue.rowOf(1))
	}
}

func TestQueueSelectionUsesThemeAccent(t *testing.T) {
	a, _ := playbackApp(t)
	a.settings.Seed, a.settings.Mode = "#e91e63", "light"
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)

	if err := tt.ClickWith(ui.Cmd, "Track b"); err != nil {
		t.Fatal(err)
	}
	tt.Move(0, 0)
	row, ok := tt.Find("Track b")
	if !ok {
		t.Fatalf("selected row missing: %q", tt.Texts())
	}
	want := m3.New(a.settings.Theme(uiFont), false).Scheme.Primary
	got := tt.Image().RGBAAt(int(row.X+row.W-40), int(row.Y+row.H/2))
	if got.R != want.R || got.G != want.G || got.B != want.B || got.A != want.A {
		t.Fatalf("selection colour is %v; want theme accent %v", got, want)
	}
}

func TestQueueSelectionMovesAndRemovesTogether(t *testing.T) {
	a, _ := playbackApp(t)
	q := tracks("a", "b", "c", "d")
	a.play(q[0], q)
	a.nowPlaying.open, a.nowPlaying.queueOnly, a.nowPlaying.playerShown = true, true, true
	tt := ui.NewTester(a.nowPlayingView, 1000, 700)
	tt.Frame()

	if err := tt.ClickWith(ui.Cmd, "Track b"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Cmd, "Track c"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "a" || a.nowPlaying.queueSelection.Len() != 2 {
		t.Fatalf("modifier-click selected %d tracks and started %q", a.nowPlaying.queueSelection.Len(), a.current.VideoID)
	}

	from, ok1 := tt.Find("Track b")
	to, ok2 := tt.Find("Track d")
	if !ok1 || !ok2 {
		t.Fatalf("rows missing: %q", tt.Texts())
	}
	fx, fy := from.X+from.W/2, from.Y+from.H/2
	endY := to.Y + to.H - 1
	tt.Press(fx, fy)
	for y := fy; y < endY; y += 4 {
		tt.Move(fx, y)
	}
	tt.Move(fx, endY)
	tt.Release(fx, endY)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "d", "b", "c"}) {
		t.Fatalf("dragging a selected row did not move the group: %v", got)
	}
	if a.nowPlaying.queueSelection.Len() != 0 {
		t.Fatalf("queue edit kept a stale selection: %d", a.nowPlaying.queueSelection.Len())
	}

	if err := tt.ClickWith(ui.Cmd, "Track b"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ClickWith(ui.Cmd, "Track c"); err != nil {
		t.Fatal(err)
	}
	rowD, ok := tt.Find("Track d")
	if !ok {
		t.Fatalf("no row d: %q", tt.Texts())
	}
	tt.RightClickAt(rowD.X+rowD.W/2, rowD.Y+rowD.H/2)
	tt.Frame()
	if a.nowPlaying.queueSelection.Len() != 2 || !tt.HasText("Remove 2 tracks") {
		t.Fatalf("context menu dropped the selection: %d, %q", a.nowPlaying.queueSelection.Len(), tt.Texts())
	}
	if err := tt.Click("Remove 2 tracks"); err != nil {
		t.Fatal(err)
	}
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "d"}) || a.current.VideoID != "a" {
		t.Fatalf("batch remove left queue %v and current %q", got, a.current.VideoID)
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

// tonesApp is an app whose every track is one tone of the given
// length in seconds, to test the hand over between tracks on real audio.
func tonesApp(t *testing.T, seconds int) (*app, *resolved) {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration="+strconv.Itoa(seconds), tone).CombinedOutput(); err != nil {
		t.Fatalf("making a tone: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.file = tone
	return a, r
}

// pollUntil polls the app as its ticker does until cond holds, and fails the
// test when it does not in time.
func pollUntil(t *testing.T, a *app, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (current %s, index %d)", what, a.current.VideoID, a.queue.index)
		}
		a.poll()
		time.Sleep(5 * time.Millisecond)
	}
}

// followsWithoutPause plays until the current track is want, and fails if the
// player let the sound end on the way: a hand over keeps one stream running
// from the track before to the one after.
func followsWithoutPause(t *testing.T, a *app, want string) {
	t.Helper()
	pollUntil(t, a, "track "+want, func() bool {
		if a.current.VideoID != want && a.player.Ended() && !a.stream.busy() {
			t.Fatalf("the stream ended before %s took over", want)
		}
		return a.current.VideoID == want
	})
}

func TestTheNextTrackIsReadiedAndFollowsWithoutAPause(t *testing.T) {
	a, r := tonesApp(t, 1)
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	pollUntil(t, a, "the next track to be readied", func() bool { return a.player.Preloaded() == "b" })
	if a.current.VideoID != "a" {
		t.Fatalf("b was readied after a ended")
	}
	followsWithoutPause(t, a, "b")
	if a.queue.index != 1 || a.playErr != "" || a.stream.busy() {
		t.Errorf("after the hand over: index %d, playErr %q, stream busy %v", a.queue.index, a.playErr, a.stream.busy())
	}
	if n := r.count("b"); n != 1 {
		t.Errorf("b was resolved %d times, want once", n)
	}
	// And the one after it is readied in turn.
	pollUntil(t, a, "c to be readied", func() bool { return a.player.Preloaded() == "c" })
	followsWithoutPause(t, a, "c")
}

func TestTheLastTrackIsNotFollowedByAnything(t *testing.T) {
	a, _ := tonesApp(t, 1)
	q := tracks("a")
	a.play(q[0], q)
	pollUntil(t, a, "the track to play", a.player.Playing)
	time.Sleep(50 * time.Millisecond)
	a.poll()
	if got := a.player.Preloaded(); got != "" {
		t.Fatalf("preloaded %q after the last track", got)
	}
	pollUntil(t, a, "the queue to stop", func() bool { return !a.player.Active() })
	if a.current.VideoID != "a" || a.playErr != "" {
		t.Errorf("current %s, playErr %q", a.current.VideoID, a.playErr)
	}
}

func TestARepeatedTrackIsReadiedAgain(t *testing.T) {
	a, _ := tonesApp(t, 1)
	a.repeat = repeatTrack
	q := tracks("a", "b")
	a.play(q[0], q)
	pollUntil(t, a, "the track to be readied again", func() bool { return a.player.Preloaded() == "a" })
	pollUntil(t, a, "the hand over", func() bool {
		return a.player.Preloaded() == "" && a.player.Playing() && a.player.Position() < 500*time.Millisecond && !a.stream.busy() && a.preloaded.id == ""
	})
	if a.current.VideoID != "a" || a.queue.index != 0 || a.player.Ended() {
		t.Errorf("repeat: current %s, index %d, ended %v", a.current.VideoID, a.queue.index, a.player.Ended())
	}
}

func TestEditingTheQueueChangesWhatIsReadied(t *testing.T) {
	a, _ := tonesApp(t, 1)
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	pollUntil(t, a, "b to be readied", func() bool { return a.player.Preloaded() == "b" })
	a.removeFromQueue(1) // b leaves the queue; c follows
	if got := a.player.Preloaded(); got == "b" {
		t.Fatal("still holding b after its track left the queue")
	}
	followsWithoutPause(t, a, "c")
	if a.queue.index != 1 {
		t.Errorf("index %d, want c at 1", a.queue.index)
	}
}

func TestATrackThatCannotBeResolvedIsLeftToTheSkip(t *testing.T) {
	a, r := tonesApp(t, 1)
	r.err["b"] = errors.New("Video unavailable")
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	pollUntil(t, a, "the failed attempt", func() bool { return a.preloadFailed == "b" })
	if a.player.Preloaded() != "" {
		t.Error("something is preloaded for a track that did not resolve")
	}
	n := r.count("b")
	a.poll()
	a.poll()
	if r.count("b") != n {
		t.Error("a failed preload was tried again on every poll")
	}
	// At the end of a, b fails as it always did, and the queue moves on to c.
	pollUntil(t, a, "c to play", func() bool { return a.current.VideoID == "c" && a.player.Playing() })
	if !strings.Contains(a.notice, "Skipped Track b") {
		t.Errorf("notice = %q, want b skipped", a.notice)
	}
}

func TestSkippingWhileATrackIsReadiedPlaysTheTrackChosen(t *testing.T) {
	a, _ := tonesApp(t, 3)
	q := tracks("a", "b", "c")
	a.play(q[0], q)
	pollUntil(t, a, "b to be readied", func() bool { return a.player.Preloaded() == "b" })
	a.playVideoID("c")
	pollUntil(t, a, "c to play", func() bool { return a.current.VideoID == "c" && a.player.Playing() })
	if a.queue.index != 2 {
		t.Errorf("index %d, want 2", a.queue.index)
	}
	if _, ok := a.player.Advanced(); ok {
		t.Error("a hand over to b was left unseen")
	}
}

func TestSeekingNearTheEndStillHandsOver(t *testing.T) {
	a, _ := tonesApp(t, 3)
	q := tracks("a", "b")
	a.play(q[0], q)
	pollUntil(t, a, "b to be readied", func() bool { return a.player.Preloaded() == "b" })
	a.player.Pause()
	a.seek(2500 * time.Millisecond) // dropping the readied stream, which the next poll readies again
	a.player.Resume()
	pollUntil(t, a, "b to be readied again", func() bool { return a.player.Preloaded() == "b" || a.current.VideoID == "b" })
	followsWithoutPause(t, a, "b")
}

// The 48 DIP targets of two neighbouring buttons must not cover each other's
// drawn shape: a press just inside the edge of "Up next" is its own, not the
// next button's.
func TestPlayerBarButtonsKeepTheirOwnEdges(t *testing.T) {
	const shape = 32 // the drawn side of both buttons
	for _, side := range []string{"left", "right"} {
		a, _ := playbackApp(t)
		a.router.Push("/album/MPREb_test")
		tt := ui.NewTester(a.view, 1000, 700)
		if err := tt.Click("Album Track One"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		r, ok := tt.Find("Up next")
		if !ok {
			t.Fatalf("no Up next: %q", tt.Texts())
		}
		inset := (r.W - shape) / 2
		x := r.X + inset + 2
		if side == "right" {
			x = r.X + r.W - inset - 2
		}
		tt.ClickAt(x, r.Y+r.H/2)
		if !a.nowPlaying.open || !a.nowPlaying.queueOnly {
			t.Errorf("a press 2 px inside the %s edge of Up next did not open Up next (open=%v, queueOnly=%v)",
				side, a.nowPlaying.open, a.nowPlaying.queueOnly)
		}
	}
}

func TestSheetCornerDoesNotCoverPlayerCloseButton(t *testing.T) {
	oldReach := sheetReach
	// Exercise the larger continuous corner on every platform, including Linux.
	sheetReach = continuousCornerReach
	t.Cleanup(func() { sheetReach = oldReach })

	a, _ := playbackApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	a.nowPlaying.open, a.nowPlaying.playerShown = true, true
	tt.Frame()
	close, ok := tt.Find("Close the player")
	if !ok {
		t.Fatalf("close button missing: %q", tt.Texts())
	}
	x, y := close.X+close.W/2, close.Y+close.H/2
	tt.Move(x, y)
	if got := tt.Cursor(); got != ui.CursorPointer {
		t.Fatalf("cursor over close button = %v, want pointer", got)
	}
	tt.ClickAt(x, y)
	if a.nowPlaying.open && !a.nowPlaying.closing {
		t.Fatal("clicking the close button did not start closing the player")
	}
}

func TestClosingPlayerBuildsThePageUnderThePlayer(t *testing.T) {
	oldMotion := m3.SpatialDefault
	m3.SpatialDefault = m3.Spring{Stiffness: 0.01, Damping: 1}
	t.Cleanup(func() { m3.SpatialDefault = oldMotion })

	a, _ := playbackApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	a.nowPlaying.open, a.nowPlaying.playerShown = true, true
	tt.Frame()
	if _, ok := tt.Find(searchPlaceholder); ok {
		t.Fatal("the home page search is visible behind the full player")
	}

	if err := tt.Click("Close the player"); err != nil {
		t.Fatal(err)
	}
	if !a.nowPlaying.open || !a.nowPlaying.closing {
		t.Fatal("the click did not start the player close animation")
	}
	if _, ok := tt.Find(searchPlaceholder); !ok {
		t.Fatalf("the page was not built as the player closed: %q", tt.Texts())
	}
}

// A track YouTube gives no audio for says why in the skip toast, and Learn
// more explains an outdated yt-dlp for the build the listener runs.
func TestFormatUnavailableOffersHelpForTheBuild(t *testing.T) {
	for _, tc := range []struct {
		full bool
		want string
	}{
		{true, "You use the full build"},
		{false, "You use the minimal build"},
	} {
		a, r := playbackApp(t)
		a.bundled = func(string) bool { return tc.full }
		r.err["b"] = errors.New("yt-dlp could not resolve the audio: Requested format is not available")
		tt := ui.NewTester(a.view, 1000, 700)
		q := tracks("a", "b", "c")
		a.play(q[1], q)
		tt.Frame()
		if err := tt.Click("Learn more"); err != nil {
			t.Fatalf("full=%v: %v", tc.full, err)
		}
		tt.Frame()
		if !tt.HasText(tc.want) {
			t.Errorf("full=%v: dialog lacks %q: %q", tc.full, tc.want, tt.Texts())
		}
	}
}

func TestOtherSkipsHaveNoLearnMore(t *testing.T) {
	a, r := playbackApp(t)
	r.err["b"] = errors.New("Video unavailable")
	tt := ui.NewTester(a.view, 1000, 700)
	q := tracks("a", "b", "c")
	a.play(q[1], q)
	tt.Frame()
	if tt.HasText("Learn more") {
		t.Errorf("unrelated skip offers Learn more: %q", tt.Texts())
	}
}

func TestNarrowPlayerBarRevealsVerticalVolumeSliderOnHover(t *testing.T) {
	a, _ := playbackApp(t)
	a.current = tracks("a")[0]
	tt := ui.NewTester(a.view, 920, 700)
	tt.Frame()
	if _, ok := tt.Find("Mute"); !ok {
		t.Fatalf("the narrow player bar hides its mute button: %q", tt.Texts())
	}
	if _, ok := tt.Find("Volume"); ok {
		t.Fatalf("the volume slider takes space before the button is hovered: %q", tt.Texts())
	}

	mute, _ := tt.Find("Mute")
	tt.Move(mute.X+mute.W/2, mute.Y+mute.H/2)
	volume, ok := tt.Find("Volume")
	if !ok {
		t.Fatalf("hovering Mute does not reveal the volume slider: %q", tt.Texts())
	}
	if volume.H <= volume.W {
		t.Fatalf("the narrow volume slider is not vertical: %+v", volume)
	}

	tt.Move(volume.X+volume.W/2, volume.Y+volume.H/2)
	if _, ok := tt.Find("Volume"); !ok {
		t.Fatal("moving onto the slider closes its hover panel")
	}
	tt.ClickAt(volume.X+volume.W/2, volume.Y+8)
	if a.volume < 95 {
		t.Fatalf("dragging the vertical slider up did not raise volume: %v", a.volume)
	}

	tt.Move(24, 24)
	if _, ok := tt.Find("Volume"); ok {
		t.Fatal("moving away from the volume control leaves its slider open")
	}
}

func TestWidePlayerBarKeepsHorizontalVolumeSlider(t *testing.T) {
	a, _ := playbackApp(t)
	a.current = tracks("a")[0]
	tt := ui.NewTester(a.view, 1280, 700)
	tt.Frame()
	volume, ok := tt.Find("Volume")
	if !ok {
		t.Fatalf("the wide player bar hides its volume slider: %q", tt.Texts())
	}
	if volume.W <= volume.H {
		t.Fatalf("the wide volume slider is not horizontal: %+v", volume)
	}
}

// The window names the track while it plays, the way the taskbar shows it,
// and the app once it pauses.
func TestTheWindowTitleNamesTheTrackPlaying(t *testing.T) {
	a, _ := tonesApp(t, 3)
	item := youtube.MusicItem{VideoID: "a", Title: "All Because of You", Artist: "Bill", Artists: "Bill Withers, Grover Washington"}
	if got := a.windowTitle(); got != "Meiro" {
		t.Errorf("before playing the title is %q", got)
	}
	a.play(item, []youtube.MusicItem{item})
	pollUntil(t, a, "a to play", func() bool { return a.player.Playing() })
	if got, want := a.windowTitle(), "Bill Withers, Grover Washington - All Because of You"; got != want {
		t.Errorf("while playing the title is %q, want %q", got, want)
	}
	// An item whose row links no artist names one only in its byline.
	radio := youtube.MusicItem{VideoID: "b", Title: "Kitto Coaster", Subtitle: "Ado", Kind: youtube.KindTrack}
	a.play(radio, []youtube.MusicItem{radio})
	pollUntil(t, a, "b to play", func() bool { return a.current.VideoID == "b" && a.player.Playing() })
	if got, want := a.windowTitle(), "Ado - Kitto Coaster"; got != want {
		t.Errorf("a row that links no artist leaves the title %q, want %q", got, want)
	}
	a.togglePlay()
	if got := a.windowTitle(); got != "Meiro" {
		t.Errorf("once paused the title is %q", got)
	}
}
