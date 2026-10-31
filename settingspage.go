package main

import (
	"math"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
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

// settingsRow is one row of the settings page: a section heading, or a card
// under it.
type settingsRow struct {
	key     string
	heading string                      // set for a heading
	search  string                      // words the card answers
	card    func(a *app, c *ui.Context) // set for a card
}

type settingsSearchState struct {
	query string
	open  bool
	field ui.Handle
}

// settingsRows are the rows of the settings page, top to bottom. Cards that
// answer the same question sit under one heading.
var settingsRows = []settingsRow{
	{key: "h-account", heading: "Account"},
	{key: "account", search: "sign in profile account session", card: (*app).accountCard},
	{key: "lastfm", search: "last.fm scrobbling music service", card: (*app).lastFMCard},

	{key: "h-look", heading: "Appearance and sound"},
	{key: "appearance", search: "theme appearance system light dark colour color colours colors violet indigo blue sky teal green lime amber orange red rose orchid hue palette tonal spot vibrant expressive neutral monochrome artwork dynamic matugen source", card: (*app).appearanceCard},
	{key: "sound", search: "sound audio normalize loudness logarithmic equaliser volume", card: (*app).soundCard},

	{key: "h-playback", heading: "Playback"},
	{key: "playback", search: "auto skip disliked songs thumbs down", card: (*app).playbackCard},
	{key: "crossfade", search: "crossfade fade transition gap between tracks songs seconds", card: (*app).crossfadeCard},

	{key: "h-system", heading: "System"},
	{key: "desktop", search: "desktop linux notifications discord rich presence activity status tray icon wordmark o mark low key subtle notch macbook lyrics", card: (*app).desktopCard},
	{key: "keyboard", search: "keyboard shortcuts keys", card: (*app).keyboardCard},

	{key: "h-data", heading: "Storage and privacy"},
	{key: "cache", search: "storage cache playback downloads", card: (*app).playbackCacheCard},
	{key: "history", search: "privacy history listening plays", card: (*app).historyCard},

	{key: "h-about", heading: "About and support"},
	{key: "updates", search: "about updates version", card: (*app).updatesCard},
	{key: "tools", search: "support tools", card: (*app).toolsCard},
	{key: "report", search: "support report problem bug", card: (*app).reportCard},
	{key: "logs", search: "logs folder diagnostics", card: (*app).logsCard},
}

func settingsRowsFor(query string) []settingsRow {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return settingsRows
	}
	var filtered []settingsRow
	for start := 0; start < len(settingsRows); {
		heading := settingsRows[start]
		end := start + 1
		for end < len(settingsRows) && settingsRows[end].heading == "" {
			end++
		}
		sectionMatches := strings.Contains(strings.ToLower(heading.heading), query)
		var matchingCards []settingsRow
		for _, row := range settingsRows[start+1 : end] {
			if sectionMatches || strings.Contains(row.search, query) {
				matchingCards = append(matchingCards, row)
			}
		}
		if len(matchingCards) > 0 {
			filtered = append(filtered, heading)
			filtered = append(filtered, matchingCards...)
		}
		start = end
	}
	return filtered
}

func (a *app) settingsSearchBar(c *ui.Context) {
	sc := m3.Of(c).Scheme
	const buttonWidth, maxWidth = float32(40), float32(520)
	control := ui.Row(c.Key("settings-search")).Width(maxWidth).MinWidth(0).Shrink(1)
	target := float32(0)
	if a.settingsSearch.open {
		target = 1
	}
	grown := m3.Animate(control, "open", target, m3.SpatialFast)
	if !a.settingsSearch.open && grown < 0.02 {
		control.MaxWidth(buttonWidth)
		control.Children(func() {
			button := m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconSearch, Label: "Search settings", Kind: m3.StandardIcon,
				Size: m3.Small40, Key: "settings-search-open", Container: sc.SurfaceContainerHigh,
			})
			if button.Clicked() {
				a.settingsSearch.open = true
				a.settingsSearch.field.Focus()
			}
		})
		return
	}
	control.MaxWidth(buttonWidth + (maxWidth-buttonWidth)*min(max(grown, 0), 1))
	control.Children(func() {
		result, _ := m3.SearchBar(c, m3.SearchSpec{
			Value:       &a.settingsSearch.query,
			Placeholder: "Search settings",
			Key:         "settings-search-bar",
			Focus:       &a.settingsSearch.field,
			MaxWidth:    maxWidth,
			Dismiss:     true,
		})
		if result.Changed || result.Cleared {
			a.list.ScrollTo(0, ui.Start)
		}
		if result.Dismissed {
			a.closeSettingsSearch()
		}
	})
}

func (a *app) closeSettingsSearch() {
	a.settingsSearch = settingsSearchState{}
}

// settingsPage is where the user chooses how the app looks, sounds and plays,
// and manages the account, the caches, Last.fm and the shortcuts.
//
// The rows are the rows of a list, so only the ones in view are built, laid
// out and painted: the page is long, and scrolling it rebuilds the frame.
func (a *app) settingsPage(c *ui.Context) {
	rows := settingsRowsFor(a.settingsSearch.query)
	if len(rows) == 0 {
		ui.Column(c).Fill().Center().Children(func() {
			a.message(c, m3.IconSearch, "No settings found", "Try another search.", "", nil)
		})
		return
	}
	a.list.Key = func(i int) any {
		if i >= len(rows) {
			return nil
		}
		return rows[i].key
	}
	ui.List(c, a.list, len(rows), func(i int) {
		row := rows[i]
		if row.card == nil {
			top := float32(16)
			if i == 0 {
				top = 0
			}
			ui.Row(c).FillWidth().Padding(top, pageGutter, 12).Children(func() {
				m3.EmphasizedText(c, m3.TitleMedium, row.heading).TextColor(m3.Of(c).Scheme.Primary)
			})
			return
		}
		ui.Column(c).FillWidth().Padding(0, pageGutter, 16).Children(func() {
			row.card(a, c)
		})
	}).Grow(1).Padding(0, 0, a.clearance()-16)
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
				a.settings.SetMode(m3.Modes()[mode])
				a.saveSettings()
			}
		})

		// Where the colours come from: one chosen here, the cover of the song
		// playing, or the set matugen writes.
		ui.Row(c).Gap(16).AlignItems(ui.Center).Wrap().Children(func() {
			settingLabel(c, "Colour source", "").Grow(1)
			source := 0
			if a.settings.Matugen {
				source = 2
			} else if a.settings.Dynamic {
				source = 1
			}
			if m3.ButtonGroup(c, "colour-source", &source, []string{"Fixed", "Artwork", "Matugen"}, nil) {
				a.settings.Dynamic, a.settings.Matugen = source == 1, source == 2
				a.saveSettings()
			}
		})
		if a.settings.Matugen && a.colors == nil {
			m3.Text(c, m3.BodyMedium, "Waiting for colors.json from matugen.").TextColor(sc.OnSurfaceVariant)
		}

		// The seed: twelve colours, and any hue between.
		if !a.settings.Dynamic && !a.settings.Matugen {
			ui.Column(c).Gap(14).Children(func() {
				settingLabel(c, "Colour", "Every colour in the app grows from this one.")
				ui.Row(c).Gap(8).Wrap().Children(func() {
					for _, swatch := range swatches {
						if a.swatch(c, swatch.name, swatch.hex) {
							a.settings.Seed, a.settings.Dynamic, a.settings.Matugen = swatch.hex, false, false
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
						a.settings.Seed, a.settings.Dynamic, a.settings.Matugen = settings.SeedHex(m3.FromHue(value)), false, false
						a.saveSettings()
					}
				})
			})
		}

		// How the seed is spent.
		if !a.settings.Matugen {
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
		}

		// How large the interface is, at a readable size in the interface
		// that grows with it.
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Zoom", "Make everything larger or smaller.").Grow(1)
			zoom := settings.ClampZoom(a.settings.Zoom)
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				a.hinted(c, func() {
					if m3.IconButton(c, m3.IconButtonSpec{
						Icon: m3.IconRemove, Label: "Zoom out", Kind: m3.StandardIcon, Size: m3.Small40,
						Key: "zoom-out", Disabled: zoom <= settings.MinZoom,
					}).Clicked() {
						a.zoomBy(-1)
					}
				}, keymap.ZoomOut)
				m3.EmphasizedText(c, m3.TitleMedium, strconv.Itoa(zoom)+"%").Width(64).TextAlign(ui.Center).TextColor(sc.OnSurface)
				a.hinted(c, func() {
					if m3.IconButton(c, m3.IconButtonSpec{
						Icon: m3.IconAdd, Label: "Zoom in", Kind: m3.StandardIcon, Size: m3.Small40,
						Key: "zoom-in", Disabled: zoom >= settings.MaxZoom,
					}).Clicked() {
						a.zoomBy(1)
					}
				}, keymap.ZoomIn)
			})
		})
	})
}

// playbackCacheCard controls the number of recently played songs kept on
// disk for replay without another network stream.
func (a *app) playbackCacheCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Playback cache", m3.IconFolder, func() {
		ui.Column(c).Gap(12).Children(func() {
			settingLabel(c, "Keep recent songs", "Enter any non-negative whole number and press Enter to apply. Set 0 to turn the cache off. Older songs are removed first; each is kept in the format YouTube serves it, in a private Meiro cache folder.")
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
			settingLabel(c, "Cache folder", "Meiro creates a private ‘Meiro Audio Cache’ folder inside this location. Changing folders removes the songs Meiro cached in the old one.")
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
			if a.cacheDirError != "" {
				m3.Text(c, m3.BodyMedium, a.cacheDirError).TextColor(sc.Error)
			}
		})
	})
}

// soundCard holds the equaliser's bands. Slider changes update the player as
// they happen; only saving the settings waits for the drag to finish.
func (a *app) soundCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Sound", m3.IconVolumeUp, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Normalize loudness", "Adjust track gain to keep songs at a more consistent volume. Requires a signed-in YouTube session.").Grow(1)
			on := a.settings.NormalizeLoudness
			if m3.Switch(c, &on, "Normalize loudness").Changed() {
				a.setNormalizeLoudness(on)
			}
		})
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Logarithmic volume", "Give the volume slider finer steps at low volumes.").Grow(1)
			on := a.settings.LogVolume
			if m3.Switch(c, &on, "Logarithmic volume").Changed() {
				a.settings.LogVolume = on
				a.saveSettings()
				a.applyLoudness()
			}
		})
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Equaliser", "Shape ten bands of the sound as you adjust them, without restarting the stream.").Grow(1)
			on := a.settings.EQOn
			if m3.Switch(c, &on, "Equaliser on").Changed() {
				a.settings.EQOn = on
				a.applyEQ()
			}
		})
		a.normalizeEQBands()
		ui.Row(c).Gap(8).Wrap().Children(func() {
			for _, preset := range eqPresets {
				selected := slices.Equal(a.settings.EQBands, preset.bands)
				if m3.Button(c, m3.ButtonSpec{
					Label: preset.name, Kind: m3.Tonal, Size: m3.ExtraSmall32,
					Toggle: true, Selected: selected, Disabled: !a.settings.EQOn, Key: "eq-preset-" + preset.name,
				}).Clicked() {
					copy(a.settings.EQBands, preset.bands)
					copy(a.eqApplied[:], preset.bands)
					a.applyEQ()
				}
			}
		})
		// The bands, one upright slider each above its frequency, the way an
		// equaliser is drawn: the handle rests on the 0 dB mark, and a boost
		// or cut fills between the two.
		ui.Row(c).FillWidth().MaxWidth(640).Gap(4).Justify(ui.SpaceBetween).Children(func() {
			for i := range player.EQBandCount {
				ui.Column(c).Gap(6).Width(m3.MinTarget).AlignItems(ui.Center).Children(func() {
					m3.Slider(c, &a.settings.EQBands[i], -eqBoostRange, eqBoostRange, m3.SliderSpec{
						Label: eqLabels[i] + " dB", Key: "eq-" + strconv.Itoa(i),
						Thickness: 6, Vertical: true, Disabled: !a.settings.EQOn,
					}).Height(148).FillWidth()
					if a.eqApplied[i] != a.settings.EQBands[i] {
						copy(a.eqApplied[:], a.settings.EQBands)
						a.player.SetEQ(a.eqBands())
						a.scheduleEQ()
					}
					m3.Text(c, m3.LabelLarge, eqLabels[i]).TextColor(sc.OnSurfaceVariant)
				})
			}
		})
	})
}

func (a *app) playbackCard(c *ui.Context) {
	a.settingsCard(c, "Disliked songs", m3.IconMusicNote, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Auto-skip disliked songs", "Only songs known to be disliked are skipped.").Grow(1)
			on := a.settings.AutoSkipDisliked
			if m3.Switch(c, &on, "Skip disliked songs").Changed() {
				a.settings.AutoSkipDisliked = on
				a.saveSettings()
			}
		})
	})
}

// crossfadeCard fades the end of a track into the start of the next. The
// player takes a change live, so the slider applies as it moves and the
// settings are saved once it has been quiet.
func (a *app) crossfadeCard(c *ui.Context) {
	sc := m3.Of(c).Scheme
	a.settingsCard(c, "Crossfade", m3.IconMusicNote, func() {
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			settingLabel(c, "Fade between tracks", "").Grow(1)
			on := a.settings.Crossfade
			if m3.Switch(c, &on, "Crossfade on").Changed() {
				a.settings.Crossfade = on
				a.applyCrossfade()
				a.saveSettings()
			}
		})
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			m3.Text(c, m3.LabelLarge, "Duration").TextColor(sc.OnSurfaceVariant).Width(64).Shrink(0)
			value := float64(settings.ClampCrossfade(a.settings.CrossfadeSeconds))
			slider := m3.Slider(c, &value, settings.MinCrossfade, settings.MaxCrossfade, m3.SliderSpec{
				Label: "Crossfade duration", Thickness: 16, Key: "crossfade-seconds", Disabled: !a.settings.Crossfade,
			}).Grow(1)
			if slider.Changed() {
				a.settings.CrossfadeSeconds = int(math.Round(value))
				a.applyCrossfade()
				a.saveSettings()
			}
			m3.EmphasizedText(c, m3.TitleMedium, strconv.Itoa(settings.ClampCrossfade(a.settings.CrossfadeSeconds))+" s").
				Width(48).Shrink(0).TextAlign(ui.End).TextColor(sc.OnSurface)
		})
	})
}

// swatch is one seed colour, drawn as the primary colour it would give. The
// chosen one squares off and shows a check.
func (a *app) swatch(c *ui.Context, name, hex string) bool {
	th := m3.Of(c)
	sc := th.Scheme
	seed, _ := settings.ParseSeed(hex)
	fill, on := seed, ui.RGB(255, 255, 255)
	if m3.Contrast(fill, on) < 3 {
		on = ui.RGB(24, 24, 27)
	}
	selected := !a.settings.Dynamic && !a.settings.Matugen && hex == a.settings.Seed
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
	border := ui.Transparent
	if selected {
		border = sc.Surface
	} else if b.Hovered() {
		border = sc.OnSurface.Alpha(0.16)
	}
	b.Border(3, border)
	if selected {
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
	t := &a.theme
	if t.previews == nil || seed != t.previewSeed || dark != t.previewDark {
		t.previews, t.previewSeed, t.previewDark = make(map[m3.Style]m3.Scheme, len(m3.Styles())), seed, dark
	}
	if preview, ok := t.previews[style]; ok {
		return preview
	}
	preview := m3.NewScheme(m3.NewPalettes(seed, style), dark)
	t.previews[style] = preview
	return preview
}

// styleTile previews a palette style in the colours the seed would give it.
func (a *app) styleTile(c *ui.Context, style m3.Style) bool {
	th := m3.Of(c)
	sc := th.Scheme
	preview := a.stylePreview(th.Seed, th.Dark, style)
	selected := th.Style == style
	b := ui.ButtonBase(c.Key("style-" + style.String()))
	b.Column().AlignItems(ui.Stretch).Width(138).MinHeight(120).Padding(14).Gap(10).Radius(m3.ExtraLarge - 4).Background(preview.PrimaryContainer).Cursor(ui.CursorPointer).
		Label(style.String())
	border := ui.Transparent
	if selected {
		border = sc.Primary
	} else if b.Hovered() {
		border = sc.OnSurface.Alpha(0.16)
	}
	b.Border(3, border)
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
			if current := a.account.session; current != nil {
				a.avatar(c, current.name(), current.details.Thumbnail, 128, 56)
				ui.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
					m3.EmphasizedText(c, m3.TitleMedium, current.name()).SingleLine()
					if current.details.Email != "" {
						m3.Text(c, m3.BodyMedium, current.details.Email).SingleLine().TextColor(sc.OnSurfaceVariant)
					}
				})
				if m3.Button(c, m3.ButtonSpec{Label: "Sign out", Icon: m3.IconLogout, Kind: m3.Tonal, Size: m3.Medium56, Key: "sign-out"}).Clicked() {
					a.signOut()
				}
				return
			}
			settingLabel(c, "Not signed in", "Sign in to see your library. Browsing and playing work without it.").Grow(1)
			if m3.Button(c, m3.ButtonSpec{Label: "Sign in", Icon: m3.IconLogin, Size: m3.Medium56, Key: "sign-in"}).Clicked() {
				a.openSignIn()
			}
		})
		if s := &a.account.signIn; s.err != "" && !s.open {
			m3.Text(c, m3.BodyMedium, s.err).TextColor(sc.Error).MaxLines(3)
		}
	})
}

// chooseAudioCacheDirectory opens the native folder picker, then switches
// future playback to an app-owned audio folder inside the chosen directory.
// The songs cached in the folder before are removed.
func (a *app) chooseAudioCacheDirectory() {
	defaultRoot, _ := mygo.App.Path(mygo.PathCache)
	current := a.cacheDirectoryText
	if current == "" {
		current = defaultRoot
	}
	limit, parent := a.settings.CacheSongs, a.desktop.window()
	type chosen struct {
		root  string
		cache *audio.Cache
		err   string
	}
	spawn(a, func() chosen {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: parent, Title: "Choose audio cache folder", DefaultPath: current,
			Directory: true, CreateDirectories: true,
		})
		if err != nil {
			return chosen{err: "Could not open the folder picker: " + err.Error()}
		}
		if len(paths) == 0 {
			return chosen{}
		}
		root, err := filepath.Abs(paths[0])
		if err != nil {
			return chosen{err: "Could not use that folder: " + err.Error()}
		}
		if filepath.Clean(root) == filepath.Clean(current) {
			return chosen{root: root}
		}
		cache, root, err := audio.Open(root, limit)
		if err != nil {
			return chosen{err: "Could not use that folder: " + err.Error()}
		}
		return chosen{root: root, cache: cache}
	}, func(c chosen) {
		if c.err != "" {
			a.cacheDirError = c.err
			return
		}
		if c.root == "" {
			return // the picker was cancelled
		}
		if c.cache != nil {
			c.cache.SetLimit(a.settings.CacheSongs)
			if previous := a.replaceAudioCache(c.cache); previous != nil {
				a.run(func() {
					previous.Close()
					// The old location is app-owned and no longer used.
					if previous.Dir() != c.cache.Dir() {
						previous.Purge()
					}
				})
			}
		}
		a.settings.CacheDirectory = c.root
		if filepath.Clean(c.root) == filepath.Clean(defaultRoot) {
			a.settings.CacheDirectory = ""
		}
		a.cacheDirectoryText, a.cacheDirError = c.root, ""
		a.saveSettings()
	})
}

// desktopCard holds the switches that tie Meiro to the rest of the system:
// notifications, the tray, the global shortcut and starting at login.
func (a *app) desktopCard(c *ui.Context) {
	a.settingsCard(c, "Desktop", m3.IconNotifications, func() {
		ui.Column(c).Gap(4).Children(func() {
			if runtime.GOOS == "darwin" {
				a.notchSetting(c)
				ui.Row(c.Key("keep-running")).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
					settingLabel(c, "Keep running when closed", "Hide the window and keep Meiro running. Reopen it from the Dock, notch or menu bar.").Grow(1)
					choice := a.closeToBackgroundChoice()
					on := choice != nil && *choice
					if m3.Switch(c, &on, "Keep running in background").Changed() {
						a.answerClose(on, true)
					}
				})
			} else {
				a.desktopSwitch(c, "Track notifications", "Announce each new track. Clicking one brings Meiro back.",
					a.settings.NotifyTrack, func(on bool) {
						a.settings.NotifyTrack = on
						if !on {
							a.notificationArtwork.stop()
						}
					})
				a.desktopSwitch(c, "Menu bar icon", "Keep Meiro in the menu bar with play, next and quit.",
					a.settings.ShowTray, func(on bool) {
						a.settings.ShowTray = on
						a.syncDesktop()
					})
				if trayMarkSupported(runtime.GOOS) {
					ui.Row(c).Gap(16).AlignItems(ui.Center).Padding(8, 0).Children(func() {
						settingLabel(c, "Tray icon", "Choose which icon appears in the system tray; the O mark is more subtle.").Grow(1)
						mark := 0
						if a.settings.TrayMark == settings.TrayMarkOMark {
							mark = 1
						}
						if m3.ButtonGroup(c, "tray-mark", &mark, []string{"Wordmark", "O mark"}, nil) {
							if mark == 1 {
								a.settings.TrayMark = settings.TrayMarkOMark
							} else {
								a.settings.TrayMark = settings.TrayMarkWordmark
							}
							a.syncDesktop()
							a.saveSettings()
						}
					})
				}
				a.desktopSwitch(c, "Close to tray", "Closing the window keeps Meiro running in the tray instead of quitting.",
					a.settings.CloseToTray != nil && *a.settings.CloseToTray, func(on bool) {
						a.answerClose(on, true)
					})
			}
			a.desktopSwitch(c, "Global shortcut", "Play or pause from anywhere with "+toggleShortcut+".",
				a.settings.GlobalShortcut, func(on bool) {
					a.settings.GlobalShortcut = on
					a.syncShortcut()
				})
			a.desktopSwitch(c, "Discord Rich Presence", "Share the track, artist, cover and elapsed time while music plays. The status clears when playback pauses or stops.",
				a.settings.DiscordPresence, func(on bool) {
					a.settings.DiscordPresence = on
					a.syncSystemMedia()
				})
			a.desktopSwitch(c, "Open at login", "Start Meiro when you log in.",
				a.settings.OpenAtLogin, func(on bool) {
					a.settings.OpenAtLogin = on
					a.desktop.setOpenAtLogin(on)
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
