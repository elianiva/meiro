package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// Every page draws its own content, in both appearances, with and without a
// track playing, so a page that silently renders nothing (or the wrong page)
// fails rather than merely drawing some text.
func TestEveryPageDraws(t *testing.T) {
	pages := []struct {
		path string
		want []string
	}{
		{"/home", []string{greeting(time.Now()), "Quick picks"}},
		{"/explore", []string{"Explore", "Moods"}},
		{"/library", []string{"Library", "Your library lives here"}},
		{"/search", []string{"Search", "Find your next favourite"}},
		{"/settings", []string{"Settings", "Appearance"}},
		{"/album/MPREb_test", []string{"Album", "ALBUM", "Album Track One"}},
		{"/playlist/VLPL_video", []string{"Playlist", "PLAYLIST", "Playlist video"}},
		{"/artist/UCabc", []string{"Artist", "ARTIST"}},
		{"/recap", []string{"Recap", "Your recap lives here"}},
		{"/nowhere", []string{"Not found", "That page does not exist."}},
	}
	for _, page := range pages {
		for _, dark := range []bool{false, true} {
			name := page.path
			if dark {
				name += " (dark)"
			}
			t.Run(name, func(t *testing.T) {
				a := newTestApp()
				a.router.Push(page.path)
				tt := ui.NewTester(a.view, 1000, 700)
				tt.SetDark(dark)
				a.current = youtube.MusicItem{VideoID: "vid-1", Title: "Ambient One"}
				a.npOpen = page.path == "/home"
				tt.Frame()
				for _, want := range page.want {
					if !tt.HasText(want) {
						t.Errorf("%s did not show %q: %q", page.path, want, tt.Texts())
					}
				}
			})
		}
	}
}
