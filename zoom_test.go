package main

import (
	"math"
	"strconv"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
)

type contentSize struct{ w, h float32 }

func sizedWindow(t *testing.T, a *app, w, h int) (*ui.Tester, func() contentSize) {
	t.Helper()
	var last contentSize
	tt := ui.NewTester(func(c *ui.Context) {
		last.w, last.h = c.Size()
		a.view(c)
	}, w, h)
	return tt, func() contentSize { return last }
}

func TestZoomShortcutsScaleTheWindow(t *testing.T) {
	a := newTestApp(t)
	tt, size := sizedWindow(t, a, 1000, 700)
	tt.Frame()
	if got := size(); got != (contentSize{1000, 700}) {
		t.Fatalf("unzoomed content = %v, want 1000x700", got)
	}

	tt.Key(ui.Cmd, ui.KeyEqual)
	tt.Frame()
	if a.settings.Zoom != 110 {
		t.Fatalf("zoom in kept %d%%, want 110%%", a.settings.Zoom)
	}
	want := contentSize{1000 / 1.1, 700 / 1.1}
	if got := size(); math.Abs(float64(got.w-want.w)) > 0.5 || math.Abs(float64(got.h-want.h)) > 0.5 {
		t.Errorf("zoomed content = %v, want %v", got, want)
	}

	tt.Key(ui.Cmd, ui.KeyMinus)
	tt.Frame()
	if a.settings.Zoom != settings.DefaultZoom {
		t.Errorf("zoom out kept %d%%, want %d%%", a.settings.Zoom, settings.DefaultZoom)
	}

	tt.Key(ui.Cmd, ui.KeyMinus)
	tt.Frame()
	if a.settings.Zoom != 90 {
		t.Errorf("zoom out kept %d%%, want 90%%", a.settings.Zoom)
	}

	tt.Key(ui.Cmd, ui.Key0)
	tt.Frame()
	if got := size(); got != (contentSize{1000, 700}) {
		t.Errorf("after reset the content = %v, want 1000x700", got)
	}

	a.settings.Zoom = settings.MaxZoom
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyEqual)
	tt.Frame()
	if a.settings.Zoom != settings.MaxZoom {
		t.Errorf("zoom in went past the end to %d%%", a.settings.Zoom)
	}
}

func TestSettingsZoomButtonsChangeTheInterface(t *testing.T) {
	a := newTestApp(t)
	a.router.Push("/settings")
	tt, _ := sizedWindow(t, a, 1000, 4000)
	tt.Frame()
	if !tt.HasText(strconv.Itoa(settings.DefaultZoom) + "%") {
		t.Fatalf("no zoom control: %q", tt.Texts())
	}

	if err := tt.Click("Zoom in"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Zoom != 110 {
		t.Fatalf("zoomed to %d%%, want 110%%", a.settings.Zoom)
	}
	if !tt.HasText("110%") {
		t.Errorf("the control does not show the new zoom: %q", tt.Texts())
	}

	if err := tt.Click("Zoom out"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.settings.Zoom != settings.DefaultZoom {
		t.Errorf("zoomed back to %d%%, want %d%%", a.settings.Zoom, settings.DefaultZoom)
	}
}
