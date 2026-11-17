package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/sentry"
	"github.com/elianiva/meiro/m3"
)

var posthogProjectKey, sentryProjectDSN string

func posthogKey() string {
	if posthogProjectKey != "" {
		return posthogProjectKey
	}
	return os.Getenv("MEIRO_POSTHOG_KEY")
}

func sentryDSN() string {
	if sentryProjectDSN != "" {
		return sentryProjectDSN
	}
	return os.Getenv("MEIRO_SENTRY_DSN")
}

const privacyURL = "https://github.com/elianiva/meiro#privacy"

type privacyState struct {
	ask     bool
	sending bool
}

func (a *app) startPrivacy() {
	if a.capture == nil || a.settings.UsagePingSent {
		return
	}
	if a.settings.UsagePing == nil {
		a.privacy.ask = true
		return
	}
	if a.usagePingOn() {
		a.sendUsagePing()
	}
}

func (a *app) usagePingOn() bool {
	return a.settings.UsagePing != nil && *a.settings.UsagePing
}

func (a *app) answerUsagePing(details bool) {
	on := true
	a.settings.UsagePing, a.settings.UsagePingDetails = &on, details
	a.saveSettings()
	a.sendUsagePing()
}

func (a *app) declineUsagePing() {
	off := false
	a.settings.UsagePing, a.settings.UsagePingDetails = &off, false
	a.saveSettings()
}

func (a *app) setUsagePing(on bool) {
	a.settings.UsagePing = &on
	if !on {
		a.settings.UsagePingDetails = false
	}
	a.saveSettings()
	if on {
		a.sendUsagePing()
	}
}

func (a *app) sendUsagePing() {
	capture := a.capture
	if capture == nil || a.settings.UsagePingSent || a.privacy.sending || !a.usagePingOn() {
		return
	}
	var properties map[string]any
	if a.settings.UsagePingDetails {
		properties = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": appVersion()}
	}
	a.privacy.sending = true
	spawn(a, func() error {
		return capture("install", properties)
	}, func(err error) {
		a.privacy.sending = false
		if err != nil {
			log.Printf("sending the install ping: %v", err)
			return
		}
		a.settings.UsagePingSent = true
		a.saveSettings()
	})
}

func appVersion() string {
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return "development"
}

func (a *app) reportCrash(crash sentry.Crash) bool {
	send := a.sendCrash
	if send == nil || !a.settings.CrashReports {
		return false
	}
	a.run(func() {
		if err := send(crash); err != nil {
			log.Printf("sending a crash report: %v", err)
		}
	})
	return true
}

func (a *app) privacyDialog(c *ui.Context) {
	if !a.privacy.ask {
		return
	}
	sc := m3.Of(c).Scheme
	open := a.privacy.ask
	m3.Dialog(c, &open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Send one anonymous ping?")
		m3.Text(c, m3.BodyMedium, "One message, so I know how many people use Meiro. Nothing is sent unless you choose.").TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyMedium, "Send details adds your OS, architecture and the Meiro version. Just ping sends only a random ID.").TextColor(sc.OnSurfaceVariant)
		ui.Row(c).FillWidth().AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "No thanks", Kind: m3.TextOnly, Key: "decline-usage-ping"}).Clicked() {
				a.privacy.ask = false
				a.declineUsagePing()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if m3.Button(c, m3.ButtonSpec{Label: "Just ping", Kind: m3.TextOnly, Key: "ping-without-details"}).Clicked() {
					a.privacy.ask = false
					a.answerUsagePing(false)
				}
				if m3.Button(c, m3.ButtonSpec{Label: "Send details", Key: "ping-with-details"}).Clicked() {
					a.privacy.ask = false
					a.answerUsagePing(true)
				}
			})
		})
	})
	if !open && a.privacy.ask {
		a.privacy.ask = false
		a.declineUsagePing()
	}
}

func (a *app) privacyCard(c *ui.Context) {
	if a.capture == nil && a.sendCrash == nil {
		return
	}
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Privacy", m3.IconShield, func() {
		if a.capture != nil {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				body := "One message on first run, so I know how many people use Meiro. It carries a random ID and nothing about you."
				if a.settings.UsagePingSent {
					body = "Sent, ping only. It carried a random ID and nothing about you."
					if a.settings.UsagePingDetails {
						body = "Sent with the OS, architecture and Meiro version. It carried nothing else."
					}
				}
				settingLabel(c, "Anonymous ping", body).Grow(1)
				if a.settings.UsagePingSent {
					ui.Row(c).Gap(4).AlignItems(ui.Center).Shrink(0).Children(func() {
						ui.Icon(c, m3.IconCheck).FontSize(20).TextColor(sc.Primary)
						m3.Text(c, m3.LabelLarge, "Sent").TextColor(sc.Primary)
					})
					return
				}
				on := a.usagePingOn()
				if m3.Switch(c, &on, "Anonymous install ping").Changed() {
					a.setUsagePing(on)
				}
			})
			if a.usagePingOn() && !a.settings.UsagePingSent {
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					settingLabel(c, "System details", "Include the OS, architecture and Meiro version with the ping, so I can see which builds people run.").Grow(1)
					on := a.settings.UsagePingDetails
					if m3.Switch(c, &on, "Include system details").Changed() {
						a.settings.UsagePingDetails = on
						a.saveSettings()
					}
				})
			}
		}
		if a.sendCrash != nil {
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Crash reports", "When Meiro crashes, send the details to Sentry. Passwords, cookies and web addresses are removed first. Off unless you turn it on.").Grow(1)
				on := a.settings.CrashReports
				if m3.Switch(c, &on, "Send crash reports").Changed() {
					a.settings.CrashReports = on
					a.saveSettings()
				}
			})
		}
		ui.Row(c).Gap(16).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "What is sent", Kind: m3.TextOnly, Key: "privacy-learn-card"}).Clicked() {
				a.openPrivacy()
			}
		})
	})
}

func (a *app) openPrivacy() {
	spawn(a, func() error { return a.openURL(privacyURL) }, func(err error) {
		if err != nil {
			a.notice = "Could not open the privacy page: " + err.Error()
		}
	})
}
