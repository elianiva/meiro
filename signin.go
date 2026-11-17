package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

type signInState struct {
	open         bool
	err          string
	attempt      uint64
	cookie       string
	browsers     bool
	importFailed string
	try          task
	inWindow     bool
	hostAccess   bool
	advanced     bool
	accounts     []youtube.AccountChannel
	imported     string
	source       string
}

func (s *signInState) reset() {
	s.open, s.err, s.attempt, s.cookie, s.browsers, s.importFailed, s.inWindow, s.hostAccess, s.advanced = false, "", 0, "", false, "", false, false, false
	s.accounts, s.imported, s.source = nil, "", ""
}

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

func (a *app) syncSignInDialog() {
	s := &a.account.signIn
	if !s.open && (s.attempt != 0 || s.try.busy() || s.cookie != "") {
		a.dismissSignIn()
	}
}

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
	a.trySignIn(sessionSource{channel: a.settings.Channel}, signInTimeout, func(context.Context) (string, error) { return cookie, nil })
}

func (a *app) windowSignIn() {
	s := &a.account.signIn
	if s.try.busy() {
		return
	}
	s.inWindow = true
	a.trySignIn(sessionSource{channel: a.settings.Channel}, cookies.SignInTimeout+signInTimeout, a.account.window)
}

func (a *app) importSignIn(browser cookies.Source) {
	s := &a.account.signIn
	if s.try.busy() {
		return
	}
	s.err, s.importFailed, s.hostAccess = "", "", false
	type importedAccounts struct {
		cookie   string
		accounts []youtube.AccountChannel
	}
	startTask(a, &s.try, signInTimeout, func(ctx context.Context) (importedAccounts, error) {
		cookie, err := browser.Read(ctx)
		if err != nil {
			return importedAccounts{}, err
		}
		auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{})
		if err != nil {
			return importedAccounts{}, err
		}
		list, err := a.account.newClient(auth).GetSignInAccounts(ctx)
		if err != nil {
			return importedAccounts{}, err
		}
		if list == nil || len(list.Items) == 0 {
			return importedAccounts{}, errors.New("YouTube did not return any accounts; sign in at music.youtube.com in that browser, then try again")
		}
		return importedAccounts{cookie: cookie, accounts: list.Items}, nil
	}, func(result importedAccounts, err error) {
		if err != nil {
			s.err, s.importFailed = err.Error(), browser.Label
			return
		}
		s.accounts, s.imported, s.source = result.accounts, result.cookie, browser.Label
	})
}

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
		opened, err := acc.openSession(ctx, cookie, from)
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
		a.useSession(opened, from)
	})
}

func (a *app) signInDialog(c *ui.Context) {
	s := &a.account.signIn
	if !s.open {
		return
	}
	if s.accounts != nil {
		a.signInAccountPicker(c)
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
			if s.advanced && m3.Button(c, m3.ButtonSpec{Label: "Sign in", Disabled: busy || strings.TrimSpace(s.cookie) == "", Key: "submit-sign-in"}).Clicked() {
				a.submitSignIn()
			}
		})
	})
}

func (a *app) signInAccountPicker(c *ui.Context) {
	s := &a.account.signIn
	busy := s.try.busy()
	sc := m3.Of(c).Scheme
	m3.Dialog(c, &s.open, 520, func() {
		m3.EmphasizedText(c, m3.HeadlineSmall, "Choose your YouTube account")
		m3.Text(c, m3.BodyMedium, "Choose the account or channel to use from "+s.source+". Meiro remembers your choice.").TextColor(sc.OnSurfaceVariant)
		for i, account := range s.accounts {
			row := ui.ButtonBase(c.Key("sign-in-account-" + strconv.Itoa(i)))
			row.Height(72).PaddingX(16).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).
				Cursor(ui.CursorPointer).Label(account.Name).Disabled(busy || account.Disabled).
				Background(m3.StateFill(sc.SurfaceContainerHigh, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
			row.Children(func() {
				a.avatar(c, account.Name, account.Thumbnail, 96, 40)
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					m3.Text(c, m3.TitleMedium, account.Name).SingleLine()
					switch {
					case account.Email != "":
						m3.Text(c, m3.BodySmall, account.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
					case account.Handle != "":
						m3.Text(c, m3.BodySmall, account.Handle).SingleLine().TextColor(sc.OnSurfaceVariant)
					case account.Byline != "":
						m3.Text(c, m3.BodySmall, account.Byline).SingleLine().TextColor(sc.OnSurfaceVariant)
					}
				})
			})
			if row.Clicked() {
				cookie := s.imported
				a.trySignIn(sessionSource{channel: account.ChannelID, accountIndex: account.AccountIndex, browser: s.source, email: account.Email},
					signInTimeout, func(context.Context) (string, error) { return cookie, nil })
			}
		}
		if s.err != "" {
			m3.Text(c, m3.BodyMedium, s.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Back", Kind: m3.TextOnly, Disabled: busy, Key: "back-sign-in"}).Clicked() {
				s.accounts, s.imported, s.source, s.err = nil, "", "", ""
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.dismissSignIn()
			}
		})
	})
}

const musicURL = "https://music.youtube.com"
