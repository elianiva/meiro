package main

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

type switchable struct {
	mode string
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

func stateApp(t *testing.T, s *switchable, signedIn bool) *app {
	t.Helper()
	a := newTestApp(t)
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

var pageCases = []struct {
	name, path string
	signedIn   bool
	healthy    string
	empty      string
}{
	{"home", "/home", false, "Quick picks", "Nothing to show yet"},
	{"explore", "/explore", false, "Moods", "Nothing to explore yet"},
	{"library", "/library", true, "Liked Music", "Your library is empty"},
	{"album", "/album/MPREb_test", false, "Album Track One", "This album is empty"},
	{"playlist", "/playlist/VLPL_video", false, "Playlist video", "This playlist is empty"},
	{"artist", "/artist/UCartist", false, "Artist Track", "Nothing here yet"},
}

func open(a *app, path string) *ui.Tester {
	a.router.Push(path)
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	return tt
}

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

func TestOfflinePagesSayWhyAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "offline"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, "You are offline", "Check your connection, then retry.", "Retry")
			mustNotShow(t, tt, "Could not load this page", p.healthy)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "offline"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "You are offline", "Retry")
	})
}

func TestFailedPagesShowTheErrorAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "server"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, "Could not load this page", "HTTP 500", "Retry")
			mustNotShow(t, tt, "You are offline", p.healthy)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "server"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "Could not load this page", "HTTP 500", "Retry")
	})
}

func TestEmptyPagesSayWhatIsMissingAndOfferRetry(t *testing.T) {
	for _, p := range pageCases {
		t.Run(p.name, func(t *testing.T) {
			a := stateApp(t, &switchable{mode: "empty"}, p.signedIn)
			tt := open(a, p.path)
			mustShow(t, tt, p.empty, "Retry")
			mustNotShow(t, tt, "You are offline", "Could not load this page", p.healthy)
		})
	}
	t.Run("search", func(t *testing.T) {
		a := stateApp(t, &switchable{mode: "empty"}, false)
		tt := ui.NewTester(a.view, 1000, 700)
		search(t, a, tt, "yorushika")
		mustShow(t, tt, "No results for “yorushika”", "Retry")
	})
	t.Run("stats", func(t *testing.T) {
		a := newTestApp(t)
		a.historyLoaded = true
		tt := open(a, "/stats")
		mustShow(t, tt, "No plays yet", "14 days")
		mustNotShow(t, tt, "Retry", "Most played")
	})
}

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

func TestStaleErrorDoesNotReachAnotherPage(t *testing.T) {
	s := &switchable{mode: "offline"}
	a := stateApp(t, s, false)
	var held []func()
	a.run = func(work func()) { held = append(held, work) }
	tt := open(a, "/home")
	s.mode = ""
	a.router.Push("/explore")
	tt.Frame()
	for _, work := range held {
		work()
	}
	tt.Frame()
	mustShow(t, tt, "Moods")
	mustNotShow(t, tt, "You are offline", "Retry")
	a.run = func(work func()) { work() }
	a.router.Push("/home")
	tt.Frame()
	mustShow(t, tt, "Quick picks")
	mustNotShow(t, tt, "You are offline")
}
