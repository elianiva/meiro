package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

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
//
// The box has no size on purpose: it only places the menu. A box with a size
// would sit over the spot the pointer pressed, and take the next right-click
// there for itself, so the item under it would never open its menu again.
func (m *itemMenu) pointerAnchor(c *ui.Context, key string) (ui.Element, bool) {
	if !m.atPointer || m.key != key {
		return ui.Element{}, false
	}
	return ui.Box(c).Size(0, 0).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).Left(m.x).Top(m.y), true
}

// anchor picks what the menu points at: the pointer's box if there is one,
// else the item's menu button.
func (m *itemMenu) anchor(button, pointer ui.Element, hasPointer bool) ui.Element {
	if m.atPointer && hasPointer {
		return pointer
	}
	return button
}

// cardMenu offers actions that make sense for a card. Songs share their
// queue and artist actions with song rows; other cards can open their page or
// copy its link.
func (a *app) cardMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem, queue []youtube.MusicItem) {
	if !a.menu.owns(key) {
		return
	}
	target := item.Target()
	if target.Kind == youtube.TargetTrack {
		a.songMenu(c, anchor, key, item, songOptions{})
		return
	}
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		label := ""
		icon := m3.IconChevronRight
		switch target.Kind {
		case youtube.TargetAlbum:
			label, icon = "Open album", m3.IconAlbum
		case youtube.TargetPlaylist:
			label, icon = "Open playlist", m3.IconQueue
		case youtube.TargetArtist:
			label, icon = "Go to artist", m3.IconPerson
		case youtube.TargetPodcast:
			label = "Open podcast"
		case youtube.TargetTrack, youtube.TargetNone:
		}
		if label != "" && m3.MenuItem(c, label, icon).Clicked() {
			a.activate(item, queue)
			close()
		}
		if target.Kind == youtube.TargetPlaylist && m3.MenuItem(c, "Shuffle play", m3.IconShuffle).Clicked() {
			a.playCollection(item, key, true)
			close()
		}
		if library := item.InLibrary(); a.canSave(library) {
			label, icon := libraryLabel(library)
			if m3.MenuItem(c, label, icon).Clicked() {
				a.toggleLibrary(library, item.Title)
				close()
			}
		}
		if (target.Kind == youtube.TargetAlbum || target.Kind == youtube.TargetPlaylist) && m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
			a.startRadio(item, key)
			close()
		}
		if target.ID != "" && m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.BrowseURL(target.ID))
			c.Toast("Link copied")
			close()
		}
		if target.Kind == youtube.TargetPlaylist && m3.MenuItem(c, "Delete playlist", m3.IconDelete).Clicked() {
			a.askDeletePlaylist(target.ID, item.Title)
			close()
		}
	})
}

// songMenu offers local queue actions and read-only actions for a track.
func (a *app) songMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem, o songOptions) {
	if !a.menu.owns(key) {
		return
	}
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		if artistID := item.ArtistID(); artistID != "" {
			if m3.MenuItem(c, "Go to artist", m3.IconPerson).Clicked() {
				a.openTarget(youtube.Target{Kind: youtube.TargetArtist, ID: artistID}, detail{})
				close()
			}
		}
		if o.list == "queue" {
			a.queueMenuItems(c, o.position, close)
		} else {
			if m3.MenuItem(c, "Play next", m3.IconNext).Clicked() {
				a.enqueue(item, true)
				close()
			}
			if m3.MenuItem(c, "Add to queue", m3.IconQueue).Clicked() {
				a.enqueue(item, false)
				close()
			}
		}
		if a.canLike(item) {
			label, icon := likeLabel(item)
			if m3.MenuItem(c, label, icon).Clicked() {
				a.toggleLike(item)
				close()
			}
		}
		if m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
			a.startRadio(item, key)
			close()
		}
		if item.VideoID != "" && m3.MenuItem(c, "Add to playlist", m3.IconLibrary).Clicked() {
			a.openAddToPlaylist(item)
			close()
		}
		if r, playlistID := a.route(); item.VideoID != "" && item.PlaylistSetID != "" && r.id == routePlaylist && m3.MenuItem(c, "Remove from playlist", m3.IconClose).Clicked() {
			a.removeFromPlaylist(playlistID, item)
			close()
		}
		if m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.WatchURL(item.VideoID))
			c.Toast("Link copied")
			close()
		}
	})
}

// queueMenuItems are the edits a row of the queue offers for the track at a
// place in it. The track playing stays where it is.
func (a *app) queueMenuItems(c *ui.Context, index int, close func()) {
	q := &a.queue
	if selected := a.selectedQueueIndices(); len(selected) > 1 {
		label := "Remove " + strconv.Itoa(len(selected)) + " tracks"
		m3.MenuItem(c, label, m3.IconClose).OnClick(func() {
			a.removeSelectedFromQueue()
			close()
		})
		return
	}
	if index == q.index || index >= q.len() {
		return
	}
	if index > 0 {
		m3.MenuItem(c, "Move up", m3.IconExpandLess).OnClick(func() {
			a.shiftInQueue(index, -1)
			close()
		})
	}
	if index < q.len()-1 {
		m3.MenuItem(c, "Move down", m3.IconExpandMore).OnClick(func() {
			a.shiftInQueue(index, 1)
			close()
		})
	}
	m3.MenuItem(c, "Remove from queue", m3.IconClose).OnClick(func() {
		a.removeFromQueue(index)
		close()
	})
}

// heroMenu offers what YouTube Music's menu on the artwork of an album, a
// playlist or an artist does: start its radio, shuffle it, or copy the page's
// link.
func (a *app) heroMenu(c *ui.Context, anchor ui.Element) {
	if !a.menu.owns("hero") {
		return
	}
	r, id := a.route()
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		if r.target != youtube.TargetPodcast {
			if m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
				a.startRadioPage()
				close()
			}
			if m3.MenuItem(c, "Shuffle play", m3.IconShuffle).Clicked() {
				a.shufflePage()
				close()
			}
		}
		if id != "" && m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.BrowseURL(id))
			c.Toast("Link copied")
			close()
		}
		if r.id == routePlaylist && m3.MenuItem(c, "Delete playlist", m3.IconDelete).Clicked() {
			a.askDeletePlaylist(id, a.page.detail.title)
			close()
		}
	})
}
