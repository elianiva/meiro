package main

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

func TestItemMenuOpensAndClosesOnItsItem(t *testing.T) {
	var m itemMenu
	if m.opened("a") || m.owns("a") {
		t.Fatal("a fresh menu belongs to an item")
	}
	m.show("a", false, 5, 6)
	if !m.opened("a") || m.opened("b") || !m.owns("a") {
		t.Fatalf("menu not open on a only: %+v", m)
	}
	m.close()
	if m.opened("a") || !m.owns("a") {
		t.Fatalf("closing should keep the item for its exit: %+v", m)
	}
}

func TestItemMenuAnchorsAtThePointerOnlyWhenPointerOpened(t *testing.T) {
	var m itemMenu
	m.show("a", true, 12, 34)
	if !m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("pointer anchor lost: %+v", m)
	}
	// A later menu-button open on another item forgets the pointer, and keeps
	// the old coordinates from leaking into its anchor choice.
	m.show("b", false, 99, 99)
	if m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("button open kept pointer anchor: %+v", m)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("button-opened menu has a pointer anchor")
		}
		m.show("a", true, 1, 2)
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("pointer anchor offered to another item")
		}
		if _, ok := m.pointerAnchor(c, "a"); !ok {
			t.Error("pointer anchor missing on its item")
		}
	}, 100, 100)
	tt.Frame()
}

func TestThemesAreIsolatedPerWindow(t *testing.T) {
	var wg sync.WaitGroup
	for i, seed := range []string{"#d81b78", "#1b78d8", "#78d81b", "#6750a4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			color, _ := parseSeed(seed)
			want := m3.New(m3.Config{Seed: color, Font: "Font" + string(rune('A'+i))}, false)
			var got *m3.Theme
			tt := ui.NewTester(func(c *ui.Context) {
				m3.Provide(c, want)
				got = m3.Of(c)
			}, 100, 100)
			for range 20 {
				tt.Frame()
				if got != want || got.UI(ui.LightTheme()).Font != want.Font {
					t.Errorf("window %d saw another window's theme", i)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// shuffleMenuLibrary serves a library holding one playlist card, and the
// playlist that card reads.
type shuffleMenuLibrary struct{}

func (shuffleMenuLibrary) RoundTrip(request *http.Request) (*http.Response, error) {
	body := ""
	if request.Body != nil {
		data, _ := io.ReadAll(request.Body)
		request.Body = io.NopCloser(strings.NewReader(string(data)))
		body = string(data)
	}
	send := func(text string) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(text)),
			Request:    request,
		}, nil
	}
	switch {
	case strings.Contains(body, "VLPLshuffle"):
		return send(shufflePlaylistBody)
	case strings.Contains(body, "FEmusic_library_landing"):
		return send(shuffleLibraryBody)
	}
	return fakeMusic{}.RoundTrip(request)
}

// shuffleLibraryBody holds one playlist card.
const shuffleLibraryBody = `{"contents":{"musicCarouselShelfRenderer":{"contents":[
	{"musicTwoRowItemRenderer":{
		"title":{"runs":[{"text":"Shuffled Mix"}]},
		"subtitle":{"runs":[{"text":"Playlist"}]},
		"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLshuffle"}}
	}}
]}}}`

// shufflePlaylistBody is the playlist that card reads, three songs long.
const shufflePlaylistBody = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"one","title":{"simpleText":"One"},"lengthText":{"simpleText":"1:00"}}},
	{"playlistVideoRenderer":{"videoId":"two","title":{"simpleText":"Two"},"lengthText":{"simpleText":"2:00"}}},
	{"playlistVideoRenderer":{"videoId":"three","title":{"simpleText":"Three"},"lengthText":{"simpleText":"3:00"}}}
]}}}`

// The menu of a playlist card offers shuffle play, which puts the whole
// playlist on from a song drawn at random.
func TestAPlaylistCardMenuOffersShufflePlay(t *testing.T) {
	a := newTestApp()
	client := youtube.NewClient(youtube.Options{APIKey: "test", HTTPClient: &http.Client{Transport: shuffleMenuLibrary{}}})
	a.public, a.authed, a.signedIn = client, client, true
	tt := ui.NewTester(a.view, 1000, 700)
	a.router.Push("/library")
	for i := 0; i < 20 && len(a.feed.sections) == 0; i++ {
		tt.Frame()
	}
	tt.Frame()
	card, ok := tt.Find("Shuffled Mix")
	if !ok {
		t.Fatalf("the library has no playlist card: %q", tt.Texts())
	}
	tt.Move(card.X+card.W/2, card.Y+card.H/2)
	tt.Frame()
	if err := tt.Click("More options for Shuffled Mix"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Shuffle play") {
		t.Fatalf("the playlist menu does not offer shuffle play: %q", tt.Texts())
	}
	if err := tt.Click("Shuffle play"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !a.shuffle {
		t.Error("shuffle play left shuffle off")
	}
	if len(a.queue) < 3 {
		t.Fatalf("shuffle play queued %d of the playlist's 3 songs", len(a.queue))
	}
	held := map[string]int{}
	for _, item := range a.queue[:3] {
		held[item.VideoID]++
	}
	for _, id := range []string{"one", "two", "three"} {
		if held[id] != 1 {
			t.Fatalf("the queue holds the playlist as %v", held)
		}
	}
	opened := map[string]bool{a.queue[a.index].VideoID: true}
	for range 40 {
		a.playCollection(youtube.MusicItem{BrowseID: "VLPLshuffle", Title: "Shuffled Mix"}, "", 0, true)
		opened[a.queue[a.index].VideoID] = true
	}
	if len(opened) < 2 {
		t.Errorf("shuffle play always opened on %v", opened)
	}
}
