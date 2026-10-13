package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
)

func TestDefaultShortcutsNavigate(t *testing.T) {
	a := newTestApp()
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
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	a.setVolume(50)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyUp)
	if a.volume != 55 {
		t.Errorf("volume up: %v", a.volume)
	}
	tt.Key(ui.Cmd, ui.KeyDown)
	tt.Key(ui.Cmd, ui.KeyDown)
	if a.volume != 45 {
		t.Errorf("volume down: %v", a.volume)
	}
}

// A shortcut the user moved answers to its new key only.
func TestChangedShortcutReplacesTheDefault(t *testing.T) {
	a := newTestApp()
	a.settings.Shortcuts = map[string]string{"settings": "Cmd+Shift+P"}
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyComma)
	tt.Frame()
	if a.router.Path() == "/settings" {
		t.Fatal("the old key still opens settings")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyP)
	tt.Frame()
	if a.router.Path() != "/settings" {
		t.Fatalf("the new key opened %s", a.router.Path())
	}
}

func TestRecordingANewShortcut(t *testing.T) {
	a := newTestApp()
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
	a := newTestApp()
	a.router.Push("/settings")
	tt := ui.NewTester(a.view, 1000, 5000)
	tt.Frame()
	a.keyboard.recording = keymap.Mute
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.keyboard.recording != "" || len(a.settings.Shortcuts) != 0 {
		t.Errorf("recording %q, shortcuts %v", a.keyboard.recording, a.settings.Shortcuts)
	}
}

func TestHoldingTheModifierShowsTheKeysOverTheControls(t *testing.T) {
	for _, mods := range []ui.Modifiers{ui.Ctrl, ui.Super} {
		a := newTestApp()
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
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 700)
	tt.HoldModifiers(ui.Ctrl)
	tt.Frame()
	if hasTextExactly(tt, "Space") {
		t.Error("the keys showed at once")
	}
}
