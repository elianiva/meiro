// Package keymap is the app's keyboard shortcuts: what each one does, its
// default key, how a key is written in the settings file and how it is
// labelled on each platform.
package keymap

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Action is something a shortcut does. The value is what the settings file
// calls it, so it never changes once released.
type Action string

// The actions with a shortcut.
const (
	PlayPause   Action = "playPause"
	Previous    Action = "previous"
	Next        Action = "next"
	VolumeUp    Action = "volumeUp"
	VolumeDown  Action = "volumeDown"
	Mute        Action = "mute"
	Shuffle     Action = "shuffle"
	Repeat      Action = "repeat"
	Search      Action = "search"
	Home        Action = "home"
	Explore     Action = "explore"
	Library     Action = "library"
	Settings    Action = "settings"
	ClosePlayer Action = "closePlayer"
)

// Binding is a key with the modifiers held for it. Cmd is the platform's
// shortcut modifier (Command on macOS, Control elsewhere) and is kept apart
// from the others, so a binding means the same on every platform and a
// settings file can move between them.
type Binding struct {
	Cmd  bool
	Mods ui.Modifiers // the modifiers besides Cmd
	Key  ui.Key
}

// Modifiers returns the modifiers to hold for the binding on this platform.
func (b Binding) Modifiers() ui.Modifiers {
	if b.Cmd {
		return b.Mods | ui.Cmd
	}
	return b.Mods
}

// FromEvent is the binding of a key pressed with mods held. The platform's
// shortcut modifier becomes Cmd.
func FromEvent(mods ui.Modifiers, key ui.Key) Binding {
	b := Binding{Mods: mods &^ ui.Cmd, Key: key}
	b.Cmd = mods&ui.Cmd != 0
	return b
}

// Info describes an action for the settings page.
type Info struct {
	Action Action
	Title  string
	// Group is the heading it is listed under.
	Group string
	// Defaults are the keys an unchanged action answers to. Changing the
	// shortcut replaces all of them.
	Defaults []Binding
}

func cmd(key ui.Key) Binding { return Binding{Cmd: true, Key: key} }

// Actions lists every action with a shortcut, in the order the settings page
// shows them.
var Actions = []Info{
	{PlayPause, "Play or pause", "Playback", []Binding{{Key: ui.KeySpace}}},
	{Previous, "Previous track", "Playback", []Binding{cmd(ui.KeyLeft)}},
	{Next, "Next track", "Playback", []Binding{cmd(ui.KeyRight)}},
	{VolumeUp, "Volume up", "Playback", []Binding{cmd(ui.KeyUp)}},
	{VolumeDown, "Volume down", "Playback", []Binding{cmd(ui.KeyDown)}},
	{Mute, "Mute", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyM}}},
	{Shuffle, "Shuffle", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyS}}},
	{Repeat, "Repeat", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyR}}},
	{Search, "Search", "Navigation", []Binding{cmd(ui.KeyK), cmd(ui.KeyF)}},
	{Home, "Home", "Navigation", []Binding{cmd(ui.Key1)}},
	{Explore, "Explore", "Navigation", []Binding{cmd(ui.Key2)}},
	{Library, "Library", "Navigation", []Binding{cmd(ui.Key3)}},
	{Settings, "Settings", "Navigation", []Binding{cmd(ui.KeyComma)}},
	{ClosePlayer, "Close the player panel", "Navigation", []Binding{{Key: ui.KeyEscape}}},
}

// Groups are the headings of Actions, in order.
var Groups = []string{"Playback", "Navigation"}

// Title is the name of an action, or its ID for one this version does not
// know.
func Title(a Action) string {
	for _, info := range Actions {
		if info.Action == a {
			return info.Title
		}
	}
	return string(a)
}

// Map is the shortcuts in force: the defaults, with what the user changed.
type Map struct {
	bindings map[Action][]Binding
}

// New returns the shortcuts the overrides change, written as String writes a
// binding, by action ID. An override that names no action, or reads as no
// key, is ignored and the action keeps its default: the settings file is
// edited by hand, so that is not an error. Two actions may be given the same
// key there; the settings page is what keeps that from happening (Conflict).
func New(overrides map[string]string) Map {
	m := Map{bindings: make(map[Action][]Binding, len(Actions))}
	for _, info := range Actions {
		m.bindings[info.Action] = info.Defaults
	}
	for _, info := range Actions {
		text, ok := overrides[string(info.Action)]
		if !ok {
			continue
		}
		b, ok := Parse(text)
		if !ok {
			continue
		}
		m.bindings[info.Action] = []Binding{b}
	}
	return m
}

// Bindings are the keys that do an action.
func (m Map) Bindings(a Action) []Binding { return m.bindings[a] }

// Primary is the key shown for an action, and false when it has none.
func (m Map) Primary(a Action) (Binding, bool) {
	if bs := m.bindings[a]; len(bs) > 0 {
		return bs[0], true
	}
	return Binding{}, false
}

// Conflict reports the action other than a that already answers to b.
func (m Map) Conflict(a Action, b Binding) (Action, bool) {
	for _, info := range Actions {
		if info.Action == a {
			continue
		}
		for _, have := range m.bindings[info.Action] {
			if have == b {
				return info.Action, true
			}
		}
	}
	return "", false
}

// IsDefault reports whether b is how the action is bound out of the box.
func IsDefault(a Action, b Binding) bool {
	for _, info := range Actions {
		if info.Action == a {
			return len(info.Defaults) == 1 && info.Defaults[0] == b
		}
	}
	return false
}

// Label writes the primary key of an action for display, as Binding.Label.
func (m Map) Label(a Action, goos string) string {
	if b, ok := m.Primary(a); ok {
		return b.Label(goos)
	}
	return "—"
}

// Labels writes every key of an action for display, joined by " / ".
func (m Map) Labels(a Action, goos string) string {
	bs := m.bindings[a]
	parts := make([]string, len(bs))
	for i, b := range bs {
		parts[i] = b.Label(goos)
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " / ")
}

// OS is the platform the app runs on, for Label.
func OS() string { return runtime.GOOS }

// Label writes the binding as the platform writes shortcuts: "⌘ K" on macOS
// with the modifier symbols of its menus, "Ctrl+K" on Linux and Windows.
func (b Binding) Label(goos string) string {
	key := keyLabel(b.Key)
	if goos == "darwin" {
		var sb strings.Builder
		if b.Mods&ui.Ctrl != 0 {
			sb.WriteString("⌃")
		}
		if b.Mods&ui.Alt != 0 {
			sb.WriteString("⌥")
		}
		if b.Mods&ui.Shift != 0 {
			sb.WriteString("⇧")
		}
		if b.Cmd || b.Mods&ui.Super != 0 {
			sb.WriteString("⌘")
		}
		if sb.Len() > 0 {
			sb.WriteString(" ")
		}
		return sb.String() + key
	}
	var parts []string
	if b.Cmd || b.Mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.Mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if b.Mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if b.Mods&ui.Super != 0 {
		if goos == "windows" {
			parts = append(parts, "Win")
		} else {
			parts = append(parts, "Super")
		}
	}
	return strings.Join(append(parts, key), "+")
}

// Glyph names a key that is drawn as a picture rather than written.
type Glyph uint8

// The keys drawn as pictures.
const (
	GlyphNone Glyph = iota
	GlyphLeft
	GlyphRight
	GlyphUp
	GlyphDown
	GlyphShift
	GlyphSpace
	GlyphEnter
)

// Part is one piece of what to press: a word or symbol, or a Glyph.
type Part struct {
	Text  string
	Glyph Glyph
}

var keyGlyphs = map[ui.Key]Glyph{
	ui.KeyLeft: GlyphLeft, ui.KeyRight: GlyphRight, ui.KeyUp: GlyphUp, ui.KeyDown: GlyphDown,
	ui.KeySpace: GlyphSpace, ui.KeyEnter: GlyphEnter,
}

// HintParts is what to press once the shortcut modifier is down, for a badge
// on a button: the other modifiers, then the key, with arrows, Shift, Space
// and Enter as glyphs. Cmd is left out, as it is already down; a key bound
// without it is written in full.
func (b Binding) HintParts(goos string) []Part {
	mac := goos == "darwin"
	var parts []Part
	if b.Mods&ui.Ctrl != 0 {
		parts = append(parts, Part{Text: pick(mac, "⌃", "Ctrl")})
	}
	if b.Mods&ui.Alt != 0 {
		parts = append(parts, Part{Text: pick(mac, "⌥", "Alt")})
	}
	if b.Mods&ui.Shift != 0 {
		parts = append(parts, Part{Glyph: GlyphShift})
	}
	if b.Mods&ui.Super != 0 {
		super := "Super"
		switch goos {
		case "darwin":
			super = "⌘"
		case "windows":
			super = "Win"
		}
		parts = append(parts, Part{Text: super})
	}
	if g, ok := keyGlyphs[b.Key]; ok {
		return append(parts, Part{Glyph: g})
	}
	return append(parts, Part{Text: keyLabel(b.Key)})
}

func pick[T any](ok bool, yes, no T) T {
	if ok {
		return yes
	}
	return no
}

// CmdLabel writes the platform's shortcut modifier alone: "⌘" or "Ctrl".
func CmdLabel(goos string) string {
	if goos == "darwin" {
		return "⌘"
	}
	return "Ctrl"
}

// String writes the binding as the settings file does: "Shift+Cmd+K".
func (b Binding) String() string {
	var parts []string
	if b.Mods&ui.Shift != 0 {
		parts = append(parts, "Shift")
	}
	if b.Cmd {
		parts = append(parts, "Cmd")
	}
	if b.Mods&ui.Ctrl != 0 {
		parts = append(parts, "Ctrl")
	}
	if b.Mods&ui.Alt != 0 {
		parts = append(parts, "Alt")
	}
	if b.Mods&ui.Super != 0 {
		parts = append(parts, "Super")
	}
	return strings.Join(append(parts, keyName(b.Key)), "+")
}

// Parse reads what String writes, in any case and order of modifiers.
func Parse(text string) (Binding, bool) {
	parts := strings.Split(text, "+")
	key, ok := keyByName[strings.ToLower(parts[len(parts)-1])]
	if !ok {
		return Binding{}, false
	}
	var b Binding
	for _, mod := range parts[:len(parts)-1] {
		switch strings.ToLower(mod) {
		case "cmd":
			b.Cmd = true
		case "shift":
			b.Mods |= ui.Shift
		case "ctrl":
			b.Mods |= ui.Ctrl
		case "alt":
			b.Mods |= ui.Alt
		case "super":
			b.Mods |= ui.Super
		default:
			return Binding{}, false
		}
	}
	// Control on Linux is Cmd already: keep one spelling of one key.
	b = FromEvent(b.Modifiers(), key)
	return b, true
}

// Known reports whether a key can be bound.
func Known(key ui.Key) bool { _, ok := nameByKey[key]; return ok }
