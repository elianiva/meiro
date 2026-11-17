package m3

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDialogScrollsPastTheWindow(t *testing.T) {
	open := true
	const window = 300
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		Dialog(c, &open, 400, func() {
			ui.Text(c, "first").Label("first")
			ui.Box(c).Height(600).Label("spacer")
			ui.Text(c, "last").Label("last")
		})
	}, 500, window)
	tt.Frame()

	panel, ok := tt.Find("dialog-panel")
	if !ok || panel.Y < 0 || panel.Y+panel.H > window {
		t.Fatalf("the dialog is not inside the window: %+v", panel)
	}
	if last, ok := tt.Find("last"); ok && last.H > 0 {
		t.Fatalf("the end of the dialog is already in view, at %+v, so nothing scrolls", last)
	}

	tt.Scroll(panel.X+panel.W/2, panel.Y+panel.H/2, 0, 2000)
	tt.Frame()
	last, ok := tt.Find("last")
	if !ok || last.H == 0 || last.Y < panel.Y || last.Y+last.H > panel.Y+panel.H {
		t.Fatalf("scrolling did not bring the end into the dialog: ok=%v last %+v panel %+v", ok, last, panel)
	}
	first, _ := tt.Find("first")
	if first.H > 0 && first.Y >= panel.Y {
		t.Fatalf("scrolling left the start of the dialog in view, at %+v", first)
	}
}

func TestDialogFitsATallWindow(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		Dialog(c, &open, 400, func() {
			ui.Text(c, "only").Label("only")
		})
	}, 500, 400)
	tt.Frame()

	panel, ok := tt.Find("dialog-panel")
	only, shown := tt.Find("only")
	if !ok || !shown || only.H == 0 || only.Y < panel.Y || only.Y+only.H > panel.Y+panel.H {
		t.Fatalf("a short dialog hides its content: panel %+v only %+v", panel, only)
	}
	if panel.H > 200 {
		t.Fatalf("a short dialog takes the window's height, %+v", panel)
	}
}

func TestDialogClosesFromTheScrimAndEscape(t *testing.T) {
	for _, close := range []func(*ui.Tester){
		func(tt *ui.Tester) { tt.Key(0, ui.KeyEscape) },
		func(tt *ui.Tester) { tt.ClickAt(8, 8) },
	} {
		open := true
		tt := ui.NewTester(func(c *ui.Context) {
			Provide(c, New(Config{}, false))
			Dialog(c, &open, 200, func() {
				ui.Text(c, "inside").Label("inside")
			})
		}, 500, 400)
		tt.Frame()
		close(tt)
		tt.Frame()
		tt.Frame()
		if open {
			t.Fatal("the dialog stayed open")
		}
		if _, ok := tt.Find("inside"); ok {
			t.Fatal("the dialog still shows after it closed")
		}
	}

	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		Dialog(c, &open, 200, func() {
			ui.Text(c, "inside").Label("inside")
		})
	}, 500, 400)
	tt.Frame()
	panel, _ := tt.Find("dialog-panel")
	tt.ClickAt(panel.X+panel.W/2, panel.Y+panel.H/2)
	tt.Frame()
	if !open {
		t.Fatal("a press on the panel closed the dialog")
	}
}
