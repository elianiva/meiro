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
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/internal/settings"
)

// inkEnd returns the x of the rightmost pixel across the middle of r that is
// not the panel's surface: the end of the text it shows.
func inkEnd(img *image.RGBA, r ui.Rect) int {
	edge := miniWidth - 14
	surface := img.At(edge, int(r.Y+r.H/2))
	r0, g0, b0, _ := surface.RGBA()
	for x := edge; x >= 0; x-- {
		for y := int(r.Y + r.H/3); y <= int(r.Y+2*r.H/3); y++ {
			r, g, b, _ := img.At(x, y).RGBA()
			if channelGap(r, r0)+channelGap(g, g0)+channelGap(b, b0) > 0x0600 {
				return x
			}
		}
	}
	return -1
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
}

func TestLinuxTrayMenuOpensMeiro(t *testing.T) {
	opened := false
	menu := linuxTrayMenu(func() { opened = true })
	items := menu.Items()
	if len(items) != 3 || items[0].Label != "Open Meiro" || items[0].Click == nil ||
		items[1].Type != mygo.MenuItemSeparator || items[2].Role != mygo.RoleQuit {
		t.Fatalf("tray menu = %#v, want Open Meiro, separator, and Quit", items)
	}
	items[0].Click(items[0], nil)
	if !opened {
		t.Fatal("choosing Open Meiro did not restore the main window")
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

func TestMiniPlayerShowsTabsAndSeveralQueueItems(t *testing.T) {
	a := newTestApp(t)
	items := tracks("a", "b", "c", "d", "e", "f")
	for i := range items {
		items[i].Subtitle = "Artist " + items[i].VideoID
		items[i].Duration = "3:4" + strconv.Itoa(i)
	}
	a.queue.restore(playstate.State{Items: items, Index: 0, RecStart: -1, Source: "Reiwa Era J-Pop"})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	a.setMiniShown(true)
	tt.Frame()
	for _, text := range []string{"Lyrics", "Related", "Playing from", "Reiwa Era J-Pop", "Auto-play", "Clear queue", "Track b", "Track e", "Artist e"} {
		if !tt.HasText(text) {
			t.Fatalf("the panel does not show %q: %q", text, tt.Texts())
		}
	}
	track, hasTrack := tt.Find("Track a")
	lyrics, hasLyrics := tt.Find("Lyrics")
	if !hasTrack || !hasLyrics || lyrics.Y <= track.Y {
		t.Fatalf("the queue panel is not below the player: track %v, lyrics %v", track, lyrics)
	}
	lastTrack, hasLastTrack := tt.Find("Track e")
	footer, hasFooter := tt.Find("Open Meiro")
	if !hasLastTrack || !hasFooter || lastTrack.Y+lastTrack.H > footer.Y {
		t.Fatalf("the queue does not show Track e above its footer: track %v, footer %v", lastTrack, footer)
	}
	// The column is narrow, so the Clear queue label ends on the same edge as
	// the lengths instead of 16 px short of it.
	duration, ok := tt.Find("3:42")
	if !ok {
		t.Fatalf("no length: %q", tt.Texts())
	}
	clear, ok := tt.Find("Clear queue")
	if !ok {
		t.Fatalf("no Clear queue: %q", tt.Texts())
	}
	img := tt.Image()
	if lengthEnd, clearEnd := inkEnd(img, duration), inkEnd(img, clear); lengthEnd != clearEnd {
		t.Errorf("the Clear queue label ends at %d and the lengths end at %d", clearEnd, lengthEnd)
	}
	autoPlay := a.settings.AutoPlay
	if err := tt.Click("Toggle auto-play"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.AutoPlay == autoPlay {
		t.Fatal("the compact auto-play switch did not toggle")
	}
	if err := tt.Click("Lyrics"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.nowPlaying.lyrics.videoID != "a" {
		t.Fatalf("lyrics were not loaded for the playing track: %q", a.nowPlaying.lyrics.videoID)
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
