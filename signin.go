package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// signInState is the sign-in dialog, while it is open.
type signInState struct {
	open bool
	err  string
	// attempt is the credential attempt this dialog owns.
	attempt uint64
	// cookie is the text the user pasted.
	cookie string
	// browsers is the menu of browsers to import from.
	browsers bool
	// importFailed is the browser a session could not be imported from, so the
	// dialog points at pasting the Cookie header instead; "" otherwise.
	importFailed string
	// try checks a pasted, imported or window session; it is busy while it
	// does.
	try task
	// inWindow is set while the browser window waits for the user.
	inWindow bool
	// hostAccess is set when the Flatpak could not open a browser window, so
	// the dialog shows the command that lets it.
	hostAccess bool
	// advanced shows the ways to sign in that copy a session from a browser
	// the user keeps using: importing it, and pasting its Cookie header.
	advanced bool
}

// reset empties the dialog. Its task is the caller's to stop.
func (s *signInState) reset() {
	s.open, s.err, s.attempt, s.cookie, s.browsers, s.importFailed, s.inWindow, s.hostAccess, s.advanced = false, "", 0, "", false, "", false, false, false
}

// openSignIn opens the dialog that takes the cookie of the user's browser
// session. YouTube Music does not let another app sign in to it: the
// account's home page and library are only served to a request that carries
// the cookie of a signed-in browser.
func (a *app) openSignIn() {
	acc := &a.account
	if acc.signIn.open {
		return
	}
	if acc.creds.deletePending {
		a.notice = "Wait for sign-out to finish before signing in again."
		return
	}
	acc.signIn.reset()
	acc.signIn.open, acc.signIn.attempt = true, acc.creds.attempt.Add(1)
}

// syncSignInDialog notices a dialog the user closed, by its scrim or the
// Escape key, and dismisses what it left behind.
func (a *app) syncSignInDialog() {
	s := &a.account.signIn
	if !s.open && (s.attempt != 0 || s.try.busy() || s.cookie != "") {
		a.dismissSignIn()
	}
}

// dismissSignIn closes the dialog, stops its check and drops the pasted
// cookie. A session saved by a check that lost this race is removed in the
// background, without touching a newer sign-in.
func (a *app) dismissSignIn() {
	acc := &a.account
	attempt := acc.signIn.attempt
	acc.creds.attempt.Add(1)
	acc.signIn.try.stop()
	acc.signIn.reset()
	if attempt == 0 || acc.creds.store == nil {
		return
	}
	spawn(a, func() error { return acc.creds.deleteForDialog(attempt) }, func(err error) {
		if err != nil {
			a.notice = "The cancelled sign-in could not be removed from storage: " + err.Error()
		}
	})
}

// submitSignIn tries the pasted cookie.
func (a *app) submitSignIn() {
	s := &a.account.signIn
	if s.try.busy() {
		return
	}
	cookie := cookies.Header(s.cookie)
	if _, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{}); err != nil {
		s.err = "That is not the cookie of a signed-in session: it has no SAPISID. Copy the whole value of the Cookie header."
		return
	}
	// A pasted session came from no browser, so it names none.
	a.trySignIn(sessionSource{channel: a.settings.Channel}, signInTimeout, func(context.Context) (string, error) { return cookie, nil })
}

// windowSignIn has the user sign in in a browser window that Meiro opens, and
// takes the session from it. The session names no browser: the window is gone
// once it is read, so there is nowhere to read it from again, and nothing
// else renews its cookies.
func (a *app) windowSignIn() {
	s := &a.account.signIn
	if s.try.busy() {
		return
	}
	s.inWindow = true
	a.trySignIn(sessionSource{channel: a.settings.Channel}, cookies.SignInTimeout+signInTimeout, a.account.window)
}

// importSignIn takes the session of a browser the user is signed in with.
func (a *app) importSignIn(browser cookies.Source) {
	if a.account.signIn.try.busy() {
		return
	}
	a.trySignIn(sessionSource{channel: a.settings.Channel, browser: browser.Label}, signInTimeout, browser.Read)
}

// trySignIn reads a cookie, asks YouTube whom it signs in, and keeps it as
// the sign-in when YouTube takes it. timeout bounds the read and the check.
func (a *app) trySignIn(from sessionSource, timeout time.Duration, read func(context.Context) (string, error)) {
	acc := &a.account
	s := &acc.signIn
	s.err, s.importFailed, s.hostAccess = "", "", false
	attempt := s.attempt
	startTask(a, &s.try, timeout, func(ctx context.Context) (*session, error) {
		cookie, err := read(ctx)
		if err != nil {
			return nil, err
		}
		opened, err := acc.openSession(ctx, cookie, from.channel)
		if err != nil {
			return nil, err
		}
		return opened, acc.creds.saveForDialog(ctx, cookie, attempt)
	}, func(opened *session, err error) {
		s.inWindow = false
		if err != nil {
			s.err = err.Error()
			s.importFailed = from.browser
			s.hostAccess = errors.Is(err, cookies.ErrNeedsHostAccess)
			return
		}
		s.reset()
		// The browser the session came from is where it is read again once
		// YouTube retires its cookie. A pasted session names none, which is
		// also what forgets a browser the user has moved on from.
		a.useSession(opened, sessionSource{channel: opened.channel, browser: from.browser})
	})
}

// signInDialog asks for the cookie of the user's browser session.
func (a *app) signInDialog(c *ui.Context) {
	s := &a.account.signIn
	if !s.open {
		return
	}
	busy := s.try.busy()
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &s.open, 520, func() {
		ui.Box(c).Size(56, 56).Radius(m3.Large).Background(sc.PrimaryContainer).Center().Children(func() {
			ui.Icon(c, m3.IconLogin).FontSize(28).TextColor(sc.OnPrimaryContainer)
		})
		m3.EmphasizedText(c, m3.HeadlineSmall, "Sign in to YouTube Music")
		m3.Text(c, m3.BodyMedium, "Meiro opens a separate browser window where you sign in to your Google account. Meiro never sees your password. It keeps the session of that window, which gives it the same account access as a browser, and closes the window. Your own browser is not used.").TextColor(sc.OnSurfaceVariant)
		if m3.Button(c, m3.ButtonSpec{Label: "Sign in with a browser window", Icon: m3.IconLogin, Size: m3.Medium56, Disabled: busy, Key: "sign-in-window"}).Clicked() {
			a.windowSignIn()
		}
		if s.inWindow {
			m3.Text(c, m3.BodyMedium, "Finish signing in in the window that opened. Meiro closes it for you.").TextColor(sc.Primary)
		}
		toggle := m3.Button(c, m3.ButtonSpec{Label: "Advanced", Icon: advancedIcon(s.advanced), Kind: m3.TextOnly, Key: "sign-in-advanced"})
		if toggle.Clicked() {
			s.advanced = !s.advanced
		}
		if s.advanced {
			m3.Text(c, m3.BodyMedium, "Or use a browser you are already signed in with. This copy can stop working when that browser renews its cookies. Sign in at "+musicURL+" there first, then pick it:").TextColor(sc.OnSurfaceVariant)
			picker := m3.Button(c, m3.ButtonSpec{Label: "Import from a browser", Icon: m3.IconExpandMore, Kind: m3.Tonal, Size: m3.Medium56, Disabled: busy, Key: "import-from"})
			if picker.Clicked() {
				s.browsers = !s.browsers
			}
			m3.Menu(c, picker, &s.browsers, 240, func() {
				for _, source := range a.account.sources() {
					if m3.MenuItem(c, source.Label, nil).Clicked() {
						s.browsers = false
						a.importSignIn(source)
					}
				}
			})
			if s.importFailed != "" {
				m3.EmphasizedText(c, m3.TitleSmall, "Importing from "+s.importFailed+" did not work. Paste the Cookie header instead.").TextColor(sc.Primary)
			}
			m3.Text(c, m3.BodyMedium, "To paste it: open music.youtube.com while signed in, open your browser's developer tools (F12) and go to the Network tab, reload, pick a request to music.youtube.com, and copy the value of its Cookie request header.").TextColor(sc.OnSurfaceVariant)
			box := ui.Column(c.Key("cookie-box")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
			if box.FocusWithin() {
				box.Border(2, sc.Primary)
			}
			box.Children(func() {
				ui.TextAreaBase(c.Key("cookie"), &s.cookie).Placeholder("Paste the Cookie header").Label("Cookie header").
					Password().Height(96).FontSize(14).TextColor(sc.OnSurface).AutoFocus()
			})
		}
		m3.Text(c, m3.BodyMedium, "Meiro saves the session in a file in its data folder that only your user can read. Sign out to remove Meiro's saved copy.").TextColor(sc.OnSurfaceVariant)
		if s.hostAccess {
			m3.Text(c, m3.BodyMedium, "To open your browser, Meiro needs permission to start programs outside its Flatpak sandbox. Run this command, then restart Meiro:").TextColor(sc.Error)
			ui.Column(c).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest).Children(func() {
				m3.Text(c, m3.BodyMedium, cookies.HostAccessCommand).TextColor(sc.OnSurface).Selectable()
			})
			if m3.Button(c, m3.ButtonSpec{Label: "Copy command", Icon: m3.IconCopy, Kind: m3.Tonal, Key: "copy-host-access"}).Clicked() {
				c.WriteClipboard(cookies.HostAccessCommand)
				c.Toast("Command copied")
			}
		} else if s.err != "" {
			m3.Text(c, m3.BodyMedium, s.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.dismissSignIn()
			}
			// Only the pasted Cookie header needs a button of its own: the
			// window and an import start as soon as they are picked.
			if s.advanced && m3.Button(c, m3.ButtonSpec{Label: "Sign in", Disabled: busy || strings.TrimSpace(s.cookie) == "", Key: "submit-sign-in"}).Clicked() {
				a.submitSignIn()
			}
		})
	})
}

// musicURL is where the user signs in, and the page whose requests carry the
// cookie the app needs.
const musicURL = "https://music.youtube.com"
