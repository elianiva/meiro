package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/lastfm"
	"github.com/elianiva/meiro/internal/secretfile"
	"github.com/elianiva/meiro/youtube"
)

// fakeScrobbler is Last.fm as the app's tests see it: it records every call
// and answers as the test sets it up to.
type fakeScrobbler struct {
	mu    sync.Mutex
	calls []url.Values
	// reply answers a call; the default accepts it.
	reply func(url.Values) (status int, body string)
	// approvals is how many getSession calls say "not approved" first.
	approvals int
}

func (f *fakeScrobbler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	form, _ := url.ParseQuery(string(body))
	f.mu.Lock()
	f.calls = append(f.calls, form)
	status, reply := http.StatusOK, `{}`
	switch method := form.Get("method"); {
	case f.reply != nil:
		status, reply = f.reply(form)
	case method == "auth.getToken":
		reply = `{"token":"TOKEN"}`
	case method == "auth.getSession" && f.approvals > 0:
		f.approvals--
		status, reply = http.StatusForbidden, `{"error":14,"message":"Unauthorized Token"}`
	case method == "auth.getSession":
		reply = `{"session":{"name":"neo","key":"SESSION-KEY"}}`
	}
	f.mu.Unlock()
	w.WriteHeader(status)
	_, _ = w.Write([]byte(reply))
}

// called is the calls made so far to a method.
func (f *fakeScrobbler) called(method string) []url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []url.Values
	for _, call := range f.calls {
		if call.Get("method") == method {
			out = append(out, call)
		}
	}
	return out
}

func (f *fakeScrobbler) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// scrobbleApp is a playback app connected to a fake Last.fm as "neo", whose
// session and outbox are kept in a folder the test can look at.
func scrobbleApp(t *testing.T) (*app, *fakeScrobbler, string) {
	t.Helper()
	a, _ := playbackApp(t)
	f := &fakeScrobbler{}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	dir := t.TempDir()
	a.scrobble.client = &lastfm.Client{Key: "key", Secret: "secret", Root: server.URL}
	a.openScrobbler(lastFMSession(dir), filepath.Join(dir, "pending.json"))
	a.settings.LastFMUser, a.scrobble.session = "neo", "SK"
	// The clock stands a few minutes after the plays listenTo makes.
	a.scrobble.now = func() time.Time { return time.Date(2026, 3, 1, 12, 10, 0, 0, time.UTC) }
	a.saver.Path = filepath.Join(dir, "settings.json")
	t.Cleanup(a.scrobble.outbox.Wait)
	return a, f, dir
}

// song is a track YouTube Music credits properly.
func song(id string) youtube.MusicItem {
	return youtube.MusicItem{VideoID: id, Title: "Song " + id, Artist: "Artist " + id, Album: "Album " + id, Duration: "3:00"}
}

// listenTo plays the current play through a fake clock until it qualifies.
func listenTo(a *app, total time.Duration) {
	at := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	var position time.Duration
	for range int(total/2/(250*time.Millisecond)) + 8 {
		at, position = at.Add(250*time.Millisecond), position+250*time.Millisecond
		a.observe(at, position, total)
	}
}

func TestNothingIsSentUntilPlaybackIsConfirmed(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	hold := deferredRunner(a) // the audio is still being found
	q := []youtube.MusicItem{song("a"), song("b")}
	a.play(q[0], q)
	if !a.stream.busy() {
		t.Fatal("the track should still be loading")
	}
	a.observeListen() // a poll while loading
	if f.total() != 0 {
		t.Fatalf("a request went out while the track was loading: %v", f.calls)
	}
	hold()
	// The resolver points at a file that is not there: the track fails.
	for range 5 {
		a.poll()
	}
	if f.total() != 0 {
		t.Errorf("a request went out for a track that failed to play: %v", f.calls)
	}
}

func TestNowPlayingIsSentWhenPlaybackStarts(t *testing.T) {
	a, _ := tonesApp(t, 3)
	f2 := &fakeScrobbler{}
	server := httptest.NewServer(f2)
	t.Cleanup(server.Close)
	a.scrobble.client = &lastfm.Client{Key: "key", Secret: "secret", Root: server.URL}
	a.settings.LastFMUser, a.scrobble.session = "neo", "SK"
	q := []youtube.MusicItem{song("a")}
	a.play(q[0], q)
	if f2.total() != 0 {
		t.Fatalf("selecting a track sent %v", f2.calls)
	}
	pollUntil(t, a, "now playing", func() bool { return len(f2.called("track.updateNowPlaying")) > 0 })
	call := f2.called("track.updateNowPlaying")[0]
	if call.Get("artist") != "Artist a" || call.Get("track") != "Song a" || call.Get("album") != "Album a" || call.Get("sk") != "SK" {
		t.Errorf("now playing = %v", call)
	}
	if len(f2.called("track.scrobble")) != 0 {
		t.Error("a track that has just started was scrobbled")
	}
}

func TestConnectingInTheMiddleOfATrackSendsNowPlaying(t *testing.T) {
	a, _ := tonesApp(t, 5)
	f := &fakeScrobbler{}
	server := httptest.NewServer(f)
	t.Cleanup(server.Close)
	a.scrobble.client = &lastfm.Client{Key: "key", Secret: "secret", Root: server.URL}
	dir := t.TempDir()
	a.openScrobbler(lastFMSession(dir), filepath.Join(dir, "pending.json"))
	q := []youtube.MusicItem{song("a")}
	a.play(q[0], q) // before Last.fm is connected
	pollUntil(t, a, "playback", a.player.Playing)
	a.finishConnect(lastfm.Session{Name: "neo", Key: "SK"})
	pollUntil(t, a, "now playing", func() bool { return len(f.called("track.updateNowPlaying")) > 0 })
	if got := f.called("track.updateNowPlaying")[0].Get("track"); got != "Song a" {
		t.Errorf("now playing %q", got)
	}
}

func TestAQualifyingPlayIsScrobbledOnceWithItsStartTime(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	a.beginListen(song("a"), false)
	listenTo(a, 3*time.Minute)
	listenTo(a, 3*time.Minute) // more of the same play: it scrobbles once
	calls := f.called("track.scrobble")
	if len(calls) != 1 {
		t.Fatalf("%d scrobbles, want 1: %v", len(calls), calls)
	}
	call := calls[0]
	want := map[string]string{
		"artist[0]": "Artist a", "track[0]": "Song a", "album[0]": "Album a", "duration[0]": "180",
		"chosenByUser[0]": "1",
	}
	for name, value := range want {
		if call.Get(name) != value {
			t.Errorf("%s = %q, want %q", name, call.Get(name), value)
		}
	}
	// Playback was confirmed by the first sample, at 12:00:00.25 UTC.
	if ts := call.Get("timestamp[0]"); ts != "1772366400" {
		t.Errorf("timestamp = %s, want the play's start (1772366400)", ts)
	}
}

func TestChosenByUserFollowsWhoChoseTheTrack(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	deferredRunner(a)
	chosen := func() bool { return a.scrobble.listen.Chosen }

	q := []youtube.MusicItem{song("a"), song("b"), song("c"), song("d")}
	a.play(q[1], q) // picked from a list
	if !chosen() {
		t.Error("a track the listener picked was not chosen by them")
	}
	a.advance() // the queue moves on to c
	if chosen() || a.current.VideoID != "c" {
		t.Errorf("c, which the queue moved to: current %s, chosen %t", a.current.VideoID, chosen())
	}
	a.enqueue(song("e"), false)
	a.advance() // d
	if chosen() || a.current.VideoID != "d" {
		t.Errorf("d: current %s, chosen %t", a.current.VideoID, chosen())
	}
	a.advance() // e, which the listener queued
	if !chosen() || a.current.VideoID != "e" {
		t.Errorf("e was queued by the listener: current %s, chosen %t", a.current.VideoID, chosen())
	}
	a.previous() // going back is the listener's doing
	if !chosen() {
		t.Error("going to the previous track was not the listener's choice")
	}
	a.play(q[0], q[:1])
	a.enqueue(song("z"), false)
	fresh := []youtube.MusicItem{song("a"), song("z")}
	a.play(fresh[0], fresh) // a new queue forgets what was queued before
	a.advance()
	if chosen() || a.current.VideoID != "z" {
		t.Errorf("z: current %s, chosen %t: the queue it was added to is gone", a.current.VideoID, chosen())
	}
}

func TestRecommendedTracksAreNotChosenByTheUser(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	deferredRunner(a)
	a.settings.AutoPlay = true
	q := []youtube.MusicItem{song("a")}
	a.play(q[0], q)
	a.queue.appendGenerated([]youtube.MusicItem{song("r")})
	a.advance()
	if a.current.VideoID != "r" || a.scrobble.listen.Chosen {
		t.Errorf("current %s, chosen %t: a recommendation is not the listener's pick", a.current.VideoID, a.scrobble.listen.Chosen)
	}
}

func TestRepeatedTrackIsNotChosenAgain(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	deferredRunner(a)
	a.repeat = repeatTrack
	a.play(song("a"), []youtube.MusicItem{song("a")})
	a.trackEnded()
	if a.scrobble.listen.Chosen {
		t.Error("a track repeating itself was chosen by the listener")
	}
}

// panelFor is the answer of YouTube Music's /next for a track: the track
// itself, with the byline that names who made it.
func panelFor(id, byline string) string {
	return `{"contents":{"playlistPanelRenderer":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"` + id +
		`","title":{"runs":[{"text":"From the panel"}]},"longBylineText":{"runs":[` + byline + `]}}}]}}}`
}

func TestAnArtistLeftOutOfAListIsLookedUp(t *testing.T) {
	for _, tc := range []struct {
		name, byline string
		sent         bool
	}{
		{"named", `{"text":"Yorushika","navigationEndpoint":{"browseEndpoint":{"browseId":"UCyorushika"}}},{"text":" • "},{"text":"Album","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_x"}}}`, true},
		{"not a link", `{"text":"Plain text"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, f, _ := scrobbleApp(t)
			a.account.public = youtube.NewClient(youtube.Options{APIKey: "k", HTTPClient: &http.Client{Transport: playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(panelFor("x", tc.byline))), Request: r}, nil
			})}})
			// What a search for the artist hands over: a title, and no artist.
			a.current = youtube.MusicItem{VideoID: "x", Title: "Searched song", Subtitle: "Song"}
			a.beginListen(a.current, false)
			listenTo(a, 3*time.Minute)
			calls := f.called("track.scrobble")
			if !tc.sent {
				if len(calls) != 0 || len(f.called("track.updateNowPlaying")) != 0 {
					t.Errorf("sent without an artist: %v", f.calls)
				}
				return
			}
			if len(calls) != 1 || calls[0].Get("artist[0]") != "Yorushika" || calls[0].Get("track[0]") != "Searched song" || calls[0].Get("album[0]") != "Album" {
				t.Errorf("scrobbles = %v, want the looked-up artist with the title the list gave", calls)
			}
			if len(f.called("track.updateNowPlaying")) != 1 {
				t.Errorf("now playing sent %d times, want once", len(f.called("track.updateNowPlaying")))
			}
		})
	}
}

// countingYouTube answers every /next with the panel of the track asked for,
// and counts the questions.
func countingYouTube(a *app, byline string) *atomic.Int32 {
	var asked atomic.Int32
	a.account.public = youtube.NewClient(youtube.Options{APIKey: "k", HTTPClient: &http.Client{Transport: playbackRoundTrip(func(r *http.Request) (*http.Response, error) {
		asked.Add(1)
		body, _ := io.ReadAll(r.Body)
		id := "x"
		if strings.Contains(string(body), `"y"`) {
			id = "y"
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(panelFor(id, byline))), Request: r}, nil
	})}})
	return &asked
}

const yorushika = `{"text":"Yorushika","navigationEndpoint":{"browseEndpoint":{"browseId":"UCyorushika"}}}`

func TestAnArtistLookedUpOnceIsRemembered(t *testing.T) {
	a, f, dir := scrobbleApp(t)
	asked := countingYouTube(a, yorushika)
	a.openCredits(filepath.Join(dir, "credits.json"))
	bare := youtube.MusicItem{VideoID: "x", Title: "Searched", Subtitle: "Song"}

	a.current = bare
	a.beginListen(bare, false)
	a.beginListen(bare, false) // played again, as a repeat or from another list
	if asked.Load() != 1 {
		t.Errorf("%d requests for one track, want 1", asked.Load())
	}
	a.scrobble.credits.Wait()

	// After a restart the answer is still known.
	b, _, _ := scrobbleApp(t)
	again := countingYouTube(b, yorushika)
	b.openCredits(filepath.Join(dir, "credits.json"))
	b.beginListen(bare, false)
	if again.Load() != 0 || b.scrobble.listen.Track.Artist != "Yorushika" {
		t.Errorf("after a restart: %d requests, artist %q", again.Load(), b.scrobble.listen.Track.Artist)
	}
	_ = f
}

func TestAListThatNamesTheArtistFillsInOtherListsWithoutAsking(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	asked := countingYouTube(a, yorushika)
	deferredRunner(a)
	// One list names the artist; the tracks reach the queue through playFrom.
	a.play(song("x"), []youtube.MusicItem{song("x")})
	// Another leaves it out, as a search for the artist does.
	bare := youtube.MusicItem{VideoID: "x", Title: "Song x", Subtitle: "Song"}
	a.play(bare, []youtube.MusicItem{bare})
	if got := a.scrobble.listen.Track.Artist; got != "Artist x" {
		t.Errorf("artist %q, want the one the first list named", got)
	}
	if asked.Load() != 0 {
		t.Errorf("%d requests, want none", asked.Load())
	}
	// A queued track teaches too.
	a.enqueue(song("q"), false)
	if got := a.withKnownCredits(youtube.MusicItem{VideoID: "q"}).Artist; got != "Artist q" {
		t.Errorf("a queued track was not learned: %q", got)
	}
}

func TestNothingIsRememberedWithoutAnAccount(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	deferredRunner(a)
	a.settings.LastFMUser = ""
	a.play(song("x"), []youtube.MusicItem{song("x")})
	if a.scrobble.credits.Len() != 0 {
		t.Error("credits were kept for a user who has not connected Last.fm")
	}
}

func TestLearningTheCurrentTracksArtistFillsTheListenInFlight(t *testing.T) {
	a, _, _ := scrobbleApp(t)
	deferredRunner(a)
	bare := youtube.MusicItem{VideoID: "x", Title: "Searched"}
	a.current = bare
	a.beginListen(bare, false) // the lookup is held back by the runner
	if a.scrobble.listen.Track.Valid() {
		t.Fatal("the track had an artist already")
	}
	// The queue's first page arrives with the track's own entry.
	a.learnCredits(youtube.MusicItem{VideoID: "x", Title: "Searched", Artist: "Yorushika"})
	if got := a.scrobble.listen.Track.Artist; got != "Yorushika" {
		t.Errorf("artist %q, want it filled from the page that came in", got)
	}
}

func TestNothingIsSentWhenNotConnectedOrPaused(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	a.setLastFMPaused(true)
	a.beginListen(song("a"), false)
	listenTo(a, 3*time.Minute)
	a.settings.LastFMPaused = false
	a.settings.LastFMUser = ""
	a.beginListen(song("b"), false)
	listenTo(a, 3*time.Minute)
	if f.total() != 0 || a.scrobble.outbox.Len() != 0 {
		t.Errorf("sent while paused or disconnected: %v", f.calls)
	}
}

func TestATransientFailureKeepsThePlayForARestart(t *testing.T) {
	a, f, dir := scrobbleApp(t)
	f.reply = func(url.Values) (int, string) {
		return http.StatusServiceUnavailable, `{"error":11,"message":"offline"}`
	}
	a.beginListen(song("a"), false)
	listenTo(a, 3*time.Minute)
	if got := len(f.called("track.scrobble")); got != flushAttempts {
		t.Errorf("%d attempts, want %d with backoff between", got, flushAttempts)
	}
	if a.scrobble.outbox.Len() != 1 || a.scrobble.reconnect {
		t.Fatalf("pending %d, reconnect %t: a transient failure keeps the play and the session", a.scrobble.outbox.Len(), a.scrobble.reconnect)
	}
	a.scrobble.outbox.Wait()

	// The app is closed and opened again, and Last.fm is back.
	b, f2, _ := scrobbleApp(t)
	b.openScrobbler(lastFMSession(dir), filepath.Join(dir, "pending.json"))
	t.Cleanup(b.scrobble.outbox.Wait)
	if b.scrobble.outbox.Len() != 1 {
		t.Fatalf("the play did not survive the restart: %d pending", b.scrobble.outbox.Len())
	}
	b.flushScrobbles()
	calls := f2.called("track.scrobble")
	if len(calls) != 1 || calls[0].Get("timestamp[0]") != "1772366400" || calls[0].Get("track[0]") != "Song a" || calls[0].Get("chosenByUser[0]") != "1" {
		t.Fatalf("retry = %v, want the play with its original timestamp and choice", calls)
	}
	if b.scrobble.outbox.Len() != 0 {
		t.Error("the delivered play is still pending")
	}
}

func TestARefusedSessionAsksToReconnectAndKeepsThePlays(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	f.reply = func(url.Values) (int, string) {
		return http.StatusForbidden, `{"error":9,"message":"Invalid session key"}`
	}
	a.beginListen(song("a"), false)
	listenTo(a, 3*time.Minute)
	if !a.scrobble.reconnect || a.scrobble.outbox.Len() != 1 {
		t.Fatalf("reconnect %t, pending %d", a.scrobble.reconnect, a.scrobble.outbox.Len())
	}
	before := f.total()
	a.flushScrobbles()
	a.beginListen(song("b"), false)
	if f.total() != before {
		t.Error("requests kept going out with a session Last.fm refused")
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4400)
	tt.Frame()
	if !tt.HasText("Connect again") {
		t.Errorf("settings do not ask to reconnect: %q", tt.Texts())
	}
}

func TestARequestLastFmRejectsForGoodIsDroppedNotRetried(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	f.reply = func(form url.Values) (int, string) {
		for name, value := range form {
			if strings.HasPrefix(name, "track[") && value[0] == "Song bad" {
				return http.StatusBadRequest, `{"error":6,"message":"Invalid parameters"}`
			}
		}
		return http.StatusOK, `{}`
	}
	// Three plays wait together: the middle one is bad.
	for _, id := range []string{"a", "bad", "c"} {
		a.scrobble.outbox.Add(lastfm.Scrobble{Track: lastfm.Track{Artist: "X", Title: "Song " + id}, Started: time.Now().Add(-time.Hour)})
	}
	a.scrobble.session = "SK"
	a.flushScrobbles()
	if a.scrobble.outbox.Len() != 0 {
		t.Fatalf("%d plays still pending: a bad play must not hold the rest back", a.scrobble.outbox.Len())
	}
	var sent []string
	for _, call := range f.called("track.scrobble") {
		if call.Get("track[0]") != "" && call.Get("track[1]") == "" && call.Get("track[0]") != "Song bad" {
			sent = append(sent, call.Get("track[0]"))
		}
	}
	if !slices.Equal(sent, []string{"Song a", "Song c"}) {
		t.Errorf("delivered one by one: %v", sent)
	}
	if n := len(f.called("track.scrobble")); n > 6 {
		t.Errorf("%d requests: the bad play was retried", n)
	}
}

func TestConnectingPausingAndDisconnectingInSettings(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	a.settings.LastFMUser, a.scrobble.session = "", ""
	f.approvals = 2 // the user takes a moment to approve
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4400)
	tt.Frame()
	if len(f.calls) != 0 {
		t.Fatalf("Last.fm was called before the user asked: %v", f.calls)
	}

	if err := tt.Click("Connect Last.fm"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if want := "https://www.last.fm/api/auth/?api_key=key&token=TOKEN"; len(opened) != 1 || opened[0] != want {
		t.Fatalf("opened %v, want %s", opened, want)
	}
	if a.settings.LastFMUser != "neo" || a.scrobble.session != "SESSION-KEY" || a.scrobbling() == false {
		t.Fatalf("user %q, session %q: connecting enables scrobbling at once", a.settings.LastFMUser, a.scrobble.session)
	}
	if n := len(f.called("auth.getSession")); n != 3 {
		t.Errorf("%d getSession calls, want 3: two before the approval, one after", n)
	}
	if key, err := a.scrobble.store.Load(context.Background()); err != nil || key != "SESSION-KEY" {
		t.Errorf("saved session = %q, %v", key, err)
	}
	a.saver.Wait()
	if data := readFile(t, a.saver.Path); strings.Contains(data, "SESSION-KEY") || !strings.Contains(data, `"lastfmUser": "neo"`) {
		t.Errorf("settings file holds %s: the account's name belongs there, the key does not", data)
	}
	tt.Frame()
	if !tt.HasText("Connected as neo") {
		t.Fatalf("settings do not show the connection: %q", tt.Texts())
	}

	if err := tt.Click("Scrobble to Last.fm"); err != nil {
		t.Fatal(err)
	}
	if !a.settings.LastFMPaused || a.scrobbling() {
		t.Error("the switch did not pause scrobbling")
	}
	if a.settings.LastFMUser != "neo" || a.scrobble.session == "" {
		t.Error("pausing disconnected")
	}
	if err := tt.Click("Scrobble to Last.fm"); err != nil {
		t.Fatal(err)
	}
	if a.settings.LastFMPaused || !a.scrobbling() {
		t.Error("the switch did not resume scrobbling")
	}

	a.scrobble.outbox.Add(lastfm.Scrobble{Track: lastfm.Track{Artist: "A", Title: "T"}, Started: time.Now()})
	if err := tt.Click("Disconnect"); err != nil {
		t.Fatal(err)
	}
	if a.settings.LastFMUser != "" || a.scrobble.session != "" || a.scrobble.outbox.Len() != 0 {
		t.Errorf("after disconnecting: user %q, session %q, pending %d", a.settings.LastFMUser, a.scrobble.session, a.scrobble.outbox.Len())
	}
	if _, err := a.scrobble.store.Load(context.Background()); !errors.Is(err, secretfile.ErrNotSaved) {
		t.Errorf("the session key is still saved: %v", err)
	}
	before := f.total()
	a.beginListen(song("a"), false)
	listenTo(a, 3*time.Minute)
	if f.total() != before {
		t.Error("a play was sent after disconnecting")
	}
	tt.Frame()
	if !tt.HasText("Connect Last.fm") {
		t.Errorf("settings do not offer to connect again: %q", tt.Texts())
	}
}

func TestConnectingCanBeCancelled(t *testing.T) {
	a, f, _ := scrobbleApp(t)
	a.settings.LastFMUser, a.scrobble.session = "", ""
	f.approvals = 1 << 30
	gate := make(chan struct{})
	a.sleep = func(ctx context.Context, _ time.Duration) error {
		<-gate
		return ctx.Err()
	}
	a.run = func(work func()) { go work() } // the wait must not block the test
	a.openURL = func(string) error { return nil }
	a.connectLastFM()
	waitFor(t, func() bool { return len(f.called("auth.getSession")) > 0 })
	a.cancelLastFMConnect()
	close(gate)
	if a.scrobble.connect.busy() || a.settings.LastFMUser != "" {
		t.Error("cancelling left the connection going")
	}
}

func TestWithoutAnApplicationKeyLastFmCannotBeConnected(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4400)
	tt.Frame()
	if !tt.HasText("has no Last.fm application key") {
		t.Errorf("settings do not say why: %q", tt.Texts())
	}
	a.connectLastFM()
	if a.scrobble.connect.busy() {
		t.Error("connecting started without a key")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
