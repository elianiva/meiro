package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/youtube"
)

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
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			t.Error("a request that failed read the browser again")
			return "", nil
		}}}
	}
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

// YouTube retires the cookie of a browser session as the browser goes on using
// it. A saved session it no longer recognises is read from the browser the
// sign-in was imported from, and the session that reads replaces it.
func TestRestoreReadsTheBrowserWhenYouTubeRefusesTheSavedSession(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			return "SAPISID=fresh", nil
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}

	a.restoreAccount()
	if !a.signedIn || a.authed == nil {
		t.Fatalf("the session read from the browser did not sign in: signedIn=%v", a.signedIn)
	}
	if a.ytDlpCookie != "SAPISID=fresh" {
		t.Errorf("yt-dlp cookie = %q, want the session read from the browser", a.ytDlpCookie)
	}
	if store.cookie != "SAPISID=fresh" {
		t.Errorf("saved cookie = %q, want the session read from the browser", store.cookie)
	}
	if a.notice != "" {
		t.Errorf("a session that was replaced reported %q", a.notice)
	}
}

// Without a browser to read, a refused session is reported as it was.
func TestRestoreWithoutABrowserKeepsTheRefusal(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}

	a.restoreAccount()
	if a.signedIn {
		t.Fatalf("a refused session signed in: signedIn=%v", a.signedIn)
	}
	if !strings.Contains(a.notice, "Could not restore") || !strings.Contains(a.notice, "did not recognise") {
		t.Errorf("the refusal was not surfaced: %q", a.notice)
	}
	if store.cookie != "SAPISID=stale" {
		t.Errorf("saved cookie = %q, want the one that was already there", store.cookie)
	}
}

// A browser that is not signed in explains itself, and leaves the saved
// session alone.
func TestRestoreReportsWhyTheBrowserCouldNotReplaceTheSession(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			return "", errors.New("Helium has no YouTube session; sign in at music.youtube.com there first")
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}

	a.restoreAccount()
	if a.signedIn {
		t.Fatalf("a session that was not replaced signed in: signedIn=%v", a.signedIn)
	}
	if !strings.Contains(a.notice, "did not recognise") || !strings.Contains(a.notice, "sign in at music.youtube.com") {
		t.Errorf("the notice does not say why the browser could not replace the session: %q", a.notice)
	}
	if store.cookie != "SAPISID=stale" {
		t.Errorf("saved cookie = %q, want the one that was already there", store.cookie)
	}
}

// A session read from the browser that YouTube refuses too is not kept.
func TestRestoreDoesNotKeepASessionYouTubeAlsoRefuses(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			return "SAPISID=also-stale", nil
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}

	a.restoreAccount()
	if a.signedIn {
		t.Fatalf("a session YouTube refused signed in: signedIn=%v", a.signedIn)
	}
	if !strings.Contains(a.notice, "did not recognise") {
		t.Errorf("the refusal was not surfaced: %q", a.notice)
	}
	if store.cookie != "SAPISID=stale" {
		t.Errorf("saved cookie = %q, want the one that was already there", store.cookie)
	}
}

// A session read from the browser after the user signed out is not kept: the
// sign-out is newer than the refresh.
func TestARefreshDoesNotUndoASignOut(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.settings.SignInSource = "Helium"
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}
	var pending []func()
	a.run = func(work func()) { pending = append(pending, work) }
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			// The user signs out while the browser is being read.
			a.signOut()
			return "SAPISID=fresh", nil
		}}}
	}

	a.restoreAccount()
	pending[0]()
	if store.cookie != "SAPISID=stale" {
		t.Errorf("a refresh that outran the sign-out saved %q", store.cookie)
	}
	if a.signedIn || a.ytDlpCookie != "" {
		t.Errorf("a refresh that outran the sign-out signed in: signedIn=%v", a.signedIn)
	}
	for _, work := range pending[1:] {
		work()
	}
	if store.cookie != "" {
		t.Errorf("the sign-out left %q saved", store.cookie)
	}
}

// A sign-in remembers the browser it came from, and a pasted one forgets it.
func TestSignInRemembersTheBrowserItCameFrom(t *testing.T) {
	a := newTestApp()
	a.store = &memoryStore{}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=abc"}},
		})
	}

	a.signInWithGoogle()
	a.signIn.cookie = "SAPISID=abc"
	a.finishSignIn(context.Background(), "SAPISID=abc", a.signIn.generation, "", "Helium")
	if a.settings.SignInSource != "Helium" {
		t.Errorf("a session imported from Helium remembers %q", a.settings.SignInSource)
	}

	a.signInWithGoogle()
	a.signIn.cookie = "SAPISID=abc"
	a.finishSignIn(context.Background(), "SAPISID=abc", a.signIn.generation, "", "")
	if a.settings.SignInSource != "" {
		t.Errorf("a pasted session still names the browser %q", a.settings.SignInSource)
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

	a.finishSignIn(context.Background(), "SAPISID=abc", attempt, "", "")
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

// countingMusic answers the way refusingMusic does, and counts the library
// landings it serves, so a test can see that a page loaded again.
type countingMusic struct {
	fresh string
	loads *atomic.Int32
}

func (m countingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(strings.NewReader(string(body)))
		if strings.Contains(string(body), "FEmusic_library_landing") {
			m.loads.Add(1)
		}
	}
	return refusingMusic{fresh: m.fresh}.RoundTrip(request)
}

// The session a browser replaces is replaced here too: a running app asks
// YouTube whether it still has one, and reads the browser when the answer is
// no.
func TestASessionYouTubeRetiredIsReplacedWhileRunning(t *testing.T) {
	var loads atomic.Int32
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.signedIn, a.ytDlpCookie = true, "SAPISID=stale"
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			return "SAPISID=fresh", nil
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: countingMusic{fresh: "SAPISID=fresh", loads: &loads}},
		})
	}
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	a.checkSignIn()

	if a.ytDlpCookie != "SAPISID=fresh" {
		t.Errorf("the session in use is %q, want the one read from the browser", a.ytDlpCookie)
	}
	if store.cookie != "SAPISID=fresh" {
		t.Errorf("saved cookie = %q, want the one read from the browser", store.cookie)
	}
	if !a.signedIn || a.notice != "" {
		t.Errorf("replacing the session reported signedIn=%v notice=%q", a.signedIn, a.notice)
	}
	if got := loads.Load(); got != 1 {
		t.Errorf("the page loaded again %d times, want once, with the account", got)
	}
}

// A session YouTube still takes is left alone: the check costs a request, and
// changes nothing else.
func TestASessionYouTubeStillTakesIsLeftAlone(t *testing.T) {
	var loads atomic.Int32
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=live"}
	a.store = store
	a.signedIn, a.ytDlpCookie = true, "SAPISID=live"
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			t.Error("the browser was read although YouTube still takes the session")
			return "", nil
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: countingMusic{fresh: "SAPISID=live", loads: &loads}},
		})
	}
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	a.checkSignIn()

	if a.ytDlpCookie != "SAPISID=live" || store.cookie != "SAPISID=live" {
		t.Errorf("a session YouTube still takes was changed: in use %q, saved %q", a.ytDlpCookie, store.cookie)
	}
	if !a.signedIn || a.notice != "" {
		t.Errorf("a check that found nothing reported signedIn=%v notice=%q", a.signedIn, a.notice)
	}
	if got := loads.Load(); got != 0 {
		t.Errorf("the page loaded %d times, want it left as it was", got)
	}
}

// A session the browser cannot replace stops acting signed in, and says why.
func TestASessionThatCannotBeReplacedStopsActingSignedIn(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=stale"}
	a.store = store
	a.signedIn, a.ytDlpCookie = true, "SAPISID=stale"
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			return "", errors.New("Helium has no YouTube session; sign in at music.youtube.com there first")
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}

	a.checkSignIn()

	if a.signedIn || a.authed != nil {
		t.Errorf("a session YouTube refuses left signedIn=%v", a.signedIn)
	}
	if !strings.Contains(a.notice, "did not recognise") || !strings.Contains(a.notice, "sign in at music.youtube.com") {
		t.Errorf("the notice does not say why: %q", a.notice)
	}
	if store.cookie != "SAPISID=stale" {
		t.Errorf("saved cookie = %q, want the one that was already there", store.cookie)
	}
}

// A request that fails for another reason says nothing about the session.
func TestATransientFailureLeavesTheSessionAlone(t *testing.T) {
	a := newTestApp()
	a.store = &memoryStore{cookie: "SAPISID=live"}
	a.signedIn, a.ytDlpCookie = true, "SAPISID=live"
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			t.Error("the browser was read for a request that failed")
			return "", nil
		}}}
	}
	a.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: failingMusic{}},
		})
	}

	a.checkSignIn()

	if !a.signedIn || a.ytDlpCookie != "SAPISID=live" || a.notice != "" {
		t.Errorf("a failed request changed the session: signedIn=%v cookie=%q notice=%q", a.signedIn, a.ytDlpCookie, a.notice)
	}
}

// A signed-out app asks YouTube nothing.
func TestASessionCheckDoesNothingWhileSignedOut(t *testing.T) {
	a := newTestApp()
	a.store = &memoryStore{}
	a.signedIn, a.ytDlpCookie = false, ""
	a.settings.SignInSource = "Helium"
	a.signInSources = func() []importSource {
		return []importSource{{label: "Helium", read: func(context.Context) (string, error) {
			t.Error("a signed-out app read the browser")
			return "", nil
		}}}
	}
	a.checkSignIn()
	if a.signedIn || a.notice != "" {
		t.Errorf("a signed-out check changed the app: signedIn=%v notice=%q", a.signedIn, a.notice)
	}
}
