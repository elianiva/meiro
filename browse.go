package main

import (
	"math"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

const (
	cardWidth    = 160
	cardArt      = cardWidth
	cardGap      = 4
	shelfGap     = 8
	columnWidth  = 392
	minTopColumn = 340
	rowHeight    = 64
)

func (a *app) pageList(c *ui.Context) {
	a.syncRows()
	rows := a.page.rows
	a.list.Key = func(i int) any {
		if i >= len(rows) {
			return nil
		}
		return rows[i].key()
	}
	a.list.Header = func(i int) bool {
		return i < len(rows) && rows[i].shelf == "playlist-sort-header"
	}
	a.list.Label = func(i int) string {
		if i >= len(rows) {
			return ""
		}
		if r := rows[i]; r.kind == rowTrack {
			return r.title
		}
		return rows[i].title
	}
	list := ui.List(c, a.list, len(rows), func(i int) {
		a.rowView(c, rows, i)
	}).Grow(1).Padding(0, 0, a.clearance()).TrackScroll(a.page.scroll).HandleInput(a.pullScroll)
	r, _ := a.route()
	a.listWidthLastFrame = list.Bounds().W
	first, _ := a.list.Visible()
	a.detailCompact = r.isDetail() && first > 0
	if a.gridColumns(r) != a.page.rowsKey.columns {
		c.Invalidate()
	}
	if r.id == routeSearch && a.list.AtEnd() {
		if s := a.pageState(); s.more != "" && s.moreErr == "" {
			a.loadMore()
		}
	}
}

func (a *app) rowView(c *ui.Context, rows []row, i int) {
	r := rows[i]
	switch r.kind {
	case rowHero:
		a.hero(c)
	case rowTopResult:
		if len(r.items) > 0 {
			a.topResult(c, r.items[0])
		}
	case rowHeading:
		next := i+1 < len(rows) && (rows[i+1].kind == rowCards || rows[i+1].kind == rowColumns)
		if r.shelf == "playlist-sort-header" {
			sc := m3.Of(c).Scheme
			ui.Row(c).Padding(8, pageGutter-8, 8, pageGutter).Gap(12).AlignItems(ui.Center).Background(sc.Surface).Children(func() {
				m3.EmphasizedText(c, m3.TitleLarge, r.title).SingleLine().Grow(1).MinWidth(0).TextColor(sc.OnSurface)
				a.playlistSearch(c)
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
		queue := r.queue
		if queue == nil {
			queue = a.page.playable
		}
		if item, ok := r.trackItem(queue); ok {
			a.songRow(c, item, queue, songOptions{number: r.number(), list: r.shelf, position: r.position, track: r.track, inset: true, calls: r.calls, callsSet: true})
		}
	case rowSimilar:
		sc := m3.Of(c).Scheme
		ui.Column(c).Padding(24, pageGutter, 4, pageGutter).Children(func() {
			ui.Box(c).Height(1).Background(sc.OutlineVariant)
		})
		a.sectionHeading(c, r, false, false)
	case rowMore:
		ui.Row(c).Justify(ui.Center).Padding(16).Children(func() {
			if m3.Button(c, m3.ButtonSpec{Label: r.title, Kind: m3.Tonal, Size: m3.Medium56, Key: "more"}).Clicked() {
				if r.shelf == "similar" {
					a.moreSimilar()
					return
				}
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

func (a *app) carousel(key string) *m3.CarouselState {
	return a.carousels.GetOrPut(key, func() *m3.CarouselState { return &m3.CarouselState{} })
}

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
		if r.playAll && len(r.queue) > 0 {
			if m3.Button(c, m3.ButtonSpec{Label: "Play all", Kind: m3.Outlined, Size: m3.Small40, Key: r.shelf + "-play-all"}).Clicked() {
				a.playShelf(r)
			}
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

func (a *app) cardShelf(c *ui.Context, r row) {
	const gap = shelfGap
	st := a.carousel(r.shelf)
	again := st.NeedsMeasurement()
	first, last := st.VisibleRange(len(r.items), cardWidth, gap, pageGutter-8, 1)
	ui.Column(c).Children(func() {
		m3.Carousel(c, st, r.shelf, gap, pageGutter-8, func() {
			shelfSpacer(c, float32(first)*(cardWidth+gap)-gap)
			for i := first; i < last; i++ {
				a.card(c, r.items[i], r.cards[i], r.queue)
			}
			shelfSpacer(c, float32(len(r.items)-last)*(cardWidth+gap)-gap)
		})
	})
	if again {
		c.Invalidate()
	}
}

func (a *app) gridColumns(r *route) int {
	if !r.grid {
		return 0
	}
	width := a.listWidthLastFrame
	if width <= 0 {
		width = 1000
	}
	return max(1, int((gridInner(width)+cardGap)/(cardWidth+cardGap)))
}

func gridInner(width float32) float32 {
	return width - 2*(pageGutter-8)
}

func gridGap(width float32, columns int) float32 {
	if columns < 2 {
		return cardGap
	}
	return max(cardGap, (gridInner(width)-float32(columns)*cardWidth)/float32(columns-1))
}

func (a *app) cardGrid(c *ui.Context, r row) {
	width := a.listWidthLastFrame
	if width <= 0 {
		width = 1000
	}
	gap := gridGap(width, a.page.rowsKey.columns)
	ui.Row(c).Gap(gap).AlignItems(ui.Start).Padding(0, pageGutter-8, cardGap).Children(func() {
		for i, item := range r.items {
			a.card(c, item, r.cards[i], r.queue)
		}
	})
}

func shelfSpacer(c *ui.Context, width float32) {
	if width > 0 {
		ui.Box(c).Width(width).Shrink(0)
	}
}

func (a *app) columnShelf(c *ui.Context, r row) {
	const gap = shelfGap
	width := a.columnShelfWidth(r)
	st := a.carousel(r.shelf)
	again := st.NeedsMeasurement()
	groups := (len(r.items) + 3) / 4
	first, last := st.VisibleRange(groups, width, gap, pageGutter-8, 1)
	ui.Column(c).Children(func() {
		m3.Carousel(c, st, r.shelf, gap, pageGutter-8, func() {
			shelfSpacer(c, float32(first)*(width+gap)-gap)
			for g := first; g < last; g++ {
				start := g * 4
				group := r.items[start:min(start+4, len(r.items))]
				ui.Column(c.Key(start)).Width(width).Shrink(0).Children(func() {
					for i, item := range group {
						a.songRow(c, item, r.queue, songOptions{list: r.shelf, position: start + i})
					}
				})
			}
			shelfSpacer(c, float32(groups-last)*(width+gap)-gap)
		})
	})
	if again {
		c.Invalidate()
	}
}

func (a *app) columnShelfWidth(r row) float32 {
	if !r.playAll || a.listWidthLastFrame <= 0 {
		return columnWidth
	}
	inner := a.listWidthLastFrame - 2*(pageGutter-8)
	if inner < 3*minTopColumn+2*shelfGap {
		return columnWidth
	}
	return min(columnWidth, (inner-2*shelfGap)/3)
}

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

type cardView struct {
	key       string
	menu      string
	menuLabel string
	playLabel string
	kind      youtube.TargetKind
}

func newCardView(item youtube.MusicItem, list string, position int) cardView {
	key := cardKey(item, list, position)
	label := "Play " + item.Title
	target := item.Target()
	if target.Kind == youtube.TargetArtist {
		label = "Shuffle play " + item.Title
	}
	return cardView{
		key:       key,
		menu:      key + "-menu",
		menuLabel: "More options for " + item.Title,
		playLabel: label,
		kind:      target.Kind,
	}
}

type coverMatch struct{ card string }

type cardCover struct {
	match coverMatch
	kind  youtube.TargetKind
	id    string
}

func (a *app) rememberCover(card string, item youtube.MusicItem) {
	target := item.Target()
	if !target.Opens() || target.ID == "" {
		return
	}
	a.cover = cardCover{match: coverMatch{card: card}, kind: target.Kind, id: target.ID}
}

func hoverOpacity(hovered bool) float32 {
	if hovered {
		return m3.StateHover
	}
	return 0
}

func artRadius(item youtube.MusicItem, square float32) float32 {
	if item.Target().Kind == youtube.TargetArtist {
		return m3.Full
	}
	return square
}

type artKey struct {
	url    string
	pixels int
}

func (a *app) artBitmap(url string, pixels int) *ui.Bitmap {
	if url == "" {
		return nil
	}
	key := artKey{url, pixels}
	if art, ok := a.artMemo[key]; ok {
		return art
	}
	art := a.thumbs.Bitmap(url, pixels)
	if a.artMemo == nil {
		a.artMemo = make(map[artKey]*ui.Bitmap, 32)
	}
	a.artMemo[key] = art
	return art
}

func (a *app) beginArtFrame() { clear(a.artMemo) }

func (a *app) art(c *ui.Context, url string, pixels int, size, radius float32, overlays ...func()) ui.Element {
	return m3.Art(c, a.artBitmap(url, a.artPixels(pixels, size)), size, radius, overlays...)
}

const coverGrace = 2 * time.Second

type coverState struct {
	art    *ui.Bitmap
	forURL string
	at     time.Time
}

func (a *app) playerArtwork(st *coverState, c *ui.Context, url string, pixels int, size, radius float32, overlays ...func()) ui.Element {
	if url == "" {
		*st = coverState{}
		return m3.Art(c, nil, size, radius, overlays...)
	}
	if st.forURL != url {
		st.forURL, st.at = url, c.Now()
	}
	if art := a.artBitmap(url, a.artPixels(pixels, size)); art != nil {
		st.art = art
	} else if st.art != nil && c.Now().Sub(st.at) > coverGrace {
		st.art = nil
	}
	return m3.Art(c, st.art, size, radius, overlays...)
}

func (a *app) avatar(c *ui.Context, name, url string, pixels int, size float32) {
	m3.Avatar(c, name, a.thumbs.Bitmap(url, a.artPixels(pixels, size)), size)
}

var artSteps = [...]int{64, 96, 128, 192, 256, 320, 384, 448, 512, 640}

func (a *app) artPixels(pixels int, size float32) int {
	if a.displayScale == 0 {
		return pixels
	}
	need := int(math.Ceil(float64(size * a.displayScale * a.zoom())))
	for _, step := range artSteps {
		if step >= need {
			return min(step, pixels)
		}
	}
	return pixels
}

func (a *app) card(c *ui.Context, item youtube.MusicItem, view cardView, queue []youtube.MusicItem) {
	sc := m3.Of(c).Scheme
	kind := view.kind
	key := view.key
	card := ui.ButtonBase(c.Key(key))
	hovered := card.Hovered()
	card.Column().AlignItems(ui.Start).Width(cardWidth).Shrink(0).Padding(0, 0, 12, 0).Gap(10).Radius(m3.ExtraLarge).Cursor(ui.CursorPointer).
		Background(m3.Layer(sc.Surface, sc.OnSurface, hoverOpacity(hovered || card.FocusVisible()))).
		Label(item.Title)
	played, menuClicked := false, false
	var menuButton, menuAnchor ui.Element
	hasMenu, hasAnchor := false, false
	card.Children(func() {
		radius := artRadius(item, m3.ExtraLarge)
		opening := a.isOpening(key)
		art := a.art(c, item.Thumbnail, 256, cardArt, radius, func() {
			if isNewPlaylist(item) {
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
			if kind != youtube.TargetNone {
				menuButton = m3.IconButton(c, m3.IconButtonSpec{
					Icon: m3.IconMore, Label: view.menuLabel, Key: view.menu,
					Container: sc.SurfaceContainerHighest.Alpha(0.85),
				}).Attach(ui.AnchorTopRight, ui.AnchorTopRight).Top(0).Right(0)
				hasMenu = true
				if !hovered && !card.FocusWithin() && !a.menu.opened(view.menu) {
					menuButton.Opacity(0).PassThrough()
				}
			}
			if kind == youtube.TargetTrack || kind == youtube.TargetAlbum || kind == youtube.TargetPlaylist || kind == youtube.TargetArtist {
				fab := m3.FAB(c, m3.FABSpec{Icon: m3.IconPlay, Size: m3.FABSmall, Tone: m3.FABPrimary, Radius: 16, Key: "play"}).
					Attach(ui.AnchorBottomRight, ui.AnchorBottomRight).Right(12).Bottom(12)
				fab.Label(view.playLabel)
				if !hovered && !card.FocusWithin() {
					fab.Opacity(0).PassThrough()
				}
				if fab.Clicked() {
					played = true
				}
			}
		})
		if item.Target().Opens() {
			art.Transition(ui.ElementTransition{Animation: &ui.Snappy, Match: coverMatch{card: key}})
		}
		ui.Column(c).Width(cardWidth).Shrink(0).Padding(0, 16, 0, 16).Gap(2).Children(func() {
			m3.EmphasizedText(c, m3.TitleSmall, item.Title).Width(cardWidth - 32).SingleLine().TextColor(sc.OnSurface)
			if item.Subtitle != "" {
				m3.Text(c, m3.BodySmall, item.Subtitle).Width(cardWidth - 32).SingleLine().TextColor(sc.OnSurfaceVariant)
			}
		})
		menuAnchor, hasAnchor = a.menu.pointerAnchor(c, view.menu)
	})
	if hasMenu {
		pointer := card.RightClicked() || menuButton.RightClicked()
		x, y, _ := card.PointerPosition()
		if menuButton.Clicked() || pointer || card.Shortcut(ui.Shift, ui.KeyF10) {
			a.menu.show(view.menu, pointer, x, y)
			menuClicked = true
		}
	}
	if hasMenu && a.menu.owns(view.menu) {
		a.cardMenu(c, a.menu.anchor(menuButton, menuAnchor, hasAnchor), view.menu, item, queue)
	}
	switch {
	case menuClicked:
	case played:
		a.playCollection(item, key, kind == youtube.TargetArtist)
	case card.Clicked() && isNewPlaylist(item):
		a.openNewPlaylist(youtube.MusicItem{})
	case card.Clicked():
		a.rememberCover(key, item)
		a.activate(item, queue)
	}
}
