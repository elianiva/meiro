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

// posthogProjectKey and sentryProjectDSN are where the app's two messages
// go. A release build has tools/dist.sh generate a file that sets them from
// MEIRO_POSTHOG_KEY and MEIRO_SENTRY_DSN; a development build reads those
// variables when it runs.
//
// Without a PostHog key the app sends nothing and never asks, since a notice
// about a message that could not go out would only waste the user's time.
// Without a Sentry DSN it offers no crash reports, since there is nowhere to
// send them.
//
// Neither value is confidential: whatever a desktop app can send with, the
// people running it can read. The PostHog key only lets a message into the
// project, and the DSN only lets a report in; neither grants reading.
var posthogProjectKey, sentryProjectDSN string

// posthogKey is the project key this build sends the install ping with.
func posthogKey() string {
	if posthogProjectKey != "" {
		return posthogProjectKey
	}
	return os.Getenv("MEIRO_POSTHOG_KEY")
}

// sentryDSN is the project this build sends crash reports to.
func sentryDSN() string {
	if sentryProjectDSN != "" {
		return sentryProjectDSN
	}
	return os.Getenv("MEIRO_SENTRY_DSN")
}

// privacyURL is where the repository explains what Meiro sends.
const privacyURL = "https://github.com/elianiva/meiro#privacy"

// privacyState is the first-run notice about the install ping.
type privacyState struct {
	// ask is whether the notice is on screen.
	ask bool
	// sending is set while the ping is on its way, so one answer is one
	// message and the next launch is the only retry.
	sending bool
}

// startPrivacy asks about the install ping on first run, and sends one the
// user agreed to that has not gone out, which a launch before the network was
// up leaves behind. Main thread only.
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

// usagePingOn reports whether the user agreed to the install ping.
func (a *app) usagePingOn() bool {
	return a.settings.UsagePing != nil && *a.settings.UsagePing
}

// answerUsagePing records the user's answer. details says whether the ping
// carries the OS, architecture and Meiro version; without it the message
// holds only its event name and a random ID.
func (a *app) answerUsagePing(details bool) {
	on := true
	a.settings.UsagePing, a.settings.UsagePingDetails = &on, details
	a.saveSettings()
	a.sendUsagePing()
}

// declineUsagePing records a no, which sends nothing now or later.
func (a *app) declineUsagePing() {
	off := false
	a.settings.UsagePing, a.settings.UsagePingDetails = &off, false
	a.saveSettings()
}

// setUsagePing is the Settings switch. Turning it on keeps the details
// choice already made, and sends the ping now unless it went out already.
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

// sendUsagePing sends the one anonymous install ping. With the system details
// chosen it carries the OS, architecture and app version, so the project can
// see which builds people run; otherwise it holds only its event name. The
// random ID is made by the collector client and never saved, so neither
// message says who ran it.
func (a *app) sendUsagePing() {
	capture := a.capture
	if capture == nil || a.settings.UsagePingSent || a.privacy.sending || !a.usagePingOn() {
		return
	}
	var properties map[string]any
	if a.settings.UsagePingDetails {
		properties = map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "version": appVersion()}
	}
	// The answer is kept, so a failure leaves the ping for the next launch,
	// which asks nothing again. Sent is kept only once it arrived, so a quit
	// in the middle sends it again rather than losing it.
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

// appVersion is the release's version, or "development" for a build without
// one. The install ping and a crash report both name it.
func appVersion() string {
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return "development"
}

// reportCrash sends a crash report when the user turned them on, and reports
// whether it did.
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

// privacyDialog asks, once, whether Meiro may count this install. Closing
// it without an answer counts as a no, so the question is never repeated.
func (a *app) privacyDialog(c *ui.Context) {
	if !a.privacy.ask {
		return
	}
	sc := m3.Of(c).Scheme
	open := a.privacy.ask
	m3.Dialog(c, &open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Send one anonymous ping?")
		m3.Text(c, m3.BodyMedium, "The first time Meiro runs, it can send one message so I know how many people use it. Nothing is sent unless you pick one.").TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyMedium, "Send details adds your OS, architecture and the Meiro version, so I can see which builds people run. Just ping sends only a random ID. No thanks sends nothing.").TextColor(sc.OnSurfaceVariant)
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: "What is sent", Kind: m3.TextOnly, Key: "privacy-learn"}).Clicked() {
				a.openPrivacy()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "No thanks", Kind: m3.TextOnly, Key: "decline-usage-ping"}).Clicked() {
				a.privacy.ask = false
				a.declineUsagePing()
			}
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
	// The scrim and the Escape key close it without an answer, which is a no.
	if !open && a.privacy.ask {
		a.privacy.ask = false
		a.declineUsagePing()
	}
}

// privacyCard holds the messages the app can send, and what each one is.
// Neither is on until the user says so.
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

// openPrivacy shows the repository's privacy section in the browser.
func (a *app) openPrivacy() {
	spawn(a, func() error { return a.openURL(privacyURL) }, func(err error) {
		if err != nil {
			a.notice = "Could not open the privacy page: " + err.Error()
		}
	})
}
