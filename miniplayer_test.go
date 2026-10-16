package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/playstate"
)

func TestMiniPlayerShowsAndControlsWhatPlays(t *testing.T) {
	a := newTestApp()
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

func TestMiniPlayerExpandsToTabs(t *testing.T) {
	a := newTestApp()
	a.queue.restore(playstate.State{Items: tracks("a", "b"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniTallHeight)
	tt.Frame()
	if tt.HasText("Lyrics") {
		t.Fatalf("tabs show before they are asked for: %q", tt.Texts())
	}
	if err := tt.Click("Show up next, lyrics and related"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Lyrics") || !tt.HasText("Related") || !tt.HasText("Track b") {
		t.Fatalf("the panel is not shown: %q", tt.Texts())
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
	a := newTestApp()
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{
			Plain:  "first words\nsecond words",
			Synced: []lrclib.Line{{At: 0, Text: "first words"}, {At: 30 * time.Second, Text: "second words"}},
		}, nil
	}
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniTallHeight)
	tt.Frame()
	for _, label := range []string{"Show up next, lyrics and related", "Lyrics"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
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
	a := newTestApp()
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
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniTallHeight)
	tt.Frame()
	for _, label := range []string{"Show up next, lyrics and related", "Lyrics"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	if !tt.HasText("Couldn't load lyrics. Try again in a moment.") || tt.HasText("No lyrics for this song.") {
		t.Fatalf("a failed lookup is not told apart from no lyrics: %q", tt.Texts())
	}
	a.loadPanel()
	tt.Frame()
	if calls != 2 || !tt.HasText("first words") {
		t.Fatalf("the lookup was not tried again: %d calls, %q", calls, tt.Texts())
	}
}
