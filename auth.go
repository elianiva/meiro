package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// musicURL is where the user signs in, and the page whose requests carry the
// cookie the app needs.
const musicURL = "https://music.youtube.com"

// signInState is the sign-in the user is going through, if any.
type signInState struct {
	open bool
	err  string
	// generation identifies work started by this dialog.
	generation uint64
	// cookie is the text the user pasted.
	cookie string
	// busy is set while a cookie is being imported or tried.
	busy bool
	// browsers is the menu of browsers to import from.
	browsers bool
	// cancel stops the check of the pasted cookie.
	cancel context.CancelFunc
}

// signInWithGoogle opens the dialog that takes the cookie of the user's
// browser session. YouTube Music does not let another app sign in to it: the
// account's home page and library are only served to a request that carries
// the cookie of a signed-in browser.
func (a *app) signInWithGoogle() {
	if a.signIn.open {
		return
	}
	if a.credentialDeletePending {
		a.notice = "Wait for sign-out to finish before signing in again."
		return
	}
	a.signIn = signInState{open: true, generation: a.signInAttempt.Add(1)}
}

// dismissSignIn cancels pending work and drops the pasted credential when the
// dialog closes. A session saved by an attempt that lost this race is removed
// in the background, without touching a newer sign-in.
func (a *app) dismissSignIn() {
	attempt := a.signIn.generation
	if attempt == 0 && a.signIn.cancel == nil && a.signIn.cookie == "" {
		return
	}
	a.signInAttempt.Add(1)
	if a.signIn.cancel != nil {
		a.signIn.cancel()
	}
	a.signIn = signInState{}
	if attempt != 0 && a.store != nil {
		a.run(func() { a.deleteSavedSignInAttempt(attempt) })
	}
}

// deleteSavedSignInAttempt removes a credential only if it still belongs to
// the dismissed attempt. The lock makes this check and removal atomic with
// respect to a newer sign-in saving its own credential.
func (a *app) deleteSavedSignInAttempt(attempt uint64) {
	a.credentialMu.Lock()
	defer a.credentialMu.Unlock()
	if a.store == nil || a.savedSignInAttempt != attempt {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.store.Delete(ctx); err != nil {
		a.update(func() {
			a.notice = "The cancelled sign-in could not be removed from storage: " + err.Error()
		})
		return
	}
	a.savedSignInAttempt = 0
}

// saveSignIn persists a validated cookie only while its dialog is still
// active. If dismissal races the write, it removes that attempt's credential
// before allowing a newer write to proceed.
func (a *app) saveSignIn(ctx context.Context, cookie string, attempt uint64) error {
	if a.store == nil {
		return nil
	}
	a.credentialMu.Lock()
	defer a.credentialMu.Unlock()
	if ctx.Err() != nil || a.signInAttempt.Load() != attempt {
		return context.Canceled
	}
	if err := a.store.Save(ctx, cookie); err != nil {
		return err
	}
	a.savedSignInAttempt = attempt
	if ctx.Err() == nil && a.signInAttempt.Load() == attempt {
		return nil
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.store.Delete(cleanupCtx); err != nil {
		return errors.Join(context.Canceled, err)
	}
	a.savedSignInAttempt = 0
	return context.Canceled
}

// cookieNamePattern is where the value of a Cookie header starts, whether the
// text is the header, or a cURL command with it as an argument.
var cookieNamePattern = regexp.MustCompile(`(?i)cookie:`)

// cookieHeader picks the cookie out of what the user pasted: the value of the
// header alone, the header with its name, or a whole line copied as a cURL
// command.
func cookieHeader(text string) string {
	text = strings.TrimSpace(text)
	// The name is matched on the text itself: lowercasing a copy can change
	// its length, and the offsets would no longer fit.
	if at := cookieNamePattern.FindStringIndex(text); at != nil {
		text = text[at[1]:]
	}
	text = strings.TrimSpace(text)
	if i := strings.IndexAny(text, "'\"\n"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// submitSignIn tries the pasted cookie.
func (a *app) submitSignIn() {
	if a.signIn.busy {
		return
	}
	attempt := a.signIn.generation
	cookie := cookieHeader(a.signIn.cookie)
	if _, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{}); err != nil {
		a.signIn.err = "That is not the cookie of a signed-in session: it has no SAPISID. Copy the whole value of the Cookie header."
		return
	}
	a.signIn.busy, a.signIn.err = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	a.signIn.cancel = cancel
	channel := a.settings.Channel
	a.run(func() {
		defer cancel()
		a.finishSignIn(ctx, cookie, attempt, channel)
	})
}

// importSignIn takes the session of a browser the user is signed in with.
func (a *app) importSignIn(source importSource) {
	if a.signIn.busy {
		return
	}
	attempt := a.signIn.generation
	a.signIn.busy, a.signIn.err = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	a.signIn.cancel = cancel
	channel := a.settings.Channel
	a.run(func() {
		defer cancel()
		cookie, err := source.read(ctx)
		if err != nil {
			a.update(func() {
				if a.signIn.open && a.signIn.generation == attempt && a.signInAttempt.Load() == attempt {
					a.signIn.busy, a.signIn.err = false, err.Error()
				}
			})
			return
		}
		a.finishSignIn(ctx, cookie, attempt, channel)
	})
}

// accountClient builds the authenticated client for a channel selection. An
// empty channel acts as the account's default one.
func (a *app) accountClient(cookie, channel string) (*youtube.Client, error) {
	auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{OnBehalfOfUser: channel})
	if err != nil {
		return nil, err
	}
	return a.newClient(auth), nil
}

// openAccount signs in with cookie and returns the client, the account, and
// the channel in use. It tries the wanted channel first, and falls back to the
// account's default when that channel is gone.
func (a *app) openAccount(ctx context.Context, cookie, channel string) (*youtube.Client, *youtube.AccountDetails, string, error) {
	client, err := a.accountClient(cookie, channel)
	if err != nil {
		return nil, nil, channel, err
	}
	details, err := client.GetAccountDetails(ctx)
	if err != nil && channel != "" {
		channel = ""
		if client, err = a.accountClient(cookie, ""); err != nil {
			return nil, nil, channel, err
		}
		details, err = client.GetAccountDetails(ctx)
	}
	if err != nil {
		return nil, nil, channel, err
	}
	if err := validateAccountDetails(details); err != nil {
		return nil, nil, channel, err
	}
	return client, details, channel, nil
}

// accountChannels reads the channels an authenticated client can act as, and
// gives none when the request fails.
func accountChannels(ctx context.Context, client *youtube.Client) []youtube.AccountChannel {
	list, err := client.GetAccounts(ctx)
	if err != nil || list == nil {
		return nil
	}
	return list.Items
}

// loadAccountChannels fills the channel switcher after the account is in use,
// so a slow channel list does not hold up the sign-in.
func (a *app) loadAccountChannels(client *youtube.Client) {
	if client == nil {
		return
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		accounts := accountChannels(ctx, client)
		if len(accounts) == 0 {
			return
		}
		a.update(func() {
			if a.authed == client {
				a.accounts = accounts
			}
		})
	})
}

// finishSignIn asks YouTube whom the cookie signs in and, when it takes it,
// keeps it as the sign-in. It runs off the main thread.
func (a *app) finishSignIn(ctx context.Context, cookie string, attempt uint64, channel string) {
	client, details, channel, err := a.openAccount(ctx, cookie, channel)
	if err == nil {
		err = a.saveSignIn(ctx, cookie, attempt)
	}
	a.update(func() {
		if !a.signIn.open || a.signIn.generation != attempt || a.signInAttempt.Load() != attempt {
			return
		}
		a.signIn.busy = false
		if err != nil {
			a.signIn.err = err.Error()
			return
		}
		a.accountGen++
		a.authed, a.signedIn, a.account = client, true, *details
		a.accounts = nil
		if a.settings.Channel != channel {
			a.settings.Channel = channel
			a.saveSettings()
		}
		a.ytDlpCookie = cookie
		a.signIn = signInState{}
		a.onSignedIn()
		a.loadAccountChannels(client)
	})
}

// switchAccount makes the session act as another of the account's channels and
// reloads the page, so it shows that channel's account. It runs off the main
// thread because it asks YouTube about the chosen channel.
func (a *app) switchAccount(channel string) {
	if channel == a.settings.Channel {
		return
	}
	cookie := a.ytDlpCookie
	if cookie == "" {
		return
	}
	a.settings.Channel = channel
	a.saveSettings()
	gen := a.accountGen
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		auth, err := youtube.NewCookieAuth(cookie, youtube.CookieOptions{OnBehalfOfUser: channel})
		if err != nil {
			a.update(func() { a.notice = "Could not switch channel: " + err.Error() })
			return
		}
		client := a.newClient(auth)
		details, err := client.GetAccountDetails(ctx)
		if err == nil {
			err = validateAccountDetails(details)
		}
		if err != nil {
			a.update(func() { a.notice = "Could not switch channel: " + err.Error() })
			return
		}
		a.update(func() {
			if gen != a.accountGen {
				return // the user signed out while the channel was being opened
			}
			a.authed, a.account = client, *details
			a.onSignedIn()
			a.loadAccountChannels(client)
		})
	})
}

func validateAccountDetails(details *youtube.AccountDetails) error {
	if details == nil || details.Name == "" && details.ChannelID == "" {
		return errors.New("YouTube did not recognise the cookie as a signed-in session; it may have expired")
	}
	return nil
}

// restoreAccount loads the cookie saved by an earlier run and the account it
// belongs to.
func (a *app) restoreAccount() {
	if a.store == nil {
		return
	}
	gen := a.accountGen
	saved := a.settings.Channel
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		a.credentialMu.Lock()
		cookie, err := a.store.Load(ctx)
		a.credentialMu.Unlock()
		if err != nil {
			if !errors.Is(err, errNotSignedIn) {
				a.reportRestoreError(gen, err)
			}
			return // not signed in
		}
		client, details, channel, err := a.openAccount(ctx, cookie, saved)
		if err != nil {
			a.reportRestoreError(gen, err)
			return
		}
		a.update(func() {
			if gen != a.accountGen {
				return // the user signed in or out while the cookie was being read
			}
			a.authed, a.signedIn, a.account = client, true, *details
			if a.settings.Channel != channel {
				a.settings.Channel = channel
				a.saveSettings()
			}
			a.ytDlpCookie = cookie
			a.signIn.err = ""
			a.onSignedIn()
			a.loadAccountChannels(client)
		})
	})
}

func (a *app) reportRestoreError(gen int, err error) {
	a.update(func() {
		if gen != a.accountGen {
			return // the user signed in or out while the cookie was being read
		}
		a.authed, a.signedIn, a.account = nil, false, youtube.AccountDetails{}
		a.accounts = nil
		a.ytDlpCookie = ""
		a.signIn.err = "Could not restore your YouTube Music session: " + err.Error()
		a.notice = a.signIn.err
	})
}

// signOut forgets the account.
func (a *app) signOut() {
	a.cancelSuggestions()
	a.accountGen++
	a.signInAttempt.Add(1)
	if a.signIn.cancel != nil {
		a.signIn.cancel()
	}
	a.signIn = signInState{}
	a.authed, a.signedIn, a.account = nil, false, youtube.AccountDetails{}
	a.accounts = nil
	a.ytDlpCookie = ""
	a.onSignedIn()
	if a.store == nil {
		return
	}
	store := a.store
	a.credentialDeletePending = true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		a.credentialMu.Lock()
		err := store.Delete(ctx)
		if err == nil {
			a.savedSignInAttempt = 0
		}
		a.credentialMu.Unlock()
		a.update(func() {
			a.credentialDeletePending = false
			if err != nil {
				a.notice = "Signed out, but the saved session could not be removed: " + err.Error()
			}
		})
	})
}

// onSignedIn reloads the page when it is one the account changes. The home
// page is loaded at launch, before the saved sign-in is back, so it needs the
// reload as much as the library does.
func (a *app) onSignedIn() {
	a.forgetPages()
	if path := a.router.Path(); path != "/search" && path != "/settings" {
		a.reloadPage()
	}
}

// signInDialog asks for the cookie of the user's browser session.
func (a *app) signInDialog(c *ui.Context) {
	if !a.signIn.open {
		return
	}
	sc := m3.Active().Scheme
	m3.Dialog(c, &a.signIn.open, 520, func() {
		ui.Box(c).Size(56, 56).Radius(m3.Large).Background(sc.PrimaryContainer).Center().Children(func() {
			ui.Icon(c, m3.IconLogin).FontSize(28).TextColor(sc.OnPrimaryContainer)
		})
		m3.EmphasizedText(c, m3.HeadlineSmall, "Sign in to YouTube Music")
		m3.Text(c, m3.BodyMedium, "Meiro imports your browser's reusable YouTube session, which gives it the same account access as that browser. Sign in at music.youtube.com in your browser first, then pick it:").TextColor(sc.OnSurfaceVariant)
		ui.Link(c, musicURL, "Open YouTube Music").TextColor(sc.Primary)
		picker := m3.Button(c, m3.ButtonSpec{Label: "Import from a browser", Icon: m3.IconExpandMore, Kind: m3.Tonal, Size: m3.Medium56, Disabled: a.signIn.busy, Key: "import-from"})
		if picker.Clicked() {
			a.signIn.browsers = !a.signIn.browsers
		}
		m3.Menu(c, picker, &a.signIn.browsers, 240, func() {
			for _, source := range importSources() {
				if m3.MenuItem(c, source.label, nil).Clicked() {
					a.signIn.browsers = false
					a.importSignIn(source)
				}
			}
		})
		m3.Text(c, m3.BodyMedium, "Meiro saves the session in your system credential store. If your system has no credential store, it uses a file readable only by your user. A locked or failing credential store will not trigger that fallback. Sign out to remove Meiro's saved copy.").TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyMedium, "You can also paste the Cookie request header for music.youtube.com from your browser's developer tools.").TextColor(sc.OnSurfaceVariant)
		box := ui.Column(c.Key("cookie-box")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
		if box.FocusWithin() {
			box.Border(2, sc.Primary)
		}
		box.Children(func() {
			ui.TextAreaBase(c.Key("cookie"), &a.signIn.cookie).Placeholder("Paste the Cookie header").Label("Cookie header").
				Password().Height(96).FontSize(14).TextColor(sc.OnSurface).AutoFocus()
		})
		if a.signIn.err != "" {
			m3.Text(c, m3.BodyMedium, a.signIn.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if a.signIn.busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.dismissSignIn()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Sign in", Disabled: a.signIn.busy || strings.TrimSpace(a.signIn.cookie) == "", Key: "submit-sign-in"}).Clicked() {
				a.submitSignIn()
			}
		})
	})
}

// accountButton is the account's picture in the top bar, which opens a menu
// of the account, the settings and the sign-in.
func (a *app) accountButton(c *ui.Context) {
	sc := m3.Active().Scheme
	button := ui.ButtonBase(c.Key("account"))
	button.Size(48, 48).Radius(m3.Full).Center().Cursor(ui.CursorPointer).Label("Account").Tooltip("Account")
	if button.Hovered() || a.menuOpen {
		button.Background(sc.OnSurface.Alpha(m3.StateHover))
	}
	button.Children(func() {
		if a.signedIn {
			name := a.account.Name
			if name == "" {
				name = "Signed in"
			}
			m3.Avatar(c, name, a.thumbs.bitmap(a.account.Thumbnail, 96), 36)
			return
		}
		ui.Box(c).Size(36, 36).Radius(m3.Full).Background(sc.SurfaceContainerHighest).Center().Children(func() {
			ui.Icon(c, m3.IconPerson).FontSize(22).TextColor(sc.OnSurfaceVariant)
		})
	})
	if button.Clicked() {
		a.menuOpen = !a.menuOpen
	}
	m3.Menu(c, button, &a.menuOpen, 280, func() {
		if a.signedIn {
			ui.Column(c).Padding(12, 12, 8).Gap(2).Children(func() {
				name := a.account.Name
				if name == "" {
					name = "Signed in"
				}
				m3.EmphasizedText(c, m3.TitleSmall, name).SingleLine()
				if a.account.Email != "" {
					m3.Text(c, m3.BodySmall, a.account.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
				}
			})
			ui.Divider(c)
			a.channelItems(c)
		}
		if m3.MenuItem(c, "Settings", m3.IconSettings).Clicked() {
			a.menuOpen = false
			a.router.Push("/settings")
		}
		if a.signedIn {
			if m3.MenuItem(c, "Sign out", m3.IconLogout).Clicked() {
				a.menuOpen = false
				a.signOut()
			}
		} else if m3.MenuItem(c, "Sign in", m3.IconLogin).Clicked() {
			a.menuOpen = false
			a.signInWithGoogle()
		}
	})
}

// channelItems lists the channels the account can act as. It shows nothing
// when there is only one, and marks the one in use.
func (a *app) channelItems(c *ui.Context) {
	if len(a.accounts) < 2 {
		return
	}
	sc := m3.Active().Scheme
	m3.Text(c, m3.LabelMedium, "Channel").Padding(12, 12, 4).TextColor(sc.OnSurfaceVariant)
	for _, channel := range a.accounts {
		name := channel.Name
		if name == "" {
			name = channel.Handle
		}
		row := ui.ButtonBase(c.Key("channel-" + channel.ChannelID))
		row.Height(48).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).Cursor(ui.CursorPointer).Label(name).
			Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
		selected := channel.ChannelID == a.settings.Channel
		row.Children(func() {
			if selected {
				ui.Icon(c, m3.IconCheck).FontSize(22).TextColor(sc.Primary)
			} else {
				ui.Box(c).Size(22, 22)
			}
			m3.Text(c, m3.BodyLarge, name).Grow(1).SingleLine().TextColor(sc.OnSurface)
		})
		if row.Clicked() && !selected {
			a.menuOpen = false
			a.switchAccount(channel.ChannelID)
		}
	}
	ui.Divider(c)
}
