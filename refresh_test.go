package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPullFromTheTopLoadsThePageAgain(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	homeLoads.Store(0)

	// A pull that stops short of the threshold shows how far it came, and
	// loads nothing.
	tt.Scroll(500, 300, 0, -pullRefresh/2)
	tt.Frame()
	if got := homeLoads.Load(); got != 0 {
		t.Fatalf("a pull of %v loaded the page %d times, want none", pullRefresh/2, got)
	}
	if got := a.pull.distance; got != pullRefresh/2 {
		t.Errorf("the pull came %v, want %v", got, pullRefresh/2)
	}
	if _, ok := tt.Find("Pull to refresh"); !ok {
		t.Error("the pull shows nothing while it comes")
	}
	// The pull goes away with the scrolling that made it, a moment later.
	tt.Advance(pullSettle)
	if got := a.pull.distance; got != 0 {
		t.Errorf("the pull stayed at %v after the scrolling stopped", got)
	}

	// A pull past the threshold loads the page again.
	tt.Scroll(500, 300, 0, -pullRefresh)
	tt.Frame()
	if got := homeLoads.Load(); got != 1 {
		t.Fatalf("home loaded %d times, want once after the pull", got)
	}
	if got := a.pull.distance; got != 0 {
		t.Errorf("the pull stayed at %v after loading the page", got)
	}

	// A mouse wheel pulls the same way, a notch at a time.
	for range 3 {
		tt.Wheel(500, 300, 0, -pullRefresh/3)
	}
	tt.Frame()
	if got := homeLoads.Load(); got != 2 {
		t.Errorf("home loaded %d times, want twice after three notches", got)
	}
}

func TestScrollingBackToTheTopDoesNotLoadThePage(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/home")
	// A window shorter than the page, so that the page has somewhere to
	// scroll away from its top.
	tt := ui.NewTester(a.view, 1000, 300)
	tt.Frame()
	homeLoads.Store(0)
	tt.Scroll(500, 150, 0, 400)
	tt.Frame()
	if a.page.scroll.Y <= 0 {
		t.Fatalf("the page did not scroll: Y = %v, MaxY = %v", a.page.scroll.Y, a.page.scroll.MaxY)
	}
	// Scrolling back up to the top is the list's own scroll, however far it
	// comes, and not a pull.
	tt.Scroll(500, 150, 0, -400)
	tt.Frame()
	if got := a.page.scroll.Y; got != 0 {
		t.Errorf("the page is at %v, want its top", got)
	}
	if got := homeLoads.Load(); got != 0 {
		t.Errorf("scrolling back to the top loaded the page %d times, want none", got)
	}
	if got := a.pull.distance; got != 0 {
		t.Errorf("scrolling back to the top pulled the page %v", got)
	}
}

// Shift turns the wheel sideways, so a pull must leave it to the shelf under
// the pointer.
func TestShiftScrollDoesNotPullThePage(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/home")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	homeLoads.Store(0)

	// The tester's scroll sends no modifiers, so call the handler as the
	// runtime would with Shift held.
	if a.pullScroll(ui.InputEvent{Kind: ui.InputScroll, DY: -pullRefresh, Mods: ui.Shift}) {
		t.Error("the pull took a Shift scroll")
	}
	if got := a.pull.distance; got != 0 {
		t.Errorf("a Shift scroll pulled the page %v", got)
	}
	if got := homeLoads.Load(); got != 0 {
		t.Errorf("a Shift scroll loaded the page %d times, want none", got)
	}

	// The same scroll without Shift is still a pull.
	if !a.pullScroll(ui.InputEvent{Kind: ui.InputScroll, DY: -pullRefresh}) {
		t.Error("the pull left an ordinary scroll")
	}
	if got := homeLoads.Load(); got != 1 {
		t.Errorf("an ordinary pull loaded the page %d times, want once", got)
	}
}

func TestPullLeavesAPageThatLoadsNothing(t *testing.T) {
	resetFakes()
	a := newTestApp(t)
	a.router.Push("/album/MPREb_test")
	tt := ui.NewTester(a.view, 1000, 700)
	tt.Frame()
	page := a.page
	tt.Scroll(500, 300, 0, -pullRefresh)
	tt.Frame()
	if got := a.pull.distance; got != 0 {
		t.Errorf("an album page pulled %v, want nothing", got)
	}
	if a.page != page {
		t.Error("an album page loaded again")
	}
}
