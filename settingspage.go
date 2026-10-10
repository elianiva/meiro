package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// swatches are the seed colours the settings offer.
var swatches = []struct{ name, hex string }{
	{"Violet", "#6750a4"},
	{"Indigo", "#4c5ed6"},
	{"Blue", "#1b6ef3"},
	{"Sky", "#0288d1"},
	{"Teal", "#00897b"},
	{"Green", "#2e8b3a"},
	{"Lime", "#7a9a00"},
	{"Amber", "#d99a00"},
	{"Orange", "#e0620d"},
	{"Red", "#d6323a"},
	{"Rose", "#d81b78"},
	{"Orchid", "#a73fc0"},
}

// settingsPage is where the user chooses the colours of the app and manages
// the account. Every control here restyles the whole window as it is used.
func (a *app) settingsPage(c *ui.Context) {
	ui.Scroll(c).Grow(1).Padding(0, pageGutter, a.clearance()).Children(func() {
		ui.Column(c).MaxWidth(780).Gap(16).Children(func() {
			a.appearanceCard(c)
			a.playbackCacheCard(c)
			a.accountCard(c)
			a.aboutCard(c)
		})
	})
}

// settingsCard is a tonal card that holds one group of settings.
func (a *app) settingsCard(c *ui.Context, title string, icon *ui.SVG, build func()) {
	sc := m3.Of(c).Scheme
	ui.Column(c).Padding(24).Gap(22).Radius(m3.ExtraLarge).Background(sc.SurfaceContainerLow).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon).FontSize(24).TextColor(sc.Primary)
			m3.EmphasizedText(c, m3.TitleLarge, title)
		})
		build()
	})
}

// settingLabel names a control, with what it does under it.
func settingLabel(c *ui.Context, title, body string) ui.Element {
	sc := m3.Of(c).Scheme
	return ui.Column(c).Gap(2).MinWidth(0).Children(func() {
		m3.EmphasizedText(c, m3.TitleMedium, title)
		if body != "" {
			m3.Text(c, m3.BodyMedium, body).TextColor(sc.OnSurfaceVariant)
		}
	})
}

func (a *app) appearanceCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	th := m3.Of(c)
	a.settingsCard(c, "Appearance", m3.IconPalette, func() {
		// Light, dark, or whatever the desktop is.
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Theme", "Follow the desktop, or stay light or dark.").Grow(1)
			mode := int(th.Mode)
			if m3.ButtonGroup(c, "mode", &mode, []string{"System", "Light", "Dark"}, nil) {
				a.settings.setMode(m3.Modes()[mode])
				a.saveSettings()
			}
		})

		// The seed: twelve colours, and any hue between.
		ui.Column(c).Gap(14).Children(func() {
			settingLabel(c, "Colour", "Every colour in the app grows from this one.")
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for _, swatch := range swatches {
					if a.swatch(c, swatch.name, swatch.hex) {
						a.settings.Seed, a.settings.Dynamic = swatch.hex, false
						a.saveSettings()
					}
				}
			})
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				m3.Text(c, m3.LabelLarge, "Hue").TextColor(sc.OnSurfaceVariant).Width(36).Shrink(0)
				hue, _ := m3.HueOf(th.Seed)
				value := hue
				slider := m3.Slider(c, &value, 0, 360, m3.SliderSpec{Label: "Hue", Thickness: 16, Hue: true, Key: "hue"})
				if slider.Changed() {
					a.settings.Seed, a.settings.Dynamic = seedHex(m3.FromHue(value)), false
					a.saveSettings()
				}
			})
		})

		// How the seed is spent.
		ui.Column(c).Gap(14).Children(func() {
			settingLabel(c, "Palette", "How boldly the colour is used.")
			ui.Row(c).Gap(12).Wrap().Children(func() {
				for _, style := range m3.Styles() {
					if a.styleTile(c, style) {
						a.settings.Style = int(style)
						a.saveSettings()
					}
				}
			})
		})

		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Colour from artwork", "Take the colour from the cover of the song that is playing.").Grow(1)
			on := a.settings.Dynamic
			if m3.Switch(c, &on, "Colour from artwork").Changed() {
				a.settings.Dynamic = on
				a.saveSettings()
			}
		})
	})
}

// playbackCacheCard controls the number of recently played songs kept on
// disk for replay without another network stream.
func (a *app) playbackCacheCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Playback cache", m3.IconMusicNote, func() {
		ui.Column(c).Gap(12).Children(func() {
			settingLabel(c, "Keep recent songs", "Enter any non-negative whole number and press Enter to apply. Set 0 to turn the cache off. Older songs are removed first; cached files are MP3s stored in a private Meiro cache folder.")
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				input := ui.TextInputBase(c.Key("audio-cache-limit"), &a.cacheSongsText).
					Width(128).Height(48).Shrink(0).Padding(12).Radius(m3.Medium).Background(sc.SurfaceContainerHigh).
					TextColor(sc.OnSurface).FontSize(16).Label("Songs to cache").Placeholder("10")
				m3.Text(c, m3.BodyMedium, "songs").Width(48).Shrink(0).TextColor(sc.OnSurfaceVariant)
				if input.Submitted() {
					if limit, err := strconv.Atoi(strings.TrimSpace(a.cacheSongsText)); err == nil && limit >= 0 {
						a.setAudioCacheLimit(limit)
						a.cacheLimitError = ""
					} else {
						a.cacheLimitError = "Enter a whole number of 0 or more."
					}
				}
			})
			if a.cacheLimitError != "" {
				m3.Text(c, m3.BodyMedium, a.cacheLimitError).TextColor(sc.Error)
			}
			settingLabel(c, "Cache folder", "Meiro creates a private ‘Meiro Audio Cache’ folder inside this location. Changing folders leaves the old cache in place.")
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				location := a.cacheDirectoryText
				if location == "" {
					location = "Default app cache folder"
				}
				m3.Text(c, m3.BodyMedium, location).TextColor(sc.OnSurfaceVariant).MinWidth(0).Grow(1).SingleLine()
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFolder, Label: "Choose cache folder", Kind: m3.OutlinedIcon, Size: m3.Small40, Key: "audio-cache-folder"}).Clicked() {
					a.chooseAudioCacheDirectory()
				}
			})
			if a.cacheDirectoryError != "" {
				m3.Text(c, m3.BodyMedium, a.cacheDirectoryError).TextColor(sc.Error)
			}
		})
	})
}

// swatch is one seed colour, drawn as the primary colour it would give. The
// chosen one squares off and shows a check.
func (a *app) swatch(c *ui.Context, name, hex string) bool {
	th := m3.Of(c)
	sc := th.Scheme
	seed, _ := parseSeed(hex)
	fill, on := seed, ui.RGB(255, 255, 255)
	if m3.Contrast(fill, on) < 3 {
		on = ui.RGB(24, 24, 27)
	}
	selected := !a.settings.Dynamic && hex == a.settings.Seed
	b := ui.ButtonBase(c.Key("swatch-" + hex))
	rest := float32(28)
	if selected {
		rest = 16
	}
	radius := m3.Animate(b, "r", rest, m3.SpatialFast)
	if b.Pressed() {
		radius = 12
	}
	b.Size(56, 56).Radius(max(radius, 0)).Background(fill).Center().Cursor(ui.CursorPointer).Label(name).Tooltip(name)
	if b.Hovered() && !selected {
		b.Border(3, sc.OnSurface.Alpha(0.16))
	}
	if selected {
		b.Border(3, sc.Surface)
		m3.Elevation(c, b, 1)
	}
	b.Children(func() {
		if selected {
			ui.Icon(c, m3.IconCheck).FontSize(28).TextColor(on)
		}
	})
	return b.Clicked()
}

// stylePreview returns the scheme a palette style would give the seed in a
// light or dark appearance, reusing what it resolved until either changes.
func (a *app) stylePreview(seed ui.Color, dark bool, style m3.Style) m3.Scheme {
	if a.previews == nil || seed != a.previewSeed || dark != a.previewDark {
		a.previews, a.previewSeed, a.previewDark = make(map[m3.Style]m3.Scheme, len(m3.Styles())), seed, dark
	}
	if preview, ok := a.previews[style]; ok {
		return preview
	}
	preview := m3.NewScheme(m3.NewPalettes(seed, style), dark)
	a.previews[style] = preview
	return preview
}

// styleTile previews a palette style in the colours the seed would give it.
func (a *app) styleTile(c *ui.Context, style m3.Style) bool {
	th := m3.Of(c)
	sc := th.Scheme
	preview := a.stylePreview(th.Seed, th.Dark, style)
	selected := th.Style == style
	b := ui.ButtonBase(c.Key("style-" + style.String()))
	b.Column().AlignItems(ui.Stretch).Width(138).Padding(14).Gap(10).Radius(m3.ExtraLarge - 4).Background(preview.PrimaryContainer).Cursor(ui.CursorPointer).
		Label(style.String())
	if selected {
		b.Border(3, sc.Primary)
	} else if b.Hovered() {
		b.Border(3, sc.OnSurface.Alpha(0.16))
	}
	b.Children(func() {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
			for _, dot := range []ui.Color{preview.Primary, preview.Secondary, preview.Tertiary} {
				ui.Box(c).Size(22, 22).Radius(m3.Full).Background(dot)
			}
			ui.Spacer(c)
			if selected {
				ui.Icon(c, m3.IconCheck).FontSize(20).TextColor(preview.OnPrimaryContainer)
			}
		})
		ui.Column(c).Gap(2).Children(func() {
			m3.EmphasizedText(c, m3.TitleSmall, style.String()).TextColor(preview.OnPrimaryContainer)
			m3.Text(c, m3.BodySmall, style.Blurb()).TextColor(preview.OnPrimaryContainer.Alpha(0.8)).MaxLines(2)
		})
	})
	return b.Clicked()
}

func (a *app) accountCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Account", m3.IconPerson, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			if a.signedIn {
				name := a.account.Name
				if name == "" {
					name = "Signed in"
				}
				m3.Avatar(c, name, a.thumbs.bitmap(a.account.Thumbnail, 128), 56)
				ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
					m3.EmphasizedText(c, m3.TitleMedium, name).SingleLine()
					if a.account.Email != "" {
						m3.Text(c, m3.BodyMedium, a.account.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
					}
				})
				if m3.Button(c, m3.ButtonSpec{Label: "Sign out", Icon: m3.IconLogout, Kind: m3.Tonal, Size: m3.Medium56, Key: "sign-out"}).Clicked() {
					a.signOut()
				}
				return
			}
			settingLabel(c, "Not signed in", "Sign in to see your library. Browsing and playing work without it.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Sign in", Icon: m3.IconLogin, Size: m3.Medium56, Key: "sign-in"}).Clicked() {
				a.signInWithGoogle()
			}
		})
		if a.signIn.err != "" && !a.signIn.open {
			m3.Text(c, m3.BodyMedium, a.signIn.err).TextColor(sc.Error).MaxLines(3)
		}
	})
}

func (a *app) aboutCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Keyboard", m3.IconLyrics, func() {
		ui.Column(c).Gap(10).Children(func() {
			for _, k := range [][2]string{
				{"Space", "Play or pause"},
				{"⌘ ←   ⌘ →", "Previous and next track"},
				{"⌘ K", "Search"},
				{"⌘ ,", "Settings"},
			} {
				ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Padding(4, 10).Radius(m3.Small).Background(sc.SurfaceContainerHighest).Width(110).Shrink(0).Children(func() {
						m3.Text(c, m3.LabelLarge, k[0]).TextColor(sc.OnSurface).SingleLine()
					})
					m3.Text(c, m3.BodyMedium, k[1]).TextColor(sc.OnSurfaceVariant)
				})
			}
		})
	})
}
