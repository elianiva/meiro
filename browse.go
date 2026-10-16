package main

import (
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// The sizes of what a shelf holds, in DIPs.
const (
	cardWidth   = 176
	cardArt     = cardWidth - 16
	cardGap     = 4
	columnWidth = 392
	rowHeight   = 64
)

// pageList builds the rows of the page in a list that builds only those in
// view, so a page of hundreds of songs is as light as one of ten.
func (a *app) pageList(c *ui.Context) {
	a.syncRows()
	rows := a.page.rows
	// A button in a row can reload the page, and empty the rows, while the list
	// still looks for the row that held the focus.
	a.list.Key = func(i int) any {
		if i >= len(rows) {
			return nil
		}
		return rows[i].key()
	}
	a.list.Label = func(i int) string {
		if i >= len(rows) {
			return ""
		}
		if r := rows[i]; r.kind == rowTrack {
			return r.item.Title
		}
		return rows[i].title
	}
	list := ui.List(c, a.list, len(rows), func(i int) {
		a.rowView(c, rows, i)
	}).Grow(1).Padding(0, 0, a.clearance())
	// The grid's lines depend on the width, which is known only once the list
	// has been laid out: plan the lines again when it turns out different.
	r, _ := a.route()
	a.listWidth = list.Bounds().W
	if a.gridColumns(r) != a.page.rowsKey.columns {
		c.Invalidate()
	}
	// A search shows the pages of its results as its list reaches the end of
	// what it has, the way YouTube Music does: where the list ended up is what
	// asks for the next one. The pages that keep the button that asks leave
	// this to the click.
	if r.id == routeSearch && a.list.AtEnd() {
		if s := a.pageState(); s.more != "" && s.moreErr == "" {
			a.loadMore()
		}
	}
}

// rowView builds one row of the page.
func (a *app) rowView(c *ui.Context, rows []row, i int) {
	r := rows[i]
	switch r.kind {
	case rowHero:
		a.hero(c)
	case rowTopResult:
		a.topResult(c, r.item)
	case rowHeading:
		next := i+1 < len(rows) && (rows[i+1].kind == rowCards || rows[i+1].kind == rowColumns)
		if r.shelf == "playlist-sort-header" {
			sc := m3.Of(c).Scheme
			ui.Row(c).Padding(8, pageGutter-8, 8, pageGutter).AlignItems(ui.Center).Children(func() {
				m3.EmphasizedText(c, m3.TitleLarge, r.title).SingleLine().Grow(1).MinWidth(0).TextColor(sc.OnSurface)
				a.playlistSort(c)
			})
		} else {
			a.sectionHeading(c, r, next, i == 0)
		}
	case rowCards:
		a.cardShelf(c, r)
	case rowColumns:
		a.columnShelf(c, r)
	case rowGrid:
		a.cardGrid(c, r)
	case rowTrack:
		a.songRow(c, r.item, a.page.playable, songOptions{number: r.number(), list: r.shelf, position: r.position, track: r.track, inset: true})
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
	case rowStreaming:
		ui.Row(c).Height(96).Justify(ui.Center).AlignItems(ui.Center).Children(func() {
			m3.LoadingIndicator(c, 40, true)
		})
	case rowError:
		if r.offline {
			a.message(c, m3.IconError, "You are offline", "Check your connection, then retry.", "Retry", a.retry)
		} else {
			a.message(c, m3.IconError, "Could not load this page", firstLine(r.title), "Retry", a.retry)
		}
	case rowEmpty:
		a.message(c, m3.IconMusicNote, r.title, r.body, "Retry", a.retry)
	}
}

// carousel returns the scroll of the shelf with a key.
func (a *app) carousel(key string) *m3.CarouselState {
	return a.carousels.GetOrPut(key, func() *m3.CarouselState { return &m3.CarouselState{} })
}

// sectionHeading names a section of a page, with arrows that page its shelf.
func (a *app) sectionHeading(c *ui.Context, r row, paged, first bool) {
	sc := m3.Of(c).Scheme
	top := float32(28)
	if first {
		top = 4
	}
	ui.Row(c).Padding(top, pageGutter-8, 8, pageGutter).Gap(8).AlignItems(ui.Center).Children(func() {
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

// cardShelf is a row of cards that scrolls sideways. Only the cards in view
// are built; what is off view keeps its width as a spacer, so the shelf
// scrolls exactly as it did when every card was built.
func (a *app) cardShelf(c *ui.Context, r row) {
	const gap = 4
	st := a.carousel(r.shelf)
	again := st.NeedsMeasurement()
	first, last := st.VisibleRange(len(r.items), cardWidth, gap, pageGutter-8, 1)
	ui.Column(c).Children(func() {
		m3.Carousel(c, st, r.shelf, gap, pageGutter-8, func() {
			shelfSpacer(c, float32(first)*(cardWidth+gap)-gap)
			for i := first; i < last; i++ {
				a.card(c, r.items[i], r.queue, r.shelf, i)
			}
			shelfSpacer(c, float32(len(r.items)-last)*(cardWidth+gap)-gap)
		})
	})
	if again {
		// The first frame planned from a viewport that is not measured yet;
		// the next one fills the shelf with the width it now knows.
		c.Invalidate()
	}
}

// gridColumns is how many cards fit across a line of a page's grid, given the
// width the list had in the last frame: a line is as many cards as the page
// is wide. It is zero for a page without a grid.
func (a *app) gridColumns(r *route) int {
	if !r.grid {
		return 0
	}
	width := a.listWidth
	if width <= 0 {
		width = 1000
	}
	return max(1, int((gridInner(width)+cardGap)/(cardWidth+cardGap)))
}

// gridInner is the width of a grid's lines inside a list of the given width.
func gridInner(width float32) float32 {
	return width - 2*(pageGutter-8)
}

// gridGap is the space between the cards of a line of columns cards: the
// leftover width is shared out between them, so every line, the last one
// too, ends flush with the page's edge as the shelves above it do.
func gridGap(width float32, columns int) float32 {
	if columns < 2 {
		return cardGap
	}
	return max(cardGap, (gridInner(width)-float32(columns)*cardWidth)/float32(columns-1))
}

// cardGrid is one line of cards of a grid, which wraps onto as many lines as
// the page is wide, so the whole of it is in view without scrolling sideways.
func (a *app) cardGrid(c *ui.Context, r row) {
	width := a.listWidth
	if width <= 0 {
		width = 1000
	}
	gap := gridGap(width, a.page.rowsKey.columns)
	ui.Row(c).Gap(gap).AlignItems(ui.Start).Padding(0, pageGutter-8, cardGap).Children(func() {
		for i, item := range r.items {
			a.card(c, item, r.queue, r.shelf, r.position+i)
		}
	})
}

// shelfSpacer stands in for the items of a shelf that are off view, so the
// shelf keeps the width all of them would have taken. A width of zero or
// less is nothing to stand in for.
func shelfSpacer(c *ui.Context, width float32) {
	if width > 0 {
		ui.Box(c).Width(width).Shrink(0)
	}
}

// columnShelf is songs in columns of four, which scroll sideways: the shelf
// of quick picks. As the card shelf, it builds only the columns in view.
func (a *app) columnShelf(c *ui.Context, r row) {
	const gap = 8
	st := a.carousel(r.shelf)
	again := st.NeedsMeasurement()
	groups := (len(r.items) + 3) / 4
	first, last := st.VisibleRange(groups, columnWidth, gap, pageGutter-8, 1)
	ui.Column(c).Children(func() {
		m3.Carousel(c, st, r.shelf, gap, pageGutter-8, func() {
			shelfSpacer(c, float32(first)*(columnWidth+gap)-gap)
			for g := first; g < last; g++ {
				start := g * 4
				group := r.items[start:min(start+4, len(r.items))]
				ui.Column(c.Key(start)).Width(columnWidth).Shrink(0).Children(func() {
					for i, item := range group {
						a.songRow(c, item, r.queue, songOptions{list: r.shelf, position: start + i})
					}
				})
			}
			shelfSpacer(c, float32(groups-last)*(columnWidth+gap)-gap)
		})
	})
	if again {
		// As the card shelf: plan again once the viewport is known.
		c.Invalidate()
	}
}

// itemKey identifies an item, under a prefix that names where it is drawn.
func itemKey(prefix string, item youtube.MusicItem) string {
	target := item.Target()
	id := target.ID
	for _, other := range []string{item.ID, item.BrowseID, item.PlaylistID} {
		if id == "" {
			id = other
		}
	}
	if id == "" {
		id = string(item.Kind) + "\x00" + item.Thumbnail + "\x00" + item.Duration
	}
	return prefix + ":" + strconv.Itoa(int(target.Kind)) + string(item.Kind) + "\x00" + id
}

func cardKey(item youtube.MusicItem, list string, position int) string {
	return itemKey("card", item) + "\x00" + list + "#" + strconv.Itoa(position)
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
	if item.Target().Kind == youtube.TargetArtist {
		return m3.Full
	}
	return square
}

// art shows the artwork at url in a square of size points with rounded
// corners. The picture is asked for at pixels across and already rounded, so
// drawing it needs no clip; an overlay that covers the corners rounds itself.
func (a *app) art(c *ui.Context, url string, pixels int, size, radius float32, overlays ...func()) ui.Element {
	return m3.Art(c, a.thumbs.Bitmap(url, pixels, artwork.Corner(radius, size)), size, radius, overlays...)
}

// avatar is a person's picture on a disc of size points, rounded like art.
func (a *app) avatar(c *ui.Context, name, url string, pixels int, size float32) {
	m3.Avatar(c, name, a.thumbs.Bitmap(url, pixels, artwork.Corner(m3.Full, size)), size)
}

// card shows an album, a playlist, an artist or a song as artwork over its
// name. A play button rises over the artwork under the pointer.
func (a *app) card(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem, list string, position int) {
	sc := m3.Of(c).Scheme
	kind := item.Target().Kind
	key := cardKey(item, list, position)
	card := ui.ButtonBase(c.Key(key))
	hovered := card.Hovered()
	card.Column().AlignItems(ui.Start).Width(cardWidth).Shrink(0).Padding(8).Gap(10).Radius(m3.ExtraLarge).Cursor(ui.CursorPointer).
		Background(m3.Layer(sc.Surface, sc.OnSurface, hoverOpacity(hovered || card.FocusVisible()))).
		Label(item.Title)
	played, menuClicked := false, false
	var menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	card.Children(func() {
		radius := artRadius(item, m3.LargeIncreased)
		opening := a.isOpening(key)
		a.art(c, item.Thumbnail, 256, cardArt, radius, func() {
			if isNewPlaylist(item) {
				// The tile has no artwork of its own: the plus covers the
				// panel a missing one stands in with.
				ui.Box(c).Size(cardArt, cardArt).Radius(radius).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
					Background(sc.SurfaceContainerHighest).Center().Children(func() {
					ui.Icon(c, m3.IconAdd).FontSize(max(cardArt*0.4, 18)).TextColor(sc.OnSurfaceVariant)
				})
				return
			}
			if item.IsVideo() {
				m3.VideoBadge(c, cardArt)
			}
			if opening {
				ui.Box(c).Size(40, 40).Radius(m3.Full).Center().Background(sc.PrimaryContainer).
					Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(8).Bottom(8).Children(func() {
					m3.LoadingIndicatorIn(c, 28, sc.OnPrimaryContainer)
				})
				return
			}
			if hovered || card.FocusVisible() || a.menu.opened(key+"-menu") {
				if kind != youtube.TargetNone {
					menuButton = m3.IconButton(c, m3.IconButtonSpec{
						Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
						// On a picture the dots need a ground of their own.
						Container: sc.SurfaceContainerHighest.Alpha(0.85),
					}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(0).Right(0)
					hasMenu = true
				}
			}
			if hovered && (kind == youtube.TargetTrack || kind == youtube.TargetAlbum || kind == youtube.TargetPlaylist) {
				fab := m3.FAB(c, m3.FABSpec{Icon: m3.IconPlay, Size: m3.FABSmall, Tone: m3.FABPrimary, Key: "play"}).
					Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(8).Bottom(8)
				enter := m3.Move(m3.SpatialFast)
				enter.Enter = &ui.Motion{Y: 8, Scale: 0.85}
				fab.Transition(enter)
				if fab.Clicked() {
					played = true
				}
			}
		})
		m3.EmphasizedText(c, m3.TitleSmall, item.Title).Width(cardArt).SingleLine().TextColor(sc.OnSurface)
		if item.Subtitle != "" {
			m3.Text(c, m3.BodySmall, item.Subtitle).Width(cardArt).SingleLine().TextColor(sc.OnSurfaceVariant).Margin(-6, 0, 0)
		}
		// A menu the pointer opened points at the pointer; one the menu
		// button opened points at the button.
		menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key+"-menu")
	})
	if hasMenu {
		pointer := card.RightClicked() || menuButton.RightClicked()
		x, y, _ := card.PointerPosition()
		if menuButton.Clicked() || pointer {
			a.menu.show(key+"-menu", pointer, x, y)
			menuClicked = true
		}
	}
	if hasMenu && a.menu.owns(key+"-menu") {
		a.cardMenu(c, a.menu.anchor(menuButton, menuAnchor, hasAnchor), key+"-menu", item, queue)
	}
	switch {
	case menuClicked:
		// The card's button also receives this pointer event. Opening its menu
		// must not activate the card underneath it.
	case played:
		a.playCollection(item, key, false)
	case card.Clicked() && isNewPlaylist(item):
		a.openNewPlaylist(youtube.MusicItem{})
	case card.Clicked():
		a.activate(item, queue)
	}
}

// cardMenu offers actions that make sense for a card. Songs share their
// queue and artist actions with song rows; other cards can open their page or
// copy its link.
func (a *app) cardMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem, queue []youtube.MusicItem) {
	if !a.menu.owns(key) {
		return
	}
	target := item.Target()
	if target.Kind == youtube.TargetTrack {
		a.songMenu(c, anchor, key, item, songOptions{})
		return
	}
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		label := ""
		icon := m3.IconChevronRight
		switch target.Kind {
		case youtube.TargetAlbum:
			label, icon = "Open album", m3.IconAlbum
		case youtube.TargetPlaylist:
			label, icon = "Open playlist", m3.IconQueue
		case youtube.TargetArtist:
			label, icon = "Go to artist", m3.IconPerson
		case youtube.TargetTrack, youtube.TargetNone:
		}
		if label != "" && m3.MenuItem(c, label, icon).Clicked() {
			a.activate(item, queue)
			close()
		}
		if target.Kind == youtube.TargetPlaylist && m3.MenuItem(c, "Shuffle play", m3.IconShuffle).Clicked() {
			a.playCollection(item, key, true)
			close()
		}
		if library := item.InLibrary(); a.canSave(library) {
			label, icon := libraryLabel(library)
			if m3.MenuItem(c, label, icon).Clicked() {
				a.toggleLibrary(library, item.Title)
				close()
			}
		}
		if (target.Kind == youtube.TargetAlbum || target.Kind == youtube.TargetPlaylist) && m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
			a.startRadio(item, key)
			close()
		}
		if target.ID != "" && m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.BrowseURL(target.ID))
			c.Toast("Link copied")
			close()
		}
	})
}

// songOptions shape a row of a song.
type songOptions struct {
	// number shows a place in the list where the artwork would be.
	number int
	// list names the list the row belongs to, so that the same song in two
	// lists, as in the queue behind a page of quick picks, is two rows with
	// two keys rather than one shared menu.
	list     string
	position int
	// track is the exact occurrence in the queue. It differs from position
	// when a playlist search hides entries before this row.
	track int
	// inset puts the row in from the edges of the page, as the rows of a
	// page of songs are; rows in a shelf sit flush.
	inset bool
	// directQueue plays the row's song from the list it is in, named source,
	// rather than from the page.
	directQueue bool
	source      string
}

// songKey identifies a row of a song in its list, for the element tree and
// for the menu state that a row opens.
func songKey(item youtube.MusicItem, o songOptions) string {
	return itemKey("song", item) + "\x00" + strconv.Itoa(o.number) + "\x00" + o.list + "#" + strconv.Itoa(o.position)
}

// songRow shows a song, an album, an artist or a playlist as a row: artwork,
// name over details, and the length. The song playing is picked out in a
// tonal container with dancing bars.
func (a *app) songRow(c *ui.Context, item youtube.MusicItem, queue []youtube.MusicItem, o songOptions) {
	sc := m3.Of(c).Scheme
	isSong := item.IsTrack()
	playing := isSong && item.VideoID == a.current.VideoID
	key := songKey(item, o)
	row := ui.Row(c.Key(key))
	row.Height(rowHeight).Shrink(0).Padding(8, 8).Gap(4).AlignItems(ui.Center).Radius(m3.Large)
	if o.inset {
		row.Margin(0, pageGutter-8)
	}
	container := ui.Transparent
	if playing {
		container = sc.SecondaryContainer
	}
	var main, menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	row.Children(func() {
		main = ui.ButtonBase(c.Key(key + "-activate"))
		hovered := row.Hovered()
		main.Grow(1).Basis(0).MinWidth(0).Height(rowHeight - 16).Gap(12).AlignItems(ui.Center).
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
				a.art(c, item.Thumbnail, 128, 48, artRadius(item, m3.Medium), func() {
					if isSong && (playing || hovered) {
						ui.Box(c).Fill().Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).Radius(artRadius(item, m3.Medium)).Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
							switch {
							case playing && a.loading():
								m3.LoadingIndicatorIn(c, 24, ui.RGB(255, 255, 255))
							case playing:
								m3.Equalizer(c, 18, a.player.Playing(), ui.RGB(255, 255, 255))
							default:
								ui.Icon(c, m3.IconPlay).FontSize(24).TextColor(ui.RGB(255, 255, 255))
							}
						})
					}
					if item.IsVideo() {
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
			// A menu the pointer opened points at the pointer; one the menu
			// button opened points at the button.
			menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key)
		})
		if hovered || main.FocusVisible() || a.menu.opened(key) {
			menuButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
			})
			hasMenu = true
		} else {
			// The button's place stays, so the length does not move when the
			// pointer arrives.
			rowButtonSlot(c)
		}
		// A row of the queue has its own remove button; the track playing
		// stays.
		if o.list == "queue" {
			// Two buttons side by side need their shapes 8 apart, or the
			// pointer targets overlap.
			ui.Box(c).Size(4, 0).Shrink(0)
			if o.position != a.queue.index {
				m3.IconButton(c, m3.IconButtonSpec{
					Icon: m3.IconClose, Label: "Remove " + item.Title + " from the queue", Key: key + "-remove",
				}).OnClick(func() { a.removeFromQueue(o.position) })
			} else {
				rowButtonSlot(c)
			}
		}
	})
	if hasMenu {
		pointer := main.RightClicked() || menuButton.RightClicked()
		x, y, _ := main.PointerPosition()
		if menuButton.Clicked() || pointer {
			a.menu.show(key, pointer, x, y)
		}
	}
	if hasMenu && a.menu.owns(key) {
		anchor := a.menu.anchor(menuButton, menuAnchor, hasAnchor)
		if isSong {
			a.songMenu(c, anchor, key, item, o)
		} else {
			// A row of an album, an artist or a playlist is a row of
			// something that opens a page, as a card in a shelf is, and
			// offers what that card's menu does.
			a.cardMenu(c, anchor, key, item, queue)
		}
	}
	if main.Clicked() {
		switch {
		case o.directQueue:
			index := o.track
			if index < 0 {
				index = indexOf(queue, item.VideoID)
			}
			a.playFrom(item, queue, max(index, 0), youtube.UpNextOptions{}, o.source)
		case o.track >= 0:
			a.activateAt(item, queue, o.track)
		default:
			a.activate(item, queue)
		}
	}
}

// rowButtonSlot holds the place of a row's icon button while it is not shown.
func rowButtonSlot(c *ui.Context) {
	ui.Box(c).Size(32, 32).Shrink(0)
}

// songMenu offers local queue actions and read-only actions for a track.
func (a *app) songMenu(c *ui.Context, anchor ui.Element, key string, item youtube.MusicItem, o songOptions) {
	if !a.menu.owns(key) {
		return
	}
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		if artistID := item.ArtistID(); artistID != "" {
			if m3.MenuItem(c, "Go to artist", m3.IconPerson).Clicked() {
				a.openTarget(youtube.Target{Kind: youtube.TargetArtist, ID: artistID}, detail{})
				close()
			}
		}
		if o.list == "queue" {
			a.queueMenuItems(c, o.position, close)
		} else {
			if m3.MenuItem(c, "Play next", m3.IconNext).Clicked() {
				a.enqueue(item, true)
				close()
			}
			if m3.MenuItem(c, "Add to queue", m3.IconQueue).Clicked() {
				a.enqueue(item, false)
				close()
			}
		}
		if a.canLike(item) {
			label, icon := likeLabel(item)
			if m3.MenuItem(c, label, icon).Clicked() {
				a.toggleLike(item)
				close()
			}
		}
		if m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
			a.startRadio(item, key)
			close()
		}
		if item.VideoID != "" && m3.MenuItem(c, "Add to playlist", m3.IconLibrary).Clicked() {
			a.openAddToPlaylist(item)
			close()
		}
		if r, playlistID := a.route(); item.VideoID != "" && item.PlaylistSetID != "" && r.id == routePlaylist && m3.MenuItem(c, "Remove from playlist", m3.IconClose).Clicked() {
			a.removeFromPlaylist(playlistID, item)
			close()
		}
		if m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.WatchURL(item.VideoID))
			c.Toast("Link copied")
			close()
		}
	})
}

// queueMenuItems are the edits a row of the queue offers for the track at a
// place in it. The track playing stays where it is.
func (a *app) queueMenuItems(c *ui.Context, index int, close func()) {
	q := &a.queue
	if index == q.index || index >= q.len() {
		return
	}
	if index > 0 {
		m3.MenuItem(c, "Move up", m3.IconExpandLess).OnClick(func() {
			a.shiftInQueue(index, -1)
			close()
		})
	}
	if index < q.len()-1 {
		m3.MenuItem(c, "Move down", m3.IconExpandMore).OnClick(func() {
			a.shiftInQueue(index, 1)
			close()
		})
	}
	m3.MenuItem(c, "Remove from queue", m3.IconClose).OnClick(func() {
		a.removeFromQueue(index)
		close()
	})
}

// topResult is the card a search's results lead with: the artwork of what the
// search found, its name, and what can be done with it. Selecting it opens the
// page it names, or plays it; the play and shuffle buttons do that without
// opening the page first.
func (a *app) topResult(c *ui.Context, item youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	target := item.Target()
	key := "top-result"
	card := ui.ButtonBase(c.Key(key))
	card.Padding(8).Margin(0, pageGutter-8).Gap(20).Radius(m3.ExtraLarge).AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(item.Title).
		Background(m3.StateFill(ui.Transparent, sc.OnSurface, card.Hovered(), card.Pressed(), card.FocusVisible()))
	played, shuffled, menuClicked := false, false, false
	hovered := card.Hovered()
	var menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	card.Children(func() {
		a.art(c, item.Thumbnail, 256, 132, artRadius(item, m3.LargeIncreased), func() {
			if a.isOpening(key) {
				// The scrim covers the artwork: attached, it leaves the flow
				// the picture is laid out in.
				ui.Box(c).Size(132, 132).Radius(artRadius(item, m3.LargeIncreased)).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
					Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
					m3.LoadingIndicatorIn(c, 28, ui.RGB(255, 255, 255))
				})
				return
			}
			// The menu sits on the artwork, as a card's does.
			if hovered || card.FocusVisible() || a.menu.opened(key+"-menu") {
				if target.Kind != youtube.TargetNone {
					menuButton = m3.IconButton(c, m3.IconButtonSpec{
						Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
						// On a picture the dots need a ground of their own.
						Container: sc.SurfaceContainerHighest.Alpha(0.85),
					}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(0).Right(0)
					hasMenu = true
				}
			}
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			m3.EmphasizedText(c, m3.HeadlineSmall, item.Title).MaxLines(2).TextColor(sc.OnSurface)
			if item.Subtitle != "" {
				m3.Text(c, m3.BodyMedium, item.Subtitle).SingleLine().TextColor(sc.OnSurfaceVariant)
			}
			switch target.Kind {
			case youtube.TargetTrack:
				ui.Row(c).Padding(4, 0, 0).Children(func() {
					if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: m3.Small40, Key: key + "-play"}).Clicked() {
						played = true
					}
				})
			case youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist:
				ui.Row(c).Gap(8).Padding(4, 0, 0).Children(func() {
					if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: m3.Small40, Key: key + "-play"}).Clicked() {
						played = true
					}
					if m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: m3.Small40, Key: key + "-shuffle"}).Clicked() {
						shuffled = true
					}
				})
			case youtube.TargetNone:
			}
		})
		// A menu the pointer opened points at the pointer; one the menu
		// button opened points at the button.
		menuAnchor, hasAnchor = a.menu.pointerAnchor(c, key+"-menu")
	})
	if hasMenu {
		pointer := card.RightClicked() || menuButton.RightClicked()
		x, y, _ := card.PointerPosition()
		if menuButton.Clicked() || pointer {
			a.menu.show(key+"-menu", pointer, x, y)
			menuClicked = true
		}
	}
	if hasMenu && a.menu.owns(key+"-menu") {
		a.cardMenu(c, a.menu.anchor(menuButton, menuAnchor, hasAnchor), key+"-menu", item, a.page.playable)
	}
	switch {
	case menuClicked:
		// The buttons inside the card receive their presses too: the switch
		// is what keeps a press on the menu from opening the page under it.
	case played:
		a.playCollection(item, key, false)
	case shuffled:
		a.playCollection(item, key, true)
	case card.Clicked():
		a.activate(item, a.page.playable)
	}
}

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
				Background(ui.Transparent).Label("More options for " + d.title)
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
			if r.id == routePlaylist && a.detailSearchOpen {
				ui.Box(c).Height(10)
				ui.Row(c).Padding(0, 24).Children(func() { a.playlistSearch(c) })
			}
			ui.Box(c).Height(10)
			ui.Row(c).Gap(8).Wrap().Children(func() {
				none := len(a.page.playable) == 0
				if m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay, Size: buttons, Disabled: none, Key: "hero-play"}).Clicked() {
					a.playAll()
				}
				if m3.Button(c, m3.ButtonSpec{Label: "Shuffle", Icon: m3.IconShuffle, Kind: m3.Tonal, Size: buttons, Disabled: none, Key: "hero-shuffle"}).Clicked() {
					a.shufflePage()
				}
				a.libraryButton(c)
				a.subscribeButton(c)
				if r.id == routePlaylist && !a.detailSearchOpen {
					a.playlistSearch(c)
				}
			})
		})
	})
	if catcher.RightClicked() {
		x, y, _ := catcher.PointerPosition()
		a.menu.show("hero", true, x, y)
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

// heroMenu offers what YouTube Music's menu on the artwork of an album, a
// playlist or an artist does: start its radio, shuffle it, or copy the page's
// link.
func (a *app) heroMenu(c *ui.Context, anchor ui.Element) {
	if !a.menu.owns("hero") {
		return
	}
	_, id := a.route()
	m3.Menu(c, anchor, &a.menu.open, 232, func() {
		close := func() { a.menu.close() }
		if m3.MenuItem(c, "Start radio", m3.IconShuffle).Clicked() {
			a.startRadioPage()
			close()
		}
		if m3.MenuItem(c, "Shuffle play", m3.IconShuffle).Clicked() {
			a.shufflePage()
			close()
		}
		if id != "" && m3.MenuItem(c, "Copy link", m3.IconCopy).Clicked() {
			c.WriteClipboard(youtube.BrowseURL(id))
			c.Toast("Link copied")
			close()
		}
	})
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
	// Animate the parent width so the search expands from its icon button
	// without relying on Grow inside the shrink-wrapped action group.
	control := ui.Row(c.Key("playlist-search")).MinWidth(0).Grow(1).Shrink(1)
	target := float32(0)
	if a.detailSearchOpen {
		target = 1
	}
	grown := m3.Animate(control, "open", target, m3.SpatialFast)
	if !a.detailSearchOpen && grown < 0.02 {
		control.Width(a.detailSearchButton())
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
	control.Width(a.detailSearchButton() + (maxWidth-a.detailSearchButton())*min(max(grown, 0), 1))
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

// playlistSort draws one compact field-and-direction control above the list.
func (a *app) playlistSort(c *ui.Context) {
	label := "Sort: " + a.detailSort.label()
	if a.detailSort.active() {
		label += " · " + a.detailSort.directionLabel()
	}
	rotation := float32(0)
	if a.detailSortOpen {
		rotation = 180
	}
	anchor := m3.Button(c, m3.ButtonSpec{Label: label, Icon: m3.IconExpandMore, IconRotation: rotation, Kind: m3.Outlined, Size: m3.Medium56, Key: "playlist-sort"})
	if anchor.Clicked() {
		a.detailSortOpen = true
	}
	if a.detailSortOpen {
		m3.Menu(c, anchor, &a.detailSortOpen, 280, func() {
			choices := []struct {
				field playlistSortField
				label string
			}{
				{playlistSortOriginal, "Original order"},
				{playlistSortTitle, "Title, A–Z"},
				{playlistSortArtist, "Artist, A–Z"},
				{playlistSortDuration, "Duration, shortest first"},
			}
			for _, choice := range choices {
				item := m3.MenuItem(c, choice.label, nil)
				if item.Clicked() {
					a.detailSort.choose(choice.field)
					a.detailSortOpen = false
				}
			}
			if a.detailSort.active() {
				label := "Sort descending"
				if a.detailSort.desc {
					label = "Sort ascending"
				}
				if m3.MenuItem(c, label, nil).Clicked() {
					a.detailSort.desc = !a.detailSort.desc
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
	case youtube.TargetTrack, youtube.TargetNone:
	}
	return ""
}
