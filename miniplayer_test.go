package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

func TestTrayIconUsesPlatformAppropriateMark(t *testing.T) {
	for _, tc := range []struct {
		goos     string
		mark     settings.TrayMark
		name     string
		template bool
	}{
		{goos: "windows", mark: settings.TrayMarkOMark, name: "icon.png", template: false},
		{goos: "darwin", mark: settings.TrayMarkWordmark, name: "tray-wordmark.png", template: true},
		{goos: "darwin", mark: settings.TrayMarkOMark, name: "tray-o-mark.png", template: true},
		{goos: "linux", mark: settings.TrayMarkWordmark, name: "tray-wordmark.png", template: true},
		{goos: "linux", mark: settings.TrayMarkOMark, name: "tray-o-mark.png", template: true},
		{goos: "freebsd", mark: settings.TrayMarkOMark, name: "tray-wordmark.png", template: true},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			if got := trayIconName(tc.goos, tc.mark); got != tc.name {
				t.Errorf("trayIconName(%q, %q) = %q, want %q", tc.goos, tc.mark, got, tc.name)
			}
			if got := trayIconTemplate(tc.goos); got != tc.template {
				t.Errorf("trayIconTemplate(%q) = %v, want %v", tc.goos, got, tc.template)
			}
		})
	}
}

func TestTrayIconStyleCanBeChangedAndSavedInSettings(t *testing.T) {
	a := newTestApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	tt.Frame()
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("tray")
	tt.Frame()
	if a.settings.TrayMark != settings.TrayMarkWordmark || !tt.HasText("Wordmark") || !tt.HasText("O mark") {
		t.Fatalf("tray icon choices or default are wrong: mark=%q, text=%q", a.settings.TrayMark, tt.Texts())
	}
	if err := tt.Click("O mark"); err != nil {
		t.Fatal(err)
	}
	a.saver.Wait()
	if got := settings.Load(a.saver.Path).TrayMark; got != settings.TrayMarkOMark {
		t.Fatalf("saved tray mark = %q, want %q", got, settings.TrayMarkOMark)
	}
	if err := tt.Click("Wordmark"); err != nil {
		t.Fatal(err)
	}
	a.saver.Wait()
	if got := settings.Load(a.saver.Path).TrayMark; got != settings.TrayMarkWordmark {
		t.Errorf("saved tray mark = %q, want %q", got, settings.TrayMarkWordmark)
	}
}

func TestMiniPlayerShowsAndControlsWhatPlays(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	tt.Frame()
	if !tt.HasText("Nothing playing") {
		t.Fatalf("an idle player says nothing about it: %q", tt.Texts())
	}

	a.queue.restore(playstate.State{Items: tracks("a", "b"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt.Frame()
	if !tt.HasText("Track a") || tt.HasText("Nothing playing") {
		t.Fatalf("the playing track is not shown: %q", tt.Texts())
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "b" || !tt.HasText("Track b") {
		t.Fatalf("Next did not move on: current %q, shown %q", a.current.VideoID, tt.Texts())
	}
	if err := tt.Click("Mute"); err != nil {
		t.Fatal(err)
	}
	if a.volume != 0 {
		t.Fatalf("clicking Mute did not mute: volume = %v", a.volume)
	}
}

func TestGoToArtistFromMiniPlayerOpensTheArtistPage(t *testing.T) {
	a := newTestApp(t)
	track := youtube.MusicItem{
		VideoID: "vid-3", BrowseID: fakeArtist, Kind: youtube.KindTrack,
		Title: "Album Track One", Subtitle: "Aurora Vale", Duration: "3:42",
	}
	a.queue.restore(playstate.State{Items: []youtube.MusicItem{track}, Index: 0, RecStart: -1})
	a.current = a.queue.current()
	a.setMiniShown(true)

	main := ui.NewTester(a.view, 1000, 700)
	main.Frame()
	mini := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	mini.Frame()
	if err := mini.Click("More options for Album Track One"); err != nil {
		t.Fatal(err)
	}
	mini.Frame()
	if !mini.HasText("Go to artist") {
		t.Fatalf("the mini player's track menu lacks Go to artist: %q", mini.Texts())
	}
	if err := mini.Click("Go to artist"); err != nil {
		t.Fatal(err)
	}
	mini.Frame()
	main.Frame()

	if a.path() != "/artist/"+fakeArtist {
		t.Fatalf("Go to artist opened %q", a.path())
	}
	if a.nowPlaying.miniOpen {
		t.Fatal("the mini player stayed open after opening the artist page")
	}
	if !main.HasText("Aurora Vale") || !main.HasText("Artist Track") {
		t.Fatalf("the main window did not show the artist page: %q", main.Texts())
	}
}

func TestMiniPlayerRendersWithPopoverVibrancy(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	tt.SetVibrancy(true)
	if !tt.HasText("Nothing playing") || !tt.HasText("Open Meiro") || !tt.HasText("Quit") {
		t.Fatalf("vibrant mini player lost its content: %q", tt.Texts())
	}
}

func TestMinimizeToTrayShowsTheTrayWithoutRememberingTheChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.settings.ShowTray = false

	if !a.answerClose(true, false) {
		t.Fatal("Minimize to tray did not hide the window")
	}
	a.saver.Wait()
	saved := settings.Load(path)
	if !a.settings.ShowTray || !saved.ShowTray {
		t.Fatalf("the tray is off after choosing it: in memory %v, saved %v", a.settings.ShowTray, saved.ShowTray)
	}
	if a.settings.CloseToTray != nil || saved.CloseToTray != nil {
		t.Fatalf("an answer without Remember was kept: in memory %v, saved %v", a.settings.CloseToTray, saved.CloseToTray)
	}

	if a.answerClose(false, true) {
		t.Fatal("Close Meiro hid the window")
	}
	a.saver.Wait()
	if saved := settings.Load(path); saved.CloseToTray == nil || *saved.CloseToTray {
		t.Fatal("a remembered Close Meiro was not kept")
	}
}

func TestLyricsSwitchBetweenSyncedAndStatic(t *testing.T) {
	a := newTestApp(t)
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{
			Plain:  "first words\nsecond words",
			Synced: []lrclib.Line{{At: 0, Text: "first words"}, {At: 30 * time.Second, Text: "second words"}},
		}, nil
	}
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	a.setMiniShown(true)
	tt.Frame()
	if err := tt.Click("Lyrics"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Synced") || !tt.HasText("Static") || !tt.HasText("first words") {
		t.Fatalf("timed lyrics are not shown with their modes: %q", tt.Texts())
	}
	if err := tt.Click("Static"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.StaticLyrics || !tt.HasText("first words\nsecond words") {
		t.Fatalf("static mode shows the plain text: %v %q", a.settings.StaticLyrics, tt.Texts())
	}
	if err := tt.Click("Synced"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.StaticLyrics || !tt.HasText("second words") || tt.HasText("first words\nsecond words") {
		t.Fatalf("synced mode lists lines: %v %q", a.settings.StaticLyrics, tt.Texts())
	}
}

func TestPlayingFromRelatedQueuesOnlySongs(t *testing.T) {
	a := newTestApp(t)
	song := youtube.MusicItem{VideoID: "song-mixed", Title: "Playing Song", Kind: youtube.KindTrack}
	a.queue.restore(playstate.State{Items: []youtube.MusicItem{song}, Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	a.setMiniShown(true)
	tt.Frame()
	if err := tt.Click("Related"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// The songs play with each other as the queue; the playlist and the
	// artist between them are not queued.
	if err := tt.Click("First Related Song"); err != nil {
		t.Fatal(err)
	}
	if got := ids(a.queue.items[:a.queue.limit(false)]); !slices.Equal(got, []string{"mixed-1", "mixed-2"}) || a.queue.source != "Related" {
		t.Fatalf("playing from Related queued %v from %q", got, a.queue.source)
	}
}

func TestFailedLyricsLookupIsTriedAgain(t *testing.T) {
	a := newTestApp(t)
	calls := 0
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("lrclib: 503 Service Unavailable")
		}
		return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: "first words"}}}, nil
	}
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	a.setMiniShown(true)
	tt.Frame()
	if err := tt.Click("Lyrics"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Couldn't load lyrics. Try again in a moment.") || tt.HasText("No lyrics for this song.") {
		t.Fatalf("a failed lookup is not told apart from no lyrics: %q", tt.Texts())
	}
	a.loadPanel()
	tt.Frame()
	if calls != 2 || !tt.HasText("first words") {
		t.Fatalf("the lookup was not tried again: %d calls, %q", calls, tt.Texts())
	}
}

// redArt answers every picture with a solid red PNG.
type redArt struct{}

func (redArt) RoundTrip(request *http.Request) (*http.Response, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = 220, 255
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"image/png"}},
		Body:       io.NopCloser(&body),
		Request:    request,
	}, nil
}

func TestMiniPlayerAloneTakesTheArtworkColour(t *testing.T) {
	a := newTestApp(t)
	a.thumbs = artwork.New(&http.Client{Transport: redArt{}}, func() {})
	a.settings.Dynamic = true
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	// A size-templated URL names a different picture for every size asked.
	a.current.Thumbnail = "https://lh3.googleusercontent.com/cover=w544-h544"
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	for range 200 {
		tt.Frame()
		if a.theme.dynamicOK {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the mini player never took a colour from the artwork")
}
