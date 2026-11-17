package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
)

type keyboardState struct {
	keys      *keymap.Map
	recording keymap.Action
	field     ui.Handle
	problem   string
	hints     bool
	heldSince time.Time
	hintDelay time.Duration
}

const hintDelay = 500 * time.Millisecond

func (a *app) keyMap() keymap.Map {
	if a.keyboard.keys == nil {
		m := keymap.New(a.settings.Shortcuts)
		a.keyboard.keys = &m
	}
	return *a.keyboard.keys
}

func (a *app) shortcutsChanged() {
	a.keyboard.keys = nil
	a.keyboard.problem = ""
	a.saveSettings()
}

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

func (a *app) resetShortcut(action keymap.Action) {
	delete(a.settings.Shortcuts, string(action))
	a.shortcutsChanged()
}

func (a *app) resetShortcuts() {
	a.settings.Shortcuts = nil
	a.shortcutsChanged()
}

func (a *app) shortcuts(c *ui.Context) {
	if a.keyboard.recording != "" {
		return
	}
	keys := a.keyMap()
	for _, info := range keymap.Actions {
		playerNotOpen := info.Action == keymap.ClosePlayer && !a.nowPlaying.open
		if playerNotOpen {
			continue
		}
		for _, b := range keys.Bindings(info.Action) {
			if c.Shortcut(b.Modifiers(), b.Key) {
				a.perform(info.Action)
			}
		}
	}
}

func (a *app) reserveSpaceForPlayback(shell ui.Element) {
	if a.keyboard.recording != "" {
		return
	}
	for _, b := range a.keyMap().Bindings(keymap.PlayPause) {
		if b.Key != ui.KeySpace || b.Modifiers() != 0 {
			continue
		}
		shell.OnShortcut(0, ui.KeySpace, func() { a.perform(keymap.PlayPause) })
	}
}

const volumeStep = 5

const (
	playPauseBadgeDrop = -12
	defaultBadgeDrop   = -16
)

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
		a.searchField.Focus()
		a.navigate(routeSearch)
	case keymap.Home:
		a.navigate(routeHome)
	case keymap.Explore:
		a.navigate(routeExplore)
	case keymap.Library:
		a.navigate(routeLibrary)
	case keymap.Settings:
		a.navigate(routeSettings)
	case keymap.Refresh:
		if r, _ := a.route(); r.refreshable() {
			a.reloadPage()
		}
	case keymap.ClosePlayer:
		a.nowPlaying.closing = true
	case keymap.ZoomIn:
		a.zoomBy(1)
	case keymap.ZoomOut:
		a.zoomBy(-1)
	case keymap.ResetZoom:
		a.resetZoom()
	}
}

func (a *app) startRecording(action keymap.Action) {
	a.keyboard.recording, a.keyboard.problem = action, ""
	a.keyboard.field.Focus()
}

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
			ui.Row(c.Key(info.Action)).Gap(16).AlignItems(ui.Center).Children(func() {
				m3.Text(c, m3.BodyLarge, info.Title).TextColor(sc.OnSurface).Grow(1)
				recording := a.keyboard.recording == info.Action
				label := keys.Labels(info.Action, keymap.OS())
				kind := m3.Tonal
				if recording {
					label, kind = "Press the new keys…", m3.Filled
				}
				b := m3.Button(c, m3.ButtonSpec{Label: label, Kind: kind, Size: m3.Small40, Key: "key-" + string(info.Action)})
				if recording {
					b.Bind(&a.keyboard.field).HandleInput(func(ev ui.InputEvent) bool { return a.captureKey(info.Action, ev) })
				}
				if b.Clicked() {
					a.startRecording(info.Action)
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

var glyphIcons = map[keymap.Glyph]*ui.SVG{
	keymap.GlyphLeft: m3.IconArrowBack, keymap.GlyphRight: m3.IconArrowForward,
	keymap.GlyphUp: m3.IconArrowUp, keymap.GlyphDown: m3.IconArrowDown,
	keymap.GlyphShift: m3.IconShift, keymap.GlyphSpace: m3.IconSpaceBar, keymap.GlyphEnter: m3.IconKeyReturn,
}

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

func (a *app) hinted(c *ui.Context, build func(), actions ...keymap.Action) ui.Element {
	return ui.Row(c.Key(actions[0])).Shrink(0).Children(func() {
		build()
		if a.keyboard.hints {
			parts := a.hintParts(actions...)
			if len(parts) == 0 {
				return
			}
			at := ui.Box(c).Absolute().PassThrough()
			if actions[0] == keymap.Search {
				at.Top(13).Right(16)
			} else {
				drop := float32(defaultBadgeDrop)
				if actions[0] == keymap.PlayPause {
					drop = playPauseBadgeDrop
				}
				at.Left(0).Right(0).Bottom(drop).Center()
			}
			at.Children(func() { m3.KeyBadge(c, parts...) })
		}
	})
}
