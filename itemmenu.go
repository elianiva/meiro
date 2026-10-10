package main

import "github.com/egoist/mygo/ui"

// itemMenu is the state of the one item menu a window shows at a time: which
// item opened it and where it points. Cards, song rows and the player's
// artwork all share it, so their menus open, close and anchor alike.
type itemMenu struct {
	open bool
	key  string
	// atPointer marks a menu the secondary button opened, whose anchor is the
	// pointer rather than the item's menu button; x and y are where the
	// pointer was, in the box of the item clicked.
	atPointer bool
	x, y      float32
}

// opened says whether the menu is open on the item with this key.
func (m *itemMenu) opened(key string) bool { return m.open && m.key == key }

// owns says whether the menu was last opened on the item with this key, open
// or not: the item keeps building the menu until it has played its exit.
func (m *itemMenu) owns(key string) bool { return m.key == key }

// show opens the menu on the item with this key. A menu opened by the pointer
// points at x, y rather than at the item's menu button.
func (m *itemMenu) show(key string, atPointer bool, x, y float32) {
	m.key, m.open, m.atPointer = key, true, atPointer
	if atPointer {
		m.x, m.y = x, y
	}
}

func (m *itemMenu) close() { m.open = false }

// pointerAnchor is a box at where the pointer was, in the layout of the item
// the menu was opened on, if the secondary button opened the menu on the item
// with this key. It must be built inside that item.
func (m *itemMenu) pointerAnchor(c *ui.Context, key string) (ui.Element, bool) {
	if !m.atPointer || m.key != key {
		return ui.Element{}, false
	}
	return ui.Box(c).Size(1, 1).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).Left(m.x).Top(m.y), true
}

// anchor picks what the menu points at: the pointer's box if there is one,
// else the item's menu button.
func (m *itemMenu) anchor(button, pointer ui.Element, hasPointer bool) ui.Element {
	if m.atPointer && hasPointer {
		return pointer
	}
	return button
}
