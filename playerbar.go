package main

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// transport builds the controls that start, stop and move through the queue:
// shuffle, previous, play or pause, next and repeat. The play button is a
// circle that squares off while the music plays, so the state shows in its
// shape. size is the play button's side.
func (a *app) transport(c *ui.Context, size float32, gap float32) {
	small := m3.IconButtonSpec{Size: m3.Small40}
	ui.Row(c).Gap(gap).AlignItems(ui.Center).Justify(ui.Center).Shrink(0).Children(func() {
		spec := small
		spec.Icon, spec.Label, spec.Toggle, spec.Selected, spec.Key = m3.IconShuffle, "Shuffle", true, a.shuffle, "shuffle"
		a.hinted(c, func() {
			if m3.IconButton(c, spec).Clicked() {
				a.setShuffle(!a.shuffle)
			}
		}, keymap.Shuffle)
		spec = small
		spec.Icon, spec.Label, spec.Key = m3.IconPrevious, "Previous", "previous"
		a.hinted(c, func() {
			if m3.IconButton(c, spec).Clicked() {
				a.previous()
			}
		}, keymap.Previous)
		loading := a.loading()
		playing := a.player.Playing() && !loading
		icon, label := m3.IconPlay, "Play"
		switch {
		case loading:
			label = "Loading"
		case playing:
			icon, label = m3.IconPause, "Pause"
		}
		a.hinted(c, func() {
			if m3.IconButton(c, m3.IconButtonSpec{
				Icon: icon, Label: label, Kind: m3.FilledIcon, Dimension: size, Morph: true, Selected: playing, Loading: loading, Key: "play-pause",
			}).Clicked() {
				a.togglePlay()
			}
		}, keymap.PlayPause)
		spec = small
		spec.Icon, spec.Label, spec.Key = m3.IconNext, "Next", "next"
		a.hinted(c, func() {
			if m3.IconButton(c, spec).Clicked() {
				a.advance()
			}
		}, keymap.Next)
		spec = small
		repeatIcon := m3.IconRepeat
		if a.repeat == repeatTrack {
			repeatIcon = m3.IconRepeatOne
		}
		spec.Icon, spec.Label, spec.Toggle, spec.Selected, spec.Key = repeatIcon, "Repeat", true, a.repeat != repeatOff, "repeat"
		a.hinted(c, func() {
			if m3.IconButton(c, spec).Clicked() {
				a.cycleRepeat()
			}
		}, keymap.Repeat)
	})
}

// scrubber is the position slider: a wave that travels while the music plays
// and goes flat when it stops. It follows the player until the user takes it,
// and seeks when they let go. Times stand at both ends.
func (a *app) scrubber(c *ui.Context) {
	sc := m3.Of(c).Scheme
	known := a.total > 0
	total := a.total.Seconds()
	if !known {
		total, a.scrub = 1, 0
	}
	a.scrub = min(a.scrub, total)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		m3.Text(c, m3.LabelMedium, clock(time.Duration(a.scrub*float64(time.Second)))).
			MinWidth(40).NoWrap().TextAlign(ui.End).Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
		slider := m3.Slider(c, &a.scrub, 0, total, m3.SliderSpec{
			Label: "Position", Key: "scrubber", Wavy: true, Waving: a.player.Playing(), Disabled: !known,
		})
		if slider.Pressed() {
			a.scrubbing = true
		}
		if a.scrubbing && !slider.Pressed() {
			a.scrubbing = false
			a.seek(time.Duration(a.scrub * float64(time.Second)))
		} else if slider.Changed() && !slider.Pressed() {
			// Arrow keys, Home and End change the value without a pointer press.
			a.seek(time.Duration(a.scrub * float64(time.Second)))
		}
		m3.Text(c, m3.LabelMedium, clock(a.total)).MinWidth(40).NoWrap().Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
	})
}

// volumeIcon is the speaker shape for the current volume.
func (a *app) volumeIcon() *ui.SVG {
	icon := m3.IconVolumeUp
	switch {
	case a.volume == 0:
		icon = m3.IconVolumeOff
	case a.volume < 45:
		icon = m3.IconVolumeDown
	}
	return icon
}

// muteButton is the speaker button, which mutes or restores the volume.
func (a *app) muteButton(c *ui.Context) ui.Element {
	button := m3.IconButton(c, m3.IconButtonSpec{Icon: a.volumeIcon(), Label: "Mute", Key: "mute"})
	if button.Clicked() {
		a.toggleMute()
	}
	return button
}

// volumeSlider is the volume setting, horizontal in the player bar and
// vertical in the narrow bar's hover panel.
func (a *app) volumeSlider(c *ui.Context, vertical bool) ui.Element {
	v := a.volume
	slider := m3.Slider(c, &v, 0, 100, m3.SliderSpec{Label: "Volume", Key: "volume", Vertical: vertical})
	if slider.Changed() {
		a.setVolume(v)
	}
	// Save after a pointer drag, or after each keyboard edit.
	switch {
	case slider.Pressed():
		a.nowPlaying.volumeHeld = true
	case a.nowPlaying.volumeHeld:
		a.nowPlaying.volumeHeld = false
		a.saveSettings()
	case slider.Changed():
		a.saveSettings()
	}
	return slider
}

// volumeControl is the speaker button, which mutes, and the slider beside it.
func (a *app) volumeControl(c *ui.Context, sliderWidth float32) {
	a.nowPlaying.volumePopup = false
	ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
		a.hinted(c, func() {
			a.muteButton(c)
		}, keymap.Mute)
		if sliderWidth <= 0 {
			return
		}
		a.hinted(c, func() {
			ui.Row(c).Width(sliderWidth).Children(func() {
				a.volumeSlider(c, false)
			})
		}, keymap.VolumeUp, keymap.VolumeDown)
	})
}

// compactVolumeControl keeps the mute button in the narrow player bar and
// reveals an upright volume slider while the button or its panel is hovered.
func (a *app) compactVolumeControl(c *ui.Context) {
	sc := m3.Of(c).Scheme
	ui.Row(c).Shrink(0).Children(func() {
		var anchor ui.Element
		a.hinted(c, func() { anchor = a.muteButton(c) }, keymap.Mute)

		open := anchor.Hovered() || anchor.FocusVisible() || a.nowPlaying.volumePopup
		var slider ui.Element
		panel := ui.PopoverBase(c, anchor, &open, func(panel ui.Element) {
			panel.Key("volume-popup").Width(64).Padding(8).Radius(m3.Large).Margin(0).Background(sc.SurfaceContainerHigh)
			m3.Elevation(c, panel, 3)
			a.hinted(c, func() {
				ui.Column(c).Width(48).Height(128).Center().Children(func() {
					slider = a.volumeSlider(c, true)
				})
			}, keymap.VolumeUp, keymap.VolumeDown)
		})
		keepOpen := anchor.Hovered() || anchor.FocusVisible() || panel.Hovered() || slider.FocusVisible()
		if next := open && keepOpen; next != a.nowPlaying.volumePopup {
			a.nowPlaying.volumePopup = next
			c.Invalidate()
		}
	})
}

// speedLabel names a playback speed as "1.5×".
func speedLabel(speed float64) string {
	return strconv.FormatFloat(speed, 'f', -1, 64) + "×"
}

// speedControl is the button that shows how fast the current track plays and
// opens the menu that chooses it; key tells the player's buttons apart.
func (a *app) speedControl(c *ui.Context, key string) {
	sc := m3.Of(c).Scheme
	speed := a.speedFor(a.current)
	anchor := m3.Button(c, m3.ButtonSpec{Label: speedLabel(speed), Kind: m3.TextOnly, Size: m3.Small40, Key: key})
	// A menu ignores the press that lands on the button that opened it, so
	// the button closes it, as the account menu's button does.
	if anchor.Clicked() {
		if a.nowPlaying.speedMenu == key {
			a.nowPlaying.speedMenu = ""
		} else {
			a.nowPlaying.speedMenu = key
		}
	}
	open := a.nowPlaying.speedMenu == key
	if !open {
		return
	}
	m3.Menu(c, anchor, &open, 200, func() {
		m3.Text(c, m3.LabelMedium, "Playback speed").Padding(12, 12, 4).TextColor(sc.OnSurfaceVariant)
		for _, choice := range playbackSpeeds {
			label := speedLabel(choice)
			if choice == 1 {
				label = "Normal"
			}
			row := ui.ButtonBase(c.Key("speed-" + label))
			row.Height(36).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).Cursor(ui.CursorPointer).Label(label).
				Role(ui.RoleMenuItemRadio).Checked(choice == speed).
				Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			row.Children(func() {
				if choice == speed {
					ui.Icon(c, m3.IconCheck).FontSize(22).TextColor(sc.Primary)
				} else {
					ui.Box(c).Size(22, 22)
				}
				m3.Text(c, m3.BodyLarge, label).Grow(1).SingleLine().TextColor(sc.OnSurface)
			})
			if row.Clicked() {
				open = false
				a.setSpeed(choice)
			}
		}
	})
	if !open {
		a.nowPlaying.speedMenu = ""
	}
}

// trackLine is the name of the current track over who made it, or the
// reason it does not play.
func (a *app) trackLine(c *ui.Context, title, subtitle m3.Role, links bool) ui.Element {
	sc := m3.Of(c).Scheme
	return ui.Column(c).Gap(1).MinWidth(0).Children(func() {
		m3.EmphasizedText(c, title, a.current.Title).SingleLine().TextColor(sc.OnSurface)
		line, colour := a.current.Subtitle, sc.OnSurfaceVariant
		if a.playErr != "" {
			line, colour = firstLine(a.playErr), sc.Error
		}
		if links && a.playErr == "" {
			parts := a.current.PlayerByline()
			subtitle.Style(ui.RichText(c.Key(a.current.VideoID)), false).MaxLines(2).TextColor(colour).Children(func() {
				for i, part := range parts {
					if !part.Target().Opens() {
						ui.Text(c.Key(i), part.Text)
						continue
					}
					link := ui.Link(c.Key(i), part.Text, "").TextColor(colour).Disabled(a.nowPlaying.closing)
					label := "Go to artist: "
					if part.Target().Kind == youtube.TargetAlbum {
						label = "Go to album: "
					}
					link.Label(label + part.Text).Tooltip(label + part.Text)
					if link.Clicked() {
						a.openPlayerLink(part)
					}
				}
			})
			return
		}
		m3.Text(c, subtitle, line).SingleLine().TextColor(colour)
	})
}

// openPlayerLink keeps audio and the queue untouched. The full player finishes
// its closing animation before navigating; the bar opens the page directly.
func (a *app) openPlayerLink(part youtube.BylinePart) {
	if a.nowPlaying.open {
		a.nowPlaying.navigateTo = &part
		a.nowPlaying.closing = true
		return
	}
	a.openTarget(part.Target(), detail{title: part.Text})
}

// playerBar is the floating player: what plays on the left, the transport and
// the scrubber in the middle, volume and the full-screen player on the right.
func (a *app) playerBar(c *ui.Context) {
	sc := m3.Of(c).Scheme
	w, _ := c.Size()
	wide := w > 1180
	roomy := w > 800
	bar := ui.Row(c.Key("player")).Absolute().Left(playerGutter).Right(playerGutter).Bottom(playerGutter).
		Height(playerHeight).Padding(10, 20, 10, 12).Gap(16).AlignItems(ui.Center).
		Radius(m3.ExtraLarge).Background(sc.SurfaceContainerHigh)
	m3.Elevation(c, bar, 3)
	slide := m3.SpatialDefault.Motion()
	slide.Enter = &ui.Motion{Y: 48}
	slide.Position = true
	bar.Transition(slide)
	bar.Children(func() {
		info := ui.ButtonBase(c.Key("player-info"))
		info.Grow(1).Basis(0).MinWidth(0).Gap(14).AlignItems(ui.Center).Radius(m3.Large).Padding(0, 8, 0, 0).
			Cursor(ui.CursorPointer).Label("Open the player").
			Background(m3.StateFill(ui.Transparent, sc.OnSurface, info.Hovered(), info.Pressed(), info.FocusVisible()))
		info.Children(func() {
			a.art(c, a.current.Thumbnail, playerArt, 64, m3.Large, func() {
				if a.current.IsVideo() {
					m3.VideoBadge(c, 64)
				}
			})
			a.trackLine(c, m3.TitleSmall, m3.BodySmall, true).Grow(1)
		})
		if info.Clicked() {
			a.nowPlaying.open, a.nowPlaying.closing = true, false
			a.loadPanel()
		}

		// In a narrow window the track keeps its share: the transport takes
		// what it needs and the buttons on the right take no more than they
		// are wide.
		transportGrow := float32(2)
		if !roomy {
			transportGrow = 1
		}
		ui.Column(c).Grow(transportGrow).Basis(0).MinWidth(220).MaxWidth(580).Gap(0).Children(func() {
			a.transport(c, 48, 4)
			a.scrubber(c)
		})

		// The buttons on the right never overflow the bar: when their share is
		// too narrow for them, the track's share gives way.
		end := ui.Row(c).Justify(ui.End).AlignItems(ui.Center).Gap(8)
		if roomy {
			end.Grow(1).Basis(0)
		} else {
			end.Shrink(0)
		}
		end.Children(func() {
			if wide {
				a.volumeControl(c, 108)
			} else {
				a.compactVolumeControl(c)
			}
			a.speedControl(c, "bar-speed")
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconQueue, Label: "Up next", Key: "bar-queue"}).Clicked() {
				a.openPlayer(tabQueue, true)
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconExpandLess, Label: "Open the player", Key: "bar-expand"}).Clicked() {
				a.openPlayer(a.nowPlaying.tab, false)
			}
		})
	})
}
