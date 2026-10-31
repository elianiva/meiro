package main

import (
	"runtime"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// Size of the tray's mini player in DIPs.
const (
	miniWidth = 344
	miniArt   = 64
	// miniHeight keeps the flyout narrow and tall, with room for the queue.
	// It never changes: a native popover that resizes draws its content stretched.
	miniHeight = 640
)

// miniPlayer is the panel of the tray icon: what plays, where it is, the
// transport, and the way back to the window or out of the app. It is drawn
// in a flyout of the tray, a window of its own, from the same state as the
// main window.
func (a *app) miniPlayer(c *ui.Context) {
	// The main window may be hidden, so it is not the only frame that follows
	// the artwork.
	a.followArtwork()
	m3.Provide(c, a.resolveTheme(c))
	sc := m3.Of(c).Scheme
	surface := sc.SurfaceContainer
	if c.Vibrancy() {
		surface = ui.Transparent
	}
	col := ui.Column(c.Key("mini")).Fill().Padding(12).Gap(8).Background(surface)
	if runtime.GOOS != "darwin" {
		// macOS rounds the popover itself. Elsewhere the panel rounds its own
		// corners, so the root must not fill them with the theme's background.
		c.Root().Background(ui.Transparent)
		col.Radius(m3.ExtraLarge)
	}
	col.Children(func() {
		ui.Column(c.Key("mini-content")).Grow(1).MinHeight(0).Gap(8).Children(func() {
			ui.Row(c.Key("mini-player")).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				// Drawn at playerArt pixels, the size followArtwork takes its colour
				// from, so the picture it needs is the one this frame fetches.
				a.art(c, a.current.Thumbnail, playerArt, miniArt, m3.Large, func() {
					if a.current.IsVideo() {
						m3.VideoBadge(c, miniArt)
					}
				})
				if a.current.VideoID == "" {
					ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						m3.EmphasizedText(c, m3.TitleMedium, "Nothing playing").TextColor(sc.OnSurface)
						m3.Text(c, m3.BodySmall, "Pick something in Meiro").TextColor(sc.OnSurfaceVariant)
					})
				} else {
					a.trackLine(c, m3.TitleSmall, m3.BodySmall, false).Grow(1)
				}
			})
			a.scrubber(c)
			a.transport(c, 48, 4)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				// The slider fills the width: miniWidth (344) less the
				// padding (24) and the mute button (40) and its gap (8).
				a.volumeControl(c, 272)
			})
			a.sidePanel(c, false, true)
		})
		ui.Row(c).Gap(8).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Open Meiro", Kind: m3.Tonal, Key: "mini-open"}).Clicked() {
				a.desktop.closeMini()
				a.desktop.bringToFront()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Quit", Kind: m3.TextOnly, Key: "mini-quit"}).Clicked() {
				mygo.App.Quit()
			}
		})
	})
}

// mini is the mini player's window while it is open. The system closes it
// on a press outside, so it is made anew each time it opens.
type mini struct {
	win atomic.Pointer[mygo.Window]
}

// showMini opens the mini player next to the tray icon and reports it through
// shown, and again with false when it closes. A click on the icon while it is
// open leaves it open. It runs on the main thread.
func (d *desktop) showMini(view func(*ui.Context), shown func(bool)) {
	if d == nil || d.tray == nil {
		return
	}
	if d.mini.win.Load() != nil {
		return
	}
	fl := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:         d.tray,
		Placement:    mygo.PlacementBottom,
		Width:        miniWidth,
		Height:       miniHeight,
		Focusable:    true,
		Shadow:       true,
		Popover:      runtime.GOOS == "darwin",
		Vibrancy:     mygo.VibrancyPopover,
		CornerRadius: int(m3.ExtraLarge),
		Content:      ui.View(view),
	})
	d.mini.win.Store(fl)
	shown(true)
	fl.OnClosed(func() {
		d.mini.win.CompareAndSwap(fl, nil)
		d.update(func() { shown(false) })
	})
}

// closeMini closes the mini player if it is open.
func (d *desktop) closeMini() {
	if d == nil {
		return
	}
	if fl := d.mini.win.Load(); fl != nil {
		fl.Close()
	}
}
