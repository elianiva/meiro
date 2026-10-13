package main

import (
	"context"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/m3"
)

// reportTimeout bounds reading the tool versions for a report.
const reportTimeout = 15 * time.Second

// reportCard opens a prefilled GitHub issue. The details are the versions of
// the app and its tools and the system's name; nothing of the account.
func (a *app) reportCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Support", m3.IconError, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Something not working?", "Open a new issue on GitHub with the Meiro, system, yt-dlp and ffmpeg versions filled in. Nothing about your account or listening is included.").Grow(1)
			spec := m3.ButtonSpec{Label: "Report a problem", Kind: m3.Tonal, Size: m3.Medium56, Key: "report-problem"}
			if a.report.busy() {
				spec.Disabled = true
			}
			if m3.Button(c, spec).Clicked() {
				a.reportProblem()
			}
		})
		if a.reportError != "" {
			m3.Text(c, m3.BodyMedium, a.reportError).TextColor(sc.Error).MaxLines(3)
		}
	})
}

// reportProblem reads the tool versions off the main thread, then opens the
// issue in the browser.
func (a *app) reportProblem() {
	a.reportError = ""
	version := mygo.App.Version()
	startTask(a, &a.report, reportTimeout, func(ctx context.Context) (string, error) {
		return report.Collect(ctx, version).URL(), nil
	}, func(link string, err error) {
		if err == nil {
			err = a.openURL(link)
		}
		if err != nil {
			log.Printf("report: %v", err)
			a.reportError = "Could not open the browser: " + err.Error()
		}
	})
}
