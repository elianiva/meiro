package main

import (
	"errors"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/youtube"
)

// switchable is a fake YouTube that can be told to fail or to answer with
// nothing, and then to recover, as a network that comes back.
type switchable struct {
	mode string // "", "offline", "server" or "empty"
	asks int
}

func (s *switchable) RoundTrip(request *http.Request) (*http.Response, error) {
	s.asks++
	switch s.mode {
	case "offline":
		return nil, errors.New("dial tcp: lookup music.youtube.com: no such host")
	case "server":
		return &http.Response{StatusCode: http.StatusInternalServerError, Status: "500 Internal Server Error", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("boom")), Request: request}, nil
	case "empty":
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	}
	return fakeMusic{}.RoundTrip(request)
}

// stateApp is a test app, signed in or not, whose YouTube answers through s.
func stateApp(t *testing.T, s *switchable, signedIn bool) *app {
	t.Helper()
	a := newTestApp()
	make := func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: s}, CookieAuth: auth})
	}
	a.account.newClient = make
	a.account.public = make(nil)
	if signedIn {
		auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
		if err != nil {
			t.Fatal(err)
		}
		a.account.session = &session{client: make(auth), cookie: "SAPISID=test"}
	}
	return a
}

// shot saves what the tester drew, when STATES_DIR names a place for it.
func shot(t *testing.T, tt *ui.Tester, name string) {
	t.Helper()
	dir := os.Getenv("STATES_DIR")
	if dir == "" {
		return
	}
	f, err := os.Create(filepath.Join(dir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
	t.Logf("%s texts: %q", name, tt.Texts())
}

// pageCases are the pages that load their content, and what each shows when
// it has loaded. search is run from the bar, so it is not among them.
var pageCases = []struct {
	name, path string
	signedIn   bool
	healthy    string
	empty      string
}{
	{"home", "/home", false, "Quick picks", "Nothing to show yet"},
	{"explore", "/explore", false, "Moods", "Nothing to explore yet"},
	{"library", "/library", true, "Liked Music", "Your library is empty"},
	{"recap", "/recap", true, "Top song of the year", "No recap yet"},
	{"album", "/album/MPREb_test", false, "Album Track One", "This album is empty"},
	{"playlist", "/playlist/VLPL_video", false, "Playlist video", "This playlist is empty"},
	{"artist", "/artist/UCartist", false, "Artist Track", "Nothing here yet"},
}

// open shows a page in a window.
func open(a *app, path string) *ui.Tester {
	a.router.Push(path)
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	return tt
}

// search runs a search from the bar of a window.
func search(t *testing.T, a *app, tt *ui.Tester, query string) {
	t.Helper()
	a.router.Push("/search")
	tt.Frame()
	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type(query)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
}

// mustShow fails the test unless the window shows every text.
func mustShow(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !tt.HasText(text) {
			t.Errorf("missing %q in %q", text, tt.Texts())
		}
	}
}

func mustNotShow(t *testing.T, tt *ui.Tester, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if tt.HasText(text) {
			t.Errorf("unexpected %q in %q", text, tt.Texts())
		}
	}
}

// With no network, every page that loads says so, and offers Retry.
func TestOfflinePagesSayWhyAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "offline"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, "You are offline", "Check your connection, then retry.", "Retry")
			mustNotShow(t, tt, "Could not load this page", p.healthy)
			shot(t, tt, "offline-"+p.name)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "offline"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "You are offline", "Retry")
		shot(t, tt, "offline-search")
	})
}

// When YouTube answers badly, the page says it could not load, with what it
// was told, and offers Retry.
func TestFailedPagesShowTheErrorAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "server"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, "Could not load this page", "HTTP 500", "Retry")
			mustNotShow(t, tt, "You are offline", p.healthy)
			shot(t, tt, "failed-"+p.name)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "server"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "Could not load this page", "HTTP 500", "Retry")
		shot(t, tt, "failed-search")
	})
}

// A page that loaded with nothing on it says so, and offers Retry.
func TestEmptyPagesSayWhatIsMissingAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "empty"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, p.empty, "Retry")
			mustNotShow(t, tt, "You are offline", "Could not load this page", p.healthy)
			shot(t, tt, "empty-"+p.name)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "empty"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "No results for “yorushika”", "Retry")
		shot(t, tt, "empty-search")
	})
	t.Run("stats", func(t *testing.T) {
		a := newTestApp()
		a.historyLoaded = true
		tt := open(a, "/stats")
		mustShow(t, tt, "No plays yet", "14 days")
		mustNotShow(t, tt, "Retry", "Most played")
		shot(t, tt, "empty-stats")
	})
}

// Controls: a page that loads fine shows no error and no Retry, and stats
// with plays on record show their numbers, not the empty state.
func TestHealthyPagesShowNoStateMessage(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, p.healthy)
			mustNotShow(t, tt, "Retry", "You are offline", "Could not load this page", p.empty)
			shot(t, tt, "healthy-"+p.name)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustNotShow(t, tt, "Retry", "No results for “yorushika”")
	})
	t.Run("stats", func(t *testing.T) {
		a := newTestApp()
		a.historyLoaded = true
		a.history = []history.Entry{{VideoID: "v", Title: "Song", Artist: "Someone", PlayedAt: time.Now()}}
		tt := open(a, "/stats")
		mustShow(t, tt, "Most played", "Song")
		mustNotShow(t, tt, "No plays yet")
	})
}

// Retry goes through the page's own load: once the network is back, it shows
// the page and the message is gone.
func TestRetryRecoversAndClearsTheMessage(t *testing.T) {
	for _, mode := range []string{"offline", "server", "empty"} {
		for _, p := range pageCases {
			t.Run(mode+"-"+p.name, func(t *testing.T) {
				s := &switchable{mode: mode}
				a := stateApp(t, s, p.signedIn)
				tt := open(a, p.path)
				mustShow(t, tt, "Retry")
				s.mode = ""
				if err := tt.Click("Retry"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				mustShow(t, tt, p.healthy)
				mustNotShow(t, tt, "Retry", "You are offline", "Could not load this page", p.empty)
				shot(t, tt, "recovered-"+mode+"-"+p.name)
			})
		}
	}
	t.Run("search", func(t *testing.T) {
		s := &switchable{mode: "offline"}
		a := stateApp(t, s, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "You are offline")
		s.mode = ""
		if err := tt.Click("Retry"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		mustShow(t, tt, "Yorushika")
		mustNotShow(t, tt, "Retry", "You are offline")
	})
	t.Run("still failing keeps the message", func(t *testing.T) {
		s := &switchable{mode: "offline"}
		a := stateApp(t, s, false)
		tt := open(a, "/home")
		asked := s.asks
		if err := tt.Click("Retry"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if s.asks == asked {
			t.Error("Retry asked nothing")
		}
		mustShow(t, tt, "You are offline", "Retry")
	})
}

// A failure belongs to the page that asked: one that comes back after the
// user moved on is dropped, and the page now shown keeps its own content.
func TestStaleErrorDoesNotReachAnotherPage(t *testing.T) {
	s := &switchable{mode: "offline"}
	a := stateApp(t, s, false)
	var held []func()
	a.run = func(work func()) { held = append(held, work) }
	tt := open(a, "/home") // the home load is held back
	s.mode = ""
	a.router.Push("/explore")
	tt.Frame() // the explore load is held back too
	for _, work := range held {
		work() // home fails now, after explore began
	}
	tt.Frame()
	mustShow(t, tt, "Moods")
	mustNotShow(t, tt, "You are offline", "Retry")
	// Going back to home loads it afresh rather than showing the dropped error.
	a.run = func(work func()) { work() }
	a.router.Push("/home")
	tt.Frame()
	mustShow(t, tt, "Quick picks")
	mustNotShow(t, tt, "You are offline")
}
