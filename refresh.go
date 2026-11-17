package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

const (
	pullRefresh = 120
	pullSettle  = 400 * time.Millisecond
	pullDisc    = 36
)

type pullState struct {
	distance float32
	moved    time.Time
}

func (a *app) pullable() bool {
	r, _ := a.route()
	return r.refreshable() && !a.page.feed.loading
}

func (a *app) pullScroll(ev ui.InputEvent) bool {
	if ev.Kind != ui.InputScroll || ev.DY == 0 || ev.Mods&ui.Shift != 0 || ev.Momentum || !a.pullable() {
		return false
	}
	p := &a.pull
	if ev.DY > 0 && p.distance == 0 {
		return false
	}
	if ev.DY < 0 && a.page.scroll.Y > 0 {
		return false
	}
	p.distance -= ev.DY
	switch {
	case p.distance <= 0:
		*p = pullState{}
	case p.distance >= pullRefresh:
		*p = pullState{}
		a.reloadPage()
	default:
		p.moved = ev.Time
	}
	return true
}

func (a *app) pullIndicator(c *ui.Context) {
	p := &a.pull
	if p.distance <= 0 {
		return
	}
	if c.Now().Sub(p.moved) >= pullSettle {
		*p = pullState{}
		return
	}
	c.After(pullSettle)
	sc := m3.Of(c).Scheme
	progress := min(p.distance/pullRefresh, 1)
	ui.Row(c).Absolute().Top(10).Left(0).Right(0).Height(pullDisc).
		Justify(ui.Center).AlignItems(ui.Center).PassThrough().Children(func() {
		disc := ui.Box(c).Size(pullDisc, pullDisc).Radius(m3.Full).Background(sc.SurfaceContainerHigh).
			Label("Pull to refresh")
		m3.Elevation(c, disc, 2)
		disc.Draw(func(pt *ui.Painter, r ui.Rect) {
			const width = 3
			reach := min(r.W, r.H)/2 - width
			centre := [2]float32{r.X + r.W/2, r.Y + r.H/2}
			var track ui.Path
			track.Circle(centre[0], centre[1], reach)
			pt.StrokePath(&track, width, sc.OutlineVariant)
			turn := 2 * math.Pi * float64(progress)
			var arc ui.Path
			const steps = 48
			for i := range steps + 1 {
				angle := -math.Pi/2 + turn*float64(i)/steps
				x := centre[0] + reach*float32(math.Cos(angle))
				y := centre[1] + reach*float32(math.Sin(angle))
				if i == 0 {
					arc.MoveTo(x, y)
				} else {
					arc.LineTo(x, y)
				}
			}
			pt.StrokePath(&arc, width, sc.Primary)
		})
	})
}
