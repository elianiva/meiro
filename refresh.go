package main

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// Pull to refresh: scrolling up from the top of a page that loads its content
// asks YouTube Music for that content again, as pulling a feed down does on a
// trackpad. The gesture shows a ring that fills with the pull, and the page
// loads again once it is whole.
const (
	// pullRefresh is how far the gesture pulls from the top of the page
	// before the page loads again, in DIPs.
	pullRefresh = 120
	// pullSettle is how long the gesture stays after the scrolling that
	// pulled it stops, so a pull that goes nowhere goes away.
	pullSettle = 400 * time.Millisecond
	// pullDisc is the diameter of the mark the gesture shows, in DIPs.
	pullDisc = 36
)

// pullState is the pull from the top of the page shown.
type pullState struct {
	// distance is how far the pull has come, in DIPs: 0 shows no gesture.
	distance float32
	// moved is when the scroll that pulled it last came.
	moved time.Time
}

// pullable reports whether the page shown loads its content, which pulling
// from its top asks for again.
func (a *app) pullable() bool {
	r, _ := a.route()
	return r.refreshable() && !a.page.feed.loading
}

// pullScroll reads the scrolling of the page as the pull to refresh it. It
// takes the scroll that pulls the page from its top, where there is nothing
// to scroll, and leaves every other scroll to the list.
func (a *app) pullScroll(ev ui.InputEvent) bool {
	// A flick that carries on past the top is the trackpad's own momentum and
	// not a pull: only what the fingers scroll pulls the page. Shift turns the
	// wheel sideways, which the runtime maps after the handlers pass it on, so
	// the pull leaves it to the shelf under the pointer.
	if ev.Kind != ui.InputScroll || ev.DY == 0 || ev.Mods&ui.Shift != 0 || ev.Momentum || !a.pullable() {
		return false
	}
	p := &a.pull
	if ev.DY > 0 && p.distance == 0 {
		// Nothing is being pulled: the scroll is the page's own.
		return false
	}
	if ev.DY < 0 && a.page.scroll.Y > 0 {
		// The page has content of its own above: scrolling up is not a pull.
		return false
	}
	p.distance -= ev.DY
	switch {
	case p.distance <= 0:
		// The pull went back to the top, so the gesture is over.
		*p = pullState{}
	case p.distance >= pullRefresh:
		*p = pullState{}
		a.reloadPage()
	default:
		p.moved = ev.Time
	}
	return true
}

// pullIndicator shows how far the pull to refresh has come, over the top of
// the page.
func (a *app) pullIndicator(c *ui.Context) {
	p := &a.pull
	if p.distance <= 0 {
		return
	}
	if c.Now().Sub(p.moved) >= pullSettle {
		// The gesture stopped short of the threshold: it goes with the
		// scrolling that pulled it.
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
			// The ring fills clockwise from the top as the pull comes, over a
			// track that shows how far is left.
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
