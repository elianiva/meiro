package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/lastfm"
	"github.com/elianiva/meiro/m3"
)

// lastFMCard is where the user connects Last.fm, pauses scrobbling and
// disconnects. Connecting is the user's choice: nothing is sent before it.
func (a *app) lastFMCard(c *ui.Context) {
	s := &a.scrobble
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Last.fm", m3.IconMusicNote, func() {
		switch {
		case !s.client.Configured():
			settingLabel(c, "Scrobbling", "This build of Meiro has no Last.fm application key, so it cannot connect to Last.fm.")
		case s.connect.busy():
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Waiting for approval", "Approve Meiro on the Last.fm page that opened in your browser. Meiro connects as soon as you do, and never sees your Last.fm password.").Grow(1)
				m3.LoadingIndicator(c, 40, false)
				if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.Outlined, Size: m3.Medium56, Key: "lastfm-cancel"}).Clicked() {
					a.cancelLastFMConnect()
				}
			})
		case a.settings.LastFMUser == "":
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				settingLabel(c, "Scrobbling", "Send the tracks you listen to, and what is playing now, to your Last.fm profile.").Grow(1)
				if m3.Button(c, m3.ButtonSpec{Label: "Connect Last.fm", Kind: m3.Filled, Size: m3.Medium56, Key: "lastfm-connect"}).Clicked() {
					a.connectLastFM()
				}
			})
		default:
			a.lastFMConnected(c)
		}
		if s.connectErr != "" {
			m3.Text(c, m3.BodyMedium, s.connectErr).TextColor(sc.Error).MaxLines(3)
		}
	})
}

// lastFMConnected is the card of an account that is connected.
func (a *app) lastFMConnected(c *ui.Context) {
	s := &a.scrobble
	sc := m3.Of(c).Scheme
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		body := "Plays are sent to Last.fm after you have listened to them for long enough."
		if n := s.outbox.Len(); n > 0 {
			body += fmt.Sprintf(" %d waiting to be sent.", n)
		}
		settingLabel(c, "Connected as "+a.settings.LastFMUser, body).Grow(1)
		if m3.Button(c, m3.ButtonSpec{Label: "Disconnect", Kind: m3.Tonal, Size: m3.Medium56, Key: "lastfm-disconnect"}).Clicked() {
			a.disconnectLastFM()
		}
	})
	if s.reconnect {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, m3.IconError).FontSize(24).TextColor(sc.Error)
			settingLabel(c, "Connect again", "Last.fm no longer accepts this connection. Plays wait on this device until you reconnect.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Reconnect", Kind: m3.Filled, Size: m3.Medium56, Key: "lastfm-reconnect"}).Clicked() {
				a.connectLastFM()
			}
		})
	}
	ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
		settingLabel(c, "Scrobble plays", "Turn off to pause scrobbling without disconnecting.").Grow(1)
		on := !a.settings.LastFMPaused
		if m3.Switch(c, &on, "Scrobble to Last.fm").Changed() {
			a.setLastFMPaused(!on)
		}
	})
}

// connectLastFM starts the authorization: a token is requested, Last.fm's
// page for it is opened in the browser, and the token is exchanged for a
// session as soon as the user approves it there. The user's password is
// never asked for.
func (a *app) connectLastFM() {
	s := &a.scrobble
	if !s.client.Configured() || s.connect.busy() {
		return
	}
	s.connectErr = ""
	client := s.client
	startTask(a, &s.connect, scrobbleCallTimeout, func(ctx context.Context) (string, error) {
		return client.Token(ctx)
	}, func(token string, err error) {
		if err != nil {
			s.connectErr = "Could not reach Last.fm: " + firstLine(err.Error())
			return
		}
		if err := a.openURL(client.AuthURL(token)); err != nil {
			s.connectErr = "Could not open the browser: " + firstLine(err.Error())
			return
		}
		a.awaitApproval(client, token)
	})
}

// awaitApproval exchanges the token for a session until the user has approved
// it, or the time is up.
func (a *app) awaitApproval(client *lastfm.Client, token string) {
	s := &a.scrobble
	sleep := a.sleep
	startTask(a, &s.connect, connectTimeout, func(ctx context.Context) (lastfm.Session, error) {
		for {
			session, err := client.Session(ctx, token)
			if err == nil || (!lastfm.NotApproved(err) && !lastfm.Transient(err)) {
				return session, err
			}
			if err := sleep(ctx, approvalPoll); err != nil {
				return lastfm.Session{}, err
			}
		}
	}, func(session lastfm.Session, err error) {
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			s.connectErr = "Last.fm was not approved in time. Try connecting again."
		case err != nil:
			s.connectErr = "Last.fm did not connect: " + firstLine(err.Error())
		default:
			a.finishConnect(session)
		}
	})
}

// finishConnect keeps the session the user approved: the key in the store
// that only the user can read, the account's name in the settings.
func (a *app) finishConnect(session lastfm.Session) {
	s := &a.scrobble
	store := s.store
	spawn(a, func() error {
		if store.Path == "" {
			return os.ErrNotExist
		}
		return store.Save(context.Background(), session.Key)
	}, func(err error) {
		if err != nil {
			log.Printf("last.fm: saving the session: %v", err)
			s.connectErr = "Meiro could not keep the Last.fm session safely, so it did not connect."
			return
		}
		if a.settings.LastFMUser != session.Name {
			s.outbox.Clear() // plays that were for another account
		}
		s.session, s.reconnect, s.connectErr = session.Key, false, ""
		a.settings.LastFMUser, a.settings.LastFMPaused = session.Name, false
		a.saveSettings()
		a.notice = "Connected to Last.fm as " + session.Name
		a.followCurrent()
		a.flushScrobbles()
	})
}

// cancelLastFMConnect gives up waiting for the user's approval.
func (a *app) cancelLastFMConnect() {
	a.scrobble.connect.stop()
	a.scrobble.connectErr = ""
}

// disconnectLastFM forgets the account: its session key leaves the disk, the
// plays that were waiting for it are dropped, and nothing more is sent.
func (a *app) disconnectLastFM() {
	s := &a.scrobble
	s.connect.stop()
	s.load.stop()
	s.flush.stop()
	s.listen = nil
	s.session, s.reconnect, s.connectErr = "", false, ""
	a.settings.LastFMUser, a.settings.LastFMPaused = "", false
	s.outbox.Clear()
	a.saveSettings()
	store := s.store
	spawn(a, func() error {
		if store.Path == "" {
			return nil
		}
		return store.Delete(context.Background())
	}, func(err error) {
		if err != nil {
			log.Printf("last.fm: removing the saved session: %v", err)
			a.notice = "Disconnected, but Meiro could not delete the saved Last.fm session"
		}
	})
}

// setLastFMPaused stops or resumes scrobbling without disconnecting. The
// play in progress is left out either way: it was not followed from its start.
func (a *app) setLastFMPaused(paused bool) {
	if a.settings.LastFMUser == "" || a.settings.LastFMPaused == paused {
		return
	}
	a.settings.LastFMPaused = paused
	a.scrobble.listen = nil
	a.saveSettings()
	if !paused {
		a.followCurrent()
		a.flushScrobbles()
	}
}
