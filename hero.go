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

// compactHeader puts a detail page's identity and actions in the top bar once
// its hero has scrolled out of view: the artwork, the name, and the actions
// that play it. shown is how far the header has come in, so the three parts
// arrive one after another, and leave in the opposite order. The playlist
// search stays in the song toolbar.
func (a *app) compactHeader(c *ui.Context, shown float32) {
	d := a.page.detail
	if d.title == "" {
		return
	}
	sc := m3.Of(c).Scheme
	r, _ := a.route()
	none := len(a.page.playable) == 0
	playLabel, shuffleLabel := "Play", "Shuffle"
	if r.target == youtube.TargetPlaylist {
		playLabel, shuffleLabel = "Play playlist", "Shuffle playlist"
	}
	// Each part has its own window of the same fade, so they arrive art,
	// title, then actions, and leave in reverse. The move is small: the bar
	// should read as settling, not sliding.
	part := func(from float32) float32 { return min(max((shown-from)/0.35, 0), 1) }
	artIn, titleIn, actionsIn := part(0), part(0.12), part(0.3)
	ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(1).MinWidth(0).Children(func() {
		if d.art != "" {
			a.art(c, d.art, 96, 34, m3.Small).Opacity(artIn).Translate(0, (1-artIn)*6)
		}
		m3.EmphasizedText(c, m3.TitleMedium, d.title).SingleLine().MaxWidth(140).Shrink(1).MinWidth(0).TextColor(sc.OnSurface).
			Opacity(titleIn).Translate(0, (1-titleIn)*6)
		ui.Row(c).Gap(2).Shrink(0).Opacity(actionsIn).Translate(0, (1-actionsIn)*6).Children(func() {
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconPlay, Label: playLabel, Kind: m3.FilledIcon, Size: m3.ExtraSmall32, Disabled: none, Key: "compact-play"}).Clicked() {
				a.playAll()
			}
			// A show is listened to in order, as in the hero.
			if r.target != youtube.TargetPodcast {
				if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconShuffle, Label: shuffleLabel, Kind: m3.StandardIcon, Size: m3.ExtraSmall32, Disabled: none, Key: "compact-shuffle"}).Clicked() {
					a.shufflePage()
				}
			}
			if state, id, ok := a.pageDownload(); ok {
				if m3.IconButton(c, m3.IconButtonSpec{Icon: state.icon, Label: state.label, Kind: m3.StandardIcon, Size: m3.ExtraSmall32, Disabled: state.disabled, Key: "compact-download"}).Clicked() {
					a.togglePageDownload(id, state)
				}
			}
		})
	})
}

// playlistSort draws two controls above the list: a menu that picks what to
// sort by, and a button that reverses the direction. Both sit on the songs
// toolbar with the search, so they carry no container of their own: the row is
// a line of quiet controls over the songs, not three pills.
func (a *app) playlistSort(c *ui.Context) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		a.playlistSortField(c)
		// The direction belongs to the page rather than to a field, so the
		// button stays for the original order, which then reads backwards.
		label, icon := "Sort descending", m3.IconArrowUp
		if a.detailSort.desc {
			label, icon = "Sort ascending", m3.IconArrowDown
		}
		if m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: label, Kind: m3.StandardIcon, Size: m3.Small40, Key: "playlist-sort-direction"}).Clicked() {
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
	anchor := m3.Button(c, m3.ButtonSpec{Label: "Sort: " + a.detailSort.label(), Icon: m3.IconExpandMore, IconRotation: rotation, Kind: m3.TextOnly, Size: m3.Medium56, Padding: 8, Key: "playlist-sort"})
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
