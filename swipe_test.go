package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func hoverRow(t *testing.T, tt *ui.Tester, title string) ui.Rect {
	t.Helper()
	row, ok := tt.Find(title)
	if !ok {
		t.Fatalf("no row %q: %q", title, tt.Texts())
	}
	tt.Move(row.X+row.W/2, row.Y+row.H/2)
	tt.Frame()
	return row
}

func swipeRowGesture(t *testing.T, a *app, tt *ui.Tester, row ui.Rect, dx float32) {
	t.Helper()
	a.swipeMods = 0
	x, y := row.X+row.W/2, row.Y+row.H/2
	tt.Press(x, y)
	tt.Frame()
	for _, step := range []float32{dx / 4, dx / 2, dx} {
		tt.Move(x+step, y)
		tt.Frame()
	}
	tt.Release(x+dx, y)
	tt.Frame()
}

func dragRow(t *testing.T, tt *ui.Tester, row ui.Rect, dx float32) {
	t.Helper()
	x, y := row.X+row.W/2, row.Y+row.H/2
	tt.Press(x, y)
	tt.Frame()
	tt.Move(x+dx, y)
	tt.Frame()
	tt.Release(x+dx, y)
	tt.Frame()
}

func TestASongsRowAddsItsTrackToTheQueueFromItsButton(t *testing.T) {
	a, _ := playbackApp(t)
	playing := tracks("a")
	a.play(playing[0], playing)
	tt := open(a, "/playlist/VLPL_metadata")
	hoverRow(t, tt, "Linked Track")
	if err := tt.Click("Add Linked Track to queue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "linked-track"}) {
		t.Fatalf("the queue holds %v", got)
	}
	if a.current.VideoID != "a" {
		t.Errorf("the button played %q", a.current.VideoID)
	}
	if !tt.HasText("Added Linked Track to the queue") {
		t.Errorf("the add said %q", tt.Texts())
	}
}

func TestAQueueRowRemovesItsTrackFromTheButton(t *testing.T) {
	a, _ := playbackApp(t)
	queue := tracks("a", "b", "c")
	a.play(queue[0], queue)
	tt := ui.NewTester(a.view, 1180, 850)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)

	if _, ok := tt.Find("Remove Track a from queue"); ok {
		t.Fatal("the row of the track playing offers to remove it")
	}
	hoverRow(t, tt, "Track b")
	if err := tt.Click("Remove Track b from queue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("the queue holds %v", got)
	}
	if !tt.HasText("Removed Track b from the queue") {
		t.Errorf("the remove said %q", tt.Texts())
	}
}

func TestSwipingASongRowAddsItToTheQueue(t *testing.T) {
	a, _ := playbackApp(t)
	playing := tracks("a")
	a.play(playing[0], playing)
	tt := open(a, "/playlist/VLPL_metadata")
	row := hoverRow(t, tt, "Linked Track")
	swipeRowGesture(t, a, tt, row, 140)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "linked-track"}) {
		t.Fatalf("the swipe left the queue holding %v", got)
	}
	if a.current.VideoID != "a" {
		t.Errorf("the swipe played %q", a.current.VideoID)
	}
}

func TestSwipingAQueueRowRemovesItFromTheQueue(t *testing.T) {
	for _, dx := range []float32{140, -140} {
		a, _ := playbackApp(t)
		queue := tracks("a", "b", "c")
		a.play(queue[0], queue)
		tt := ui.NewTester(a.view, 1180, 850)
		a.openPlayer(tabQueue, false)
		afterAnimations(tt)
		row := hoverRow(t, tt, "Track b")
		swipeRowGesture(t, a, tt, row, dx)
		if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "c"}) {
			t.Fatalf("a swipe of %v left the queue holding %v", dx, got)
		}
		if !tt.HasText("Removed Track b from the queue") {
			t.Errorf("a swipe of %v said %q", dx, tt.Texts())
		}
	}
}

func TestAShortSwipeLeavesTheQueueAlone(t *testing.T) {
	a, _ := playbackApp(t)
	playing := tracks("a")
	a.play(playing[0], playing)
	tt := open(a, "/playlist/VLPL_metadata")
	row := hoverRow(t, tt, "Linked Track")
	swipeRowGesture(t, a, tt, row, swipeCommit/2)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("a short swipe left the queue holding %v", got)
	}
}

func TestADragWithoutTheModifierLeavesTheQueueAlone(t *testing.T) {
	a, _ := playbackApp(t)
	queue := tracks("a", "b", "c")
	a.play(queue[0], queue)
	tt := ui.NewTester(a.view, 1180, 850)
	a.openPlayer(tabQueue, false)
	afterAnimations(tt)
	row := hoverRow(t, tt, "Track b")
	dragRow(t, tt, row, 140)
	if got := ids(a.queue.items); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("a plain drag left the queue holding %v", got)
	}
}
