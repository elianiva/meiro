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
		a.sectionHeading(c, r, next, i == 0)
	case rowCards:
		a.cardShelf(c, r)
	case rowColumns:
		a.columnShelf(c, r)
	case rowGrid:
		a.cardGrid(c, r)
	case rowTrack:
		a.songRow(c, r.item, a.page.playable, songOptions{number: r.number(), list: r.shelf, position: r.position, inset: true})
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
		a.message(c, m3.IconError, "Something went wrong", firstLine(r.title), "Try again", a.retry)
	case rowEmpty:
		a.message(c, m3.IconMusicNote, r.title, "", "", nil)
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
				ui.Column(c).Key(start).Width(columnWidth).Shrink(0).Children(func() {
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
					}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(8).Right(8)
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
		m3.EmphasizedText(c, m3.TitleSmall, item.Title).Width(cardArt-8).SingleLine().TextColor(sc.OnSurface).Margin(0, 4)
		if item.Subtitle != "" {
			m3.Text(c, m3.BodySmall, item.Subtitle).Width(cardArt-8).SingleLine().TextColor(sc.OnSurfaceVariant).Margin(-6, 4, 0)
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
	var main, menuButton, menuAnchor, removeButton ui.Element
	hasMenu, hasAnchor, hasRemove := false, false, false
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
				a.art(c, item.Thumbnail, 128, 48, artRadius(item, m3.Medium), func() {
					if isSong && (playing || hovered) {
						ui.Box(c).Fill().Radius(artRadius(item, m3.Medium)).Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
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
		if isSong && (hovered || main.FocusVisible() || a.menu.opened(key)) {
			menuButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconMore, Label: "More options for " + item.Title, Key: key + "-menu",
			})
			hasMenu = true
		}
		// A row of the queue has its own remove button; the track playing
		// stays.
		if o.list == "queue" && o.position != a.queue.index {
			removeButton = m3.IconButton(c, m3.IconButtonSpec{
				Icon: m3.IconClose, Label: "Remove " + item.Title + " from the queue", Key: key + "-remove",
			})
			hasRemove = true
		}
	})
	if hasRemove && removeButton.Clicked() {
		a.removeFromQueue(o.position)
		return
	}
	if hasMenu {
		pointer := main.RightClicked() || menuButton.RightClicked()
		x, y, _ := main.PointerPosition()
		if menuButton.Clicked() || pointer {
			a.menu.show(key, pointer, x, y)
		}
	}
	if hasMenu && a.menu.owns(key) {
		a.songMenu(c, a.menu.anchor(menuButton, menuAnchor, hasAnchor), key, item, o)
	}
	if main.Clicked() {
		if o.directQueue {
			a.playFrom(item, queue, max(indexOf(queue, item.VideoID), 0), youtube.UpNextOptions{}, o.source)
		} else {
			a.activate(item, queue)
		}
	}
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
	if index > 0 && m3.MenuItem(c, "Move up", m3.IconExpandLess).Clicked() {
		a.shiftInQueue(index, -1)
		close()
	}
	if index < q.len()-1 && m3.MenuItem(c, "Move down", m3.IconExpandMore).Clicked() {
		a.shiftInQueue(index, 1)
		close()
	}
	if m3.MenuItem(c, "Remove from queue", m3.IconClose).Clicked() {
		a.removeFromQueue(index)
		close()
	}
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
	played, shuffled := false, false
	card.Children(func() {
		a.art(c, item.Thumbnail, 256, 132, artRadius(item, m3.LargeIncreased), func() {
			if a.isOpening(key) {
				ui.Box(c).Fill().Radius(artRadius(item, m3.LargeIncreased)).Center().Background(sc.Scrim.Alpha(0.45)).Children(func() {
					m3.LoadingIndicatorIn(c, 28, ui.RGB(255, 255, 255))
				})
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
	})
	switch {
	case played:
		a.playCollection(item, key, false)
	case shuffled:
		a.playCollection(item, key, true)
	case card.Clicked():
		// The buttons are inside the card, which receives their presses too:
		// the switch above is what keeps a press on one from opening the
		// page under it.
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
	ui.Row(c).Padding(8, pageGutter, 28).Gap(28).AlignItems(ui.End).Gradient(wash, sc.Surface, 180).Children(func() {
		art := a.art(c, d.art, 448, 220, artRadius)
		m3.Elevation(c, art, 2)
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			if label := heroLabel(r.target); label != "" {
				m3.EmphasizedText(c, m3.LabelLarge, strings.ToUpper(label)).LetterSpacing(1.2).TextColor(sc.Primary)
			}
			m3.EmphasizedText(c, m3.DisplaySmall, d.title).MaxLines(2).TextColor(sc.OnSurface)
			if d.subtitle != "" {
				m3.Text(c, m3.BodyLarge, d.subtitle).MaxLines(2).TextColor(sc.OnSurfaceVariant)
			}
			ui.Box(c).Height(10)
			ui.Row(c).Gap(12).Children(func() {
				none := len(a.page.playable) == 0
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
