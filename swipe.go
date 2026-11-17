package main

import (
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

const (
	swipeCommit = 96
	swipeReach  = 160
)

type swipeState struct {
	key    string
	offset ui.Point
	reach  float32
}

func (a *app) swipeAllowed(c *ui.Context) bool {
	return a.swipeMods == 0 || c.Modifiers()&a.swipeMods != 0
}

type swipeRowOptions struct {
	key     string
	remove  bool
	padding float32
	radius  float32
}

func (a *app) swipeRow(c *ui.Context, row, main, menu, button ui.Element, o swipeRowOptions, act func()) {
	if main.Pressed() && a.swipeAllowed(c) && a.swipe.key != o.key {
		a.swipe = swipeState{key: o.key}
	}
	if a.swipe.key != o.key {
		return
	}
	main.Draggable(&a.swipe.offset, ui.DraggableOptions{
		Axis:   ui.Horizontal,
		Rigid:  true,
		Bounds: &ui.Rect{X: -swipeReach, W: 2 * swipeReach},
		Snap:   func(ui.Point) ui.Point { return ui.Point{} },
	})
	dx := a.swipe.offset.X
	if reach := max(dx, -dx); reach > a.swipe.reach {
		a.swipe.reach = reach
	}
	if dx != 0 {
		a.swipeReveal(c, row, dx, o)
		menu.Opacity(0).PassThrough()
		button.Opacity(0).PassThrough()
	}
	if _, _, ended := main.DragEnded(); ended {
		commit := a.swipe.reach >= swipeCommit
		a.swipe.reach = 0
		if commit {
			a.swipe = swipeState{}
			act()
			return
		}
	}
	if !main.Pressed() && a.swipe.offset == (ui.Point{}) {
		a.swipe.key = ""
	}
}

func (a *app) swipeReveal(c *ui.Context, row ui.Element, dx float32, o swipeRowOptions) {
	sc := m3.Of(c).Scheme
	fill, ink := sc.SecondaryContainer, sc.OnSecondaryContainer
	if o.remove {
		fill, ink = sc.ErrorContainer, sc.OnErrorContainer
	}
	row.Draw(func(p *ui.Painter, r ui.Rect) {
		strip := r
		centre := r.X + swipeGlyphInset
		if dx < 0 {
			strip.W, strip.X = -dx+o.padding, r.X+r.W+dx-o.padding
			centre = r.X + r.W - swipeGlyphInset
		} else {
			strip.W = dx + o.padding
		}
		middle := r.Y + r.H/2
		p.Clip(r, o.radius, func() {
			p.Fill(strip, fill, 0)
			p.Clip(strip, 0, func() {
				var mark ui.Path
				if o.remove {
					mark.MoveTo(centre-swipeGlyphArm, middle-swipeGlyphArm).LineTo(centre+swipeGlyphArm, middle+swipeGlyphArm)
					mark.MoveTo(centre-swipeGlyphArm, middle+swipeGlyphArm).LineTo(centre+swipeGlyphArm, middle-swipeGlyphArm)
				} else {
					mark.MoveTo(centre-swipeGlyphArm, middle).LineTo(centre+swipeGlyphArm, middle)
					mark.MoveTo(centre, middle-swipeGlyphArm).LineTo(centre, middle+swipeGlyphArm)
				}
				p.StrokePath(&mark, swipeGlyphWidth, ink)
			})
		})
	})
}

const (
	swipeGlyphInset = 28
	swipeGlyphArm   = 8
	swipeGlyphWidth = 2.5
)
