package main

import (
	"math"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/m3"
)

const (
	notchWing          = 52
	notchOpenTarget    = 32
	notchLyricsWidth   = 480
	notchLyricsHeight  = 152
	notchRadius        = 20
	notchOpenDuration  = 240 * time.Millisecond
	notchCloseDuration = 200 * time.Millisecond
	notchCollapseDelay = 2 * time.Second
)

// The player and fetched lyrics belong to app. Only presentation belongs to
// this window; opening it never changes the main player's tab or scroll.
type notchState struct {
	cutout        mygo.Rectangle
	expanded      bool
	transitioning bool
	staticScroll  ui.ScrollState
	scrub         float64
	scrubbing     bool
	collapseAt    time.Time
	// interacting is rebuilt from the current pass's controls, never UI handles.
	interacting bool
}

type notchWindow struct {
	win     atomic.Pointer[mygo.Window]
	bounds  mygo.Rectangle
	unwatch func()
}

func (n *notchState) resetLyrics() {
	n.staticScroll = ui.ScrollState{}
	n.scrubbing = false
}

func (a *app) notchSetting(c *ui.Context) {
	ui.Row(c.Key("notch-setting")).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
		settingLabel(c, "Notch player", "Show album art and playback beside the MacBook notch. Click to open synced lyrics.").Grow(1)
		on := a.settings.ShowNotch
		if m3.Switch(c, &on, "Show notch player").Changed() {
			a.settings.ShowNotch = on
			a.syncNotch()
			a.saveSettings()
		}
	})
}

func (a *app) setupNotch() {
	if runtime.GOOS != "darwin" || a.desktop == nil {
		return
	}
	refresh := func() {
		a.setNotchCutout(readNotchCutout())
	}
	a.desktop.notch.unwatch = mygo.Screen.OnDisplaysChanged(func() { a.update(refresh) })
	refresh()
}

func (a *app) setNotchCutout(cutout mygo.Rectangle) {
	if a.notch.cutout != cutout {
		a.notch.collapseAt = time.Time{}
	}
	a.notch.cutout = cutout
	a.syncNotch()
}

func (n *notchState) bounds() mygo.Rectangle {
	w, h := n.cutout.Width+2*notchWing, n.cutout.Height+6
	// Keep the full canvas until the closing animation has finished. Only the
	// painted panel changes size; text keeps its layout instead of stretching.
	if n.expanded || n.transitioning {
		w = max(w+2*notchOpenTarget, notchLyricsWidth)
		h += notchLyricsHeight
	}
	return mygo.Rectangle{X: n.cutout.X + (n.cutout.Width-w)/2, Y: n.cutout.Y, Width: w, Height: h}
}

func (a *app) syncNotch() {
	d := a.desktop
	if !a.settings.ShowNotch || a.current.VideoID == "" || a.notch.cutout.Width <= 0 {
		a.notch.expanded, a.notch.transitioning = false, false
		a.notch.scrubbing = false
		a.notch.collapseAt = time.Time{}
		if d != nil {
			d.closeNotch()
		}
		return
	}
	if d == nil || runtime.GOOS != "darwin" || d.quitting {
		return
	}
	bounds := a.notch.bounds()
	w := d.notch.win.Load()
	if w == nil {
		w = mygo.NewWindow(mygo.WindowOptions{
			Title: "Meiro Notch", X: bounds.X, Y: bounds.Y,
			Width: bounds.Width, Height: bounds.Height,
			Panel: true, Hidden: true, Frameless: true, Transparent: true,
			CornerRadius:  notchRadius,
			DisableResize: true, DisableMove: true, DisableShadow: true,
			Content: ui.View(a.notchView),
		})
		d.notch.win.Store(w)
		prepareNotchWindow(w)
		w.OnClose(func(e *mygo.CloseEvent) {
			if !d.quitting {
				e.PreventDefault()
				a.setNotchExpanded(false)
			}
		})
		w.OnClosed(func() { d.notch.win.CompareAndSwap(w, nil) })
		w.ShowInactive()
	}
	if d.notch.bounds != bounds {
		d.notch.bounds = bounds
		w.SetBounds(bounds)
	}
	setNotchFocusable(w, a.notch.expanded)
	w.Invalidate()
}

func (d *desktop) closeNotch() {
	if w := d.notch.win.Swap(nil); w != nil {
		w.Destroy()
	}
	d.notch.bounds = mygo.Rectangle{}
}

// Artwork loads can invalidate from a background worker. The native handle
// itself stays owned by the main thread, just like the tray's mini player.
func (d *desktop) invalidateNotch() {
	if w := d.notch.win.Load(); w != nil {
		w.Invalidate()
	}
}

func (a *app) setNotchExpanded(open bool) {
	if a.notch.expanded == open {
		return
	}
	a.notch.expanded, a.notch.transitioning = open, true
	a.notch.collapseAt = time.Time{}
	a.notch.scrubbing = false
	if open {
		a.notch.resetLyrics()
		a.loadLyrics()
	}
	a.syncNotch()
	// Only an explicit expansion takes the keyboard. Closing gives it back
	// before the shrinking animation finishes.
	if a.desktop != nil {
		if w := a.desktop.notch.win.Load(); w != nil {
			if open {
				focusNotchWindow(w)
			} else {
				releaseNotchFocus(w)
			}
		}
	}
}

func (a *app) notchView(c *ui.Context) {
	c.Root().Background(ui.Transparent)
	if a.current.VideoID == "" || !a.settings.ShowNotch {
		return
	}
	white := ui.RGB(255, 255, 255)
	// The black camera housing stays monochrome even with a light app theme.
	// Copy the theme so controls in the main window keep their own colours.
	theme := *a.resolveTheme(c)
	theme.Scheme.OnSurface, theme.Scheme.OnSurfaceVariant = white, white.Alpha(0.8)
	theme.Scheme.SurfaceContainerHighest = ui.RGB(32, 32, 32)
	theme.Scheme.Primary, theme.Scheme.OnPrimary = white, ui.RGB(0, 0, 0)
	m3.Provide(c, &theme)
	// Provide sets the root background too; clear it afterwards so the
	// pixels outside the rounded panel reveal the desktop.
	c.Root().Background(ui.Transparent)
	n := &a.notch
	n.interacting = false
	open := n.expanded
	reduced := c.Preferences().ReduceMotion
	headerHeight := float32(n.cutout.Height + 6)
	compactWidth := float32(n.cutout.Width + 2*notchWing)
	fullWidth := max(compactWidth+2*notchOpenTarget, float32(notchLyricsWidth))
	canvasWidth, _ := c.Size()
	root := ui.Column(c.Key("notch")).Absolute().Top(0).Background(ui.RGB(0, 0, 0)).Radius(notchRadius).Clip().Label("Notch panel")
	target, duration := float32(0), notchCloseDuration
	ease := ui.EaseInOut
	if open {
		target, duration = 1, notchOpenDuration
		ease = ui.EaseOut
	}
	size := root.AnimateTo("size", target, ui.Animation{Duration: duration, Ease: ease})
	fadeDuration, delay := 100*time.Millisecond, time.Duration(0)
	if open && !reduced {
		fadeDuration, delay = 140*time.Millisecond, 100*time.Millisecond
	}
	// AnimateColor preserves short fades with Reduce Motion; AnimateTo snaps
	// spatial changes to their target. Both retarget from the current frame.
	fade := root.AnimateColor("content", white.Alpha(target), ui.Animation{Duration: fadeDuration, Delay: delay, Ease: ui.EaseInOut})
	opacity := float32(fade.A) / 255
	headerFade := root.AnimateColor("header", white.Alpha(target), ui.Animation{Duration: 100 * time.Millisecond, Ease: ui.EaseInOut})
	headerOpacity := float32(headerFade.A) / 255
	if reduced && !open && !root.Settled("content") {
		// Fade in place before switching to the compact footprint.
		size = 1
	}
	width := compactWidth + (fullWidth-compactWidth)*size
	root.Size(width, headerHeight+notchLyricsHeight*size).Left((canvasWidth - width) / 2)
	if n.transitioning && root.Settled("size") && root.Settled("content") && root.Settled("header") {
		n.transitioning = false
		// A guarded completion, not a timer: resize after the last frame. The
		// update reads the latest target, so a quick reopen cannot be closed by
		// an obsolete callback. No UI elements escape this build pass.
		a.update(a.syncNotch)
	}
	root.Children(func() {
		ui.Row(c.Key("notch-header")).Height(headerHeight).FillWidth().Shrink(0).Justify(ui.Center).Children(func() {
			if open || n.transitioning {
				// Keep the camera and the existing wings centred. This extra
				// target fits in the space revealed to the left when expanded.
				launch := ui.ButtonBase(c.Key("notch-open")).Absolute().
					Left((width-compactWidth)/2-notchOpenTarget).Top((headerHeight-notchOpenTarget)/2).
					Size(notchOpenTarget, notchOpenTarget).Center().Radius(8).
					Label("Open Meiro").Tooltip("Open Meiro").Cursor(ui.CursorPointer).
					Disabled(!open || opacity < 1).Opacity(opacity)
				color := white.Alpha(0.7)
				launch.Background(m3.StateFill(ui.Transparent, color, launch.Hovered(), launch.Pressed(), launch.FocusVisible()))
				launch.Children(func() {
					ui.Icon(c, m3.IconOpenInNew).FontSize(16).TextColor(color)
				})
				n.trackInteraction(launch)
				if launch.Clicked() {
					// Release the panel's keyboard focus before focusing the main window.
					a.setNotchExpanded(false)
					a.desktop.bringToFront()
				}
			}
			label := "Open notch lyrics"
			if n.expanded {
				label = "Close notch lyrics"
			}
			cover := ui.ButtonBase(c.Key("notch-cover")).Size(notchWing, headerHeight).Center().Label(label).Cursor(ui.CursorPointer)
			n.trackInteraction(cover)
			cover.Children(func() {
				small := min(float32(26), headerHeight-10)
				ui.Box(c.Key("small-cover")).Absolute().Size(small, small).Left((notchWing - small) / 2).Top((headerHeight - small) / 2).Opacity(1 - headerOpacity).Children(func() {
					a.art(c, a.current.Thumbnail, 96, small, 6)
				})
				ui.Box(c.Key("collapse-arrow")).Absolute().Size(18, 18).Left((notchWing - 18) / 2).Top((headerHeight - 18) / 2).Opacity(headerOpacity).Children(func() {
					ui.Icon(c, m3.IconExpandLess).FontSize(18).TextColor(white.Alpha(0.5))
				})
			})
			// The camera occupies this area. Never place a title or a control here.
			ui.Box(c.Key("camera")).Width(float32(n.cutout.Width)).Height(headerHeight).PassThrough()
			indicator := ui.ButtonBase(c.Key("notch-indicator")).Size(notchWing, headerHeight).Center().Label(label + " from playback indicator").Cursor(ui.CursorPointer)
			n.trackInteraction(indicator)
			indicator.Children(func() {
				if a.loading() {
					m3.LoadingIndicatorIn(c, 20, white).Label("Buffering")
				} else {
					notchBars(c, a.player.Playing() && a.playErr == "", white)
				}
			})
			if cover.Clicked() || indicator.Clicked() {
				a.setNotchExpanded(!n.expanded)
			}
		})
		if open || n.transitioning {
			lift := -6 * (1 - opacity)
			if reduced {
				lift = 0
			}
			body := ui.Row(c.Key("notch-lyrics")).Absolute().Top(headerHeight+lift).Left((width-fullWidth)/2).
				Size(fullWidth, notchLyricsHeight).Padding(8, 16, 16).Gap(14).AlignItems(ui.Center).Disabled(!open)
			body.Opacity(opacity).Children(func() {
				cover := ui.ButtonBase(c.Key("notch-album")).Size(76, 76).Shrink(0).Radius(12).
					Label("Open Now Playing").Tooltip("Open Now Playing").Cursor(ui.CursorPointer).
					Disabled(!open || opacity < 1)
				n.trackInteraction(cover)
				cover.Children(func() {
					a.art(c, a.current.Thumbnail, 192, 76, 12, func() {
						state := ui.Box(c.Key("cover-state")).Absolute().Top(0).Left(0).Size(76, 76).Radius(12).PassThrough().
							Background(m3.StateFill(ui.Transparent, white, cover.Hovered(), cover.Pressed(), cover.FocusVisible()))
						if cover.FocusVisible() {
							state.Border(2, white)
						}
					}).Label("Album cover")
				})
				if cover.Clicked() {
					a.setNotchExpanded(false)
					a.openPlayer(a.nowPlaying.tab, false)
					a.desktop.bringToFront()
					a.refresh()
				}
				ui.Column(c.Key("notch-info")).Width(148).MinWidth(0).Shrink(0).Gap(4).Children(func() {
					ui.Column(c.Key("notch-title")).FillWidth().Children(func() {
						ui.Text(c, a.current.Title).FontSize(13).FontWeight(600).FixedLineHeight(18).TextColor(white).SingleLine()
						ui.Text(c, shownArtist(a.current)).FontSize(11).FixedLineHeight(16).TextColor(white.Alpha(0.5)).SingleLine()
					})
					a.notchProgress(c)
					a.notchTransport(c)
				})
				ui.Column(c.Key("notch-words")).Grow(1).Basis(0).MinWidth(0).FillHeight().MinHeight(0).Gap(4).Clip().Children(func() {
					a.notchLyrics(c)
					notchSource(c, a.nowPlaying.lyrics.footer)
				})
			})
		}
	})
	if c.Shortcut(0, ui.KeyEscape) && n.expanded {
		a.setNotchExpanded(false)
	}
	a.notchAutoCollapse(c, root)
}

// trackInteraction keeps keyboard focus and held controls from timing out.
// A mouse click's retained focus alone must not keep the panel open forever.
func (n *notchState) trackInteraction(control ui.Element) ui.Element {
	n.interacting = control.FocusVisible() || control.Pressed() || n.interacting
	return control
}

func (a *app) notchAutoCollapse(c *ui.Context, panel ui.Element) {
	n := &a.notch
	hovered := panel.Hovered() // Observe the whole panel, including gaps and children.
	if !n.expanded || n.transitioning || hovered || n.scrubbing || n.interacting {
		n.collapseAt = time.Time{}
		return
	}
	if n.collapseAt.IsZero() {
		n.collapseAt = c.Now().Add(notchCollapseDelay)
	}
	if remaining := n.collapseAt.Sub(c.Now()); remaining > 0 {
		// A wakeup only rebuilds the UI. Re-entry or a new interaction clears
		// the deadline, so an old wakeup cannot close a reopened panel.
		c.After(remaining)
		return
	}
	a.setNotchExpanded(false)
	c.AnimationFrame()
}

func (a *app) notchTransport(c *ui.Context) {
	n := &a.notch
	ui.Row(c.Key("notch-transport")).Height(40).Shrink(0).Gap(14).AlignItems(ui.Center).Justify(ui.Center).Children(func() {
		if n.trackInteraction(m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconPrevious, Label: "Previous", Size: m3.ExtraSmall32, Key: "notch-previous"})).Clicked() {
			a.previous()
		}
		loading := a.loading()
		icon, label := m3.IconPlay, "Play"
		if loading {
			label = "Loading"
		} else if a.player.Playing() {
			icon, label = m3.IconPause, "Pause"
		}
		if n.trackInteraction(m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: label, Size: m3.ExtraSmall32, Loading: loading, Key: "notch-play-pause"})).Clicked() {
			a.togglePlay()
		}
		if n.trackInteraction(m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconNext, Label: "Next", Size: m3.ExtraSmall32, Key: "notch-next"})).Clicked() {
			a.advance()
		}
	})
}

// The thin track keeps SliderBase's keyboard and pointer behaviour. Its drag
// preview belongs to this window, so a frame in the main player cannot end it.
func (a *app) notchProgress(c *ui.Context) {
	n := &a.notch
	known := a.total > 0
	total := a.total.Seconds()
	if !known {
		total = 1
	}
	if !n.scrubbing {
		n.scrub = min(max(a.playbackPosition().Seconds(), 0), total)
	}
	white := ui.RGB(255, 255, 255)
	ui.Column(c.Key("notch-progress")).FillWidth().Shrink(0).Children(func() {
		slider := ui.SliderBase(c.Key("notch-position"), &n.scrub, 0, total).
			Label("Notch position").Height(20).FillWidth().Shrink(0).Disabled(!known).Cursor(ui.CursorPointer)
		n.trackInteraction(slider)
		changed := slider.Changed()
		// Arrow-key seeking may follow mouse focus without a visible focus ring.
		n.interacting = changed || n.interacting
		wasScrubbing := n.scrubbing
		_, _, n.scrubbing = slider.Dragged()
		if known && !n.scrubbing && (wasScrubbing || changed) {
			a.scrub = n.scrub
			a.seek(time.Duration(n.scrub * float64(time.Second)))
		}
		fraction := float32(min(max(n.scrub/total, 0), 1))
		showThumb := known && (slider.Hovered() || slider.Pressed() || slider.FocusVisible())
		slider.Draw(func(p *ui.Painter, r ui.Rect) {
			track := ui.Rect{X: r.X + 3, Y: r.Y + r.H/2 - 1.5, W: r.W - 6, H: 3}
			p.Fill(track, white.Alpha(0.18), 1.5)
			if known {
				active := track
				active.W *= fraction
				p.Fill(active, white.Alpha(0.9), 1.5)
			}
			if showThumb {
				p.Fill(ui.Rect{X: track.X + track.W*fraction - 3, Y: r.Y + r.H/2 - 3, W: 6, H: 6}, white, 3)
			}
		})
		ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
			ui.Text(c, clock(time.Duration(n.scrub*float64(time.Second)))).FontSize(9).FixedLineHeight(12).TextColor(white.Alpha(0.45)).FontFeatures("tnum").SingleLine()
			end := "--:--"
			if known {
				end = clock(a.total)
			}
			ui.Text(c, end).FontSize(9).FixedLineHeight(12).TextColor(white.Alpha(0.45)).FontFeatures("tnum").SingleLine()
		})
	})
}

// Five decorative bars; only their paint is animated, never audio analysis or
// a background timer. The captured playing flag is refreshed on playback ticks.
func notchBars(c *ui.Context, playing bool, color ui.Color) {
	label := "Paused"
	if playing {
		label = "Playing"
	}
	animated := playing && !c.Preferences().ReduceMotion
	ui.Box(c.Key("notch-bars")).Size(24, 18).Label(label).Draw(func(p *ui.Painter, r ui.Rect) {
		if animated {
			p.AnimationFrameRate(30)
		}
		t := float64(p.Now().UnixMilli()) / 1000
		for i := range 5 {
			h := float32(3)
			if animated {
				h += float32(12 * (0.5 + 0.5*math.Sin(t*(4+float64(i)*0.7)+float64(i)*1.9)))
			}
			p.Fill(ui.Rect{X: r.X + float32(i)*5, Y: r.Y + (r.H-h)/2, W: 3, H: h}, color, 1.5)
		}
	})
}

func (a *app) notchLyrics(c *ui.Context) {
	l := &a.nowPlaying.lyrics
	white := ui.RGB(255, 255, 255)
	switch {
	case l.load.busy() || l.videoID != a.current.VideoID && l.err == "":
		ui.Column(c).Grow(1).Center().Children(func() { m3.LoadingIndicatorIn(c, 24, white).Label("Loading lyrics") })
	case l.err != "":
		ui.Column(c).Grow(1).MinHeight(0).Center().Gap(2).Children(func() {
			m3.Text(c, m3.BodySmall, l.err).TextColor(white.Alpha(0.65)).TextAlign(ui.Center).MaxLines(2)
			if l.videoID == "" {
				if a.notch.trackInteraction(m3.Button(c, m3.ButtonSpec{Label: "Retry lyrics", Size: m3.ExtraSmall32, Kind: m3.TextOnly, Key: "notch-retry"})).Clicked() {
					a.loadLyrics()
				}
			}
		})
	case len(l.synced) > 0:
		now := lrclib.IndexAt(l.synced, a.playbackPosition())
		motion := m3.EffectsSlow.Motion()
		motion.Position = true
		motion.Enter = &ui.Motion{Y: 8}
		ui.Column(c.Key(l.videoID)).Grow(1).MinHeight(0).FillWidth().Justify(ui.Center).Gap(6).Children(func() {
			for offset := -1; offset <= 1; offset++ {
				i := now + offset
				text := " "
				if i >= 0 && i < len(l.synced) {
					text = l.synced[i].Text
					if text == "" {
						text = "♪"
					}
				}
				color, size, weight, lines, height := white.Alpha(0.4), float32(11), 400, 1, float32(16)
				if offset == 0 {
					color, size, weight, lines, height = white, 14, 600, 2, 38
					if now < 0 {
						text = "♪"
					}
				}
				ui.Box(c.Key(i)).FillWidth().Height(height).Shrink(0).Center().Transition(motion).Children(func() {
					ui.Text(c, text).FontSize(size).FontWeight(weight).TextColor(color).TextAlign(ui.Center).MaxLines(lines).LineHeight(1.2).FillWidth()
				})
			}
		})
	default:
		m3.Text(c, m3.LabelSmall, "Synced lyrics unavailable").TextColor(white.Alpha(0.5)).TextAlign(ui.Center).Shrink(0)
		scroll := ui.Scroll(c.Key("notch-static")).TrackScroll(&a.notch.staticScroll).Grow(1).MinHeight(0)
		a.notch.interacting = a.notch.interacting || scroll.FocusVisible()
		scroll.Children(func() {
			m3.Text(c, m3.BodySmall, l.text).TextColor(white.Alpha(0.85)).LineHeight(1.4).TextAlign(ui.Center)
		})
	}
}

func notchSource(c *ui.Context, source string) {
	if source != "" {
		ui.Text(c, strings.TrimPrefix(source, "Source: ")).FontSize(9).FixedLineHeight(12).TextColor(ui.RGB(255, 255, 255).Alpha(0.35)).TextAlign(ui.Center).SingleLine().Shrink(0)
	}
}
