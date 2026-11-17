package keymap

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

type Action string

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
	Refresh     Action = "refresh"
	ClosePlayer Action = "closePlayer"
	ZoomIn      Action = "zoomIn"
	ZoomOut     Action = "zoomOut"
	ResetZoom   Action = "resetZoom"
)

type Binding struct {
	Cmd  bool
	Mods ui.Modifiers
	Key  ui.Key
}

func (b Binding) Modifiers() ui.Modifiers {
	if b.Cmd {
		return b.Mods | ui.Cmd
	}
	return b.Mods
}

func FromEvent(mods ui.Modifiers, key ui.Key) Binding {
	b := Binding{Mods: mods &^ ui.Cmd, Key: key}
	b.Cmd = mods&ui.Cmd != 0
	return b
}

type Info struct {
	Action   Action
	Title    string
	Group    string
	Defaults []Binding
}

func cmd(key ui.Key) Binding { return Binding{Cmd: true, Key: key} }

var Actions = []Info{
	{PlayPause, "Play or pause", "Playback", []Binding{{Key: ui.KeySpace}}},
	{Previous, "Previous track", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyJ}}},
	{Next, "Next track", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyL}}},
	{VolumeUp, "Volume up", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyI}}},
	{VolumeDown, "Volume down", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyO}}},
	{Mute, "Mute", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyM}}},
	{Shuffle, "Shuffle", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyS}}},
	{Repeat, "Repeat", "Playback", []Binding{{Cmd: true, Mods: ui.Shift, Key: ui.KeyR}}},
	{Search, "Search", "Navigation", []Binding{cmd(ui.KeyK), cmd(ui.KeyF)}},
	{Home, "Home", "Navigation", []Binding{cmd(ui.Key1)}},
	{Explore, "Explore", "Navigation", []Binding{cmd(ui.Key2)}},
	{Library, "Library", "Navigation", []Binding{cmd(ui.Key3)}},
	{Settings, "Settings", "Navigation", []Binding{cmd(ui.KeyComma)}},
	{Refresh, "Refresh the page", "Navigation", []Binding{cmd(ui.KeyR)}},
	{ClosePlayer, "Close the player panel", "Navigation", []Binding{{Key: ui.KeyEscape}}},
	{ZoomIn, "Zoom in", "Window", []Binding{cmd(ui.KeyEqual)}},
	{ZoomOut, "Zoom out", "Window", []Binding{cmd(ui.KeyMinus)}},
	{ResetZoom, "Actual size", "Window", []Binding{cmd(ui.Key0)}},
}

var Groups = []string{"Playback", "Navigation", "Window"}

func Title(a Action) string {
	for _, info := range Actions {
		if info.Action == a {
			return info.Title
		}
	}
	return string(a)
}

type Map struct {
	bindings map[Action][]Binding
}

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

func (m Map) Bindings(a Action) []Binding { return m.bindings[a] }

func (m Map) Primary(a Action) (Binding, bool) {
	if bs := m.bindings[a]; len(bs) > 0 {
		return bs[0], true
	}
	return Binding{}, false
}

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

func IsDefault(a Action, b Binding) bool {
	for _, info := range Actions {
		if info.Action == a {
			return len(info.Defaults) == 1 && info.Defaults[0] == b
		}
	}
	return false
}

func (m Map) Label(a Action, goos string) string {
	if b, ok := m.Primary(a); ok {
		return b.Label(goos)
	}
	return "—"
}

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

func OS() string { return runtime.GOOS }

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

type Glyph uint8

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

type Part struct {
	Text  string
	Glyph Glyph
}

var keyGlyphs = map[ui.Key]Glyph{
	ui.KeyLeft: GlyphLeft, ui.KeyRight: GlyphRight, ui.KeyUp: GlyphUp, ui.KeyDown: GlyphDown,
	ui.KeySpace: GlyphSpace, ui.KeyEnter: GlyphEnter,
}

func (b Binding) HintParts(goos string) []Part {
	if !b.Cmd {
		return nil
	}
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

func CmdLabel(goos string) string {
	if goos == "darwin" {
		return "⌘"
	}
	return "Ctrl"
}

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
	b = FromEvent(b.Modifiers(), key)
	return b, true
}

func Known(key ui.Key) bool { _, ok := nameByKey[key]; return ok }
