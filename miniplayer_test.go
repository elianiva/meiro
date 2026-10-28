package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"math"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/egoist/mygo"
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
	if runtime.GOOS == "darwin" || !trayMarkSupported(runtime.GOOS) {
		if tt.HasText("Wordmark") || tt.HasText("O mark") {
			t.Fatal("tray style choices appeared on a platform without them")
		}
		return
	}
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

func TestKeepRunningUsesTheDockOnMacAndTrayElsewhere(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	a := newTestApp(t)
	a.saver.Path = path
	a.settings.ShowTray = false

	if !a.answerClose(true, false) {
		t.Fatal("Minimize to tray did not hide the window")
	}
	a.saver.Wait()
	saved := settings.Load(path)
	wantTray := runtime.GOOS != "darwin"
	if a.settings.ShowTray != wantTray || saved.ShowTray != wantTray {
		t.Fatalf("keep running has the wrong tray state: in memory %v, saved %v, want %v", a.settings.ShowTray, saved.ShowTray, wantTray)
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

// A restored, paused queue lets the user seek through timed lyrics without a
// wall clock or network. The notch geometry represents a Retina display above
// and to the left of the primary display, in logical points.
func notchTestApp(t *testing.T) *app {
	t.Helper()
	a, _ := playbackApp(t)
	a.notch.cutout = mygo.Rectangle{X: -900, Y: -900, Width: 180, Height: 32}
	q := tracks("a", "b")
	for i := range q {
		q[i].Duration = "0:50"
	}
	a.queue.restore(playstate.State{Items: q, Index: 0, RecStart: -1})
	a.current, a.total, a.resumeID = q[0], 50*time.Second, "a"
	return a
}

func TestMacDesktopOffersNotchAndBackgroundPlayback(t *testing.T) {
	a := newTestApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 700)
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("desktop")
	tt.Advance(time.Second)
	if runtime.GOOS != "darwin" {
		if !tt.HasText("Track notifications") || !tt.HasText("Menu bar icon") || tt.HasText("Keep running when closed") {
			t.Fatal("non-Mac desktop controls changed")
		}
		return
	}
	for _, label := range []string{"Track notifications", "Menu bar icon", "Tray icon", "Close to tray"} {
		if tt.HasText(label) {
			t.Fatalf("obsolete macOS control %q is still offered", label)
		}
	}
	if !tt.HasText("Notch player") {
		t.Fatal("notch setting disappeared")
	}
	if err := tt.Click("Keep running in background"); err != nil {
		t.Fatal(err)
	}
	a.saver.Wait()
	saved := settings.Load(a.saver.Path)
	if saved.CloseToTray == nil || !*saved.CloseToTray || saved.ShowTray || saved.NotifyTrack || a.settings.ShowTray {
		t.Fatal("background playback did not persist without tray or banners")
	}
}

func TestNotchArtworkIgnoresLatePreviousCover(t *testing.T) {
	a := notchTestApp(t)
	oldRequested, releaseOld := make(chan struct{}, 1), make(chan struct{})
	released := false
	release := func() {
		if !released {
			close(releaseOld)
			released = true
		}
	}
	a.thumbs.Close()
	a.thumbs = artwork.New(&http.Client{Transport: artworkTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/old.png" {
			oldRequested <- struct{}{}
			select {
			case <-releaseOld:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for i := 0; i < len(img.Pix); i += 4 {
			channel := 0
			if r.URL.Path == "/new.png" {
				channel = 2
			}
			img.Pix[i+channel], img.Pix[i+3] = 220, 255
		}
		var body bytes.Buffer
		if err := png.Encode(&body, img); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"image/png"}}, Body: io.NopCloser(&body), Request: r}, nil
	})}, func() {})
	t.Cleanup(a.thumbs.Close)
	t.Cleanup(release)
	q := tracks("a", "b", "no-cover")
	q[0].Thumbnail, q[1].Thumbnail = "https://example.test/old.png", "https://example.test/new.png"
	a.queue.restore(playstate.State{Items: q, Index: 0, RecStart: -1})
	a.current = q[0]
	tt := ui.NewTester(a.notchView, 284, 38)
	select {
	case <-oldRequested:
	case <-time.After(time.Second):
		t.Fatal("initial cover was not requested")
	}
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	bounds := a.notch.bounds()
	tt.SetSize(bounds.Width, bounds.Height)
	tt.Advance(time.Second)
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for a.thumbs.Bitmap(q[1].Thumbnail, 192) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	tt.Advance(time.Second)
	cover, found := tt.Find("Album cover")
	if !found {
		t.Fatal("expanded album cover is missing")
	}
	x, y := int(cover.X+cover.W/2), int(cover.Y+cover.H/2)
	blue := tt.Image().RGBAAt(x, y)
	if a.current.VideoID != "b" || blue.B < 180 || blue.R > 30 {
		t.Fatalf("new song's cover is missing: %v", blue)
	}
	release()
	deadline = time.Now().Add(time.Second)
	for a.thumbs.Bitmap(q[0].Thumbnail, 192) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.thumbs.Bitmap(q[0].Thumbnail, 192) == nil {
		t.Fatal("old cover did not finish loading")
	}
	tt.Frame()
	if got := tt.Image().RGBAAt(x, y); got != blue {
		t.Fatalf("late old artwork replaced the active cover: %v", got)
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if got := tt.Image().RGBAAt(x, y); got.R != got.G || got.G != got.B {
		t.Fatalf("missing cover did not use a neutral placeholder: %v", got)
	}
}

func TestNotchLyricsFollowSeekingAndTrackChanges(t *testing.T) {
	a := notchTestApp(t)
	calls := 0
	a.lyricsSource = func(_ context.Context, q lrclib.Query) (*lrclib.Lyrics, error) {
		calls++
		return &lrclib.Lyrics{Synced: []lrclib.Line{
			{At: 0, Text: q.Title + " first"},
			{At: 10 * time.Second, Text: q.Title + " second"},
			{At: 20 * time.Second, Text: q.Title + " third"},
			{At: 30 * time.Second, Text: q.Title + " fourth"},
			{At: 40 * time.Second, Text: q.Title + " fifth"},
		}}, nil
	}
	compact := a.notch.bounds()
	tt := ui.NewTester(a.notchView, compact.Width, compact.Height)
	if calls != 0 {
		t.Fatal("collapsed notch fetched lyrics")
	}
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	expanded := a.notch.bounds()
	tt.SetSize(expanded.Width, expanded.Height)
	tt.Advance(time.Second)
	if expanded.Width != 480 || expanded.Height != 190 {
		t.Fatalf("expanded notch is not compact: %+v", expanded)
	}
	for _, xy := range [][2]int{{0, 0}, {expanded.Width - 1, 0}, {0, expanded.Height - 1}, {expanded.Width - 1, expanded.Height - 1}} {
		if tt.Image().RGBAAt(xy[0], xy[1]).A != 0 {
			t.Fatalf("corner %v is opaque", xy)
		}
	}
	if !tt.HasText("Track a first") || tt.HasText("Track a fifth") || calls != 1 {
		t.Fatalf("initial lyrics: %q, calls %d", tt.Texts(), calls)
	}
	if expanded.Y != compact.Y || expanded.X+expanded.Width/2 != compact.X+compact.Width/2 {
		t.Fatal("expanded panel moved away from camera")
	}
	if a.nowPlaying.open || a.nowPlaying.tab != tabQueue || a.nowPlaying.lyrics.followed != -1 {
		t.Fatal("notch changed the main player's presentation")
	}
	mini := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	tabTo(t, mini, "Position")
	mini.Key(0, ui.KeyEnd)
	tt.Frame()
	if !tt.HasText("Track a fifth") || tt.HasText("Track a first") {
		t.Fatalf("seek did not update notch lyrics: %q", tt.Texts())
	}
	mini.Key(0, ui.KeyHome)
	tt.Frame()
	if !tt.HasText("Track a first") || tt.HasText("Track a fifth") {
		t.Fatal("seek back did not update lyrics")
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.current.VideoID != "b" || !tt.HasText("Track b first") || tt.HasText("Track a first") {
		t.Fatalf("track change left stale lyrics: %q", tt.Texts())
	}
	if calls != 2 {
		t.Fatalf("lyrics fetched %d times, want once per song", calls)
	}
	if !a.notch.expanded {
		t.Fatal("Next closed the notch")
	}
	if err := tt.Click("Previous"); err != nil {
		t.Fatal(err)
	}
	if a.current.VideoID != "a" || !a.notch.expanded {
		t.Fatal("Previous did not return to the prior song in the open panel")
	}
	if err := tt.Click("Next"); err != nil {
		t.Fatal(err)
	}
	// Returning to a song may use either lyric cache; collapsing must not fetch.
	callsBeforeClose := calls
	if err := tt.Click("Close notch lyrics from playback indicator"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	tt.SetSize(compact.Width, compact.Height)
	if a.notch.expanded || tt.HasText("Track b first") {
		t.Fatal("indicator did not collapse lyrics")
	}
	if err := tt.Click("Open notch lyrics from playback indicator"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(expanded.Width, expanded.Height)
	tt.Key(0, ui.KeyEscape)
	if a.notch.expanded || calls != callsBeforeClose {
		t.Fatal("Escape or cached reopen failed")
	}
}

func TestNotchLyricsHaveStaticEmptyAndRetryStates(t *testing.T) {
	for _, scenario := range []string{"static", "empty", "failure"} {
		t.Run(scenario, func(t *testing.T) {
			a := notchTestApp(t)
			calls := 0
			a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
				calls++
				switch scenario {
				case "static":
					return &lrclib.Lyrics{Plain: "Words without timestamps"}, nil
				case "empty":
					return nil, nil
				default:
					if calls == 1 {
						return nil, errors.New("offline")
					}
					return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: "Recovered lyrics"}}}, nil
				}
			}
			tt := ui.NewTester(a.notchView, 284, 38)
			if err := tt.Click("Open notch lyrics"); err != nil {
				t.Fatal(err)
			}
			bounds := a.notch.bounds()
			tt.SetSize(bounds.Width, bounds.Height)
			tt.Advance(time.Second)
			if !tt.HasText("Previous") || !tt.HasText("Play") || !tt.HasText("Next") {
				t.Fatal("lyric fallback hid the transport")
			}
			switch scenario {
			case "static":
				if !tt.HasText("Synced lyrics unavailable") || !tt.HasText("Words without timestamps") {
					t.Fatal(tt.Texts())
				}
			case "empty":
				if !tt.HasText("No lyrics for this song.") {
					t.Fatal(tt.Texts())
				}
			default:
				if !tt.HasText("Couldn't load lyrics") {
					t.Fatal(tt.Texts())
				}
				if err := tt.Click("Retry lyrics"); err != nil {
					t.Fatal(err)
				}
				if calls != 2 || !tt.HasText("Recovered lyrics") {
					t.Fatal(tt.Texts())
				}
			}
		})
	}
}

func TestNotchWaitsForCurrentLyricsWhenTrackChanges(t *testing.T) {
	a := notchTestApp(t)
	a.lyricsSource = func(_ context.Context, q lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: q.Title + " lyrics"}}}, nil
	}
	drain := deferredRunner(a)
	tt := ui.NewTester(a.notchView, 284, 38)
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	bounds := a.notch.bounds()
	tt.SetSize(bounds.Width, bounds.Height)
	if !tt.HasText("Loading lyrics") {
		t.Fatal(tt.Texts())
	}
	mini := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	if err := mini.Click("Next"); err != nil {
		t.Fatal(err)
	}
	drain()
	tt.Frame()
	if !tt.HasText("Track b lyrics") || tt.HasText("Track a lyrics") {
		t.Fatalf("superseded result was applied: %q", tt.Texts())
	}
}

func TestNotchSettingIsMacOnlyAndPersists(t *testing.T) {
	a := notchTestApp(t)
	a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 900)
	if err := tt.Click("Search settings"); err != nil {
		t.Fatal(err)
	}
	tt.Type("notch")
	if runtime.GOOS != "darwin" {
		if tt.HasText("Notch player") {
			t.Fatal("macOS setting appeared on another platform")
		}
		return
	}
	if !a.settings.ShowNotch {
		t.Fatal("notch must default to enabled")
	}
	panel := ui.NewTester(a.notchView, 284, 38)
	if err := panel.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Show notch player"); err != nil {
		t.Fatal(err)
	}
	a.saver.Wait()
	panel.Frame()
	if a.settings.ShowNotch || settings.Load(a.saver.Path).ShowNotch || a.notch.expanded || panel.HasText("Open notch lyrics") {
		t.Fatal("disabled notch stayed visible or was not saved")
	}
	if err := tt.Click("Show notch player"); err != nil {
		t.Fatal(err)
	}
	a.saver.Wait()
	panel.Frame()
	if !settings.Load(a.saver.Path).ShowNotch || !panel.HasText("Open notch lyrics") {
		t.Fatal("notch did not return after re-enabling")
	}
}

func TestNotchLongLyricsLeaveRoomForControls(t *testing.T) {
	a := notchTestApp(t)
	a.current.Title = "A very long song title that must never push the lyrics or controls out of the panel"
	a.current.Artist = "An artist with a long name that stays in the middle column"
	current := "A longer lyric line that wraps across the compact panel without hiding the playback controls"
	next := "The next line is a quiet preview of the words still to come"
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: current}, {At: time.Minute, Text: next}}}, nil
	}
	tt := ui.NewTester(a.notchView, 284, 38)
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	bounds := a.notch.bounds()
	tt.SetSize(bounds.Width, bounds.Height)
	tt.Advance(time.Second)
	line, found := tt.Find(current)
	control, hasControl := tt.Find("Play")
	if !found || !hasControl || control.X+control.W > line.X || line.X+line.W > float32(bounds.Width-16) || line.H > 38 {
		t.Fatalf("lyrics overlap the transport: line %+v, control %+v", line, control)
	}
	cover, hasCover := tt.Find("Album cover")
	title, hasTitle := tt.Find(a.current.Title)
	artist, hasArtist := tt.Find(a.current.Artist)
	if !hasCover || !hasTitle || !hasArtist || cover.X+cover.W > title.X || title.X+title.W > line.X || artist.X+artist.W > line.X {
		t.Fatal("long metadata overflowed its column")
	}
	if !tt.HasText(next) || tt.HasText("Shuffle") || tt.HasText("Repeat") {
		t.Fatal("compact content changed")
	}
	tabTo(t, tt, "Next")
	tt.Key(0, ui.KeySpace)
	if a.current.VideoID != "b" || !a.notch.expanded {
		t.Fatal("keyboard transport failed")
	}
}

func TestNotchProgressSeeksWithoutInterferenceFromTheMainPlayer(t *testing.T) {
	a := notchTestApp(t)
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{Synced: []lrclib.Line{
			{At: 0, Text: "Opening line"},
			{At: 10 * time.Second, Text: "Middle line"},
			{At: 40 * time.Second, Text: "Last line"},
		}}, nil
	}
	tt := ui.NewTester(a.notchView, 284, 38)
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	bounds := a.notch.bounds()
	tt.SetSize(bounds.Width, bounds.Height)
	tt.Advance(time.Second)
	mini := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	slider, found := tt.Find("Notch position")
	if !found {
		t.Fatal("notch seek control is missing")
	}
	x, y := slider.X+slider.W/2, slider.Y+slider.H/2
	tt.Press(x, y)
	mini.Frame()
	if !a.notch.scrubbing || a.seekVersion != 0 {
		t.Fatal("main player ended a notch drag or sought before release")
	}
	tt.Release(x, y)
	tt.Advance(time.Second)
	if a.notch.scrubbing || a.seekVersion == 0 || a.playbackPosition() != 25*time.Second || !tt.HasText("0:25") {
		t.Fatalf("notch did not seek on release: position %v, texts %q", a.playbackPosition(), tt.Texts())
	}
	if !tt.HasText("Opening line") || !tt.HasText("Middle line") || !tt.HasText("Last line") {
		t.Fatal("previous, current and next lyrics are not visible")
	}
	tabTo(t, tt, "Notch position")
	tt.Key(0, ui.KeyEnd)
	tt.Advance(time.Second)
	if a.playbackPosition() != 50*time.Second || !tt.HasText("Last line") || tt.HasText("Opening line") {
		t.Fatal("End did not seek and follow lyrics")
	}
	tt.Key(0, ui.KeyHome)
	tt.Advance(time.Second)
	if a.playbackPosition() != 0 || !tt.HasText("Opening line") || tt.HasText("Last line") {
		t.Fatal("Home did not seek back")
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Key(0, ui.KeyEnd)
	line, found := tt.Find("Last line")
	if !found || line.X < 268 || line.Y < 38 || line.Y+line.H > 174 {
		t.Fatal("reduced motion did not place the active lyric in its column")
	}
	// A track whose duration is unknown cannot accept a seek.
	a.total = 0
	tt.Frame()
	version := a.seekVersion
	tt.Key(0, ui.KeyEnd)
	if a.seekVersion != version || !tt.HasText("--:--") || !a.notch.expanded {
		t.Fatal("unknown duration accepted a seek or hid the panel")
	}
}

func TestNotchOpeningClosingAndReversal(t *testing.T) {
	a := notchTestApp(t)
	calls := 0
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		calls++
		return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: "Motion test lyrics"}}}, nil
	}
	tt := ui.NewTester(a.notchView, 284, 38)
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(480, 190)
	tt.Advance(80 * time.Millisecond)
	opening, found := tt.Find("Notch panel")
	if !found || opening.H <= 38 || opening.H >= 190 || opening.W <= 284 || opening.W >= 480 {
		t.Fatalf("opening jumped to its final size: %+v", opening)
	}
	if opening.Y != 0 || opening.X+opening.W/2 != 240 || tt.Image().RGBAAt(240, 0).A != 255 {
		t.Fatal("panel moved away from the camera or faded its housing")
	}
	tt.Advance(200 * time.Millisecond)
	opened, _ := tt.Find("Notch panel")
	if opened.W != 480 || opened.H != 190 || a.notch.transitioning {
		t.Fatal("opening did not settle")
	}
	cover := tt.Image().RGBAAt(30, 100)
	next, _ := tt.Find("Next")
	if err := tt.Click("Close notch lyrics"); err != nil {
		t.Fatal(err)
	}
	// Closing retains the native canvas until the last animation frame.
	if got := a.notch.bounds(); got.Width != 480 || got.Height != 190 {
		t.Fatal("native bounds were cropped before the fade")
	}
	tt.Advance(40 * time.Millisecond)
	closing, _ := tt.Find("Notch panel")
	faded := tt.Image().RGBAAt(30, 100)
	if closing.H <= 38 || closing.H >= opened.H || faded.R >= cover.R {
		t.Fatalf("close did not shrink and fade: panel %+v, cover %v -> %v", closing, cover, faded)
	}
	tt.Press(next.X+next.W/2, next.Y+next.H/2)
	tt.Release(next.X+next.W/2, next.Y+next.H/2)
	if a.current.VideoID != "a" {
		t.Fatal("a fading control accepted a click")
	}
	closing, _ = tt.Find("Notch panel")
	if err := tt.Click("Open notch lyrics from playback indicator"); err != nil {
		t.Fatal(err)
	}
	reversed, _ := tt.Find("Notch panel")
	// Input can sample between display frames; allow that small advance, not
	// a reset to either endpoint of the animation.
	if math.Abs(float64(reversed.H-closing.H)) > 6 || reversed.W == 284 || reversed.W == 480 {
		t.Fatalf("reversal jumped: %+v -> %+v", closing, reversed)
	}
	tt.Advance(60 * time.Millisecond)
	reopening, _ := tt.Find("Notch panel")
	if reopening.H <= closing.H || reopening.H >= 190 {
		t.Fatal("reversal did not continue from its current size")
	}
	// Reverse once more before opening finishes, using the keyboard path.
	tt.Key(0, ui.KeyEscape)
	tt.Advance(240 * time.Millisecond)
	closed := a.notch.bounds()
	tt.SetSize(closed.Width, closed.Height)
	if closed.Width != 284 || closed.Height != 38 || a.notch.transitioning || tt.HasText("Motion test lyrics") || tt.HasText("Next") {
		t.Fatal("closing left an expanded or interactive panel")
	}
	if calls != 1 || a.nowPlaying.open || a.nowPlaying.tab != tabQueue {
		t.Fatal("animation refetched lyrics or changed the main player")
	}
}

func TestNotchReduceMotionOnlyFades(t *testing.T) {
	a := notchTestApp(t)
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{Synced: []lrclib.Line{{At: 0, Text: "Still lyrics"}}}, nil
	}
	tt := ui.NewTester(a.notchView, 284, 38)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(480, 190)
	tt.Advance(40 * time.Millisecond)
	panel, _ := tt.Find("Notch panel")
	line, _ := tt.Find("Still lyrics")
	partial := tt.Image().RGBAAt(30, 100)
	if panel.W != 480 || panel.H != 190 || partial.R == 0 || partial.R >= 32 {
		t.Fatalf("reduced motion did not fade in place: %+v, %v", panel, partial)
	}
	tt.Advance(80 * time.Millisecond)
	settled, _ := tt.Find("Still lyrics")
	if settled != line || a.notch.transitioning {
		t.Fatal("reduced motion moved the content")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(40 * time.Millisecond)
	closing, _ := tt.Find("Notch panel")
	if closing != panel || a.notch.bounds().Height != 190 || tt.Image().RGBAAt(30, 100).R >= 32 {
		t.Fatal("reduced motion shrank before the content faded")
	}
	tt.Advance(80 * time.Millisecond)
	closed := a.notch.bounds()
	if closed.Height != 38 || closed.Width != 284 || a.notch.transitioning {
		t.Fatal("reduced motion did not finish closing")
	}
}

func TestNotchIndicatorFollowsBufferingPlaybackAndPause(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	tone := filepath.Join(t.TempDir(), "tone.wav")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=duration=60", tone).CombinedOutput(); err != nil {
		t.Fatalf("tone: %v: %s", err, out)
	}
	a, r := playbackApp(t)
	r.file = tone
	a.notch.cutout = mygo.Rectangle{Width: 180, Height: 32}
	a.router.Push("/album/MPREb_test")
	main := ui.NewTester(a.view, 1000, 700)
	main.Frame()
	drain := deferredRunner(a)
	nt := ui.NewTester(a.notchView, 284, 38)
	if err := main.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	nt.Frame()
	if !nt.HasText("Buffering") {
		t.Fatal("pending stream has no buffering indicator")
	}
	drain()
	deadline := time.Now().Add(5 * time.Second)
	for a.player.Position() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	nt.Frame()
	if a.player.Position() == 0 || !nt.HasText("Playing") || len(a.history) != 1 {
		t.Fatalf("real audio did not start: position %v, history %d, %q", a.player.Position(), len(a.history), nt.Texts())
	}
	nt.Advance(time.Second)
	before := bytes.Clone(nt.Image().Pix)
	nt.Advance(100 * time.Millisecond)
	if bytes.Equal(before, nt.Image().Pix) {
		t.Fatal("playback indicator did not animate")
	}
	if err := nt.Click("Open notch lyrics"); err != nil {
		t.Fatal(err)
	}
	bounds := a.notch.bounds()
	nt.SetSize(bounds.Width, bounds.Height)
	nt.Advance(time.Second)
	if err := nt.Click("Pause"); err != nil {
		t.Fatal(err)
	}
	nt.Frame()
	if !a.player.Paused() || !nt.HasText("Play") || !a.notch.expanded {
		t.Fatal("notch pause failed or collapsed the panel")
	}
	if err := nt.Click("Play"); err != nil {
		t.Fatal(err)
	}
	nt.Frame()
	if a.player.Paused() || !nt.HasText("Pause") || !a.notch.expanded {
		t.Fatal("notch Play did not resume playback in the open panel")
	}
	if err := nt.Click("Pause"); err != nil {
		t.Fatal(err)
	}
	if err := nt.Click("Close notch lyrics"); err != nil {
		t.Fatal(err)
	}
	nt.Advance(time.Second)
	nt.SetSize(284, 38)
	if nt.HasText("Previous") || nt.HasText("Next") {
		t.Fatal("compact notch showed transport controls")
	}
	before = bytes.Clone(nt.Image().Pix)
	nt.Advance(time.Second)
	if !bytes.Equal(before, nt.Image().Pix) {
		t.Fatal("paused indicator kept animating")
	}
}
