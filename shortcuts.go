package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
)

// keyboardState is the shortcuts in force and what the user is doing to them.
type keyboardState struct {
	// keys is built from the settings on first use, and dropped when they
	// change.
	keys *keymap.Map
	// recording is the action waiting for its new key in the settings, or "".
	recording keymap.Action
	// problem says why the key just pressed was not taken.
	problem string
	// hints is set while the keys show over the controls they press.
	hints bool
	// heldSince is when the shortcut modifier went down, zero while up.
	heldSince time.Time
	// hintDelay is how long the modifier is held before the overlay of
	// shortcuts shows, so Ctrl+C does not flash it.
	hintDelay time.Duration
}

// hintDelay is how long Cmd or Ctrl is held before the shortcuts show.
const hintDelay = 500 * time.Millisecond

// keyMap returns the shortcuts in force.
func (a *app) keyMap() keymap.Map {
	if a.keyboard.keys == nil {
		m := keymap.New(a.settings.Shortcuts)
		a.keyboard.keys = &m
	}
	return *a.keyboard.keys
}

// shortcutsChanged keeps the settings after a change of a shortcut.
func (a *app) shortcutsChanged() {
	a.keyboard.keys = nil
	a.keyboard.problem = ""
	a.saveSettings()
}

// setShortcut binds an action to a key.
func (a *app) setShortcut(action keymap.Action, b keymap.Binding) {
	if keymap.IsDefault(action, b) {
		delete(a.settings.Shortcuts, string(action))
	} else {
		if a.settings.Shortcuts == nil {
			a.settings.Shortcuts = map[string]string{}
		}
		a.settings.Shortcuts[string(action)] = b.String()
	}
	a.shortcutsChanged()
}

// resetShortcut gives an action its default key back.
func (a *app) resetShortcut(action keymap.Action) {
	delete(a.settings.Shortcuts, string(action))
	a.shortcutsChanged()
}

// resetShortcuts gives every action its default key back.
func (a *app) resetShortcuts() {
	a.settings.Shortcuts = nil
	a.shortcutsChanged()
}

// shortcuts are the keys the window answers wherever the keyboard focus is
// that does not want them itself. They sleep while a new key is being
// recorded in the settings, so pressing it does not also do what it did.
func (a *app) shortcuts(c *ui.Context) {
	if a.keyboard.recording != "" {
		return
	}
	keys := a.keyMap()
	for _, info := range keymap.Actions {
		if info.Action == keymap.ClosePlayer && !a.nowPlaying.open {
			continue // Escape is for whatever else is open until the player is
		}
		for _, b := range keys.Bindings(info.Action) {
			if c.Shortcut(b.Modifiers(), b.Key) {
				a.perform(info.Action)
			}
		}
	}
}

// volumeStep is how much a shortcut moves the volume.
const volumeStep = 5

// perform does what an action means.
func (a *app) perform(action keymap.Action) {
	switch action {
	case keymap.PlayPause:
		a.togglePlay()
	case keymap.Previous:
		a.previous()
	case keymap.Next:
		a.advance()
	case keymap.VolumeUp:
		a.setVolume(a.volume + volumeStep)
		a.saveSettings()
	case keymap.VolumeDown:
		a.setVolume(a.volume - volumeStep)
		a.saveSettings()
	case keymap.Mute:
		a.toggleMute()
		a.saveSettings()
	case keymap.Shuffle:
		a.setShuffle(!a.shuffle)
	case keymap.Repeat:
		a.cycleRepeat()
	case keymap.Search:
		a.focusSearch = true
		a.navigate(routeSearch)
	case keymap.Home:
		a.navigate(routeHome)
	case keymap.Explore:
		a.navigate(routeExplore)
	case keymap.Library:
		a.navigate(routeLibrary)
	case keymap.Settings:
		a.navigate(routeSettings)
	case keymap.ClosePlayer:
		a.nowPlaying.closing = true
	}
}

// captureKey takes the key pressed while action is recording, as its new
// shortcut. Escape gives up; a key another action has is refused, saying
// which.
func (a *app) captureKey(action keymap.Action, ev ui.InputEvent) bool {
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	key := ev.Key
	if key == ui.KeyUnknown {
		key = ev.PhysicalKey
	}
	switch {
	case key == ui.KeyEscape && ev.Mods == 0:
		a.keyboard.recording, a.keyboard.problem = "", ""
	case !keymap.Known(key):
		// A key with no name, or a modifier alone: wait for another.
	default:
		b := keymap.FromEvent(ev.Mods, key)
		if other, taken := a.keyMap().Conflict(action, b); taken {
			a.keyboard.problem = b.Label(keymap.OS()) + " is already " + keymap.Title(other) + "."
			break
		}
		a.setShortcut(action, b)
		a.keyboard.recording = ""
	}
	return true
}

// keyboardCard lists the shortcuts, each a button that waits for its new key.
func (a *app) keyboardCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	keys := a.keyMap()
	a.settingsCard(c, "Keyboard", m3.IconLyrics, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Shortcuts", "Choose a shortcut to change it. Hold "+keymap.CmdLabel(keymap.OS())+" anywhere to see the key over each button.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Reset all", Kind: m3.Outlined, Size: m3.Small40, Key: "shortcuts-reset"}).Clicked() {
				a.keyboard.recording = ""
				a.resetShortcuts()
			}
		})
		if a.keyboard.problem != "" {
			m3.Text(c, m3.BodyMedium, a.keyboard.problem).TextColor(sc.Error)
		}
		for _, info := range keymap.Actions {
			ui.Row(c).Key(info.Action).Gap(16).AlignItems(ui.Center).Children(func() {
				m3.Text(c, m3.BodyLarge, info.Title).TextColor(sc.OnSurface).Grow(1)
				recording := a.keyboard.recording == info.Action
				label := keys.Labels(info.Action, keymap.OS())
				kind := m3.Tonal
				if recording {
					label, kind = "Press the new keys…", m3.Filled
				}
				b := m3.Button(c, m3.ButtonSpec{Label: label, Kind: kind, Size: m3.Small40, Key: "key-" + string(info.Action)})
				if recording {
					b.Focus().HandleInput(func(ev ui.InputEvent) bool { return a.captureKey(info.Action, ev) })
				}
				if b.Clicked() {
					a.keyboard.recording, a.keyboard.problem = info.Action, ""
				}
				if _, changed := a.settings.Shortcuts[string(info.Action)]; changed {
					if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconRefresh, Label: "Reset " + info.Title, Size: m3.ExtraSmall32, Key: "reset-" + string(info.Action)}).Clicked() {
						a.keyboard.recording = ""
						a.resetShortcut(info.Action)
					}
				}
			})
		}
	})
}

// updateHints decides whether the keys show this frame: while Cmd or Ctrl is
// held for a moment, the way to see what a modifier would do without leaving
// the page. It asks for another frame when the wait is not over.
func (a *app) updateHints(c *ui.Context) {
	a.keyboard.hints = false
	if c.Modifiers()&(ui.Ctrl|ui.Super) == 0 || a.keyboard.recording != "" {
		a.keyboard.heldSince = time.Time{}
		return
	}
	now := c.Now()
	if a.keyboard.heldSince.IsZero() {
		a.keyboard.heldSince = now
	}
	if wait := a.keyboard.hintDelay - now.Sub(a.keyboard.heldSince); wait > 0 {
		c.After(wait)
		return
	}
	a.keyboard.hints = true
}

// glyphIcons are the pictures for the keys drawn as such.
var glyphIcons = map[keymap.Glyph]*ui.SVG{
	keymap.GlyphLeft: m3.IconArrowBack, keymap.GlyphRight: m3.IconArrowForward,
	keymap.GlyphUp: m3.IconArrowUp, keymap.GlyphDown: m3.IconArrowDown,
	keymap.GlyphShift: m3.IconShift, keymap.GlyphSpace: m3.IconSpaceBar, keymap.GlyphEnter: m3.IconKeyReturn,
}

// hintParts is what to press, once the modifier is down, for actions: their
// first keys, side by side.
func (a *app) hintParts(actions ...keymap.Action) []m3.KeyPart {
	keys := a.keyMap()
	var parts []m3.KeyPart
	for _, action := range actions {
		b, ok := keys.Primary(action)
		if !ok {
			continue
		}
		for _, p := range b.HintParts(keymap.OS()) {
			parts = append(parts, m3.KeyPart{Text: p.Text, Icon: glyphIcons[p.Glyph]})
		}
	}
	return parts
}

// hinted builds a control and, while the keys show, lays the key of its
// actions over its corner. The wrapper is there in every frame, so the
// control keeps its state when the keys come and go; it is returned for the
// layout of the control to be set on it.
func (a *app) hinted(c *ui.Context, build func(), actions ...keymap.Action) ui.Element {
	return ui.Row(c).Key("hint-" + string(actions[0])).Shrink(0).Children(func() {
		build()
		if a.keyboard.hints {
			at := ui.Box(c).Absolute().PassThrough()
			if actions[0] == keymap.Search {
				// A wide field takes its key inside its end, where it reads
				// as part of the field.
				at.Top(13).Right(16)
			} else {
				// Centred under a control, where it covers none of its
				// neighbours: they are closer together than their badges
				// are wide.
				drop := float32(-16)
				if actions[0] == keymap.PlayPause {
					drop = -12 // its button is 8 taller, so this keeps the row level
				}
				at.Left(0).Right(0).Bottom(drop).Center()
			}
			at.Children(func() { m3.KeyBadge(c, a.hintParts(actions...)...) })
		}
	})
}
