package main

import (
	"fmt"
	"log"
	"runtime"
	"runtime/debug"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/applog"
	"github.com/elianiva/meiro/m3"
)

// crashState is the dialog that tells the user the app hit a bug, in place
// of the window vanishing. It is nil while no crash is being shown.
type crashState struct {
	open bool
	// where is what the app was doing, and message what it panicked with.
	where, message string
	// details is the report the user can copy: versions, where, the panic
	// and its stack, with secrets redacted.
	details string
	// inView is set when drawing the window is what panicked. The window is
	// then drawn as the dialog alone until it is dismissed, so a page that
	// cannot be drawn does not bring the dialog down with it.
	inView bool
}

// recoverMain is deferred by code that runs on the main thread, and turns a
// panic there into the crash dialog. It must be the deferred call itself.
func (a *app) recoverMain(where string) {
	if v := recover(); v != nil {
		a.crashed(where, v, debug.Stack(), false)
	}
}

// recoverBackground is recoverMain for work off the main thread: the dialog
// is raised on the main thread.
func (a *app) recoverBackground(where string) {
	if v := recover(); v != nil {
		stack := debug.Stack()
		a.update(func() { a.crashed(where, v, stack, false) })
	}
}

// guarded runs fn off the main thread, with a panic in it shown as a crash.
func (a *app) guarded(where string, fn func()) {
	defer a.recoverBackground(where)
	fn()
}

// frame draws the window. A panic while drawing it is shown as a crash
// instead of ending the app; the production window draws through it, and
// tests draw a.view directly, so they see a panic.
func (a *app) frame(c *ui.Context) { a.guard(c, a.view) }

func (a *app) guard(c *ui.Context, build func(*ui.Context)) {
	if d := a.crash; d != nil && d.inView {
		defer a.recoverMain("drawing the crash dialog")
		m3.Provide(c, a.resolveTheme(c))
		a.crashDialog(c)
		return
	}
	defer func() {
		if v := recover(); v != nil {
			a.crashed("drawing the window", v, debug.Stack(), true)
			a.refresh()
		}
	}()
	build(c)
}

// crashed records a panic: it goes to the log with its stack, and, unless a
// crash is already on screen, opens the dialog. Main thread only.
func (a *app) crashed(where string, v any, stack []byte, inView bool) {
	message := fmt.Sprint(v)
	details := applog.Redact(crashDetails(where, message, stack))
	log.Printf("panic while %s: %s\n%s", where, message, stack)
	if a.crash != nil {
		a.crash.inView = a.crash.inView || inView
		return
	}
	a.crash = &crashState{
		open: true, where: where, inView: inView,
		message: applog.Redact(message), details: details,
	}
}

// crashDetails is the report for a panic.
func crashDetails(where, message string, stack []byte) string {
	version := mygo.App.Version()
	if version == "" {
		version = "development build"
	}
	return fmt.Sprintf("Meiro %s on %s/%s, %s\nPanic while %s: %s\n\n%s",
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), where, message, stack)
}

// crashDialog tells the user the app hit a bug, and offers the details.
func (a *app) crashDialog(c *ui.Context) {
	d := a.crash
	if d == nil {
		return
	}
	if !d.open {
		a.dismissCrash() // closed by its scrim or the Escape key
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &d.open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Meiro hit a problem")
		m3.Text(c, m3.BodyMedium, "Something went wrong while "+d.where+". The details are in the log. Copying them helps when you report the problem.").
			TextColor(sc.OnSurfaceVariant)
		ui.Column(c).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest).Children(func() {
			m3.Text(c, m3.BodyMedium, d.message).TextColor(sc.OnSurface).MaxLines(4)
		})
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "Copy details", Kind: m3.TextOnly, Key: "copy-crash"}).Clicked() {
				c.WriteClipboard(d.details)
				c.Toast("Details copied")
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Open logs", Kind: m3.TextOnly, Disabled: a.logsDir == "", Key: "open-crash-logs"}).Clicked() {
				a.openLogs()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Continue", Key: "dismiss-crash"}).Clicked() {
				a.dismissCrash()
			}
		})
	})
}

// dismissCrash closes the dialog. When the window could not be drawn, the
// page that was shown is the likely cause, so it goes home first.
func (a *app) dismissCrash() {
	if a.crash != nil && a.crash.inView {
		a.navigate(routeHome)
	}
	a.crash = nil
}
