package main

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// musicURL is where the user signs in, and the page whose requests carry the
// cookie the app needs.
const musicURL = "https://music.youtube.com"

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
		m3.Text(c, m3.BodyMedium, "Meiro imports your browser's reusable YouTube session, which gives it the same account access as that browser. Sign in at music.youtube.com in your browser first, then pick it:").TextColor(sc.OnSurfaceVariant)
		ui.Link(c, musicURL, "Open YouTube Music").TextColor(sc.Primary)
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
		m3.Text(c, m3.BodyMedium, "Meiro saves the session in a file in its data folder that only your user can read. Sign out to remove Meiro's saved copy.").TextColor(sc.OnSurfaceVariant)
		m3.Text(c, m3.BodyMedium, "You can also paste the Cookie request header for music.youtube.com from your browser's developer tools.").TextColor(sc.OnSurfaceVariant)
		box := ui.Column(c.Key("cookie-box")).Padding(12, 16).Radius(m3.Large).Background(sc.SurfaceContainerHighest)
		if box.FocusWithin() {
			box.Border(2, sc.Primary)
		}
		box.Children(func() {
			ui.TextAreaBase(c.Key("cookie"), &s.cookie).Placeholder("Paste the Cookie header").Label("Cookie header").
				Password().Height(96).FontSize(14).TextColor(sc.OnSurface).AutoFocus()
		})
		if s.err != "" {
			m3.Text(c, m3.BodyMedium, s.err).TextColor(sc.Error).MaxLines(4)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
			if busy {
				m3.LoadingIndicator(c, 40, false)
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Cancel", Kind: m3.TextOnly, Key: "cancel-sign-in"}).Clicked() {
				a.dismissSignIn()
			}
			if m3.Button(c, m3.ButtonSpec{Label: "Sign in", Disabled: busy || strings.TrimSpace(s.cookie) == "", Key: "submit-sign-in"}).Clicked() {
				a.submitSignIn()
			}
		})
	})
}

// accountButton is the account's picture in the top bar, which opens a menu
// of the account, the settings and the sign-in.
func (a *app) accountButton(c *ui.Context) {
	sc := m3.Of(c).Scheme
	button := ui.ButtonBase(c.Key("account"))
	button.Size(48, 48).Radius(m3.Full).Center().Cursor(ui.CursorPointer).Label("Account").Tooltip("Account")
	if button.Hovered() || a.menuOpen {
		button.Background(sc.OnSurface.Alpha(m3.StateHover))
	}
	current := a.account.session
	button.Children(func() {
		if current != nil {
			m3.Avatar(c, current.name(), a.thumbs.Bitmap(current.details.Thumbnail, 96), 36)
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
		if current != nil {
			ui.Column(c).Padding(12, 12, 8).Gap(2).Children(func() {
				m3.EmphasizedText(c, m3.TitleSmall, current.name()).SingleLine()
				if current.details.Email != "" {
					m3.Text(c, m3.BodySmall, current.details.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
				}
			})
			ui.Divider(c)
			a.channelItems(c, current)
		}
		if m3.MenuItem(c, "Settings", m3.IconSettings).Clicked() {
			a.menuOpen = false
			a.navigate(routeSettings)
		}
		if current != nil {
			if m3.MenuItem(c, "Sign out", m3.IconLogout).Clicked() {
				a.menuOpen = false
				a.signOut()
			}
		} else if m3.MenuItem(c, "Sign in", m3.IconLogin).Clicked() {
			a.menuOpen = false
			a.openSignIn()
		}
	})
}

// channelItems lists the channels the account can act as. It shows nothing
// when there is only one, and marks the one in use.
func (a *app) channelItems(c *ui.Context, current *session) {
	if len(current.channels) < 2 {
		return
	}
	sc := m3.Of(c).Scheme
	m3.Text(c, m3.LabelMedium, "Channel").Padding(12, 12, 4).TextColor(sc.OnSurfaceVariant)
	for _, channel := range current.channels {
		name := channel.Name
		if name == "" {
			name = channel.Handle
		}
		row := ui.ButtonBase(c.Key("channel-" + channel.ChannelID))
		row.Height(48).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(m3.Medium).Cursor(ui.CursorPointer).Label(name).
			Background(m3.StateFill(ui.Transparent, sc.OnSurface, row.Hovered(), row.Pressed(), row.FocusVisible()))
		selected := channel.ChannelID == current.channel
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
			a.switchChannel(channel.ChannelID)
		}
	}
	ui.Divider(c)
}
