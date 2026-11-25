package main

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

func (a *app) hero(c *ui.Context) {
	sc := m3.Of(c).Scheme
	d := a.page.detail
	r, id := a.route()
	artRadius := m3.ExtraLargeInc
	if r.target == youtube.TargetArtist {
		artRadius = m3.Full
	}
	wash := sc.PrimaryContainer.Mix(sc.Surface, 0.45)
	artSize, gap, titleRole := float32(220), float32(28), m3.DisplaySmall
	if a.narrowHero() {
		artSize, gap, titleRole = 150, 20, m3.HeadlineMedium
	}
	buttons := a.heroButtonSize()
	var art, catcher ui.Element
	var menuAnchor ui.Element
	hasAnchor := false
	ui.Row(c).Padding(8, pageGutter, 28).Gap(gap).AlignItems(ui.End).Gradient(wash, sc.Surface, 180).Children(func() {
		art = a.art(c, d.art, 448, artSize, artRadius, func() {
			catcher = ui.ButtonBase(c.Key("hero-art")).Size(artSize, artSize).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
				Radius(artRadius).Background(ui.Transparent).Label("More options for " + d.title)
			menuAnchor, hasAnchor = a.menu.pointerAnchor(c, "hero")
		})
		if a.cover.match.card != "" && a.cover.kind == r.target && a.cover.id == id {
			fly := m3.SpatialDefault.Motion()
			fly.Match, fly.Fly = a.cover.match, true
			art.Transition(fly)
		}
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
				m3.Text(c, m3.BodyMedium, d.description).MaxLines(3).MaxWidth(720).TextColor(sc.OnSurfaceVariant)
			}
			ui.Box(c).Height(10)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				none := len(a.page.playable) == 0
				if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: buttons, Disabled: none, Key: "hero-play"}).Clicked() {
					a.playAll()
				}
				if r.target != youtube.TargetPodcast && m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: buttons, Disabled: none, Key: "hero-shuffle"}).Clicked() {
					a.shufflePage()
				}
				a.libraryButton(c)
				a.downloadButton(c)
				a.subscribeButton(c)
				a.blockArtistButton(c, r, id)
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

func (a *app) narrowHero() bool {
	return a.listWidthLastFrame > 0 && a.listWidthLastFrame < 720
}

func (a *app) heroButtonSize() m3.ButtonSize {
	if a.narrowHero() {
		return m3.Small40
	}
	return m3.Medium56
}

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

func (a *app) playlistSort(c *ui.Context) {
	ui.Row(c).Gap(2).AlignItems(ui.Center).Children(func() {
		a.playlistSortField(c)
		label, icon := "Sort descending", m3.IconArrowUp
		if a.detailSort.desc {
			label, icon = "Sort ascending", m3.IconArrowDown
		}
		if m3.IconButton(c, m3.IconButtonSpec{Icon: icon, Label: label, Kind: m3.StandardIcon, Size: m3.Small40, Key: "playlist-sort-direction"}).Clicked() {
			a.detailSort.flip()
		}
	})
}

func (a *app) playlistSortField(c *ui.Context) {
	rotation := float32(0)
	if a.detailSortOpen {
		rotation = 180
	}
	anchor := m3.Button(c, m3.ButtonSpec{Label: "Sort: " + a.detailSort.label(), Icon: m3.IconExpandMore, IconRotation: rotation, Kind: m3.TextOnly, Size: m3.Medium56, Padding: 8, Key: "playlist-sort"})
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
