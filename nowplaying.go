package main

import (
	"context"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// playerArt is the size, in pixels, the player asks artwork for.
const playerArt = 160

// panelTab is a tab of the full-screen player's side panel.
type panelTab uint8

const (
	tabQueue panelTab = iota
	tabLyrics
	tabRelated
)

// panelTabs are the side panel's tabs, in the order they show.
var panelTabs = [...]struct {
	tab   panelTab
	label string
}{{tabQueue, "Up next"}, {tabLyrics, "Lyrics"}, {tabRelated, "Related"}}

// nowPlayingState is the full-screen player, and what its side panel shows.
type nowPlayingState struct {
	open bool
	// closing is set while the player sinks away, before open is cleared.
	closing bool
	// navigateTo is a metadata destination waiting for the close animation.
	navigateTo *youtube.BylinePart
	tab        panelTab
	// playerShown says the player has been built, so its rise starts on its
	// first frame only.
	playerShown bool
	// queueOnly shows the side panel alone, in a window too narrow for both.
	queueOnly            bool
	queueList            ui.ListState
	queueSelection       ui.Selection[string]
	queueSelectionAnchor int
	queueSelectionCursor int
	// queueFollow is the track the queue's list last scrolled to.
	queueFollow  string
	volumeHeld   bool
	volumePopup  bool
	lyrics       lyricsState
	lyricsScroll ui.ScrollState
	related      relatedState
	relatedList  ui.ListState
	// miniOpen is whether the tray's mini player shows. Its side panel then
	// loads what its tab shows, as the full-screen player's does.
	miniOpen bool
}

// relatedState is the related songs of the track playing.
type relatedState struct {
	load    task
	videoID string
	items   []youtube.MusicItem
	err     string
}

// openPlayer opens the full-screen player on a tab.
func (a *app) openPlayer(tab panelTab, queueOnly bool) {
	a.nowPlaying.navigateTo = nil
	a.nowPlaying.open, a.nowPlaying.closing = true, false
	a.nowPlaying.tab, a.nowPlaying.queueOnly = tab, queueOnly
	a.loadPanel()
}

// setMiniShown records that the mini player opened or closed, and loads its
// panel when it opens.
func (a *app) setMiniShown(open bool) {
	a.nowPlaying.miniOpen = open
	a.loadPanel()
}

// loadPanel starts the load behind the tab of the side panel that is showing.
// Opening the panel, choosing a tab and starting a track call it; building the
// view never does.
func (a *app) loadPanel() {
	if a.notch.expanded {
		a.loadLyrics()
	}
	if !a.nowPlaying.open && !a.nowPlaying.miniOpen {
		return
	}
	switch a.nowPlaying.tab {
	case tabLyrics:
		a.loadLyrics()
	case tabRelated:
		a.loadRelated()
	case tabQueue:
	}
}

// panelTimeout bounds the load of a side panel tab.
const panelTimeout = time.Minute

// loadRelated fetches related songs for the track playing, once per track.
func (a *app) loadRelated() {
	r := &a.nowPlaying.related
	id := a.current.VideoID
	if id == "" || r.videoID == id {
		return
	}
	r.videoID, r.items, r.err = id, nil, ""
	client := a.client()
	startTask(a, &r.load, panelTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		return client.GetRelated(ctx, id)
	}, func(result *youtube.BrowseResult, err error) {
		switch {
		case err != nil:
			r.err = "Related songs aren't available."
		case len(result.Items) == 0:
			r.err = "No related songs for this track."
		default:
			r.items = result.Items
		}
	})
}

// riseDistance is how far below its place the player starts, and ends.
const riseDistance = 80

// nowPlayingView is the full-screen player: the artwork large, the controls,
// and beside them the queue or the lyrics. It rises over the sheet, and sinks
// away when it closes.
func (a *app) nowPlayingView(c *ui.Context) {
	m3.Provide(c, a.resolveTheme(c))
	sc := m3.Of(c).Scheme
	w, h := c.Size()
	wide := w > 1040 && (h >= 700 || w > 1320)
	panel := ui.Column(c.Key("now-playing")).Absolute().Left(0).Right(0).
		Background(sc.Surface).Radius(m3.ExtraLarge).Clip()
	// The player is pinned to the sheet's edges, so it rises and sinks by
	// moving its Top and its Bottom, through m3.Rise.
	away := float32(0)
	if a.nowPlaying.closing {
		away = riseDistance
	}
	lift := m3.Rise(panel, "rise", riseDistance, away, !a.nowPlaying.playerShown, m3.SpatialDefault)
	panel.Top(lift).Bottom(-lift).Opacity(1 - min(max(lift/riseDistance, 0), 1))
	a.nowPlaying.playerShown = true
	// The sheet has already built the page beneath the closing player; drop
	// the player when its slide reaches the end.
	if a.nowPlaying.closing && lift >= riseDistance-0.5 {
		a.nowPlaying.open, a.nowPlaying.closing = false, false
		if part := a.nowPlaying.navigateTo; part != nil {
			a.nowPlaying.navigateTo = nil
			a.openTarget(part.Target(), detail{title: part.Text})
		}
		return
	}
	panel.Children(func() {
		ui.Row(c).Height(76).PaddingX(pageGutter - 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			a.hinted(c, func() {
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconExpandMore, Label: "Close the player", Key: "np-close"}).Clicked() {
					a.nowPlaying.closing = true
				}
			}, keymap.ClosePlayer)
			m3.EmphasizedText(c, m3.TitleMedium, "Now playing").TextColor(sc.OnSurfaceVariant).Grow(1)
			if !wide {
				if m3.IconButton(c, m3.IconButtonSpec{
					Icon: m3.IconQueue, Label: "Up next", Toggle: true, Selected: a.nowPlaying.queueOnly, Kind: m3.TonalIcon, Key: "np-queue",
				}).Clicked() {
					a.nowPlaying.queueOnly = !a.nowPlaying.queueOnly
				}
			}
		})
		ui.Row(c).Grow(1).MinHeight(0).Padding(0, pageGutter, pageGutter).Gap(32).AlignItems(ui.Stretch).Children(func() {
			if wide || !a.nowPlaying.queueOnly {
				a.nowPlayingMain(c, h)
			}
			if wide || a.nowPlaying.queueOnly {
				a.sidePanel(c, wide, false)
			}
		})
	})
}

// nowPlayingMain is the artwork, the name, the scrubber and the controls:
// stacked in a tall window, side by side in a short one.
func (a *app) nowPlayingMain(c *ui.Context, windowHeight float32) {
	controls := func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			a.trackLine(c, m3.HeadlineSmall, m3.TitleMedium, true).Grow(1)
			a.likeButton(c)
		})
		a.scrubber(c)
		ui.Row(c).Justify(ui.Center).Children(func() {
			a.transport(c, 72, 10)
		})
		ui.Row(c).Justify(ui.Center).Children(func() {
			a.volumeControl(c, 200)
		})
	}
	if windowHeight < 700 {
		art := min(max(windowHeight-200, 160), 320)
		ui.Row(c).Grow(1).MinWidth(0).Center().Gap(28).Children(func() {
			a.nowPlayingArtwork(c, 512, art, m3.ExtraLarge)
			ui.Column(c).Grow(1).MinWidth(260).MaxWidth(420).Gap(10).Children(controls)
		})
		return
	}
	art := min(max(windowHeight-420, 300), 440)
	ui.Column(c).Grow(1).MinWidth(0).Center().Gap(20).Children(func() {
		a.nowPlayingArtwork(c, 640, art, m3.ExtraLargeInc+8)
		ui.Column(c).Width(art).Gap(14).Children(controls)
	})
}

// nowPlayingArtwork shows the cover with its video badge and track menu.
func (a *app) nowPlayingArtwork(c *ui.Context, imageSize int, artSize, radius float32) {
	key := itemKey("now-playing-menu", a.current)
	var menuButton ui.Element
	artwork := a.art(c, a.current.Thumbnail, imageSize, artSize, radius, func() {
		if a.current.IsVideo() {
			m3.VideoBadge(c, artSize)
		}
		menuButton = m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconMore, Label: "More options for " + a.current.Title, Key: key + "-button",
			// On a picture the dots need a ground of their own.
			Container: m3.Of(c).Scheme.SurfaceContainerHighest.Alpha(0.85),
			// Off the corner by the margin a card keeps around its own: on the
			// artwork itself a flush button reads as touching the edge.
		}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
	})
	m3.Elevation(c, artwork, 3)
	if menuButton.Clicked() {
		a.menu.show(key, false, 0, 0)
	}
	if a.menu.owns(key) {
		a.songMenu(c, menuButton, key, a.current, songOptions{})
	}
}

// sidePanel holds the queue, lyrics and related songs under tabs, on a tonal card.
func (a *app) sidePanel(c *ui.Context, wide, compactQueue bool) {
	sc := m3.Of(c).Scheme
	card := ui.Column(c.Key("np-side")).Radius(m3.ExtraLarge).Background(sc.SurfaceContainerLow.Alpha(0.9)).Clip().
		Gap(4)
	if compactQueue {
		card.Padding(4, 0, 0)
	} else {
		card.Padding(8, 0, 0)
	}
	if wide {
		card.Width(480).Shrink(0)
	} else {
		card.Grow(1).MinWidth(0).MinHeight(0)
	}
	card.Children(func() {
		a.nowPlayingTabs(c, compactQueue)
		switch a.nowPlaying.tab {
		case tabLyrics:
			a.lyricsView(c)
		case tabRelated:
			a.relatedView(c)
		case tabQueue:
			a.queueView(c, compactQueue)
		}
	})
}

func (a *app) nowPlayingTabs(c *ui.Context, compact bool) {
	sc := m3.Of(c).Scheme
	selectedTab := int(a.nowPlaying.tab)
	group := ui.TabsBase(c.Key("np-tabs"), &selectedTab, len(panelTabs))
	tabs := group.List
	if compact {
		tabs.Padding(4, 8, 0)
	} else {
		// The card keeps 8 above the tabs, so 4 here gives the row the same
		// 12 the card's sides give it.
		tabs.Padding(4, 12, 8)
	}
	tabs.Gap(4).Children(func() {
		for i, t := range panelTabs {
			button := group.Tab(i)
			selected := selectedTab == i
			container, content := ui.Transparent, sc.OnSurfaceVariant
			if selected {
				container, content = sc.SecondaryContainer, sc.OnSecondaryContainer
			}
			button.Grow(1).Basis(0).MinWidth(0).Height(m3.MinTarget).Center().Radius(m3.Full).
				Cursor(ui.CursorPointer).Label(t.label).Tooltip(t.label).Background(ui.Transparent)
			if compact {
				button.Margin(-4, 0).Draw(func(p *ui.Painter, r ui.Rect) {
					r.Y += (r.H - 40) / 2
					r.H = 40
					p.Fill(r, m3.StateFill(container, content, button.Hovered(), button.Pressed(), button.FocusVisible()), r.H/2)
				})
			} else {
				button.Background(m3.StateFill(container, content, button.Hovered(), button.Pressed(), button.FocusVisible()))
			}
			button.Children(func() {
				role := m3.LabelMedium
				if compact {
					role = m3.LabelSmall
				}
				text := m3.Text(c, role, t.label).SingleLine().TextAlign(ui.Center).TextColor(content)
				if selected {
					text.FontWeight(600)
				}
			})
		}
	})
	if tabs.Changed() {
		a.nowPlaying.tab = panelTabs[selectedTab].tab
		a.loadPanel()
	}
}

// queueSourceLine names the list the queue plays from, on one line: "Playing
// from" and the playlist in the same size, the playlist in bold. The label
// roles are used because their emphasis weight is the bold one.
func (a *app) queueSourceLine(c *ui.Context, compact bool) {
	sc := m3.Of(c).Scheme
	role := m3.LabelLarge
	if compact {
		role = m3.LabelMedium
	}
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		m3.Text(c, role, "Playing from").Shrink(0).TextColor(sc.OnSurfaceVariant)
		m3.EmphasizedText(c, role, a.queue.source).Grow(1).MinWidth(0).SingleLine()
	})
}

// queueRowKey is the list identity of a queue row, including repeated
// occurrences of the same track. The divider has an identity of its own.
func queueRowKey(q *queue, row int) string {
	if q.isDivider(row) {
		return "queue-recommendations"
	}
	if index, ok := q.trackAt(row); ok {
		return itemKey("queue", q.at(index)) + "#" + strconv.Itoa(index)
	}
	return ""
}

// selectQueueRow applies a modifier-click to the queue's multi-selection.
func (a *app) selectQueueRow(index int, mods ui.Modifiers) {
	row := a.queue.rowOf(index)
	key := queueRowKey(&a.queue, row)
	if mods&ui.Shift != 0 {
		anchor := a.nowPlaying.queueSelectionAnchor
		if anchor < 0 || anchor >= a.queue.len() {
			a.nowPlaying.queueSelection.Clear()
			a.nowPlaying.queueSelection.Add(key)
			a.nowPlaying.queueSelectionAnchor = index
		} else {
			a.nowPlaying.queueSelection.Clear()
			for i := min(anchor, index); i <= max(anchor, index); i++ {
				a.nowPlaying.queueSelection.Add(queueRowKey(&a.queue, a.queue.rowOf(i)))
			}
		}
	} else {
		selection := &a.nowPlaying.queueSelection
		if selection.Has(key) {
			selection.Remove(key)
		} else {
			selection.Add(key)
		}
		a.nowPlaying.queueSelectionAnchor = index
	}
	// ListState.Selected is a rendered row index, which can differ from the
	// queue item index when generated tracks add the recommendations divider.
	a.nowPlaying.queueSelectionCursor = row
}

// queueView lists the tracks of the queue, with the current one picked out.
func (a *app) queueView(c *ui.Context, compact bool) {
	sc := m3.Of(c).Scheme
	q := &a.queue
	// One rhythm down the panel: the source, the switch and the count sit the
	// same distance apart, measured to their text, and the list follows at
	// the same distance. The switch's row is 28 tall around its 16 line in
	// the mini player, so its text sits 6 lower there; the count's row holds
	// the Clear queue button, whose 48 target draws 8 above and below the 32
	// the row takes.
	gap, top, pull, pad := float32(20), float32(8), float32(8), float32(16)
	rowGap := float32(12)
	if compact {
		gap, top, pull, pad, rowGap = 8, 0, 8, 10, 8
	}
	ui.Column(c).Padding(top, 16, 0).Gap(gap).Children(func() {
		if q.source != "" {
			a.queueSourceLine(c, compact)
		}
		ui.Row(c).Gap(rowGap).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				if compact {
					m3.EmphasizedText(c, m3.LabelMedium, "Auto-play")
				} else {
					m3.EmphasizedText(c, m3.TitleMedium, "Auto-play")
					m3.Text(c, m3.BodySmall, "Add similar music when this queue ends.").TextColor(sc.OnSurfaceVariant).SingleLine()
				}
			})
			on := a.settings.AutoPlay
			switchControl := m3.Switch
			if compact {
				switchControl = m3.CompactSwitch
			}
			if switchControl(c, &on, "Toggle auto-play").Changed() {
				a.setAutoPlay(on)
			}
		})
		if a.upNext.err != "" {
			m3.Text(c, m3.BodySmall, "Recommendations aren't available right now.").TextColor(sc.Error).Shrink(0)
		}
		// The count's row: pulled up and padded by the target the Clear queue
		// button draws around its layout.
		ui.Row(c).Margin(-pull, 0).Padding(0, 0, pad).AlignItems(ui.Center).Children(func() {
			m3.Text(c, m3.BodySmall, strconv.Itoa(q.remaining())+" up next").Grow(1).TextColor(sc.OnSurfaceVariant)
			clear := func() {
				m3.Button(c, m3.ButtonSpec{Label: "Clear queue", Kind: m3.TextOnly, Size: m3.ExtraSmall32, Disabled: q.remaining() < 1, Key: "queue-clear"}).OnClick(a.clearUpcoming)
			}
			if compact {
				// The button's shape keeps 12 beside its label and the label's
				// last letter stops 4 short of that; the margin puts the label
				// on the list's edge, where the lengths end.
				ui.Row(c).Shrink(0).Margin(0, -16, 0, 0).Children(clear)
				return
			}
			clear()
		})
	})
	if q.len() == 0 {
		a.message(c, m3.IconQueue, "The queue is empty", "", "", nil)
		return
	}
	list := &a.nowPlaying.queueList
	// A press in a row can replace the queue while the list still asks about
	// a row of the one before, so every question checks the row is there.
	list.Key = func(row int) any { return queueRowKey(q, row) }
	list.Selection = &a.nowPlaying.queueSelection
	list.Selected = &a.nowPlaying.queueSelectionCursor
	list.MenuKeepsSelection = true
	list.Label = func(row int) string {
		if q.isDivider(row) {
			return "Auto-play recommendations"
		}
		if index, ok := q.trackAt(row); ok {
			return q.at(index).Title
		}
		return ""
	}
	list.Header = q.isDivider
	// Rows drag to a new place in the queue; the divider between chosen and
	// generated tracks is not draggable itself.
	list.Reorder = a.moveQueue
	if a.nowPlaying.queueFollow != a.current.VideoID {
		a.nowPlaying.queueFollow = a.current.VideoID
		list.ScrollTo(q.rowOf(q.index), ui.Start)
	}
	listBottomPadding := float32(16)
	if compact {
		listBottomPadding = 4
	}
	view := ui.List(c, list, q.rows(), func(row int) {
		index, ok := q.trackAt(row)
		if !ok {
			// Center the heading between the visible rows, including the next row's artwork inset.
			height := float32(40)
			topPadding := float32(8)
			if compact {
				height = 32
				topPadding = 4
			}
			ui.Column(c).Height(height).Padding(topPadding, 8, 0).Justify(ui.Center).Background(sc.SurfaceContainerLow.Mix(sc.Surface, 0.1)).Children(func() {
				m3.EmphasizedText(c, m3.TitleSmall, "Recommended").TextColor(sc.OnSurfaceVariant).SingleLine()
			})
			return
		}
		a.songRow(c, q.at(index), q.items, songOptions{directQueue: true, source: q.source, list: "queue", position: index, compact: compact})
	}).Grow(1).Padding(0, 8, listBottomPadding).FocusRing(false)
	if index, ok := q.trackAt(a.nowPlaying.queueSelectionCursor); ok {
		if view.Submitted() {
			a.clearQueueSelection()
			a.playFrom(q.at(index), q.items, index, youtube.UpNextOptions{}, q.source)
		}
		if list.Shortcut(c, ui.Shift, ui.KeyF10) {
			a.menu.show(songKey(q.at(index), songOptions{list: "queue", position: index}), false, 0, 0)
			c.Invalidate()
		}
	}
}

func (a *app) relatedView(c *ui.Context) {
	r := &a.nowPlaying.related
	switch {
	case r.load.busy():
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicator(c, 56, true) })
	case r.err != "":
		a.message(c, m3.IconExplore, r.err, "", "", nil)
	default:
		items := r.items
		list := &a.nowPlaying.relatedList
		list.Key = func(i int) any {
			if i >= len(items) {
				return nil
			}
			return itemKey("related", items[i]) + "#" + strconv.Itoa(i)
		}
		list.Label = func(i int) string {
			if i >= len(items) {
				return ""
			}
			return items[i].Title
		}
		ui.List(c, list, len(items), func(i int) {
			a.songRow(c, items[i], items, songOptions{directQueue: true, source: "Related", list: "related", position: i})
		}).Grow(1).Padding(0, 8, 16)
	}
}
