package main

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// updatesCard is where the user checks for updates and chooses whether they
// install themselves. Builds made without update signing say so instead.
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

// updateSubtitle names the last successful update check, when there was one.
func updateSubtitle() string {
	if last := updater.LastCheck(); !last.IsZero() {
		return "Last checked " + last.Format("Jan 2, 15:04") + "."
	}
	return "Signed releases install themselves."
}

// desktopCard holds the switches that tie Meiro to the rest of the system:
// notifications, the tray, the global shortcut and starting at login.
func (a *app) desktopCard(c *ui.Context) {
	a.settingsCard(c, "Desktop", m3.IconNotifications, func() {
		ui.Column(c).Gap(4).Children(func() {
			a.desktopSwitch(c, "Track notifications", "Announce each new track. Clicking one brings Meiro back.",
				a.settings.NotifyTrack, func(on bool) { a.settings.NotifyTrack = on })
			a.desktopSwitch(c, "Menu bar icon", "Keep Meiro in the menu bar with play, next and quit.",
				a.settings.ShowTray, func(on bool) {
					a.settings.ShowTray = on
					a.syncDesktop()
				})
			a.desktopSwitch(c, "Close to tray", "Closing the window keeps Meiro running in the tray instead of quitting.",
				a.settings.CloseToTray != nil && *a.settings.CloseToTray, func(on bool) {
					a.setCloseToTray(on)
				})
			a.desktopSwitch(c, "Global shortcut", "Play or pause from anywhere with "+toggleShortcut+".",
				a.settings.GlobalShortcut, func(on bool) {
					a.settings.GlobalShortcut = on
					a.syncShortcut()
				})
			a.desktopSwitch(c, "Open at login", "Start Meiro when you log in.",
				a.settings.OpenAtLogin, func(on bool) {
					a.settings.OpenAtLogin = on
					a.desktop.setOpenAtLogin(on)
				})
			ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
				settingLabel(c, "Listening stats", "Plays per day, most played tracks and artists.").Grow(1)
				if m3.Button(c, m3.ButtonSpec{Label: "Show stats", Kind: m3.Tonal, Size: m3.Medium56, Key: "show-stats"}).Clicked() {
					a.navigate(routeStats)
				}
			})
		})
	})
}

// desktopSwitch is one system-integration row: a label and a switch that
// saves through apply.
func (a *app) desktopSwitch(c *ui.Context, title, body string, on bool, apply func(bool)) {
	ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
		settingLabel(c, title, body).Grow(1)
		if m3.Switch(c, &on, title).Changed() {
			apply(on)
			a.saveSettings()
		}
	})
}
