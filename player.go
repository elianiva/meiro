package main

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// playerArt is the size, in pixels, the player asks artwork for.
const playerArt = 160

// transport builds the controls that start, stop and move through the queue:
// shuffle, previous, play or pause, next and repeat. The play button is a
// circle that squares off while the music plays, so the state shows in its
// shape. size is the play button's side.
func (a *app) transport(c *ui.Context, size float32, gap float32) {
	small := m3.IconButtonSpec{Size: m3.Small40}
	ui.Row(c).Gap(gap).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		spec := small
		spec.Icon, spec.Label, spec.Toggle, spec.Selected, spec.Key = m3.IconShuffle, "Shuffle", true, a.shuffle, "shuffle"
		if m3.IconButton(c, spec).Clicked() {
			a.shuffle = !a.shuffle
			a.syncSystemMedia()
		}
		spec = small
		spec.Icon, spec.Label, spec.Key = m3.IconPrevious, "Previous", "previous"
		if m3.IconButton(c, spec).Clicked() {
			a.previous()
		}
		loading := a.loading()
		playing := a.player.Playing() && !loading
		icon, label := m3.IconPlay, "Play"
		switch {
		case loading:
			label = "Loading"
		case playing:
			icon, label = m3.IconPause, "Pause"
		}
		if m3.IconButton(c, m3.IconButtonSpec{
			Icon: icon, Label: label, Kind: m3.FilledIcon, Dimension: size, Morph: true, Selected: playing, Loading: loading, Key: "play-pause",
		}).Clicked() {
			a.togglePlay()
		}
		spec = small
		spec.Icon, spec.Label, spec.Key = m3.IconNext, "Next", "next"
		if m3.IconButton(c, spec).Clicked() {
			a.advance()
		}
		spec = small
		repeatIcon := m3.IconRepeat
		if a.repeat == repeatTrack {
			repeatIcon = m3.IconRepeatOne
		}
		spec.Icon, spec.Label, spec.Toggle, spec.Selected, spec.Key = repeatIcon, "Repeat", true, a.repeat != repeatOff, "repeat"
		if m3.IconButton(c, spec).Clicked() {
			a.cycleRepeat()
		}
	})
}

// scrubber is the position slider: a wave that travels while the music plays
// and goes flat when it stops. It follows the player until the user takes it,
// and seeks when they let go. Times stand at both ends.
func (a *app) scrubber(c *ui.Context) {
	sc := m3.Active().Scheme
	known := a.total > 0
	total := a.total.Seconds()
	if !known {
		total, a.scrub = 1, 0
	}
	if a.scrub > total {
		a.scrub = total
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		m3.Text(c, m3.LabelMedium, clock(time.Duration(a.scrub*float64(time.Second)))).
			Width(40).TextAlign(ui.End).Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
		slider := m3.Slider(c, &a.scrub, 0, total, m3.SliderSpec{
			Label: "Position", Key: "scrubber", Wavy: true, Waving: a.player.Playing(), Disabled: !known,
		})
		if slider.Pressed() {
			a.scrubbing = true
		}
		if a.scrubbing && !slider.Pressed() {
			a.scrubbing = false
			a.player.Seek(time.Duration(a.scrub * float64(time.Second)))
			a.syncSystemMedia()
		}
		m3.Text(c, m3.LabelMedium, clock(a.total)).Width(40).Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
	})
}

// volume is the speaker button, which mutes, and the slider beside it.
func (a *app) volumeControl(c *ui.Context, sliderWidth float32) {
	icon := m3.IconVolumeUp
	switch {
	case a.volume == 0:
		icon = m3.IconVolumeOff
	case a.volume < 45:
		icon = m3.IconVolumeDown
	}
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		if m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: "Mute", Key: "mute"}).Clicked() {
			a.toggleMute()
		}
		if sliderWidth > 0 {
			ui.Row(c).Width(sliderWidth).Children(func() {
				v := a.volume
				slider := m3.Slider(c, &v, 0, 100, m3.SliderSpec{Label: "Volume", Key: "volume"})
				if slider.Changed() {
					a.setVolume(v)
				}
				if slider.Pressed() {
					a.volumeHeld = true
				} else if a.volumeHeld {
					a.volumeHeld = false
					a.saveSettings()
				}
			})
		}
	})
}

// trackLine is the name of the current track over who made it, or the
// reason it does not play.
func (a *app) trackLine(c *ui.Context, title, subtitle m3.Role) ui.Element {
	sc := m3.Active().Scheme
	return ui.Column(c).Gap(1).MinWidth(0).Children(func() {
		m3.EmphasizedText(c, title, a.current.Title).SingleLine().TextColor(sc.OnSurface)
		line, colour := a.current.Subtitle, sc.OnSurfaceVariant
		if a.playErr != "" {
			line, colour = firstLine(a.playErr), sc.Error
		}
		m3.Text(c, subtitle, line).SingleLine().TextColor(colour)
	})
}

// playerBar is the floating player: what plays on the left, the transport and
// the scrubber in the middle, volume and the full-screen player on the right.
func (a *app) playerBar(c *ui.Context) {
	sc := m3.Active().Scheme
	w, _ := c.Size()
	wide := w > 1180
	bar := ui.Row(c).Key("player").Absolute().Left(playerGutter).Right(playerGutter).Bottom(playerGutter).
		Height(playerHeight).Padding(10, 20, 10, 12).Gap(16).AlignItems(ui.Center).
		Radius(m3.ExtraLarge).Background(sc.SurfaceContainerHigh)
	m3.Elevation(bar, 3)
	bar.Transition(ui.ElementTransition{
		Enter: &ui.Motion{Y: 48}, Position: true, Duration: m3.SpatialDefault.Duration(), Ease: m3.SpatialDefault.Ease(),
	})
	bar.Children(func() {
		info := ui.ButtonBase(c.Key("player-info"))
		info.Grow(1).Basis(0).MinWidth(0).Gap(14).AlignItems(ui.Center).Radius(m3.Large).Padding(0, 8, 0, 0).
			Cursor(ui.CursorPointer).Label("Open the player").
			Background(m3.StateFill(ui.Transparent, sc.OnSurface, info.Hovered(), info.Pressed(), info.FocusVisible()))
		info.Children(func() {
			m3.Art(c, a.thumbs.bitmap(a.current.Thumbnail, playerArt), 64, m3.Large, func() {
				if isVideo(a.current) {
					m3.VideoBadge(c, 64)
				}
			})
			a.trackLine(c, m3.TitleSmall, m3.BodySmall).Grow(1)
		})
		if info.Clicked() {
			a.npOpen = true
			a.loadPanel()
		}

		ui.Column(c).Grow(2).Basis(0).MinWidth(260).MaxWidth(580).Gap(0).Children(func() {
			a.transport(c, 48, 4)
			a.scrubber(c)
		})

		ui.Row(c).Grow(1).Basis(0).MinWidth(0).Justify(ui.End).AlignItems(ui.Center).Gap(2).Children(func() {
			if wide {
				a.volumeControl(c, 108)
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconQueue, Label: "Up next", Key: "bar-queue"}).Clicked() {
				a.npOpen, a.npTab, a.npQueueOnly = true, 0, true
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconExpandLess, Label: "Open the player", Key: "bar-expand"}).Clicked() {
				a.npOpen, a.npQueueOnly = true, false
				a.loadPanel()
			}
		})
	})
}

// nowPlaying is the full-screen player: the artwork large, the controls, and
// beside them the queue or the lyrics. It slides up over the sheet.
func (a *app) nowPlaying(c *ui.Context) {
	sc := m3.Active().Scheme
	w, h := c.Size()
	wide := w > 1040
	wash := sc.PrimaryContainer.Mix(sc.Surface, 0.3)
	panel := ui.Column(c).Key("now-playing").Absolute().Top(0).Left(0).Right(0).Bottom(0).
		Gradient(wash, sc.Surface, 170).Radius(m3.ExtraLarge).Clip()
	panel.Transition(ui.ElementTransition{
		Enter: &ui.Motion{Y: 80}, Exit: &ui.Motion{Y: 80},
		Position: true, Duration: m3.SpatialDefault.Duration(), Ease: m3.SpatialDefault.Ease(),
	})
	panel.Children(func() {
		ui.Row(c).Height(76).PaddingX(pageGutter - 8).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconExpandMore, Label: "Close the player", Key: "np-close"}).Clicked() {
				a.npOpen = false
			}
			m3.EmphasizedText(c, m3.TitleMedium, "Now playing").TextColor(sc.OnSurfaceVariant).Grow(1)
			if !wide {
				if m3.IconButton(c, m3.IconButtonSpec{
					Icon: m3.IconQueue, Label: "Up next", Toggle: true, Selected: a.npQueueOnly, Kind: m3.TonalIcon, Key: "np-queue",
				}).Clicked() {
					a.npQueueOnly = !a.npQueueOnly
				}
			}
		})
		ui.Row(c).Grow(1).MinHeight(0).Padding(0, pageGutter, pageGutter).Gap(32).AlignItems(ui.Stretch).Children(func() {
			if wide || !a.npQueueOnly {
				a.nowPlayingMain(c, h)
			}
			if wide || a.npQueueOnly {
				a.sidePanel(c, wide)
			}
		})
	})
}

// nowPlayingMain is the artwork, the name, the scrubber and the controls:
// stacked in a tall window, side by side in a short one.
func (a *app) nowPlayingMain(c *ui.Context, windowHeight float32) {
	controls := func() {
		a.trackLine(c, m3.HeadlineSmall, m3.TitleMedium)
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
	artwork := m3.Art(c, a.thumbs.bitmap(a.current.Thumbnail, imageSize), artSize, radius, func() {
		if isVideo(a.current) {
			m3.VideoBadge(c, artSize)
		}
		menuButton = m3.IconButton(c, m3.IconButtonSpec{
			Icon: m3.IconMore, Label: "More options for " + a.current.Title, Key: key + "-button",
		}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
	})
	m3.Elevation(artwork, 3)
	if menuButton.Clicked() {
		a.trackMenuKey, a.trackMenuOpen = key, true
	}
	if a.trackMenuKey == key {
		a.songMenu(c, menuButton, key, a.current)
	}
}

// sidePanel holds the queue, lyrics and related songs under tabs, on a tonal card.
func (a *app) sidePanel(c *ui.Context, wide bool) {
	sc := m3.Active().Scheme
	card := ui.Column(c).Key("np-side").Radius(m3.ExtraLarge).Background(sc.SurfaceContainerLow.Alpha(0.9)).Clip().
		Padding(8, 0, 0).Gap(4)
	if wide {
		card.Width(480).Shrink(0)
	} else {
		card.Grow(1).MinWidth(0)
	}
	card.Children(func() {
		a.nowPlayingTabs(c)
		switch a.npTab {
		case 1:
			a.lyricsView(c)
		case 2:
			a.relatedView(c)
		default:
			a.queueView(c)
		}
	})
}

func (a *app) nowPlayingTabs(c *ui.Context) {
	sc := m3.Active().Scheme
	labels := []string{"Up next", "Lyrics", "Related"}
	ui.Row(c).Padding(12, 12, 8).Gap(4).Children(func() {
		for index, label := range labels {
			selected := a.npTab == index
			container, content := ui.Transparent, sc.OnSurfaceVariant
			if selected {
				container, content = sc.SecondaryContainer, sc.OnSecondaryContainer
			}
			button := ui.ButtonBase(c.Key("np-tab-" + label))
			button.Grow(1).Basis(0).MinWidth(0).Height(40).PaddingX(4).Center().Radius(m3.Full).
				Cursor(ui.CursorPointer).Label(label).Tooltip(label).
				Background(m3.StateFill(container, content, button.Hovered(), button.Pressed(), button.FocusVisible()))
			button.Children(func() {
				text := m3.Text(c, m3.LabelMedium, label).Grow(1).MinWidth(0).SingleLine().TextAlign(ui.Center).TextColor(content)
				if selected {
					text.FontWeight(600)
				}
			})
			if button.Clicked() {
				a.npTab = index
				a.loadPanel()
			}
		}
	})
}

// queueView lists the tracks of the queue, with the current one picked out.
func (a *app) queueView(c *ui.Context) {
	sc := m3.Active().Scheme
	if a.queueSource != "" {
		ui.Column(c).Padding(8, 16, 4).Gap(2).Children(func() {
			m3.Text(c, m3.BodySmall, "Playing from").TextColor(sc.OnSurfaceVariant)
			m3.EmphasizedText(c, m3.TitleSmall, a.queueSource).SingleLine()
		})
	}
	ui.Row(c).Padding(8, 16, 4).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			m3.EmphasizedText(c, m3.TitleMedium, "Auto-play")
			m3.Text(c, m3.BodySmall, "Add similar music when this queue ends.").TextColor(sc.OnSurfaceVariant).SingleLine()
		})
		on := a.settings.AutoPlay
		if m3.Switch(c, &on, "Toggle auto-play").Changed() {
			a.setAutoPlay(on)
		}
	})
	if a.upNextLoading {
		m3.Text(c, m3.BodySmall, "Finding recommendations…").Padding(0, 16, 4).TextColor(sc.OnSurfaceVariant)
	} else if a.upNextErr != "" {
		m3.Text(c, m3.BodySmall, "Recommendations aren't available right now.").Padding(0, 16, 4).TextColor(sc.Error)
	}
	if len(a.queue) == 0 {
		a.message(c, m3.IconQueue, "The queue is empty", "", "", nil)
		return
	}
	header := a.recommendationStart > 0 && a.recommendationStart < len(a.queue)
	rowCount := len(a.queue)
	if header {
		rowCount++
	}
	itemIndex := func(row int) (int, bool) {
		if header && row == a.recommendationStart {
			return 0, false
		}
		if header && row > a.recommendationStart {
			row--
		}
		return row, row >= 0 && row < len(a.queue)
	}
	// A press in a row can replace the queue while the list still asks about
	// a row of the one before.
	a.queueList.Key = func(row int) any {
		index, track := itemIndex(row)
		if !track {
			if header && row == a.recommendationStart {
				return "queue-recommendations"
			}
			return nil
		}
		return itemKey("queue", a.queue[index]) + "#" + strconv.Itoa(index)
	}
	a.queueList.Label = func(row int) string {
		index, track := itemIndex(row)
		if !track {
			if header && row == a.recommendationStart {
				return "Auto-play recommendations"
			}
			return ""
		}
		return a.queue[index].Title
	}
	if a.queueFollow != a.current.VideoID {
		a.queueFollow = a.current.VideoID
		follow := a.index
		if header && follow >= a.recommendationStart {
			follow++
		}
		a.queueList.ScrollTo(follow, ui.Start)
	}
	ui.List(c, &a.queueList, rowCount, func(row int) {
		index, track := itemIndex(row)
		if !track {
			ui.Column(c).Height(48).Padding(8, 16, 0).Gap(8).Children(func() {
				ui.Divider(c)
				m3.EmphasizedText(c, m3.TitleSmall, "Recommended").TextColor(sc.OnSurfaceVariant).SingleLine()
			})
			return
		}
		a.songRow(c, a.queue[index], a.queue, songOptions{directQueue: true, source: a.queueSource, list: "queue", position: index, fetchArtwork: true})
	}).Grow(1).Padding(0, 8, 16)
}

func (a *app) relatedView(c *ui.Context) {
	switch {
	case a.related.loading:
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicator(c, 56, true) })
	case a.related.err != "":
		a.message(c, m3.IconExplore, a.related.err, "", "", nil)
	default:
		a.relatedList.Key = func(i int) any {
			if i >= len(a.related.items) {
				return nil
			}
			return itemKey("related", a.related.items[i]) + "#" + strconv.Itoa(i)
		}
		a.relatedList.Label = func(i int) string {
			if i >= len(a.related.items) {
				return ""
			}
			return a.related.items[i].Title
		}
		ui.List(c, &a.relatedList, len(a.related.items), func(i int) {
			a.songRow(c, a.related.items[i], a.related.items, songOptions{directQueue: true, source: "Related", list: "related", position: i, fetchArtwork: true})
		}).Grow(1).Padding(0, 8, 16)
	}
}

// lyricsView shows the lyrics of the track playing, loading them as the tab
// opens.
func (a *app) lyricsView(c *ui.Context) {
	sc := m3.Active().Scheme
	switch {
	case a.lyrics.loading:
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicator(c, 56, true) })
	case a.lyrics.err != "":
		a.message(c, m3.IconLyrics, a.lyrics.err, "", "", nil)
	default:
		ui.Scroll(c).Grow(1).Padding(8, 24, 24).Children(func() {
			m3.Text(c, m3.BodyLarge, a.lyrics.text).LineHeight(1.6).TextColor(sc.OnSurface).Selectable()
			if a.lyrics.footer != "" {
				m3.Text(c, m3.BodySmall, a.lyrics.footer).TextColor(sc.OnSurfaceVariant).Margin(16, 0, 0)
			}
		})
	}
}
