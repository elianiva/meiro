package main

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// songOptions shape a row of a song.
type songOptions struct {
	// number shows a place in the list where the artwork would be.
	number int
	// list names the list the row belongs to, so that the same song in two
	// lists, as in the queue behind a page of quick picks, is two rows with
	// two keys rather than one shared menu.
	list     string
	position int
	// track is the exact occurrence in the queue. It differs from position
	// when a playlist search hides entries before this row.
	track int
	// inset puts the row in from the edges of the page, as the rows of a
	// page of songs are; rows in a shelf sit flush.
	inset bool
	// directQueue plays the row's song from the list it is in, named source,
	// rather than from the page.
	directQueue bool
	source      string
	// compact fits queue rows in the tray mini player.
	compact bool
}

// songKey identifies a row of a song in its list, for the element tree and
// for the menu state that a row opens.
func songKey(item youtube.MusicItem, o songOptions) string {
	return itemKey("song", item) + "\x00" + strconv.Itoa(o.number) + "\x00" + o.list + "#" + strconv.Itoa(o.position)
}

// playingRow says whether a row is picked out as the one playing. In the
// queue, which can hold the same song twice, it is the place that plays; in
// every other list it is the song playing, by its own ID.
func (a *app) playingRow(item youtube.MusicItem, o songOptions) bool {
	if !item.IsTrack() {
		return false
	}
	if o.list == "queue" {
		return o.position == a.queue.index
	}
	return item.VideoID == a.current.VideoID
}

// songRow shows a song, an album, an artist or a playlist as a row: artwork,
// name over details, and the length. The song playing is picked out in a
// tonal container with dancing bars.
func (a *app) songRow(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem, o songOptions) {
	sc := m3.Of(c).Scheme
	isSong := item.IsTrack()
	playing := a.playingRow(item, o)
	selected := o.list == "queue" && a.nowPlaying.queueSelection.Has(queueRowKey(&a.queue, a.queue.rowOf(o.position)))
	key := songKey(item, o)
	height, artSize, padding, gap, iconSize := float32(rowHeight), float32(48), float32(8), float32(12), float32(24)
	titleRole, subtitleRole := m3.BodyLarge, m3.BodyMedium
	artPixels := 128
	// An episode on its show's page says what it is about under its title,
	// as YouTube Music's does, so its row is taller and its picture larger.
	described := o.inset && item.Description != ""
	switch {
	case o.compact:
		height, artSize, padding, gap, iconSize, artPixels = 44, 32, 4, 8, 20, 96
		titleRole, subtitleRole = m3.TitleSmall, m3.BodySmall
	case described:
		height, artSize, gap, artPixels = 120, 96, 16, 256
	}
	row := ui.Row(c.Key(key))
	row.Height(height).Shrink(0).Padding(padding, 8).Gap(4).AlignItems(ui.Center).Radius(m3.Large)
	if o.inset {
		row.Margin(0, pageGutter-8)
	}
	container := ui.Transparent
	if playing {
		container = sc.SecondaryContainer
	}
	var main, menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	row.Children(func() {
		main = ui.ButtonBase(c.Key(key + "-activate"))
		hovered := row.Hovered()
		main.Grow(1).Basis(0).MinWidth(0).Height(height - 2*padding).Gap(gap).AlignItems(ui.Center).
			Radius(m3.Large).Cursor(ui.CursorPointer).Label(item.Title).Background(ui.Transparent)
		row.Background(m3.StateFill(container, sc.OnSurface, hovered && !playing, main.Pressed(), main.FocusVisible()))
		main.Children(func() {
			titleColour, subColour := sc.OnSurface, sc.OnSurfaceVariant
			if playing {
				titleColour, subColour = sc.OnSecondaryContainer, sc.OnSecondaryContainer.Alpha(0.8)
			} else if selected {
				titleColour, subColour = sc.OnPrimary, sc.OnPrimary.Alpha(0.8)
			}
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
			switch {
			case item.Duration != "":
				m3.Text(c, m3.BodyMedium, item.Duration).SingleLine().Width(48).TextAlign(ui.End).
					TextColor(subColour).Shrink(0).FontFeatures("tnum")
			case !isSong:
				ui.Icon(c, m3.IconChevronRight).FontSize(24).TextColor(sc.OnSurfaceVariant)
			}
			// A menu the pointer opened points at the pointer; one the menu
			// button opened points at the button.
			menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key)
		})
		// A row of the queue carries no icon buttons: a right click opens its
		// menu, which holds its edits, so the length can take the width the
		// buttons held. The pointer frees the dots; the keyboard keeps them,
		// since it has no other way to the menu.
		switch {
		case o.list == "queue" && (main.FocusVisible() || a.menu.opened(key+"-menu")):
			menuButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
			})
			hasMenu = true
		case o.list == "queue":
			hasMenu = true
		case hovered || main.FocusVisible() || a.menu.opened(key):
			menuButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
			})
			hasMenu = true
		default:
			// The button's place stays, so the length does not move when the
			// pointer arrives.
			rowButtonSlot(c)
		}
	})
	if hasMenu {
		pointer := main.RightClicked() || menuButton.RightClicked()
		if menuButton.Clicked() || pointer {
			// The pointer's place is read when the menu opens: reading it every
			// frame makes the row track the pointer, and a drag of the row then
			// never starts.
			x, y, _ := main.PointerPosition()
			a.menu.show(key, pointer, x, y)
		}
	}
	if hasMenu && a.menu.owns(key) {
		anchor := a.menu.anchor(menuButton, menuAnchor, hasAnchor)
		if isSong {
			a.songMenu(c, anchor, key, item, o)
		} else {
			// A row of an album, an artist or a playlist is a row of
			// something that opens a page, as a card in a shelf is, and
			// offers what that card's menu does.
			a.cardMenu(c, anchor, key, item, queue)
		}
	}
	if main.Clicked() {
		if o.list == "queue" {
			mods := main.ClickModifiers()
			if mods&(ui.Cmd|ui.Shift) != 0 {
				a.selectQueueRow(o.position, mods)
				return
			}
			a.clearQueueSelection()
		}
		switch {
		case o.directQueue:
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

// rowButtonSlot holds the place of a row's icon button while it is not shown.
func rowButtonSlot(c *ui.Context) {
	ui.Box(c).Size(32, 32).Shrink(0)
}
