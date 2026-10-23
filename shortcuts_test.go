package main

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/internal/settings"
)

func TestDefaultShortcutsNavigate(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	for _, k := range []struct {
		mods ui.Modifiers
		key  ui.Key
		path string
	}{
		{ui.Cmd, ui.KeyComma, "/settings"},
		{ui.Cmd, ui.Key3, "/library"},
		{ui.Cmd, ui.Key2, "/explore"},
		{ui.Cmd, ui.KeyF, "/search"},
		{ui.Cmd, ui.Key1, "/home"},
	} {
		tt.Key(k.mods, k.key)
		tt.Frame()
		if got := a.router.Path(); got != k.path {
			t.Errorf("%v+%v opened %s, want %s", k.mods, k.key, got, k.path)
		}
	}
}

func TestVolumeShortcuts(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	a.setVolume(50)
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyI)
	if a.volume != 55 {
		t.Errorf("volume up: %v", a.volume)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	if a.volume != 45 {
		t.Errorf("volume down: %v", a.volume)
	}
}

func TestRecordingANewShortcut(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()
	label := keymap.Binding{Cmd: true, Key: ui.KeyK}.Label(keymap.OS()) + " / " + keymap.Binding{Cmd: true, Key: ui.KeyF}.Label(keymap.OS())
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.keyboard.recording != keymap.Search || !tt.HasText("Press the new keys…") {
		t.Fatalf("not recording: %q", tt.Texts())
	}

	// A key another action has is refused, and says whose it is.
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if a.keyboard.recording != keymap.Search || a.router.Path() != "/settings" || len(a.settings.Shortcuts) != 0 {
		t.Fatalf("a taken key was accepted: %+v", a.settings.Shortcuts)
	}
	if !tt.HasText(keymap.Binding{Cmd: true, Key: ui.KeyComma}.Label(keymap.OS()) + " is already Settings.") {
		t.Errorf("no explanation: %q", tt.Texts())
	}

	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	if a.keyboard.recording != "" || a.settings.Shortcuts["search"] != "Shift+Cmd+Y" {
		t.Fatalf("not kept: %q %v", a.keyboard.recording, a.settings.Shortcuts)
	}
	// Pressing it did not do what the old shortcuts do, and now it does.
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Frame()
	if a.router.Path() != "/settings" {
		t.Errorf("the old key still searches: %s", a.router.Path())
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyY)
	tt.Frame()
	if a.router.Path() != "/search" {
		t.Errorf("the new key opened %s", a.router.Path())
	}

	a.router.Push("/settings")
	tt.Frame()
	if err := tt.Click("Reset all"); err != nil {
		t.Fatal(err)
	}
	if len(a.settings.Shortcuts) != 0 || a.keyMap().Label(keymap.Search, "linux") != "Ctrl+K" {
		t.Errorf("not reset: %v", a.settings.Shortcuts)
	}
}

func TestEscapeCancelsRecording(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()
	a.startRecording(keymap.Mute)
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.keyboard.recording != "" || len(a.settings.Shortcuts) != 0 {
		t.Errorf("recording %q, shortcuts %v", a.keyboard.recording, a.settings.Shortcuts)
	}
}

func TestHoldingTheModifierShowsTheKeysOverTheControls(t *testing.T) {
	for _, mods := range []ui.Modifiers{ui.Ctrl, ui.Super} {
		a := newTestApp(t)
		a.settings.Shortcuts = map[string]string{"settings": "Cmd+Shift+P", "next": "Cmd+J"}
		a.keyboard.hintDelay = 0
		a.current = tracks("a")[0]
		tt := ui.NewTester(a.view, 1000, 700)
		tt.Frame()
		for _, hint := range []string{"P", "J"} {
			if hasTextExactly(tt, hint) {
				t.Fatalf("%q shows with no key held", hint)
			}
		}
		tt.HoldModifiers(mods)
		tt.Frame()
		if hasTextExactly(tt, "Space") {
			t.Error("the overlay advertises Space even though it does not use the held modifier")
		}
		// The rail and the player each name the key, as changed, not the
		// modifier that is already down.
		for _, hint := range []string{"P", "1", "2", "3", "J", "R"} {
			if !hasTextExactly(tt, hint) {
				t.Errorf("holding %v: no %q over its control: %q", mods, hint, tt.Texts())
			}
		}
		tt.HoldModifiers(0)
		tt.Frame()
		if hasTextExactly(tt, "P") {
			t.Error("the keys stay after the modifier is up")
		}
	}
}

func hasTextExactly(tt *ui.Tester, want string) bool {
	return slices.Contains(tt.Texts(), want)
}

// A short press of Ctrl, as in Ctrl+C, does not flash the overlay.
func TestBriefModifierShowsNothing(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	tt.HoldModifiers(ui.Ctrl)
	tt.Frame()
	if hasTextExactly(tt, "Space") {
		t.Error("the keys showed at once")
	}
}

func tabTo(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	for range 160 {
		if tt.Focused(label) {
			return
		}
		tt.Key(0, ui.KeyTab)
	}
	t.Fatalf("Tab never reached %q", label)
}

func TestKeyboardMenusKeepFocusAndRunActions(t *testing.T) {
	for _, item := range []struct {
		path, title, first, link string
	}{
		{"/album/MPREb_test", "Album Track One", "Play next", "https://music.youtube.com/watch?v=vid-3"},
		{"/home", "Deep Focus", "Open album", "https://music.youtube.com/browse/MPREb_test"},
		{"/search", "Yorushika", "Go to artist", "https://music.youtube.com/browse/UCyorushika"},
	} {
		t.Run(item.title, func(t *testing.T) {
			a := newTestApp(t)
			a.router.Push(item.path)
			tt := ui.NewTester(a.view, 1000, 700)
			tt.Frame()
			if item.path == "/search" {
				tt.Key(ui.Cmd, ui.KeyK)
				tt.Type("yor")
				tt.Key(0, ui.KeyEnter)
			}
			tabTo(t, tt, item.title)
			tt.Key(0, ui.KeyTab)
			menu := "More options for " + item.title
			if !tt.Focused(menu) {
				t.Fatal("Tab lost the menu button")
			}
			tt.Key(ui.Shift, ui.KeyTab)
			if !tt.Focused(item.title) {
				t.Fatal("Shift+Tab lost the item")
			}
			tt.Key(0, ui.KeyTab)
			tt.Key(0, ui.KeyEnter)
			if !tt.Focused(item.first) {
				t.Fatal("the menu did not focus its first action")
			}
			tt.Key(ui.Cmd, ui.KeyComma)
			if a.router.Path() != item.path {
				t.Fatal("a background shortcut ran while the menu was open")
			}
			tt.Key(0, ui.KeyEnd)
			if !tt.Focused("Copy link") {
				t.Fatal("End did not focus the last menu action")
			}
			tt.Key(0, ui.KeyEnter)
			if a.menu.open || tt.Clipboard() != item.link || !tt.Focused(menu) {
				t.Fatalf("copy: %q, open: %v, focus restored: %v", tt.Clipboard(), a.menu.open, tt.Focused(menu))
			}
			tt.Key(ui.Shift, ui.KeyTab)
			tt.Key(ui.Shift, ui.KeyF10)
			if !tt.Focused(item.first) {
				t.Fatal("Shift+F10 did not open the item's menu")
			}
			tt.Key(0, ui.KeyEscape)
			if a.menu.open || !tt.Focused(item.title) {
				t.Fatal("Escape did not return to the item")
			}
			if item.path == "/home" {
				tt.Key(0, ui.KeyTab)
				tt.Key(0, ui.KeyTab)
				if !tt.Focused("Play Deep Focus") {
					t.Fatal("Tab did not reach the card's play button")
				}
				tt.Key(0, ui.KeyEnter)
				if a.current.VideoID != "vid-3" || a.router.Path() != "/home" {
					t.Fatal("Enter did not play the album from its card")
				}
			}
		})
	}
}

func TestKeyboardSuggestionMenuReturnsToTheSearch(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyK)
	tt.Type("yor")
	tabTo(t, tt, "Suggested Song")
	tt.Key(ui.Shift, ui.KeyF10)
	if !tt.Focused("Play next") {
		t.Fatal("the suggestion's menu did not take focus")
	}
	tt.Key(0, ui.KeyEscape)
	if a.menu.open || !tt.Focused("Suggested Song") || !a.search.suggestOpen {
		t.Fatal("Escape did not return to the search suggestion")
	}
}

func TestKeyboardPlayerTabsAndQueue(t *testing.T) {
	a, _ := playbackApp(t)
	a.current = tracks("a")[0]
	a.queue.replace(a.current, tracks("a", "b", "c"), 0, "Keyboard test")
	tt := ui.NewTester(a.view, 1280, 800)
	tt.Frame()
	tabTo(t, tt, "Up next")
	tt.Key(0, ui.KeyEnter)
	tt.Advance(time.Second)
	tabTo(t, tt, "Up next")
	for _, step := range []struct {
		key   ui.Key
		tab   panelTab
		label string
	}{{ui.KeyRight, tabLyrics, "Lyrics"}, {ui.KeyEnd, tabRelated, "Related"}, {ui.KeyHome, tabQueue, "Up next"}} {
		tt.Key(0, step.key)
		if a.nowPlaying.tab != step.tab || !tt.Focused(step.label) {
			t.Fatalf("%v did not select and focus %s", step.key, step.label)
		}
	}
	// Tab enters the queue list. Home and Down choose the second track.
	tabTo(t, tt, "Track a")
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyDown)
	tt.Key(ui.Shift, ui.KeyF10)
	if !tt.Focused("Move up") {
		t.Fatal("the selected queue track's menu did not take focus")
	}
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	if !tt.Focused("Remove from queue") {
		t.Fatal("Down did not reach Remove from queue")
	}
	tt.Key(0, ui.KeyEnter)
	if a.queue.len() != 2 || a.queue.at(1).VideoID != "c" {
		t.Fatal("keyboard did not remove only Track b")
	}
	tt.Key(0, ui.KeyEnd)
	tt.Key(0, ui.KeyEnter)
	if a.current.VideoID != "c" {
		t.Fatal("Enter did not play the selected queue track")
	}
}

func TestKeyboardVolumeIsSaved(t *testing.T) {
	for _, width := range []int{920, 1280} {
		a := newTestApp(t)
		a.current = tracks("a")[0]
		a.setVolume(37)
		a.saver.Path = filepath.Join(t.TempDir(), "settings.json")
		tt := ui.NewTester(a.view, width, 700)
		tt.Frame()
		tabTo(t, tt, "Volume")
		tt.Key(0, ui.KeyLeft)
		a.saver.Wait()
		if a.volume != 36 || settings.Load(a.saver.Path).Volume != 36 {
			t.Fatalf("width %d: keyboard volume was not applied and saved", width)
		}
		tt.Key(0, ui.KeyTab)
		if tt.Focused("Volume") {
			t.Fatalf("width %d: Tab could not leave Volume", width)
		}
	}
}

func TestKeyboardOpensTheCoverMenu(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	tabTo(t, tt, "More options for Deep Focus")
	tt.Key(0, ui.KeyEnter)
	if !a.menu.open || !tt.Focused("Start radio") {
		t.Fatal("Enter on the cover did not open its menu")
	}
}

// Space plays and pauses wherever the focus is: a click leaves it on the
// control it pressed, and that control would take Space as a click of its own.
// Enter still presses it, and a text field still takes Space as typing.
func TestSpacePlaysAndPausesAfterAClick(t *testing.T) {
	a, _ := playbackApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 4000)
	tt.Frame()
	if err := tt.Click("Album Track One"); err != nil {
		t.Fatal(err)
	}
	untilPlaying(t, a, tt)

	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !a.player.Paused() {
		t.Fatal("Space pressed the row the click focused instead of pausing")
	}
	tt.Key(0, ui.KeySpace)
	untilPlaying(t, a, tt)

	// Enter still presses the control the click focused.
	if err := tt.Click("Repeat"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	repeated := a.repeat
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.repeat == repeated {
		t.Fatal("Enter no longer presses the control the click focused")
	}

	// Space is reserved from every shell control, including a switch.
	a.router.Push("/settings")
	tt.Frame()
	if err := tt.Click("Equaliser on"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.settings.EQOn {
		t.Fatal("clicking the equaliser switch did not turn it on")
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !a.player.Paused() || !a.settings.EQOn {
		t.Fatal("Space did not pause playback without toggling the focused switch")
	}
	tt.Key(0, ui.KeySpace)
	untilPlaying(t, a, tt)
	if !a.settings.EQOn {
		t.Fatal("Space toggled the focused switch while resuming playback")
	}

	// A text field keeps Space as text input; dialogs sit outside the shell.
	a.router.Push("/search")
	tt.Frame()
	if err := tt.Click(searchPlaceholder); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.TypeKey(0, ui.KeySpace, " ")
	tt.Frame()
	if a.search.query != " " || a.player.Paused() {
		t.Fatal("Space was not typed into the search field or affected playback")
	}
}

// untilPlaying waits for the track to start, which takes a moment.
func untilPlaying(t *testing.T, a *app, tt *ui.Tester) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !a.player.Playing() && time.Now().Before(deadline) {
		a.poll()
		tt.Frame()
		time.Sleep(10 * time.Millisecond)
	}
	if !a.player.Playing() {
		t.Fatal("the track did not start")
	}
}
