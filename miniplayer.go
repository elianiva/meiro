package main

import (
	"runtime"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

const (
	miniWidth  = 344
	miniArt    = 64
	miniHeight = 640

	miniPadding         = 12
	miniGap             = 8
	miniIconButtonSide  = 40
	miniVolumeSliderMax = miniWidth - 2*miniPadding - 2*(miniIconButtonSide+miniGap)
)

func (a *app) miniPlayer(c *ui.Context) {
	a.beginArtFrame()
	a.followArtwork()
	m3.Provide(c, a.resolveTheme(c))
	sc := m3.Of(c).Scheme
	surface := sc.SurfaceContainer
	if c.Vibrancy() {
		surface = ui.Transparent
	}
	col := ui.Column(c.Key("mini")).Fill().Padding(miniPadding).Gap(miniGap).Background(surface)
	if runtime.GOOS != "darwin" {
		c.Root().Background(ui.Transparent)
		col.Radius(m3.ExtraLarge)
	}
	col.Children(func() {
		ui.Column(c.Key("mini-content")).Grow(1).MinHeight(0).Gap(8).Children(func() {
			ui.Row(c.Key("mini-player")).Gap(8).AlignItems(ui.Center).Shrink(0).Children(func() {
				a.playerArtwork(&a.miniCover, c, a.current.Thumbnail, playerArt, miniArt, m3.Large, func() {
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
				a.volumeControl(c, miniVolumeSliderMax)
				a.speedButton(c, "mini-speed")
			})
			a.sidePanel(c, false, true)
		})
		ui.Row(c).Gap(8).Justify(ui.SpaceBetween).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Open Meiro", Kind: m3.Tonal, Size: m3.Compact32, Key: "mini-open"}).Clicked() {
				a.desktop.closeMini()
				a.desktop.bringToFront()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Quit", Kind: m3.TextOnly, Size: m3.Compact32, Key: "mini-quit"}).Clicked() {
				mygo.App.Quit()
			}
		})
	})
}

type mini struct {
	win atomic.Pointer[mygo.Window]
}

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

func (d *desktop) closeMini() {
	if d == nil {
		return
	}
	if fl := d.mini.win.Load(); fl != nil {
		fl.Close()
	}
}
