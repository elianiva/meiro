package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

// These tests drive the whole app through its window, the way a user does:
// real pages, real clicks, and a fake YouTube at the network edge.

// signedInApp is an app whose account client speaks for a saved session.
func signedInApp(t *testing.T) *app {
	t.Helper()
	a := newTestApp()
	auth, err := youtube.NewCookieAuth("SAPISID=test", youtube.CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.account.session = &session{client: a.account.newClient(auth), cookie: "SAPISID=test"}
	return a
}

func TestEveryPageLoadsAndDraws(t *testing.T) {
	pages := []struct {
		path string
		want []string
	}{
		{"/home", []string{greeting(time.Now()), "Quick picks", "Ambient One"}},
		{"/explore", []string{"Moods", "Night Drive"}},
		{"/library", []string{"Library", "Your library lives here"}},
		{"/search", []string{"Find your next favourite"}},
		{"/settings", []string{"Settings", "Appearance"}},
		{"/album/MPREb_test", []string{"Album Track One"}},
		{"/playlist/VLPL_video", []string{"Playlist video"}},
		{"/artist/UCartist", []string{"Aurora Vale", "Artist Track"}},
		{"/recap", []string{"Your recap lives here"}},
		{"/nowhere", []string{"That page does not exist."}},
	}
	for _, page := range pages {
		t.Run(page.path, func(t *testing.T) {
			a := newTestApp()
			a.router.Push(page.path)
			tt := ui.NewTester(a.view, 1000, 700)
			tt.Frame()
			for _, want := range page.want {
				if !tt.HasText(want) {
					t.Errorf("%s did not show %q: %q", page.path, want, tt.Texts())
				}
			}
		})
	}
}

func TestSearchFlow(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if !tt.HasText("Yorushika songs") {
		t.Fatalf("suggestions are missing: %q", tt.Texts())
	}
	if !tt.HasText("16M monthly audience") {
		t.Fatalf("the items the query matches are missing: %q", tt.Texts())
	}
	if searches.Load() != 0 {
		t.Fatal("typing ran a search")
	}

	if err := tt.Click("Yorushika songs"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Search Result Song") || searches.Load() != 1 {
		t.Fatalf("choosing a suggestion did not show results: %q", tt.Texts())
	}
	if len(a.settings.Recent) == 0 || a.settings.Recent[0] != "Yorushika songs" {
		t.Errorf("the search was not remembered: %v", a.settings.Recent)
	}

	// The items the query matches are chosen the same way: the panel opens
	// what it names.
	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if err := tt.Click("16M monthly audience"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.path(), "/artist/UCyorushika") {
		t.Fatalf("choosing an item opened %q", a.path())
	}
}

// The search field is in the top bar of every page, and submitting it there
// shows the search page with what it ran.
func TestSearchingFromAnotherPage(t *testing.T) {
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)

	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Type("yorushika")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if r, _ := a.route(); r.id != routeSearch {
		t.Fatalf("a search from settings left the app on %q", a.path())
	}
	if !tt.HasText("Search Result Song") || searches.Load() != 1 {
		t.Fatalf("the search did not run: %q", tt.Texts())
	}
	if a.search.query != "yorushika" {
		t.Errorf("the field reads %q", a.search.query)
	}
}

// A search shows the filters YouTube offers for it, without one of the app's
// own, and each one searches the query again with the parameters it carries.
func TestSearchShowsYouTubesFilters(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if !tt.HasText("Songs") || !tt.HasText("Albums") || tt.HasText("All") {
		t.Fatalf("the filters are wrong: %q", tt.Texts())
	}

	if err := tt.Click("Songs"); err != nil {
		t.Fatal(err)
	}
	if searches.Load() != 1 || !strings.Contains(searchBody(), "EgWKAQIIAWoSEAUQAxAJ") {
		t.Fatalf("the filter did not search with its parameters: %q", searchBody())
	}
	if !tt.HasText("Filtered Song") || a.search.params != "EgWKAQIIAWoSEAUQAxAJ" {
		t.Fatalf("the filter did not show its own results: %q", tt.Texts())
	}

	if err := tt.Click("Clear filters"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Search Result Song") || a.search.params != "" || searches.Load() != 2 {
		t.Fatalf("clearing the filter did not search again: %q", tt.Texts())
	}
}

// The card a search leads with names what the search found, and selecting it
// opens that page.
func TestSearchLeadsWithItsTopResult(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if len(a.page.rows) == 0 || a.page.rows[0].kind != rowTopResult {
		t.Fatalf("the results do not lead with a card: %#v", a.page.rows)
	}
	if !tt.HasText("16M monthly audience") {
		t.Fatalf("the card shows no subtitle: %q", tt.Texts())
	}
	if err := tt.Click("Yorushika"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.path(), "/artist/") {
		t.Fatalf("the card opened %q", a.path())
	}
}

// A search shows the pages of its results itself as its list reaches the end of
// what it has, rather than offering the button that asks.
func TestSearchLoadsTheNextPageItself(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	a.runSearch("yor", "")
	searchesMore.Store(0)
	// The list reaching the end of what it has is what asks for the page
	// after it: the frame that lays the results out is enough.
	tt := ui.NewTester(a.view, 1000, 700)
	if searchesMore.Load() == 0 {
		t.Fatal("the search did not ask for the page after its results")
	}
	tt.Frame()
	if !tt.HasText("Later Song") {
		t.Fatalf("the next page did not arrive: %q", tt.Texts())
	}
	for _, r := range a.page.rows {
		if r.kind == rowMore {
			t.Fatalf("a search offers the button that asks: %q", tt.Texts())
		}
	}
}

func TestBrowsingFromHomeIntoAnAlbum(t *testing.T) {
	a := newTestApp()
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	if err := tt.Click("Deep Focus"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !strings.HasPrefix(a.router.Path(), "/album/") || !tt.HasText("Album Track One") {
		t.Fatalf("the album did not open: %q at %q", tt.Texts(), a.router.Path())
	}
}

func TestSettingsPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp()
	a.saver.Path = path
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	if err := tt.Click("Dark"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Rose"); err != nil {
		t.Fatal(err)
	}
	a.saver.Save(&a.settings)
	a.saver.Wait()

	restarted := newTestApp()
	restarted.settings = settings.Load(path)
	if restarted.settings.Mode != "dark" || restarted.settings.Seed != "#d81b78" {
		t.Errorf("settings came back as %+v", restarted.settings)
	}
}

func TestMakingAPlaylistFromTheLibrary(t *testing.T) {
	libraryLoads.Store(0)
	playlistCreates.Store(0)
	a := signedInApp(t)
	a.router.Push("/library")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()

	if err := tt.Click("New playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	a.newPlaylist.name = "Late night"
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if playlistCreates.Load() != 1 || a.newPlaylist != nil {
		t.Fatalf("playlist creates = %d, dialog %#v", playlistCreates.Load(), a.newPlaylist)
	}
	tt.Frame()
	if !tt.HasText("Created “Late night”") {
		t.Errorf("the playlist was not announced: %q", tt.Texts())
	}
}

func TestAddingASongToAPlaylist(t *testing.T) {
	a := signedInApp(t)
	tt := ui.NewTester(a.view, 1180, 850)
	card, ok := tt.Find("Ambient One")
	if !ok {
		t.Fatalf("the home page has no song: %q", tt.Texts())
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for Ambient One"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Add to playlist"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Add to Hype"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	write := written()
	if !strings.HasSuffix(write.endpoint, "/browse/edit_playlist") || write.body["playlistId"] != "PLhype" {
		t.Fatalf("write = %#v", write)
	}
	tt.Frame()
	if !tt.HasText("Added “Ambient One” to “Hype”") {
		t.Errorf("the song was not announced: %q", tt.Texts())
	}
}

func TestSessionRestoreAndSignOut(t *testing.T) {
	a := newTestApp()
	store := &memoryStore{cookie: "SAPISID=fresh"}
	a.account.creds.store = store
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}
	a.restoreAccount()
	if !a.signedIn() {
		t.Fatalf("the saved session did not sign in; notice %q", a.notice)
	}
	a.signOut()
	if a.signedIn() || store.cookie != "" {
		t.Errorf("sign-out left signedIn=%v, saved cookie %q", a.signedIn(), store.cookie)
	}
}

func TestRefusedSessionStaysSignedOut(t *testing.T) {
	a := newTestApp()
	a.account.creds.store = &memoryStore{cookie: "SAPISID=stale"}
	a.settings.SignInSource = "Browser"
	a.account.sources = func() []cookies.Source {
		return []cookies.Source{{Label: "Browser", Read: func(context.Context) (string, error) {
			return "SAPISID=stale", nil
		}}}
	}
	a.account.newClient = func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: refusingMusic{fresh: "SAPISID=fresh"}},
		})
	}
	a.restoreAccount()
	if a.signedIn() {
		t.Fatal("a session YouTube refuses signed the user in")
	}
	if !strings.Contains(a.notice, "Could not restore") {
		t.Errorf("the refusal was not surfaced: %q", a.notice)
	}
}

// Plays recorded while the history is still being read are kept, in front of
// nothing lost and behind what the file held.
func TestHistoryLoadMergesPendingPlays(t *testing.T) {
	a := newTestApp()
	a.historyPath = filepath.Join(t.TempDir(), "history.json")
	var finish func()
	a.run = func(work func()) { finish = work } // the read is held back
	a.loadHistory()
	a.recordHistory(youtube.MusicItem{VideoID: "new", Title: "New"})
	if err := os.WriteFile(a.historyPath, []byte(`[{"videoId":"old","playedAt":"2026-01-01T00:00:00Z"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	a.run = func(work func()) { work() }
	finish()
	if len(a.history) != 2 || a.history[0].VideoID != "old" || a.history[1].VideoID != "new" {
		t.Fatalf("history = %+v", a.history)
	}
}

func TestOpeningTheLogsFolderFromSettings(t *testing.T) {
	a := newTestApp()
	a.logsDir = "/logs"
	var opened []string
	a.openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 4000)
	if err := tt.Click("Open logs folder"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "/logs" {
		t.Errorf("opened %v, want the logs folder", opened)
	}
}

// Report a problem opens the issue form with the versions filled in, and
// never carries the account.
func TestReportProblemOpensPrefilledIssue(t *testing.T) {
	a := signedInApp(t)
	var opened []string
	a.openURL = func(link string) error { opened = append(opened, link); return nil }
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()

	if err := tt.Click("Report a problem"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 {
		t.Fatalf("opened %d links, want 1", len(opened))
	}
	u, err := url.Parse(opened[0])
	if err != nil || u.Host != "github.com" || u.Path != "/elianiva/meiro/issues/new" {
		t.Fatalf("unexpected link %s (%v)", opened[0], err)
	}
	if strings.Contains(opened[0], "SAPISID") {
		t.Error("the link carries the session cookie")
	}
	body := u.Query().Get("body")
	for _, want := range []string{"- Meiro: ", "- OS: " + runtime.GOOS, "- yt-dlp: ", "- ffmpeg: "} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
}
