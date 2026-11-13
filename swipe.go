package main

import (
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// A row swiped sideways acts on its track: one in a list adds the track to the
// queue, and one in the queue takes it out. The gesture is the pointer's, with
// Shift held, so that it stays apart from what the rows already answer: a
// click chooses a row, a drag in the queue moves it, and a plain drag over a
// page belongs to the list.
const (
	// swipeCommit is how far a row travels sideways before letting it go acts.
	swipeCommit = 96
	// swipeReach is how far a row follows the pointer at most.
	swipeReach = 160
)

// swipeState is the row the pointer swipes, and where it is on its way: the
// offset the row shows from its place, and the furthest it has come.
type swipeState struct {
	key    string
	offset ui.Point
	reach  float32
}

// swipeAllowed reports whether the modifiers the swipe asks for are held. A
// swipe asking for none is always allowed, which the tests use: the tester
// sends no modifiers with the pointer.
func (a *app) swipeAllowed(c *ui.Context) bool {
	return a.swipeMods == 0 || c.Modifiers()&a.swipeMods != 0
}

// swipeRowOptions are what a swiped row needs to show and carry out its
// action.
type swipeRowOptions struct {
	key string
	// remove says the row takes its track out of the queue rather than adding
	// it.
	remove bool
	// padding is the room the row keeps beside its content, which the colour
	// fills so that it meets the content as the row slides.
	padding float32
	// radius is the corner of the row, which the colour takes too: the list
	// gives its rows theirs, and the action must not round past it.
	radius float32
}

// swipeRow lets the pointer drag a row sideways, with Shift held, to act on
// its track. The row uncovers the action under it as it moves, and letting it
// go past swipeCommit acts; short of that, it springs back. A press that
// swipes is no click, as a drag that moves a row is not one.
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
		// It always comes home: acting is the row's, not the rest's.
		Snap: func(ui.Point) ui.Point { return ui.Point{} },
	})
	dx := a.swipe.offset.X
	if reach := max(dx, -dx); reach > a.swipe.reach {
		a.swipe.reach = reach
	}
	if dx != 0 {
		a.swipeReveal(c, row, dx, o)
		// The actions at the end of the row go with the content it uncovers,
		// so that the action showing under it is the only one.
		menu.Opacity(0).PassThrough()
		button.Opacity(0).PassThrough()
	}
	if _, _, ended := main.DragEnded(); ended {
		commit := a.swipe.reach >= swipeCommit
		a.swipe.reach = 0
		if commit {
			// The row may leave with the action, so it comes home at once.
			a.swipe = swipeState{}
			act()
			return
		}
	}
	if !main.Pressed() && a.swipe.offset == (ui.Point{}) {
		a.swipe.key = ""
	}
}

// swipeReveal paints the action under the edge a swiped row uncovers: its
// container colour, and a plus to add the track or a cross to take it out.
func (a *app) swipeReveal(c *ui.Context, row ui.Element, dx float32, o swipeRowOptions) {
	sc := m3.Of(c).Scheme
	fill, ink := sc.SecondaryContainer, sc.OnSecondaryContainer
	if o.remove {
		fill, ink = sc.ErrorContainer, sc.OnErrorContainer
	}
	row.Draw(func(p *ui.Painter, r ui.Rect) {
		// The colour fills the room the row keeps beside its content, so that
		// it meets the content instead of leaving the row's background in a
		// gap between them.
		strip := r
		centre := r.X + swipeGlyphInset
		if dx < 0 {
			strip.W, strip.X = -dx+o.padding, r.X+r.W+dx-o.padding
			centre = r.X + r.W - swipeGlyphInset
		} else {
			strip.W = dx + o.padding
		}
		middle := r.Y + r.H/2
		// The colour follows the row's shape at the edge it uncovers, and
		// meets the content sliding over it in a square edge: the rounded
		// shape is the row's, not a pill of the action's.
		p.Clip(r, o.radius, func() {
			p.Fill(strip, fill, 0)
			// The mark stays within what the row uncovers, so it never shows
			// over the content sliding over it.
			p.Clip(strip, 0, func() {
				// Painter.Line draws only horizontal and vertical strokes, so
				// the cross is a path.
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

// The mark a swiped row uncovers, in DIPs: how far in from the edge it sits,
// the arm of its plus or cross, and its stroke.
const (
	swipeGlyphInset = 28
	swipeGlyphArm   = 8
	swipeGlyphWidth = 2.5
)
