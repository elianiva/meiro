package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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
