package main

import (
	"cmp"
	"context"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/internal/lrclib"
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
	tab     panelTab
	// playerShown says the player has been built, so its rise starts on its
	// first frame only.
	playerShown bool
	// queueOnly shows the side panel alone, in a window too narrow for both.
	queueOnly bool
	queueList ui.ListState
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

// lyricsState is the lyrics of the track playing, as far as they are loaded.
type lyricsState struct {
	load    task
	videoID string
	// text is the lyrics as plain text, and synced the same with the moment
	// each line is sung, when LRCLIB has them timed.
	text   string
	synced []lrclib.Line
	footer string
	err    string
	// followed is the line the view last scrolled to, -1 before the first,
	// and centerPending says that line is still to be brought to the middle.
	// settled is whether the view has been placed on a line yet: the first
	// placement jumps, the rest glide.
	followed      int
	centerPending bool
	settled       bool
}

// loadedLyrics is what the lyrics of a track come to, from LRCLIB or, when
// it has none, from YouTube Music.
type loadedLyrics struct {
	text, footer string
	synced       []lrclib.Line
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

// loadLyrics fetches the lyrics of the current track, once.
func (a *app) loadLyrics() {
	l := &a.nowPlaying.lyrics
	id := a.current.VideoID
	if id == "" || l.videoID == id {
		return
	}
	l.videoID, l.text, l.footer, l.err, l.synced, l.followed = id, "", "", "", nil, -1
	a.nowPlaying.lyricsScroll.Y = 0
	l.centerPending, l.settled = false, false
	client, item, total := a.client(), a.current, a.total
	fetch := a.lyricsSource
	startTask(a, &l.load, panelTimeout, func(ctx context.Context) (loadedLyrics, error) {
		return findLyrics(ctx, fetch, client, item, total)
	}, func(found loadedLyrics, err error) {
		l.text, l.footer, l.synced = found.text, found.footer, found.synced
		switch {
		case l.text != "" || len(l.synced) > 0:
		case err != nil:
			// Not that there are none: the lookup failed. It is not
			// remembered, so opening the tab or the next track tries again.
			l.err, l.videoID = "Couldn't load lyrics. Try again in a moment.", ""
		default:
			l.err = "No lyrics for this song."
		}
	})
}

// findLyrics finds a track's lyrics, timed when it can. YouTube Music is asked
// first, as it is the service the track plays from, then LRCLIB, and the plain
// text of YouTube Music is the last resort. A source that fails is not an
// error: the next is tried, and no lyrics at all is an empty result.
func findLyrics(ctx context.Context, fetch lyricsSource, client *youtube.Client, item youtube.MusicItem, total time.Duration) (loadedLyrics, error) {
	var found, plain loadedLyrics
	var lookupErr error
	if lyrics, err := client.GetLyrics(ctx, item.VideoID); err == nil {
		plain = loadedLyrics{text: strings.TrimSpace(lyrics.Description), footer: lyrics.Footer}
		for _, line := range lyrics.Timed {
			found.synced = append(found.synced, lrclib.Line{At: line.Start, Text: line.Text})
		}
	} else if ctx.Err() != nil {
		return found, ctx.Err()
	}
	// Where the lyrics came from: what YouTube Music says of them, or itself.
	found.footer = cmp.Or(plain.footer, "Source: YouTube Music")
	if len(found.synced) == 0 && fetch != nil {
		// A track that is not resolved yet has no length of its own; the one
		// its row lists is as good, and LRCLIB takes a lookup without one.
		if total <= 0 {
			total = parseDuration(item.Duration)
		}
		got, err := fetch(ctx, lrclib.Query{Title: item.Title, Artist: item.Artist, Album: item.Album, Duration: total})
		if err != nil {
			log.Printf("lyrics: lrclib lookup failed: %v", err)
			lookupErr = err
		}
		if got != nil && len(got.Synced) > 0 {
			found.synced, found.footer = got.Synced, "Source: LRCLIB"
		}
		// Its plain text may hold a romanised copy as well; the timed lines
		// are cleaned of that, so they stand in for it when there are any.
		if plain.text == "" && got != nil && got.Plain != "" && len(found.synced) == 0 {
			plain = loadedLyrics{text: got.Plain, footer: "Source: LRCLIB"}
			if len(found.synced) == 0 {
				found.footer = plain.footer
			}
		}
	}
	found.text = plain.text
	defer func() {
		log.Printf("lyrics: video_id=%s title=%q artist=%q album=%q total=%s -> %s, synced_lines=%d plain_chars=%d",
			item.VideoID, item.Title, item.Artist, item.Album, total, found.footer, len(found.synced), len(found.text))
	}()
	if found.text == "" && len(found.synced) > 0 {
		lines := make([]string, len(found.synced))
		for i, line := range found.synced {
			lines[i] = line.Text
		}
		found.text = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	if found.text == "" && len(found.synced) == 0 {
		return found, lookupErr
	}
	return found, nil
}

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
// and goes flat when it stops, unless the user turned the wave off (it is off
// by default on macOS, see settings.Defaults), in which case the track is a
// plain flat line. It follows the player until the user takes it,
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
			Width(40).TextAlign(ui.End).Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
		slider := m3.Slider(c, &a.scrub, 0, total, m3.SliderSpec{
			Label: "Position", Key: "scrubber", Wavy: a.settings.WavyProgress, Waving: a.player.Playing(), Disabled: !known,
		})
		if slider.Pressed() {
			a.scrubbing = true
		}
		if a.scrubbing && !slider.Pressed() {
			a.scrubbing = false
			a.seek(time.Duration(a.scrub * float64(time.Second)))
		}
		m3.Text(c, m3.LabelMedium, clock(a.total)).Width(40).Shrink(0).TextColor(sc.OnSurfaceVariant).FontFeatures("tnum")
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
	// The volume is kept once the user lets go, not on every step.
	if slider.Pressed() {
		a.nowPlaying.volumeHeld = true
	} else if a.nowPlaying.volumeHeld {
		a.nowPlaying.volumeHeld = false
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
			panel.Width(64).Padding(8).Radius(m3.Large).Margin(0).Background(sc.SurfaceContainerHigh)
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

// trackLine is the name of the current track over who made it, or the
// reason it does not play.
func (a *app) trackLine(c *ui.Context, title, subtitle m3.Role) ui.Element {
	sc := m3.Of(c).Scheme
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
	sc := m3.Of(c).Scheme
	w, _ := c.Size()
	wide := w > 1180
	roomy := w > 800
	bar := ui.Row(c.Key("player")).Absolute().Left(playerGutter).Right(playerGutter).Bottom(playerGutter).
		Height(playerHeight).Padding(10, 20, 10, 12).Gap(16).AlignItems(ui.Center).
		Radius(m3.ExtraLarge).Background(sc.SurfaceContainerHigh)
	m3.Elevation(c, bar, 3)
	slide := m3.Move(m3.SpatialDefault)
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
			a.trackLine(c, m3.TitleSmall, m3.BodySmall).Grow(1)
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

		end := ui.Row(c).Justify(ui.End).AlignItems(ui.Center).Gap(8)
		if roomy {
			end.Grow(1).Basis(0).MinWidth(0)
		} else {
			end.Shrink(0)
		}
		end.Children(func() {
			if wide {
				a.volumeControl(c, 108)
			} else {
				a.compactVolumeControl(c)
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconQueue, Label: "Up next", Key: "bar-queue"}).Clicked() {
				a.openPlayer(tabQueue, true)
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconExpandLess, Label: "Open the player", Key: "bar-expand"}).Clicked() {
				a.openPlayer(a.nowPlaying.tab, false)
			}
		})
	})
}

// riseDistance is how far below its place the player starts, and ends.
const riseDistance = 80

// nowPlayingView is the full-screen player: the artwork large, the controls,
// and beside them the queue or the lyrics. It rises over the sheet, and sinks
// away when it closes.
func (a *app) nowPlayingView(c *ui.Context) {
	sc := m3.Of(c).Scheme
	w, h := c.Size()
	wide := w > 1040
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
	// The player stays built while it closes, sinks away and fades, and is
	// taken away when the sink ends.
	if a.nowPlaying.closing && lift >= riseDistance-0.5 {
		a.nowPlaying.open, a.nowPlaying.closing = false, false
		c.Invalidate()
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
			a.trackLine(c, m3.HeadlineSmall, m3.TitleMedium).Grow(1)
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
	tabs := ui.Row(c)
	if compact {
		tabs.Padding(4, 8, 0)
	} else {
		// The card keeps 8 above the tabs, so 4 here gives the row the same
		// 12 the card's sides give it.
		tabs.Padding(4, 12, 8)
	}
	tabs.Gap(4).Children(func() {
		for _, t := range panelTabs {
			selected := a.nowPlaying.tab == t.tab
			container, content := ui.Transparent, sc.OnSurfaceVariant
			if selected {
				container, content = sc.SecondaryContainer, sc.OnSecondaryContainer
			}
			button := ui.ButtonBase(c.Key("np-tab-" + t.label))
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
			if button.Clicked() {
				a.nowPlaying.tab = t.tab
				a.loadPanel()
			}
		}
	})
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
	list.Key = func(row int) any {
		if q.isDivider(row) {
			return "queue-recommendations"
		}
		if index, ok := q.trackAt(row); ok {
			return itemKey("queue", q.at(index)) + "#" + strconv.Itoa(index)
		}
		return nil
	}
	list.Label = func(row int) string {
		if q.isDivider(row) {
			return "Auto-play recommendations"
		}
		if index, ok := q.trackAt(row); ok {
			return q.at(index).Title
		}
		return ""
	}
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
	ui.List(c, list, q.rows(), func(row int) {
		index, ok := q.trackAt(row)
		if !ok {
			height := float32(48)
			if compact {
				height = 36
			}
			ui.Column(c).Height(height).Padding(8, 16, 0).Gap(8).Children(func() {
				ui.Divider(c)
				m3.EmphasizedText(c, m3.TitleSmall, "Recommended").TextColor(sc.OnSurfaceVariant).SingleLine()
			})
			return
		}
		a.songRow(c, q.at(index), q.items, songOptions{directQueue: true, source: q.source, list: "queue", position: index, compact: compact})
	}).Grow(1).Padding(0, 8, listBottomPadding)
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

// lyricsSource finds the lyrics LRCLIB has of a track.
type lyricsSource func(ctx context.Context, q lrclib.Query) (*lrclib.Lyrics, error)

// lyricsView shows the lyrics of the track playing: timed, with the line
// being sung lit and followed, or as plain text. The user chooses between
// the two when the track has both.
func (a *app) lyricsView(c *ui.Context) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	switch {
	case l.load.busy():
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicator(c, 56, true) })
	case l.err != "":
		a.message(c, m3.IconLyrics, l.err, "", "", nil)
	default:
		timed := len(l.synced) > 0
		synced := timed && !a.settings.StaticLyrics
		if timed {
			a.lyricsModes(c, synced, l.footer)
		}
		if synced {
			a.syncedLyrics(c)
			return
		}
		ui.Scroll(c).Grow(1).Padding(8, 24, 24).Children(func() {
			m3.Text(c, m3.BodyLarge, l.text).LineHeight(1.6).TextColor(sc.OnSurface).Selectable()
			if l.footer != "" && !timed {
				m3.Text(c, m3.BodySmall, l.footer).TextColor(sc.OnSurfaceVariant).Margin(16, 0, 0)
			}
		})
	}
}

// lyricsModes is the choice between the timed and the plain lyrics.
func (a *app) lyricsModes(c *ui.Context, synced bool, source string) {
	sc := m3.Of(c).Scheme
	ui.Row(c).Padding(0, 16, 4).Gap(8).AlignItems(ui.Center).Children(func() {
		if m3.Chip(c, "Synced", synced, "lyrics-synced").Clicked() && !synced {
			a.settings.StaticLyrics = false
			a.nowPlaying.lyrics.followed = -1
			a.nowPlaying.lyrics.settled = false
			a.saveSettings()
		}
		if m3.Chip(c, "Static", !synced, "lyrics-static").Clicked() && synced {
			a.settings.StaticLyrics = true
			a.saveSettings()
		}
		ui.Box(c).Grow(1)
		if source != "" {
			m3.Text(c, m3.LabelSmall, strings.TrimPrefix(source, "Source: ")).TextColor(sc.OnSurfaceVariant).SingleLine().Shrink(1).MinWidth(0)
		}
	})
}

// lyricScroll is how a lyrics view glides to the line being sung.
var lyricScroll = ui.Smooth

// syncedLyrics lists the lines, lights the one being sung and glides it to
// the middle as the song moves on. A line is dimmer and smaller the further it
// is from the one sung, and all of that eases rather than snaps. A line is a
// click away: it seeks.
func (a *app) syncedLyrics(c *ui.Context) {
	sc := m3.Of(c).Scheme
	l := &a.nowPlaying.lyrics
	scroll := &a.nowPlaying.lyricsScroll
	now := lrclib.IndexAt(l.synced, a.playbackPosition())
	if l.followed != now {
		l.followed, l.centerPending = now, true
	}
	view := ui.Scroll(c.Key("lyrics")).TrackScroll(scroll).Grow(1)
	viewport := view.LayoutBounds()
	// Room above the first line and below the last, so that each can reach
	// the middle.
	pad := max(viewport.H/2-40, 40)
	view.Padding(pad, 16, pad).Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			for i, line := range l.synced {
				a.lyricLine(c, line, i, now, scroll, viewport, sc)
			}
		})
	})
	if now < 0 && l.centerPending && viewport.H > 0 {
		l.centerPending = false
		scroll.ScrollTo(0, 0, lyricScroll)
	}
}

// lyricLook is a line's opacity and size, as a fraction of the sung line's,
// by its distance from it: the sung line is full, its neighbours are dim and
// smaller, and the rest fade quickly into the background.
func lyricLook(distance int) (opacity, size float32) {
	if distance == 0 {
		return 1, 1
	}
	k := float32(distance - 1)
	return max(0.4-0.12*k, 0.08), max(0.8-0.04*k, 0.6)
}

// lyricLine is one line of the lyrics; now is the line being sung.
func (a *app) lyricLine(c *ui.Context, line lrclib.Line, i, now int, scroll *ui.ScrollState, viewport ui.Rect, sc m3.Scheme) {
	l := &a.nowPlaying.lyrics
	distance := i - now
	if distance < 0 {
		distance = -distance
	}
	opacity, size := lyricLook(distance)
	text := line.Text
	if text == "" {
		text = "♪"
	}
	row := ui.ButtonBase(c.Key(i)).FillWidth().Padding(8, 14).Radius(m3.Large).Justify(ui.Start).Cursor(ui.CursorPointer).Label(text)
	opacity = m3.Animate(row, "opacity", opacity, m3.EffectsDefault)
	size = m3.Animate(row, "size", size, m3.EffectsSlow)
	row.Opacity(opacity).TransformOrigin(0, 0.5).Scale(size, size)
	row.Children(func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, text).FontWeight(800).TextColor(sc.OnSurface).LineHeight(1.25)
	})
	if row.Clicked() {
		a.seek(line.At)
	}
	if i == now && l.centerPending {
		if bounds := row.LayoutBounds(); bounds.H > 0 && viewport.H > 0 {
			l.centerPending = false
			target := scroll.Y + (bounds.Y + bounds.H/2) - (viewport.Y + viewport.H/2)
			if l.settled {
				scroll.ScrollTo(0, target, lyricScroll)
			} else {
				l.settled, scroll.Y = true, target
			}
		}
	}
}
