package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/betterlyrics"
	"github.com/elianiva/meiro/internal/credits"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/lyrics"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

func TestBetterLyricsComesBeforeYouTubeMusicAndLRCLIB(t *testing.T) {
	a := newTestApp(t)
	a.betterLyrics = func(context.Context, betterlyrics.Query) ([]lyrics.Line, error) {
		return []lyrics.Line{
			{At: 0, Text: "first words", Words: []lyrics.Word{{At: 0, Text: "first "}, {At: time.Second, Text: "words"}}},
			{At: 30 * time.Second, Text: "second words", Words: []lyrics.Word{{At: 30 * time.Second, Text: "second "}, {At: 31 * time.Second, Text: "words"}}},
		}, nil
	}
	asked := 0
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		asked++
		return &lrclib.Lyrics{Synced: []lyrics.Line{{At: 0, Text: "from LRCLIB"}}}, nil
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
	if !tt.HasText("first words") || tt.HasText("from LRCLIB") {
		t.Fatalf("the lyrics are %q", tt.Texts())
	}
	if asked != 0 {
		t.Fatalf("LRCLIB was asked %d times with lyrics already found", asked)
	}
	if err := tt.Click("Where these lyrics come from"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Better Lyrics") {
		t.Fatalf("the source is not named: %q", tt.Texts())
	}
}

func TestLyricsAreLookedUpAgainOnceTheLengthIsKnown(t *testing.T) {
	a := newTestApp(t)
	var asked []time.Duration
	a.betterLyrics = func(_ context.Context, q betterlyrics.Query) ([]lyrics.Line, error) {
		asked = append(asked, q.Duration)
		if q.Duration == 0 {
			// Better Lyrics keys its cache on the length, and serves only
			// cached songs: without one, it has nothing.
			return nil, nil
		}
		return []lyrics.Line{{At: 0, Text: "word timed", Words: []lyrics.Word{{At: 0, Text: "word timed"}}}}, nil
	}
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	a.total = 0
	a.loadLyrics()
	if len(asked) != 1 || asked[0] != 0 || a.nowPlaying.lyrics.footer != "Source: YouTube Music" {
		t.Fatalf("the first lookup asked for %v and found %q", asked, a.nowPlaying.lyrics.footer)
	}
	// The stream resolves, and its length goes with the lyrics lookups.
	a.total = 212 * time.Second
	a.refreshLyrics()
	if len(asked) != 2 || asked[1] != 212*time.Second {
		t.Fatalf("the second lookup asked for %v", asked)
	}
	if len(a.nowPlaying.lyrics.synced) != 1 || a.nowPlaying.lyrics.footer != "Source: Better Lyrics" {
		t.Fatalf("the lyrics are %#v from %q", a.nowPlaying.lyrics.synced, a.nowPlaying.lyrics.footer)
	}
	// One lookup with the length is all a track gets.
	a.refreshLyrics()
	a.loadLyrics()
	if len(asked) != 2 {
		t.Fatalf("the lyrics were asked for %v", asked)
	}
}

func TestLyricsUseTheArtistKnownForTheTrack(t *testing.T) {
	a := newTestApp(t)
	var asked betterlyrics.Query
	a.betterLyrics = func(_ context.Context, q betterlyrics.Query) ([]lyrics.Line, error) {
		asked = q
		return nil, nil
	}
	// An album row leaves the artist out; another list named it before.
	a.queue.restore(playstate.State{Items: []youtube.MusicItem{{VideoID: "song", Title: "E85", Kind: youtube.KindTrack, Duration: "2:34"}}, Index: 0, RecStart: -1})
	a.current = a.queue.current()
	a.scrobble.credits.Put("song", credits.Credits{Artist: "Don Toliver", Album: "OCTANE"})
	a.total = 154 * time.Second
	a.loadLyrics()
	if asked.Artist != "Don Toliver" || asked.Album != "OCTANE" || asked.Duration != 154*time.Second {
		t.Fatalf("the lyrics were looked up as %+v", asked)
	}
}

func TestTheSungLineSitsInTheMiddleOfTheLyrics(t *testing.T) {
	a := newTestApp(t)
	lines := make([]lyrics.Line, 10)
	for i := range lines {
		lines[i] = lyrics.Line{At: time.Duration(i) * 6 * time.Second, Text: fmt.Sprintf("line %d", i)}
	}
	a.lyricsSource = func(context.Context, lrclib.Query) (*lrclib.Lyrics, error) {
		return &lrclib.Lyrics{Synced: lines}, nil
	}
	a.queue.restore(playstate.State{Items: tracks("a"), Index: 0, RecStart: -1})
	a.current = a.queue.current()
	// Twenty-four seconds into a four-minute song: the fifth of the ten lines.
	a.total, a.resumeID, a.scrub = 4*time.Minute, a.current.VideoID, 24
	a.nowPlaying.tab = tabLyrics
	tt := ui.NewTester(a.miniPlayer, miniWidth, miniHeight)
	a.setMiniShown(true)
	for range 4 {
		tt.Frame()
	}
	sung, ok := tt.Find(lines[4].Text)
	if !ok {
		t.Fatalf("the line being sung is not shown: %q", tt.Texts())
	}
	// The lyrics fill the panel between the modes above them and the buttons
	// below them, so their middle is where the sung line belongs.
	modes, ok := tt.Find("Synced lyrics")
	if !ok {
		t.Fatalf("the lyrics have no switch: %q", tt.Texts())
	}
	footer, ok := tt.Find("Open Meiro")
	if !ok {
		t.Fatalf("the mini player has no buttons: %q", tt.Texts())
	}
	middle := (modes.Y + modes.H + footer.Y) / 2
	if got := sung.Y + sung.H/2; got < middle-32 || got > middle+32 {
		t.Fatalf("the sung line is at %.0f, not in the middle of the lyrics at %.0f", got, middle)
	}
}

func TestLyricWordsLightAsTheyAreSung(t *testing.T) {
	sc := m3.New(m3.Config{}, true).Scheme
	line := lyrics.Line{At: 0, Text: "one two three", Words: []lyrics.Word{
		{At: 0, Text: "one "},
		{At: time.Second, Text: "two "},
		{At: 2 * time.Second, Text: "three"},
	}}
	spans := lyricWords(line, time.Second, sc.OnSurface)
	if len(spans) != 3 || spans[0].Text != "one " || spans[1].Text != "two " || spans[2].Text != "three" {
		t.Fatalf("the words are %#v", spans)
	}
	if spans[0].Color != sc.OnSurface || spans[1].Color != sc.OnSurface {
		t.Fatalf("the words sung are not lit: %#v", spans)
	}
	if spans[2].Color == sc.OnSurface {
		t.Fatalf("the word still to come is lit: %#v", spans)
	}
}
