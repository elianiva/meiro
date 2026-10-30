package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// hero is the heading of an album, a playlist or an artist: its artwork large
// over a wash of the theme's colour, its name, and the buttons that play it.
func (a *app) hero(c *ui.Context) {
	sc := m3.Of(c).Scheme
	d := a.page.detail
	r, _ := a.route()
	artRadius := m3.ExtraLargeInc
	if r.target == youtube.TargetArtist {
		artRadius = m3.Full
	}
	wash := sc.PrimaryContainer.Mix(sc.Surface, 0.45)
	// In a narrow window the artwork and the buttons shrink, so that the
	// buttons that play the page stay on one line beside the picture.
	artSize, gap, titleRole := float32(220), float32(28), m3.DisplaySmall
	if a.narrowHero() {
		artSize, gap, titleRole = 150, 20, m3.HeadlineMedium
	}
	buttons := a.heroButtonSize()
	// The artwork opens the context menu YouTube Music's does, on a
	// right-click. The catcher is an attached, transparent button over the
	// picture: attached, it leaves the layout to the artwork it covers.
	var art, catcher ui.Element
	var menuAnchor ui.Element
	hasAnchor := false
	ui.Row(c).Padding(8, pageGutter, 28).Gap(gap).AlignItems(ui.End).Gradient(wash, sc.Surface, 180).Children(func() {
		art = a.art(c, d.art, 448, artSize, artRadius, func() {
			catcher = ui.ButtonBase(c.Key("hero-art")).Size(artSize, artSize).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
				Radius(artRadius).Background(ui.Transparent).Label("More options for " + d.title)
			menuAnchor, hasAnchor = a.menu.pointerAnchor(c, "hero")
		})
		m3.Elevation(c, art, 2)
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			if label := heroLabel(r.target); label != "" {
				m3.EmphasizedText(c, m3.LabelLarge, strings.ToUpper(label)).LetterSpacing(1.2).TextColor(sc.Primary)
			}
			m3.EmphasizedText(c, titleRole, d.title).MaxLines(2).TextColor(sc.OnSurface)
			if d.subtitle != "" {
				m3.Text(c, m3.BodyLarge, d.subtitle).MaxLines(2).TextColor(sc.OnSurfaceVariant)
			}
			if d.description != "" {
				// A line of reading length, which the hero's width would
				// otherwise stretch past.
				m3.Text(c, m3.BodyMedium, d.description).MaxLines(3).MaxWidth(720).TextColor(sc.OnSurfaceVariant)
			}
			ui.Box(c).Height(10)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				none := len(a.page.playable) == 0
				if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: buttons, Disabled: none, Key: "hero-play"}).Clicked() {
					a.playAll()
				}
				// A show is listened to in order; shuffling its episodes is
				// not something YouTube Music offers either.
				if r.target != youtube.TargetPodcast && m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: buttons, Disabled: none, Key: "hero-shuffle"}).Clicked() {
					a.shufflePage()
				}
				a.libraryButton(c)
				a.downloadButton(c)
				a.subscribeButton(c)
				if r.id == routePlaylist {
					// One call site, beside the buttons that play the page:
					// the control keeps its place, and its button, while it
					// opens into the bar.
					a.playlistSearch(c)
				}
			})
		})
	})
	if catcher.RightClicked() {
		x, y, _ := catcher.PointerPosition()
		a.menu.show("hero", true, x, y)
	}
	if catcher.Clicked() || catcher.Shortcut(ui.Shift, ui.KeyF10) {
		a.menu.show("hero", false, 0, 0)
	}
	if a.menu.owns("hero") {
		a.heroMenu(c, a.menu.anchor(art, menuAnchor, hasAnchor))
	}
}

// narrowHero is whether the page is too narrow for the hero at its full size.
func (a *app) narrowHero() bool {
	return a.listWidth > 0 && a.listWidth < 720
}

// heroButtonSize is the size of the buttons of the hero.
func (a *app) heroButtonSize() m3.ButtonSize {
	if a.narrowHero() {
		return m3.Small40
	}
	return m3.Medium56
}

// detailSearchWidth is how wide the bar the playlist search opens into reaches
// at most.
const detailSearchWidth = 520

// detailSearchButton is the side of the button that opens the playlist search,
// which is the height of the buttons beside it.
func (a *app) detailSearchButton() float32 {
	if a.narrowHero() {
		return 40
	}
	return 56
}

// playlistSearch draws the control that narrows a playlist page to the songs
// that match: an icon button that opens into the bar the Search page uses,
// beside the buttons that play the page. Closing it puts the page back to its
// whole list, since a search that showed none of a page's rows would leave
// nothing to say it was still on.
func (a *app) playlistSearch(c *ui.Context) {
	sc := m3.Of(c).Scheme
	// One element carries both states, so opening grows the control instead
	// of swapping one thing for another. Its size is a maximum, which the
	// animation moves: a width would only set the flex base, and the
	// control's Grow then fills the rest of the line, so the bar would jump
	// out at full size and jump back into the button when it closed.
	control := ui.Row(c.Key("playlist-search")).MinWidth(0).Grow(1).Shrink(1)
	target := float32(0)
	if a.detailSearchOpen {
		target = 1
	}
	grown := m3.Animate(control, "open", target, m3.SpatialFast)
	if !a.detailSearchOpen && grown < 0.02 {
		control.MaxWidth(a.detailSearchButton())
		control.Children(func() {
			button := m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconSearch, Label: "Search in this playlist",
				Kind: m3.StandardIcon, Size: a.heroButtonSize(), Key: "playlist-search-open",
				// The bar's own container, so the button that opens the
				// search and the bar it opens are one thing growing rather
				// than two that do not match.
				Container: sc.SurfaceContainerHigh,
			})
			if button.Clicked() {
				a.detailSearchOpen = true
				a.detailSearchField.Focus()
				a.detailSearchHadFocus = false
			}
		})
		return
	}
	maxWidth := float32(detailSearchWidth)
	if a.listWidth > 0 {
		maxWidth = min(maxWidth, a.listWidth-48)
	}
	control.MaxWidth(a.detailSearchButton() + (maxWidth-a.detailSearchButton())*min(max(grown, 0), 1))
	control.Children(func() {
		result, _ := m3.SearchBar(c, m3.SearchSpec{
			Value:       &a.detailFilter,
			Placeholder: "Search in this playlist",
			Key:         "playlist-search-bar",
			Focus:       &a.detailSearchField,
			MaxWidth:    maxWidth,
			Dismiss:     true,
		})
		if result.Dismissed {
			a.closePlaylistSearch()
		} else {
			if a.detailSearchHadFocus && !result.Focused && a.detailFilter == "" {
				a.closePlaylistSearch()
			}
			a.detailSearchHadFocus = result.Focused
		}
	})
}

// closePlaylistSearch puts a playlist page back to its whole list, and the
// control back to the button that opens the search.
func (a *app) closePlaylistSearch() {
	a.detailSearchOpen, a.detailSearchHadFocus = false, false
	a.detailFilter = ""
}

// playlistSort draws two controls above the list: a menu that picks what to
// sort by, and a button that reverses the direction.
func (a *app) playlistSort(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		a.playlistSortField(c)
		// The direction belongs to the page rather than to a field, so the
		// button stays for the original order, which then reads backwards.
		label, icon := "Sort descending", m3.IconArrowUp
		if a.detailSort.desc {
			label, icon = "Sort ascending", m3.IconArrowDown
		}
		if m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: label, Kind: m3.OutlinedIcon, Size: m3.Medium56, Key: "playlist-sort-direction"}).Clicked() {
			a.detailSort.flip()
		}
	})
}

// playlistSortField draws the button and menu that choose the sort field.
func (a *app) playlistSortField(c *ui.Context) {
	rotation := float32(0)
	if a.detailSortOpen {
		rotation = 180
	}
	anchor := m3.Button(c, m3.ButtonSpec{Label: "Sort: " + a.detailSort.label(), Icon: m3.IconExpandMore, IconRotation: rotation, Kind: m3.Outlined, Size: m3.Medium56, Key: "playlist-sort"})
	// A menu ignores the press that lands on the button that opened it, so
	// the button closes it, as the account menu's button does.
	if anchor.Clicked() {
		a.detailSortOpen = !a.detailSortOpen
	}
	if a.detailSortOpen {
		m3.Menu(c, anchor, &a.detailSortOpen, 220, func() {
			for _, field := range []playlistSortField{playlistSortOriginal, playlistSortTitle, playlistSortArtist, playlistSortDuration} {
				if m3.MenuItem(c, playlistSort{field: field}.label(), nil).Clicked() {
					a.detailSort.choose(field)
					a.detailSortOpen = false
				}
			}
		})
	}
}

func heroLabel(kind youtube.TargetKind) string {
	switch kind {
	case youtube.TargetAlbum:
		return "Album"
	case youtube.TargetPlaylist:
		return "Playlist"
	case youtube.TargetArtist:
		return "Artist"
	case youtube.TargetPodcast:
		return "Podcast"
	case youtube.TargetTrack, youtube.TargetNone:
	}
	return ""
}
