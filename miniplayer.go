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
	miniWidth  = 344
	miniHeight = 268
	// miniTallHeight is the height with the side panel open under the controls.
	miniTallHeight = 640
	miniArt        = 72
)

// miniPlayer is the panel of the tray icon: what plays, where it is, the
// transport, and the way back to the window or out of the app. It is drawn
// in a flyout of the tray, a window of its own, from the same state as the
// main window.
func (a *app) miniPlayer(c *ui.Context) {
	m3.Provide(c, a.resolveTheme(c))
	sc := m3.Of(c).Scheme
	col := ui.Column(c.Key("mini")).Fill().Padding(16).Gap(12).Background(sc.SurfaceContainer)
	if runtime.GOOS != "darwin" {
		// macOS rounds the popover itself.
		col.Radius(m3.ExtraLarge)
	}
	col.Children(func() {
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			a.art(c, a.current.Thumbnail, miniArt*2, miniArt, m3.Large, func() {
				if a.current.IsVideo() {
					m3.VideoBadge(c, miniArt)
				}
			})
			if a.current.VideoID == "" {
				ui.Column(c).Grow(1).MinWidth(0).Children(func() {
					m3.EmphasizedText(c, m3.TitleMedium, "Nothing playing").TextColor(sc.OnSurface)
					m3.Text(c, m3.BodySmall, "Pick something in Meiro").TextColor(sc.OnSurfaceVariant)
				})
				return
			}
			a.trackLine(c, m3.TitleMedium, m3.BodyMedium).Grow(1)
		})
		a.scrubber(c)
		a.transport(c, 52, 4)
		if a.nowPlaying.miniExpanded {
			a.sidePanel(c, false)
		}
		ui.Row(c).Gap(8).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			icon, label := m3.IconExpandMore, "Show up next, lyrics and related"
			if a.nowPlaying.miniExpanded {
				icon, label = m3.IconExpandLess, "Hide up next, lyrics and related"
			}
			if m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: label, Key: "mini-expand"}).Clicked() {
				a.setMiniExpanded(!a.nowPlaying.miniExpanded)
			}
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

// setMiniExpanded opens or closes the side panel of the mini player, and
// resizes its window to fit.
func (a *app) setMiniExpanded(on bool) {
	a.nowPlaying.miniExpanded = on
	height := miniHeight
	if on {
		height = miniTallHeight
		a.loadPanel()
	}
	a.desktop.resizeMini(miniWidth, height)
}

// mini is the mini player's window while it is open. The system closes it
// on a press outside, so it is made anew each time it opens.
type mini struct {
	// tall is whether the side panel is open, so the next window opens at that
	// height. Main thread only.
	tall bool
	win  atomic.Pointer[mygo.Window]
}

// showMini opens the mini player next to the tray icon. A click on the icon
// while it is open leaves it open. It runs on the main thread.
func (d *desktop) showMini(view func(*ui.Context)) {
	if d == nil || d.tray == nil {
		return
	}
	if d.mini.win.Load() != nil {
		return
	}
	height := miniHeight
	if d.mini.tall {
		height = miniTallHeight
	}
	fl := mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      d.tray,
		Placement: mygo.PlacementBottom,
		Width:     miniWidth,
		Height:    height,
		Focusable: true,
		Shadow:    true,
		Popover:   runtime.GOOS == "darwin",
		Content:   ui.View(view),
	})
	d.mini.win.Store(fl)
	fl.OnClosed(func() { d.mini.win.CompareAndSwap(fl, nil) })
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

// resizeMini sets the size of the mini player, now if it is open and for the next time.
func (d *desktop) resizeMini(width, height int) {
	if d == nil {
		return
	}
	d.mini.tall = height > miniHeight
	if fl := d.mini.win.Load(); fl != nil {
		fl.SetSize(width, height)
	}
}
