package keymap

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// keyNames are the keys that can be bound, by the name the settings file
// gives them. keyLabel is how they read on a keycap, where that differs.
var keyNames = []struct {
	key   ui.Key
	name  string
	label string
}{
	{ui.KeySpace, "Space", "Space"},
	{ui.KeyEnter, "Enter", "Enter"},
	{ui.KeyEscape, "Escape", "Esc"},
	{ui.KeyBackspace, "Backspace", "⌫"},
	{ui.KeyDelete, "Delete", "Del"},
	{ui.KeyInsert, "Insert", "Ins"},
	{ui.KeyHome, "Home", "Home"},
	{ui.KeyEnd, "End", "End"},
	{ui.KeyPageUp, "PageUp", "PgUp"},
	{ui.KeyPageDown, "PageDown", "PgDn"},
	{ui.KeyLeft, "Left", "←"},
	{ui.KeyRight, "Right", "→"},
	{ui.KeyUp, "Up", "↑"},
	{ui.KeyDown, "Down", "↓"},
	{ui.KeyMinus, "Minus", "-"},
	{ui.KeyEqual, "Equal", "="},
	{ui.KeyComma, "Comma", ","},
	{ui.KeyPeriod, "Period", "."},
	{ui.KeySlash, "Slash", "/"},
	{ui.KeySemicolon, "Semicolon", ";"},
	{ui.KeyQuote, "Quote", "'"},
	{ui.KeyBracketLeft, "BracketLeft", "["},
	{ui.KeyBracketRight, "BracketRight", "]"},
	{ui.KeyBackslash, "Backslash", "\\"},
	{ui.KeyBackquote, "Backquote", "`"},
	{ui.Key0, "0", "0"},
	{ui.Key1, "1", "1"},
	{ui.Key2, "2", "2"},
	{ui.Key3, "3", "3"},
	{ui.Key4, "4", "4"},
	{ui.Key5, "5", "5"},
	{ui.Key6, "6", "6"},
	{ui.Key7, "7", "7"},
	{ui.Key8, "8", "8"},
	{ui.Key9, "9", "9"},
	{ui.KeyA, "A", "A"},
	{ui.KeyB, "B", "B"},
	{ui.KeyC, "C", "C"},
	{ui.KeyD, "D", "D"},
	{ui.KeyE, "E", "E"},
	{ui.KeyF, "F", "F"},
	{ui.KeyG, "G", "G"},
	{ui.KeyH, "H", "H"},
	{ui.KeyI, "I", "I"},
	{ui.KeyJ, "J", "J"},
	{ui.KeyK, "K", "K"},
	{ui.KeyL, "L", "L"},
	{ui.KeyM, "M", "M"},
	{ui.KeyN, "N", "N"},
	{ui.KeyO, "O", "O"},
	{ui.KeyP, "P", "P"},
	{ui.KeyQ, "Q", "Q"},
	{ui.KeyR, "R", "R"},
	{ui.KeyS, "S", "S"},
	{ui.KeyT, "T", "T"},
	{ui.KeyU, "U", "U"},
	{ui.KeyV, "V", "V"},
	{ui.KeyW, "W", "W"},
	{ui.KeyX, "X", "X"},
	{ui.KeyY, "Y", "Y"},
	{ui.KeyZ, "Z", "Z"},
	{ui.KeyF1, "F1", "F1"},
	{ui.KeyF2, "F2", "F2"},
	{ui.KeyF3, "F3", "F3"},
	{ui.KeyF4, "F4", "F4"},
	{ui.KeyF5, "F5", "F5"},
	{ui.KeyF6, "F6", "F6"},
	{ui.KeyF7, "F7", "F7"},
	{ui.KeyF8, "F8", "F8"},
	{ui.KeyF9, "F9", "F9"},
	{ui.KeyF10, "F10", "F10"},
	{ui.KeyF11, "F11", "F11"},
	{ui.KeyF12, "F12", "F12"},
}

var (
	nameByKey  = map[ui.Key]string{}
	labelByKey = map[ui.Key]string{}
	keyByName  = map[string]ui.Key{}
)

func init() {
	for _, k := range keyNames {
		nameByKey[k.key], labelByKey[k.key] = k.name, k.label
		keyByName[strings.ToLower(k.name)] = k.key
	}
}

func keyName(k ui.Key) string  { return nameByKey[k] }
func keyLabel(k ui.Key) string { return labelByKey[k] }
