package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestAPanicWhileDrawingShowsTheCrashDialog(t *testing.T) {
	a := newTestApp()
	a.logsDir = "/logs"
	var opened []string
	a.openPath = func(path string) error {
		opened = append(opened, path)
		return nil
	}
	boom := true
	tt := ui.NewTester(func(c *ui.Context) {
		a.guard(c, func(c *ui.Context) {
			if boom {
				panic("boom Cookie: SAPISID=hunter2")
			}
			a.view(c)
		})
	}, 1000, 900)
	tt.Frame()
	if !tt.HasText("Meiro hit a problem") {
		t.Fatalf("no crash dialog after a panic: %q", tt.Texts())
	}
	if !tt.HasText("Copy details") || !tt.HasText("Open logs") {
		t.Errorf("the dialog lacks its buttons: %q", tt.Texts())
	}

	if err := tt.Click("Copy details"); err != nil {
		t.Fatal(err)
	}
	copied := tt.Clipboard()
	if !strings.Contains(copied, "boom") || !strings.Contains(copied, "goroutine") {
		t.Errorf("copied details lack the panic or its stack: %q", copied)
	}
	if strings.Contains(copied, "hunter2") {
		t.Errorf("copied details carry a secret: %q", copied)
	}

	if err := tt.Click("Open logs"); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || opened[0] != "/logs" {
		t.Errorf("opened %v, want the logs folder", opened)
	}

	// While drawing is what panics, the dialog stays up on its own, and
	// Continue goes home and draws the window again.
	tt.Frame()
	if a.crash == nil {
		t.Fatal("the dialog went away by itself")
	}
	boom = false
	a.router.Push("/settings")
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.crash != nil || tt.HasText("Meiro hit a problem") {
		t.Errorf("the dialog is still there: %q", tt.Texts())
	}
	if a.path() != routeByID(routeHome).path("") {
		t.Errorf("Continue left the app on %q, want home", a.path())
	}
}

func TestAPanicInBackgroundWorkShowsTheCrashDialog(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 900)
	func() {
		defer a.recoverBackground("loading a thing")
		panic("worker failed")
	}()
	tt.Frame()
	if !tt.HasText("Meiro hit a problem") || !tt.HasText("worker failed") {
		t.Fatalf("no crash dialog for a background panic: %q", tt.Texts())
	}
	if a.crash.inView {
		t.Error("a background panic must not replace the window")
	}
	if err := tt.Click("Continue"); err != nil {
		t.Fatal(err)
	}
	if a.crash != nil {
		t.Error("Continue left the crash open")
	}
}

func TestTestsDoNotHidePanicsInWork(t *testing.T) {
	a := newTestApp()
	defer func() {
		if recover() == nil {
			t.Error("a panic in work run by a test was swallowed")
		}
	}()
	a.run(func() { panic("visible") })
}
