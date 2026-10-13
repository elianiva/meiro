package keymap

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The same binding reads differently on each platform, and the modifier of
// shortcuts is Command on macOS and Control elsewhere.
func TestLabelPerPlatform(t *testing.T) {
	for _, tt := range []struct {
		b                    Binding
		darwin, linux, win32 string
	}{
		{Binding{Cmd: true, Key: ui.KeyK}, "⌘ K", "Ctrl+K", "Ctrl+K"},
		{Binding{Cmd: true, Mods: ui.Shift, Key: ui.KeyLeft}, "⇧⌘ ←", "Ctrl+Shift+←", "Ctrl+Shift+←"},
		{Binding{Key: ui.KeySpace}, "Space", "Space", "Space"},
		{Binding{Mods: ui.Alt | ui.Super, Key: ui.KeyComma}, "⌥⌘ ,", "Alt+Super+,", "Alt+Win+,"},
		{Binding{Mods: ui.Ctrl, Key: ui.KeyJ}, "⌃ J", "Ctrl+J", "Ctrl+J"},
	} {
		for goos, want := range map[string]string{"darwin": tt.darwin, "linux": tt.linux, "windows": tt.win32} {
			if got := tt.b.Label(goos); got != want {
				t.Errorf("%v on %s: got %q, want %q", tt.b, goos, got, want)
			}
		}
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, info := range Actions {
		for _, b := range info.Defaults {
			got, ok := Parse(b.String())
			if !ok || got != b {
				t.Errorf("%s: %q parsed as %+v, %v; want %+v", info.Action, b.String(), got, ok, b)
			}
		}
	}
	if b, ok := Parse("shift+CMD+k"); !ok || b != (Binding{Cmd: true, Mods: ui.Shift, Key: ui.KeyK}) {
		t.Errorf("case and order are free: %+v %v", b, ok)
	}
	for _, bad := range []string{"", "Cmd+", "Hyper+K", "Cmd+Nope", "+"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestEveryDefaultIsBindableAndUnique(t *testing.T) {
	seen := map[Binding]Action{}
	for _, info := range Actions {
		if len(info.Defaults) == 0 {
			t.Errorf("%s has no default", info.Action)
		}
		for _, b := range info.Defaults {
			if !Known(b.Key) {
				t.Errorf("%s: key %d has no name", info.Action, b.Key)
			}
			if other, dup := seen[b]; dup {
				t.Errorf("%s and %s share %s", info.Action, other, b)
			}
			seen[b] = info.Action
		}
	}
}

func TestOverrides(t *testing.T) {
	m := New(map[string]string{
		"search":   "Cmd+P",
		"home":     "nonsense",
		"nowhere":  "Cmd+Q",
		"previous": "Alt+Left",
	})
	if got := m.Bindings(Search); len(got) != 1 || got[0] != (Binding{Cmd: true, Key: ui.KeyP}) {
		t.Errorf("an override replaces every default: %+v", got)
	}
	if got := m.Bindings(Home); len(got) != 1 || got[0].Key != ui.Key1 {
		t.Errorf("an unreadable override keeps the default: %+v", got)
	}
	if got := m.Label(Previous, "linux"); got != "Alt+←" {
		t.Errorf("label: %q", got)
	}
	if a, taken := m.Conflict(Home, Binding{Cmd: true, Key: ui.KeyP}); !taken || a != Search {
		t.Errorf("conflict: %q %v", a, taken)
	}
	if _, taken := m.Conflict(Search, Binding{Cmd: true, Key: ui.KeyP}); taken {
		t.Error("an action does not conflict with itself")
	}
	// The key Search gave up is free again.
	if _, taken := m.Conflict(Home, Binding{Cmd: true, Key: ui.KeyK}); taken {
		t.Error("a key given up is still taken")
	}
}

// What a keyboard reports on this platform as the shortcut modifier is Cmd.
func TestFromEventNormalisesTheShortcutModifier(t *testing.T) {
	b := FromEvent(ui.Cmd|ui.Shift, ui.KeyK)
	if !b.Cmd || b.Mods != ui.Shift {
		t.Errorf("%+v", b)
	}
	if got := b.Modifiers(); got != ui.Cmd|ui.Shift {
		t.Errorf("modifiers: %v", got)
	}
}

func TestCmdLabel(t *testing.T) {
	if CmdLabel("darwin") != "⌘" || CmdLabel("linux") != "Ctrl" || CmdLabel("windows") != "Ctrl" {
		t.Error("the shortcut modifier is Command on macOS and Control elsewhere")
	}
}

func TestHintPartsDropTheModifierAlreadyDown(t *testing.T) {
	text := func(s string) Part { return Part{Text: s} }
	glyph := func(g Glyph) Part { return Part{Glyph: g} }
	for _, tt := range []struct {
		b    Binding
		goos string
		want []Part
	}{
		{Binding{Cmd: true, Key: ui.KeyK}, "darwin", []Part{text("K")}},
		{Binding{Cmd: true, Key: ui.KeyK}, "linux", []Part{text("K")}},
		{Binding{Cmd: true, Mods: ui.Shift, Key: ui.KeyM}, "windows", []Part{glyph(GlyphShift), text("M")}},
		{Binding{Cmd: true, Mods: ui.Alt | ui.Super, Key: ui.KeyLeft}, "windows", []Part{text("Alt"), text("Win"), glyph(GlyphLeft)}},
		{Binding{Cmd: true, Mods: ui.Alt, Key: ui.KeyUp}, "darwin", []Part{text("⌥"), glyph(GlyphUp)}},
		{Binding{Key: ui.KeySpace}, "linux", []Part{glyph(GlyphSpace)}},
		{Binding{Key: ui.KeyEscape}, "linux", []Part{text("Esc")}},
	} {
		if got := tt.b.HintParts(tt.goos); !slices.Equal(got, tt.want) {
			t.Errorf("%v on %s: %v, want %v", tt.b, tt.goos, got, tt.want)
		}
	}
}
