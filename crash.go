package main

import (
	"fmt"
	"log"
	"runtime"
	"runtime/debug"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/applog"
	"github.com/elianiva/meiro/internal/sentry"
	"github.com/elianiva/meiro/m3"
)

type crashState struct {
	open           bool
	where, message string
	details        string
	reported       bool
	inView         bool
}

func (a *app) recoverMain(where string) {
	if v := recover(); v != nil {
		a.crashed(where, v, debug.Stack(), false)
	}
}

func (a *app) recoverBackground(where string) {
	if v := recover(); v != nil {
		stack := debug.Stack()
		a.update(func() { a.crashed(where, v, stack, false) })
	}
}

func (a *app) guarded(where string, fn func()) {
	defer a.recoverBackground(where)
	fn()
}

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
		reported: a.reportCrash(sentry.Crash{Where: where, Message: message, Details: details, Stack: string(stack)}),
	}
}

func crashDetails(where, message string, stack []byte) string {
	version := mygo.App.Version()
	if version == "" {
		version = "development build"
	}
	return fmt.Sprintf("Meiro %s on %s/%s, %s\nPanic while %s: %s\n\n%s",
		version, runtime.GOOS, runtime.GOARCH, runtime.Version(), where, message, stack)
}

func (a *app) crashDialog(c *ui.Context) {
	d := a.crash
	if d == nil {
		return
	}
	closedByScrimOrEscape := !d.open
	if closedByScrimOrEscape {
		a.dismissCrash()
		return
	}
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &d.open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Meiro hit a problem")
		m3.Text(c, m3.BodyMedium, "Something went wrong while "+d.where+". The details are in the log. Copying them helps when you report the problem.").
			TextColor(sc.OnSurfaceVariant)
		if d.reported {
			m3.Text(c, m3.BodyMedium, "Crash reports are on, so Meiro sent this report to Sentry.").TextColor(sc.OnSurfaceVariant)
		}
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

func (a *app) dismissCrash() {
	if a.crash != nil && a.crash.inView {
		a.navigate(routeHome)
	}
	a.crash = nil
}
