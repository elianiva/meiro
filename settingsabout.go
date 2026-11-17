package main

import (
	"context"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/m3"
)

func (a *app) updatesCard(c *ui.Context) {
	a.settingsCard(c, "Updates", m3.IconRefresh, func() {
		version := mygo.App.Version()
		if version == "" {
			version = "a development build"
		}
		if !a.desktop.updates() {
			settingLabel(c, "Meiro "+version, "This copy updates itself once it is installed from a signed release. Development builds and package installs update another way.").Grow(1)
			return
		}
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Meiro "+version, updateSubtitle()).Grow(1)
				if m3.Button(c, m3.ButtonSpec{Label: "Check for updates", Kind: m3.Tonal, Size: m3.Medium56, Key: "check-updates"}).Clicked() {
					updater.CheckForUpdates()
				}
			})
			{
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					settingLabel(c, "Check automatically", "Look for a new version once a day.").Grow(1)
					on := updater.AutomaticChecks()
					if m3.Switch(c, &on, "Check automatically").Changed() {
						updater.SetAutomaticChecks(on)
					}
				})
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					settingLabel(c, "Install automatically", "Download in the background and switch at the next launch.").Grow(1)
					on := updater.AutomaticDownloads()
					if m3.Switch(c, &on, "Install automatically").Changed() {
						updater.SetAutomaticDownloads(on)
					}
				})
			}
		})
	})
}

func updateSubtitle() string {
	if last := updater.LastCheck(); !last.IsZero() {
		return "Last checked " + last.Format("Jan 2, 15:04") + "."
	}
	return "Signed releases install themselves."
}

const kofiURL = "https://ko-fi.com/elianiva"

func (a *app) supportCard(c *ui.Context) {
	a.settingsCard(c, "Ko-fi", m3.IconCoffee, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Buy me a coffee", "Meiro is free, with no ads and no paid tier.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Open Ko-fi", Kind: m3.Tonal, Size: m3.Medium56, Key: "open-kofi"}).Clicked() {
				a.openKoFi()
			}
		})
	})
}

func (a *app) openKoFi() {
	spawn(a, func() error { return a.openURL(kofiURL) }, func(err error) {
		if err != nil {
			a.notice = "Could not open Ko-fi: " + err.Error()
		}
	})
}

func (a *app) loadToolVersions() {
	if a.toolsLoaded || a.toolsLookup.busy() {
		return
	}
	version := mygo.App.Version()
	startTask(a, &a.toolsLookup, reportTimeout, func(ctx context.Context) (report.Info, error) {
		return a.collectInfo(ctx, version), nil
	}, func(info report.Info, _ error) {
		a.tools, a.toolsLoaded = info, true
	})
}

func (a *app) toolsCard(c *ui.Context) {
	a.settingsCard(c, "Playback tools", m3.IconSettings, func() {
		yt, ff := "Looking up…", "Looking up…"
		if a.toolsLoaded {
			yt, ff = a.tools.YtDlp, a.tools.FFmpeg
		}
		settingLabel(c, "yt-dlp "+yt, "Finds the audio of each track. The full build includes it; the minimal build uses PATH and needs Node.js or Bun.")
		settingLabel(c, "ffmpeg "+ff, "Decodes audio for playback. The full build includes it; the minimal build uses the version on PATH.")
	})
}

const reportTimeout = 15 * time.Second

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

func (a *app) reportProblem() {
	a.reportError = ""
	version := mygo.App.Version()
	startTask(a, &a.report, reportTimeout, func(ctx context.Context) (string, error) {
		return a.collectInfo(ctx, version).URL(), nil
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

func (a *app) logsCard(c *ui.Context) {
	a.settingsCard(c, "Logs", m3.IconFolder, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			body := "Meiro keeps a log of what it does and what goes wrong, with no passwords or cookies in it."
			if a.logsDir == "" {
				body = "Meiro could not open its log file, so nothing is being logged."
			}
			settingLabel(c, "Log files", body).Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Open logs folder", Kind: m3.Tonal, Size: m3.Medium56, Disabled: a.logsDir == "", Key: "open-logs"}).Clicked() {
				a.openLogs()
			}
		})
	})
}

func (a *app) openLogs() {
	dir := a.logsDir
	if dir == "" {
		return
	}
	spawn(a, func() error { return a.openPath(dir) }, func(err error) {
		if err != nil {
			a.notice = "Could not open the logs folder: " + err.Error()
		}
	})
}
