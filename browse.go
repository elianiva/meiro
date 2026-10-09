package main

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// The sizes of what a shelf holds, in DIPs.
const (
	cardWidth   = 176
	cardArt     = cardWidth - 16
	columnWidth = 392
	rowHeight   = 64
)

// browsePage shows a feed, or an album, a playlist or an artist, as rows of
// shelves and songs.
func (a *app) browsePage(c *ui.Context) { a.pageList(c) }

// libraryPage asks for a sign-in before it has anything to show.
func (a *app) libraryPage(c *ui.Context) {
	if a.signedIn {
		a.pageList(c)
		return
	}
	ui.Column(c).Fill().Center().Children(func() {
		a.message(c, m3.IconLibraryFilled, "Your library lives here",
			"Sign in to see your songs, albums and playlists.",
			"Sign in", a.signInWithGoogle)
	})
}

// pageList builds the rows of the page in a list that builds only those in
// view, so a page of hundreds of songs is as light as one of ten.
func (a *app) pageList(c *ui.Context) {
	a.setRows()
	// A button in a row can reload the page, and empty the rows, while the list
	// still looks for the row that held the focus.
	a.list.Key = func(i int) any {
		if i >= len(a.rows) {
			return nil
		}
		return a.rows[i].key()
	}
	a.list.Label = func(i int) string {
		if i >= len(a.rows) {
			return ""
		}
		if r := a.rows[i]; r.kind == rowTrack {
			return r.item.Title
		}
		return a.rows[i].title
	}
	ui.List(c, &a.list, len(a.rows), func(i int) {
		a.rowView(c, i)
	}).Grow(1).Padding(0, 0, a.clearance())
}

// rowView builds one row of the page.
func (a *app) rowView(c *ui.Context, i int) {
	r := a.rows[i]
	switch r.kind {
	case rowHero:
		a.hero(c)
	case rowHeading:
		next := i+1 < len(a.rows) && (a.rows[i+1].kind == rowCards || a.rows[i+1].kind == rowColumns)
		a.sectionHeading(c, r, next, i == 0)
	case rowCards:
		a.cardShelf(c, r)
	case rowColumns:
		a.columnShelf(c, r)
	case rowTrack:
		a.songRow(c, r.item, a.playable, songOptions{number: r.number(), inset: true})
	case rowMore:
		ui.Row(c).Justify(ui.Center).Padding(16).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: r.title, Kind: m3.Tonal, Size: m3.Medium56, Key: "more"}).Clicked() {
				a.loadMore()
			}
		})
	case rowLoading:
		ui.Row(c).Height(360).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			m3.LoadingIndicator(c, 72, true)
		})
	case rowError:
		a.message(c, m3.IconError, "Something went wrong", firstLine(r.title), "Try again", a.retry)
	case rowEmpty:
		a.message(c, m3.IconMusicNote, r.title, "", "", nil)
	}
}

// number is the place shown beside a song that has no artwork of its own.
func (r row) number() int {
	if r.track < 0 || r.item.Thumbnail != "" {
		return 0
	}
	return r.track + 1
}

// carousel returns the scroll of the shelf with a key.
func (a *app) carousel(key string) *m3.CarouselState {
	st := a.carousels[key]
	if st == nil {
		st = &m3.CarouselState{}
		a.carousels[key] = st
	}
	return st
}

// sectionHeading names a section of a page, with arrows that page its shelf.
func (a *app) sectionHeading(c *ui.Context, r row, paged, first bool) {
	sc := m3.Active().Scheme
	top := float32(28)
	if first {
		top = 4
	}
	ui.Row(c).Padding(top, pageGutter-8, 8, pageGutter).Gap(4).AlignItems(ui.Center).Children(func() {
		m3.EmphasizedText(c, m3.TitleLarge, r.title).SingleLine().Grow(1).MinWidth(0).TextColor(sc.OnSurface)
		if !paged {
			return
		}
		st := a.carousel(r.shelf)
		for _, step := range []struct {
			dir   int
			icon  *ui.SVG
			label string
		}{{-1, m3.IconChevronLeft, "Earlier"}, {1, m3.IconChevronRight, "Later"}} {
			if m3.IconButton(c, m3.IconButtonSpec{
				Icon: step.icon, Label: step.label, Kind: m3.TonalIcon,
				Disabled: !st.CanPage(step.dir), Key: r.shelf + step.label,
			}).Clicked() {
				st.Page(step.dir)
			}
		}
	})
}

// cardShelf is a row of cards that scrolls sideways.
func (a *app) cardShelf(c *ui.Context, r row) {
	ui.Column(c).Children(func() {
		m3.Carousel(c, a.carousel(r.shelf), r.shelf, 4, pageGutter-8, func() {
			for _, item := range r.items {
				a.card(c, item, r.queue)
			}
		})
	})
}

// columnShelf is songs in columns of four, which scroll sideways: the shelf
// of quick picks.
func (a *app) columnShelf(c *ui.Context, r row) {
	ui.Column(c).Children(func() {
		m3.Carousel(c, a.carousel(r.shelf), r.shelf, 8, pageGutter-8, func() {
			for start := 0; start < len(r.items); start += 4 {
				group := r.items[start:min(start+4, len(r.items))]
				ui.Column(c).Key(start).Width(columnWidth).Shrink(0).Children(func() {
					for _, item := range group {
						a.songRow(c, item, r.queue, songOptions{})
					}
				})
			}
		})
	})
}

func itemKey(prefix string, item youtube.MusicItem) string {
	return prefix + ":" + item.ID + "\x00" + item.Title + "\x00" + item.Subtitle
}

// hoverOpacity is the state layer of something that may be under the pointer.
func hoverOpacity(hovered bool) float32 {
	if hovered {
		return m3.StateHover
	}
	return 0
}

// artRadius is the corner of an item's artwork: artists are round, the rest
// are soft squares.
func artRadius(item youtube.MusicItem, square float32) float32 {
	if kind, _ := targetOf(item); kind == pageArtist {
		return m3.Full
	}
	return square
}

// card shows an album, a playlist, an artist or a song as artwork over its
// name. A play button rises over the artwork under the pointer.
func (a *app) card(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem) {
	sc := m3.Active().Scheme
	kind, _ := targetOf(item)
	key := itemKey("card", item)
	card := ui.ButtonBase(c.Key(key))
	hovered := card.Hovered()
	card.Column().AlignItems(ui.Start).Width(cardWidth).Shrink(0).Padding(8).Gap(10).Radius(m3.ExtraLarge).Cursor(ui.CursorPointer).
		Background(m3.Layer(sc.Surface, sc.OnSurface, hoverOpacity(hovered || card.FocusVisible()))).
		Label(item.Title)
	played, menuClicked := false, false
	var menuButton ui.Element
	hasMenu := false
	card.Children(func() {
		radius := artRadius(item, m3.LargeIncreased)
		opening := a.opening == itemKey("open", item)
		m3.Art(c, a.thumbs.bitmap(item.Thumbnail, 320), cardArt, radius, func() {
			if isVideo(item) {
				m3.VideoBadge(c, cardArt)
			}
			if opening {
				ui.Box(c).Size(40, 40).Radius(m3.Full).Center().Background(sc.PrimaryContainer).
					Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(8).Bottom(8).Children(func() {
					m3.LoadingIndicatorIn(c, 28, sc.OnPrimaryContainer)
				})
				return
			}
			if hovered || card.FocusVisible() || a.trackMenuOpen && a.trackMenuKey == key+"-menu" {
				if kind != "" {
					menuButton = m3.IconButton(c, m3.IconButtonSpec{
						Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
					}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
					hasMenu = true
				}
			}
			if hovered && (kind == pageTrack || kind == pageAlbum || kind == pagePlaylist) {
				fab := m3.FAB(c, m3.FABSpec{Icon: m3.IconPlay, Size: m3.FABSmall, Tone: m3.FABPrimary, Key: "play"}).
					Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(8).Bottom(8)
				fab.Transition(ui.ElementTransition{Enter: &ui.Motion{Y: 8}, Duration: m3.SpatialFast.Duration(), Ease: m3.SpatialFast.Ease()})
				if fab.Clicked() {
					played = true
				}
			}
		})
		m3.EmphasizedText(c, m3.TitleSmall, item.Title).SingleLine().TextColor(sc.OnSurface).Margin(0, 4)
		if item.Subtitle != "" {
			m3.Text(c, m3.BodySmall, item.Subtitle).SingleLine().TextColor(sc.OnSurfaceVariant).Margin(-6, 4, 0)
		}
	})
	if hasMenu && (menuButton.Clicked() || card.RightClicked()) {
		a.trackMenuKey, a.trackMenuOpen = key+"-menu", true
		menuClicked = true
	}
	if hasMenu && a.trackMenuKey == key+"-menu" {
		a.cardMenu(c, menuButton, key+"-menu", item, queue)
	}
	switch {
	case menuClicked:
		// The card's button also receives this pointer event. Opening its menu
		// must not activate the card underneath it.
	case played:
		a.playCollection(item)
	case card.Clicked():
		a.activate(item, queue)
	}
}

// cardMenu offers actions that make sense for a card. Songs share their
// queue and artist actions with song rows; other cards can open their page or
// copy its link.
func (a *app) cardMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem, queue []youtube.MusicItem) {
	if a.trackMenuKey != key {
		return
	}
	if kind, _ := targetOf(item); kind == pageTrack {
		a.songMenu(c, anchor, key, item)
		return
	}
	m3.Menu(c, anchor, &a.trackMenuOpen, 232, func() {
		close := func() { a.trackMenuOpen = false }
		kind, id := targetOf(item)
		label := ""
		icon := m3.IconChevronRight
		switch kind {
		case pageAlbum:
			label, icon = "Open album", m3.IconAlbum
		case pagePlaylist:
			label, icon = "Open playlist", m3.IconQueue
		case pageArtist:
			label, icon = "Go to artist", m3.IconPerson
		}
		if label != "" && m3.MenuItem(c, label, icon).Clicked() {
			a.activate(item, queue)
			close()
		}
		if id != "" && m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard("https://music.youtube.com/browse/" + url.PathEscape(id))
			c.Toast("Link copied")
			close()
		}
	})
}

// songOptions shape a row of a song.
type songOptions struct {
	// number shows a place in the list where the artwork would be.
	number int
	// inset puts the row in from the edges of the page, as the rows of a
	// page of songs are; rows in a shelf sit flush.
	inset       bool
	directQueue bool
	source      string
}

// songRow shows a song, an album, an artist or a playlist as a row: artwork,
// name over details, and the length. The song playing is picked out in a
// tonal container with dancing bars.
func (a *app) songRow(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem, o songOptions) {
	sc := m3.Active().Scheme
	kind, _ := targetOf(item)
	isSong := kind == pageTrack
	playing := isSong && item.VideoID == a.current.VideoID
	key := itemKey("song", item) + strconv.Itoa(o.number)
	row := ui.Row(c.Key(key))
	row.Height(rowHeight).Shrink(0).Padding(8, 8).Gap(4).AlignItems(ui.Center).Radius(m3.Large)
	if o.inset {
		row.Margin(0, pageGutter-8)
	}
	container := ui.Transparent
	if playing {
		container = sc.SecondaryContainer
	}
	var main, menuButton ui.Element
	hasMenu := false
	row.Children(func() {
		main = ui.ButtonBase(c.Key(key + "-activate"))
		hovered := row.Hovered()
		main.Grow(1).Basis(0).MinWidth(0).Height(rowHeight - 16).PaddingX(8).Gap(12).AlignItems(ui.Center).
			Radius(m3.Large).Cursor(ui.CursorPointer).Label(item.Title).Background(ui.Transparent)
		row.Background(m3.StateFill(container, sc.OnSurface, hovered && !playing, main.Pressed(), main.FocusVisible()))
		main.Children(func() {
			titleColour, subColour := sc.OnSurface, sc.OnSurfaceVariant
			if playing {
				titleColour, subColour = sc.OnSecondaryContainer, sc.OnSecondaryContainer.Alpha(0.8)
			}
			if o.number > 0 {
				ui.Row(c).Size(48, 48).Shrink(0).Center().Children(func() {
					switch {
					case playing && a.loading():
						m3.LoadingIndicatorIn(c, 24, sc.Primary)
					case playing:
						m3.Equalizer(c, 18, a.player.Playing(), sc.Primary)
					case hovered && isSong:
						ui.Icon(c, m3.IconPlay).FontSize(24).TextColor(sc.OnSurface)
					default:
						m3.Text(c, m3.BodyLarge, strconv.Itoa(o.number)).TextColor(sc.OnSurfaceVariant)
					}
				})
			} else {
				m3.Art(c, a.thumbs.bitmap(item.Thumbnail, 128), 48, artRadius(item, m3.Medium), func() {
					if isSong && (playing || hovered) {
						ui.Box(c).Fill().Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
							if playing && a.loading() {
								m3.LoadingIndicatorIn(c, 24, ui.RGB(255, 255, 255))
							} else if playing {
								m3.Equalizer(c, 18, a.player.Playing(), ui.RGB(255, 255, 255))
							} else {
								ui.Icon(c, m3.IconPlay).FontSize(24).TextColor(ui.RGB(255, 255, 255))
							}
						})
					}
					if isVideo(item) {
						m3.VideoBadge(c, 48)
					}
				})
			}
			ui.Column(c).Grow(1).MinWidth(0).Gap(0).Children(func() {
				title := m3.Text(c, m3.BodyLarge, item.Title).SingleLine().TextColor(titleColour)
				if playing {
					title.FontWeight(600)
				}
				if item.Subtitle != "" {
					m3.Text(c, m3.BodyMedium, item.Subtitle).SingleLine().TextColor(subColour)
				}
			})
			switch {
			case item.Duration != "":
				m3.Text(c, m3.BodyMedium, item.Duration).SingleLine().Width(48).TextAlign(ui.End).
					TextColor(subColour).Shrink(0).FontFeatures("tnum")
			case !isSong:
				ui.Icon(c, m3.IconChevronRight).FontSize(24).TextColor(sc.OnSurfaceVariant)
			}
		})
		if isSong && (hovered || main.FocusVisible() || a.trackMenuOpen && a.trackMenuKey == key) {
			menuButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
			})
			hasMenu = true
		}
	})
	if hasMenu && (menuButton.Clicked() || main.RightClicked()) {
		a.trackMenuKey, a.trackMenuOpen = key, true
	}
	if hasMenu && a.trackMenuKey == key {
		a.songMenu(c, menuButton, key, item)
	}
	if main.Clicked() {
		if o.directQueue {
			index := 0
			for i := range queue {
				if queue[i].VideoID == item.VideoID {
					index = i
					break
				}
			}
			a.playWithOptions(item, queue, index, youtube.UpNextOptions{}, o.source)
		} else {
			a.activate(item, queue)
		}
	}
}

// songMenu offers local queue actions and read-only actions for a track.
func (a *app) songMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem) {
	if a.trackMenuKey != key {
		return
	}
	m3.Menu(c, anchor, &a.trackMenuOpen, 232, func() {
		close := func() { a.trackMenuOpen = false }
		if artistID := artistID(item); artistID != "" {
			if m3.MenuItem(c, "Go to artist", m3.IconPerson).Clicked() {
				path := "/artist/" + url.PathEscape(artistID)
				a.details[path] = detail{kind: pageArtist}
				a.router.Push(path)
				close()
			}
		}
		if m3.MenuItem(c, "Play next", m3.IconNext).Clicked() {
			a.enqueue(item, true)
			close()
		}
		if m3.MenuItem(c, "Add to queue", m3.IconQueue).Clicked() {
			a.enqueue(item, false)
			close()
		}
		if m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard("https://music.youtube.com/watch?v=" + url.QueryEscape(item.VideoID))
			c.Toast("Link copied")
			close()
		}
	})
}

func artistID(item youtube.MusicItem) string {
	if item.VideoID != "" && (strings.HasPrefix(item.BrowseID, "UC") || strings.Contains(item.BrowseID, "privately_owned_artist")) {
		return item.BrowseID
	}
	return ""
}

// hero is the heading of an album, a playlist or an artist: its artwork large
// over a wash of the theme's colour, its name, and the buttons that play it.
func (a *app) hero(c *ui.Context) {
	sc := m3.Active().Scheme
	d := a.detail
	artRadius := m3.ExtraLargeInc
	if d.kind == pageArtist {
		artRadius = m3.Full
	}
	wash := sc.PrimaryContainer.Mix(sc.Surface, 0.45)
	ui.Row(c).Padding(8, pageGutter, 28).Gap(28).AlignItems(ui.End).Gradient(wash, sc.Surface, 180).Children(func() {
		art := m3.Art(c, a.thumbs.bitmap(d.art, 512), 220, artRadius)
		m3.Elevation(art, 2)
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			if label := heroLabel(d.kind); label != "" {
				m3.EmphasizedText(c, m3.LabelLarge, strings.ToUpper(label)).LetterSpacing(1.2).TextColor(sc.Primary)
			}
			m3.EmphasizedText(c, m3.DisplaySmall, d.title).MaxLines(2).TextColor(sc.OnSurface)
			if d.subtitle != "" {
				m3.Text(c, m3.BodyLarge, d.subtitle).MaxLines(2).TextColor(sc.OnSurfaceVariant)
			}
			ui.Box(c).Height(10)
			ui.Row(c).Gap(12).Children(func() {
				none := len(a.playable) == 0
				if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: m3.Medium56, Disabled: none, Key: "hero-play"}).Clicked() {
					a.playAll()
				}
				if m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: m3.Medium56, Disabled: none, Key: "hero-shuffle"}).Clicked() {
					a.shufflePage()
				}
			})
		})
	})
}

func heroLabel(kind string) string {
	switch kind {
	case pageAlbum:
		return "Album"
	case pagePlaylist:
		return "Playlist"
	case pageArtist:
		return "Artist"
	}
	return ""
}
