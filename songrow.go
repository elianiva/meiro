package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const rowActionGap float32 = 9

const rowEdgeGap float32 = 4

const queueList = "queue"

type songOptions struct {
	number      int
	list        string
	position    int
	track       int
	inset       bool
	directQueue bool
	source      string
	compact     bool
	calls       rowCalls
	callsSet    bool
	rowKey      string
	row         int
}

type rowCalls struct {
	key        string
	menu       string
	menuLabel  string
	queue      string
	queueLabel string
}

func newRowCalls(item youtube.MusicItem, key, list string) rowCalls {
	calls := rowCalls{
		key:       key,
		menu:      key + "-menu",
		menuLabel: "More options for " + item.Title,
	}
	if list == queueList {
		calls.queue = key + "-remove"
		calls.queueLabel = "Remove " + item.Title + " from queue"
	}
	return calls
}

type queueCallKey struct {
	videoID, title string
	position       int
	row            int
}

type queueRowView struct {
	calls  rowCalls
	rowKey string
}

const queueViewsKept = 64

func (a *app) queueRowCalls(item youtube.MusicItem, o songOptions, row int) (rowCalls, string) {
	key := queueCallKey{item.VideoID, item.Title, o.position, row}
	if view, ok := a.nowPlaying.queueViews[key]; ok {
		return view.calls, view.rowKey
	}
	if len(a.nowPlaying.queueViews) >= queueViewsKept {
		clear(a.nowPlaying.queueViews)
	}
	if a.nowPlaying.queueViews == nil {
		a.nowPlaying.queueViews = make(map[queueCallKey]queueRowView, 32)
	}
	view := queueRowView{calls: newRowCalls(item, songKey(item, o), o.list), rowKey: queueRowKey(&a.queue, row)}
	a.nowPlaying.queueViews[key] = view
	return view.calls, view.rowKey
}

func songKey(item youtube.MusicItem, o songOptions) string {
	return itemKey("song", item) + "\x00" + strconv.Itoa(o.number) + "\x00" + o.list + "#" + strconv.Itoa(o.position)
}

func (a *app) playingRow(item youtube.MusicItem, o songOptions) bool {
	if !item.IsTrack() {
		return false
	}
	if o.list == queueList {
		return o.position == a.queue.index
	}
	return item.VideoID == a.current.VideoID
}

func (a *app) songRow(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem, o songOptions) {
	sc := m3.Of(c).Scheme
	isSong := item.IsTrack()
	playing := a.playingRow(item, o)
	blocked := o.list != queueList && a.artistBlocks(item)
	selected := false
	queueCursor := false
	if o.list == queueList {
		rowKey := o.rowKey
		if rowKey == "" {
			rowKey = queueRowKey(&a.queue, o.row)
		}
		selected = a.nowPlaying.queueSelection.Has(rowKey)
		queueCursor = a.nowPlaying.queueList.Focused(c) && a.nowPlaying.queueSelectionCursor == o.row
	}
	calls := o.calls
	if !o.callsSet {
		calls = newRowCalls(item, songKey(item, o), o.list)
	}
	key := calls.key
	playingQueueRow := o.list == queueList && o.position == a.queue.index
	radius := c.Theme().Radius
	height, artSize, padding, gap, iconSize := float32(rowHeight), float32(48), float32(8), float32(12), float32(24)
	titleRole, subtitleRole := m3.BodyLarge, m3.BodyMedium
	artPixels := 128
	described := o.inset && item.Description != ""
	switch {
	case o.compact:
		height, artSize, padding, gap, iconSize, artPixels = 44, 32, 4, 8, 20, 96
		titleRole, subtitleRole = m3.TitleSmall, m3.BodySmall
	case described:
		height, artSize, gap, artPixels = 120, 96, 16, 256
	}
	row := ui.Row(c.Key(key))
	row.Height(height).Shrink(0).Padding(padding, 8).Gap(rowEdgeGap).AlignItems(ui.Center).Radius(radius).Clip()
	if o.inset {
		row.Margin(0, pageGutter-8)
	}
	container := ui.Transparent
	if playing {
		container = sc.SecondaryContainer
	}
	var main, menuButton, menuAnchor, duration, queueButton, playNextButton, actions ui.Element
	hasAnchor := false
	row.Children(func() {
		main = ui.ButtonBase(c.Key(key)).FocusRing(false)
		hovered := row.Hovered()
		main.Grow(1).Basis(0).MinWidth(0).Height(height - 2*padding).Gap(gap).AlignItems(ui.Center).
			Radius(radius).Cursor(ui.CursorPointer).Label(item.Title).Background(ui.Transparent)
		row.Background(m3.StateFill(container, sc.OnSurface, hovered && !playing, main.Pressed(), false))
		if main.FocusVisible() || queueCursor && selected {
			focusColor := sc.Primary
			if selected && !playing {
				focusColor = sc.OnPrimary
			}
			row.DrawOver(func(p *ui.Painter, r ui.Rect) {
				p.Stroke(ui.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}, focusColor, radius, 2)
			})
		}
		titleColour, subColour := sc.OnSurface, sc.OnSurfaceVariant
		switch {
		case playing:
			titleColour, subColour = sc.OnSecondaryContainer, sc.OnSecondaryContainer.Alpha(0.8)
		case selected:
			titleColour, subColour = sc.OnPrimary, sc.OnPrimary.Alpha(0.8)
		case blocked:
			titleColour, subColour = sc.OnSurface.Alpha(0.38), sc.OnSurfaceVariant.Alpha(0.38)
		}
		rightAlignedLength := item.Duration != ""
		main.Children(func() {
			if o.number > 0 {
				ui.Row(c).Size(artSize, artSize).Shrink(0).Center().Children(func() {
					switch {
					case playing && a.loading():
						m3.LoadingIndicatorIn(c, iconSize, sc.Primary)
					case playing:
						m3.Equalizer(c, 18, a.player.Playing(), sc.Primary)
					case hovered && isSong:
						ui.Icon(c, m3.IconPlay).FontSize(iconSize).TextColor(sc.OnSurface)
					default:
						m3.Text(c, m3.BodyLarge, strconv.Itoa(o.number)).TextColor(sc.OnSurfaceVariant)
					}
				})
			} else {
				a.art(c, item.Thumbnail, artPixels, artSize, artRadius(item, m3.Medium), func() {
					if isSong && (playing || hovered) {
						ui.Box(c).Fill().Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).Radius(artRadius(item, m3.Medium)).Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
							switch {
							case playing && a.loading():
								m3.LoadingIndicatorIn(c, iconSize, ui.RGB(255, 255, 255))
							case playing:
								m3.Equalizer(c, 18, a.player.Playing(), ui.RGB(255, 255, 255))
							default:
								ui.Icon(c, m3.IconPlay).FontSize(iconSize).TextColor(ui.RGB(255, 255, 255))
							}
						})
					}
					if item.IsVideo() {
						m3.VideoBadge(c, artSize)
					}
				})
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				title := m3.Text(c, titleRole, item.Title).SingleLine().TextColor(titleColour)
				if playing {
					title.FontWeight(600)
				}
				if described {
					m3.Text(c, m3.BodyMedium, item.Description).MaxLines(2).TextColor(subColour).Padding(2, 0)
				}
				if item.Subtitle != "" {
					m3.Text(c, subtitleRole, item.Subtitle).SingleLine().TextColor(subColour)
				}
			})
			showsDownloadMark := isSong && o.list != queueList && a.isDownloaded(item.VideoID)
			if showsDownloadMark {
				ui.Icon(c, m3.IconDownloadDone).FontSize(18).TextColor(subColour).Label("Downloaded")
			}
			buttons := isSong && !o.compact && !playingQueueRow
			rowActions := func() {
				if buttons {
					actions = ui.Row(c).Shrink(0).Gap(-5).Children(func() {
						if o.list != queueList {
							ui.Row(c).Width(40).Shrink(0).Children(func() {
								if hovered {
									playNextButton = a.playNextRowButton(c, calls, item)
								}
							})
						}
						ui.Row(c).Width(40).Shrink(0).Children(func() {
							if hovered {
								queueButton = a.queueRowButton(c, calls, o.list == queueList, item)
							}
						})
					})
				}
			}
			switch {
			case rightAlignedLength:
				ui.Row(c).Shrink(0).Gap(rowActionGap).AlignItems(ui.Center).Children(func() {
					rowActions()
					ui.Row(c).Shrink(0).MinWidth(40).Justify(ui.End).AlignItems(ui.Center).Children(func() {
						duration = m3.Text(c, m3.BodyMedium, item.Duration).SingleLine().
							TextColor(subColour).Shrink(0).FontFeatures("tnum")
					})
				})
			case item.Duration != "" || buttons:
				ui.Row(c).Shrink(0).Gap(rowActionGap).AlignItems(ui.Center).Children(func() {
					rowActions()
					if item.Duration != "" {
						duration = m3.Text(c, m3.BodyMedium, item.Duration).SingleLine().
							TextColor(subColour).Shrink(0).FontFeatures("tnum")
					}
				})
			case !isSong:
				ui.Icon(c, m3.IconChevronRight).FontSize(24).TextColor(sc.OnSurfaceVariant)
			}
			menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key)
		})
		menuContainer := ui.Transparent
		if selected && !playing {
			menuContainer = sc.SurfaceContainerHighest
		}
		menuButton = m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconMore, Label: calls.menuLabel, Key: calls.menu,
			Container: menuContainer, Size: m3.Small40,
		})
		menuShown := hovered || main.FocusVisible() || a.menu.opened(key)
		if rightAlignedLength {
			menuButton.Attach(ui.AnchorRight, ui.AnchorRight).Right(4)
		}
		menuShown = menuShown || menuButton.FocusVisible()
		if menuShown && rightAlignedLength {
			duration.Opacity(0)
		}
		if !menuShown {
			menuButton.Opacity(0).PassThrough()
		}
	})
	if isSong && !o.compact && !playingQueueRow {
		a.swipeRow(c, row, main, menuButton, actions, swipeRowOptions{key: key, remove: o.list == queueList, padding: 8, radius: radius}, func() { a.queueRowAction(item, o) })
	}
	pointer := main.RightClicked() || menuButton.RightClicked()
	if menuButton.Clicked() || pointer || main.Shortcut(ui.Shift, ui.KeyF10) {
		x, y, _ := main.PointerPosition()
		a.menu.show(key, pointer, x, y)
	}
	if a.menu.owns(key) {
		anchor := a.menu.anchor(menuButton, menuAnchor, hasAnchor)
		if isSong {
			a.songMenu(c, anchor, key, item, o)
		} else {
			a.cardMenu(c, anchor, key, item, queue)
		}
	}
	if playNextButton.Clicked() {
		a.enqueue(item, true)
		return
	}
	if queueButton.Clicked() {
		a.queueRowAction(item, o)
		return
	}
	if main.Clicked() {
		if o.list == queueList {
			mods := main.ClickModifiers()
			if mods&(ui.Cmd|ui.Shift) != 0 {
				a.selectQueueRow(o.position, mods)
				return
			}
			a.clearQueueSelection()
			if playing {
				a.togglePlay()
				return
			}
			a.start(a.queue.moveTo(o.position))
			return
		}
		switch {
		case o.directQueue && item.IsTrack():
			index := o.track
			if index < 0 {
				index = indexOf(queue, item.VideoID)
			}
			a.playFrom(item, queue, max(index, 0), youtube.UpNextOptions{}, o.source)
		case o.track >= 0:
			a.activateAt(item, queue, o.track)
		default:
			a.activate(item, queue)
		}
	}
}

func (a *app) queueRowAction(item youtube.MusicItem, o songOptions) {
	if o.list == queueList {
		a.removeFromQueue(o.position)
		return
	}
	a.enqueue(item, false)
}

func (a *app) playNextRowButton(c *ui.Context, calls rowCalls, item youtube.MusicItem) ui.Element {
	return m3.IconButton(c, m3.IconButtonSpec{
		Icon: m3.IconNext, Label: "Play " + item.Title + " next", Key: calls.key + "-play-next",
		Size: m3.Small40,
	})
}

func (a *app) queueRowButton(c *ui.Context, calls rowCalls, remove bool, item youtube.MusicItem) ui.Element {
	if remove {
		return m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconClose, Label: calls.queueLabel, Key: calls.queue, Size: m3.Small40,
		})
	}
	label, queueKey := calls.queueLabel, calls.queue
	if queueKey == "" {
		label, queueKey = "Add "+item.Title+" to queue", calls.key+"-queue"
	}
	return m3.IconButton(c, m3.IconButtonSpec{
		Icon: m3.IconQueue, Label: label, Key: queueKey, Size: m3.Small40,
	})
}
