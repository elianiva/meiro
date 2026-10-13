package main

import (
	"runtime"
	"strconv"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
)

// The metrics of the shell, in DIPs.
const (
	playerHeight = 88
	playerGutter = 16
	sheetInset   = 8
	pageGutter   = 24
)

// view builds the window: the navigation rail, the sheet that holds the page
// under its top bar, and the player floating over the foot of the sheet.
func (a *app) view(c *ui.Context) {
	m3.Provide(c, a.resolveTheme(c))
	a.updateHints(c)
	// The window paints no background of its own: the rail, the sheet's
	// border and the sheet between them cover it, so a frame fills the window
	// as the sheet and a thin ring rather than as a full-window quad under
	// everything.

	a.syncLocation()
	a.followArtwork()
	if a.notice != "" {
		c.Toast(a.notice)
		a.notice = ""
	}
	a.syncSignInDialog()

	bar := c.TitleBar()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.rail(c, bar)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			a.sheet(c)
		})
	})
	a.signInDialog(c)
	a.newPlaylistDialog(c)
	a.addToPlaylistDialog(c)
	a.crashDialog(c)
	m3.Snackbars(c, 0)
	a.shortcuts(c)
}

// navigate opens a page that takes no ID.
func (a *app) navigate(id routeID) { a.router.Push(routeByID(id).path("")) }

// navID is the rail's name for a destination: its path.
func navID(id routeID) string { return routeByID(id).path("") }

func (a *app) rail(c *ui.Context, bar ui.TitleBar) {
	var items, footer []m3.NavItem
	for i := range routes {
		r := &routes[i]
		if r.rail == nil || r.rail.withAccount && !a.signedIn() {
			continue
		}
		item := m3.NavItem{ID: navID(r.id), Label: r.rail.label, Icon: r.rail.icon, Selected: r.rail.filled}
		if r.rail.action != "" && a.keyboard.hints {
			item.Hint = a.hintParts(r.rail.action)
		}
		if r.rail.footer {
			footer = append(footer, item)
		} else {
			items = append(items, item)
		}
	}
	r, _ := a.route()
	selected := ""
	if r.nav != routeNotFound {
		selected = navID(r.nav)
	}
	event := m3.Rail(c, m3.RailSpec{
		Items:    items,
		Footer:   footer,
		Selected: selected,
		Expanded: a.settings.RailExpanded,
		TopInset: bar.Height,
	})
	if event.Toggle {
		a.settings.RailExpanded = !a.settings.RailExpanded
		a.saveSettings()
	}
	if event.Item != "" && event.Item != selected {
		a.router.Push(event.Item)
	}
}

// sheetCorner is the side of the square painted behind each of the sheet's
// corners: the inset, plus the reach of a continuous corner, 1.528665 of its
// radius. The sheet's own fill covers all of it but the rounded band the
// square is there to fill, so any larger square would do.
const sheetCorner = sheetInset + m3.ExtraLarge*1.528665

// sheet is the rounded surface the pages live on. The player floats over its
// foot, and the full-screen player covers it, along with the top bar and the
// page under it.
//
// The window tint between the sheet and the window's edge is painted as a
// ring of thin strips and corner squares behind the sheet, not as a
// full-window fill under it: the sheet covers all of the window but that
// edge and the corners its rounding cuts, so a full-window quad is the
// largest thing a frame would paint.
func (a *app) sheet(c *ui.Context) {
	sc := m3.Of(c).Scheme
	tint := sc.SurfaceContainerLow
	ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(sheetInset).Background(tint)
	ui.Box(c).Absolute().Top(0).Right(0).Bottom(0).Width(sheetInset).Background(tint)
	ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(sheetInset).Background(tint)
	ui.Box(c).Absolute().Top(0).Left(0).Size(sheetCorner, sheetCorner).Background(tint)
	ui.Box(c).Absolute().Top(0).Right(0).Size(sheetCorner, sheetCorner).Background(tint)
	ui.Box(c).Absolute().Bottom(0).Left(0).Size(sheetCorner, sheetCorner).Background(tint)
	ui.Box(c).Absolute().Bottom(0).Right(0).Size(sheetCorner, sheetCorner).Background(tint)
	sheet := ui.Column(c).Key("sheet").Absolute().Top(sheetInset).Left(0).Right(sheetInset).Bottom(sheetInset).
		Radius(m3.ExtraLarge)
	if !a.nowPlaying.open {
		sheet.Background(sc.Surface)
	}
	sheet.Children(func() {
		defer sheetCorners(c, tint)
		if a.nowPlaying.open {
			// The player covers the sheet whole, and the page and the top
			// bar with it. Only the player is built: the page and the bar
			// were laid out and painted in full on every frame of the wave
			// and of the slide into the player, which is most of the frame,
			// to be covered at once.
			a.nowPlayingView(c)
			return
		}
		a.nowPlaying.playerShown = false
		a.topBar(c)
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			a.routerView(c)
		})
		if a.current.VideoID != "" {
			a.playerBar(c)
		}
	})
}

// clearance is the room a list leaves at its foot so the player never hides
// its last row.
func (a *app) clearance() float32 {
	if a.current.VideoID == "" {
		return 24
	}
	return playerHeight + playerGutter*2
}

// topBar names the page, with a way back on the pages a click opened, the
// search every page runs from, and the account at its end. It drags the
// window.
func (a *app) topBar(c *ui.Context) {
	sc := m3.Of(c).Scheme
	r, _ := a.route()
	back := r.isDetail()
	padding := float32(pageGutter)
	if back {
		padding -= 8
	}
	topbar := ui.Row(c).Height(76).PaddingX(padding).Gap(16).AlignItems(ui.Center).Shrink(0).DragWindow()
	if back {
		// A page with a hero carries its wash up behind the bar.
		topbar.Background(sc.PrimaryContainer.Mix(sc.Surface, 0.45))
	}
	topbar.Children(func() {
		if back {
			if m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconArrowBack, Label: "Back", Key: "back"}).Clicked() {
				a.router.Back()
			}
		}
		title := ""
		if r.heading != nil {
			title = r.heading(c.Now())
		}
		m3.EmphasizedText(c, m3.HeadlineMedium, title).SingleLine().TextColor(sc.OnSurface)
		ui.Spacer(c)
		var bar ui.Element
		a.hinted(c, func() { bar = a.searchBar(c) }, keymap.Search).Grow(1).MinWidth(0).MaxWidth(520)
		a.searchSuggestions(c, bar)
		a.accountButton(c)
	})
}

// routerView builds the page the router shows, and the one it leaves while it
// animates away, from the route table.
func (a *app) routerView(c *ui.Context) {
	a.router.View(c, func(page *ui.Route) {
		r, _ := matchRoute(page.Path())
		page.Title(r.title)
		switch {
		case r.needsAccount != nil && !a.signedIn():
			prompt := *r.needsAccount
			if a.account.expired != "" {
				prompt = signInPrompt{m3.IconLogin, "Your session expired", "Sign in again to see " + prompt.what + ".", prompt.what}
			}
			ui.Column(c).Fill().Center().Children(func() {
				a.message(c, prompt.icon, prompt.title, prompt.body, "Sign in", a.openSignIn)
			})
		case r.view != nil:
			r.view(a, c)
		default:
			a.pageList(c)
		}
	})
}

// message shows a state in the middle of a page: a glyph in a tonal shape,
// what happened, and a button when there is something to do about it.
func (a *app) message(c *ui.Context, icon *ui.SVG, title, body, action string, run func()) {
	sc := m3.Of(c).Scheme
	ui.Column(c).FillWidth().Center().Gap(12).Padding(56, 32).Children(func() {
		ui.Box(c).Size(96, 96).Radius(m3.ExtraLarge).Background(sc.SecondaryContainer).Center().Children(func() {
			ui.Icon(c, icon).FontSize(40).TextColor(sc.OnSecondaryContainer)
		})
		ui.Box(c).Height(4)
		m3.EmphasizedText(c, m3.TitleLarge, title).TextAlign(ui.Center)
		if body != "" {
			m3.Text(c, m3.BodyLarge, body).TextColor(sc.OnSurfaceVariant).MaxLines(4).MaxWidth(460).TextAlign(ui.Center)
		}
		if action != "" && run != nil {
			ui.Box(c).Height(4)
			if m3.Button(c, m3.ButtonSpec{Label: action, Kind: m3.Tonal, Size: m3.Medium56, Key: "message-action"}).Clicked() {
				run()
			}
		}
	})
}

// sheetCorners paints the window tint over the four corners of the sheet,
// above whatever the page draws there, so the page seems to be cut by the
// sheet's rounded edge without a clip, whose mask is as large as the window.
func sheetCorners(c *ui.Context, tint ui.Color) {
	n := float32(m3.ExtraLarge) * sheetReach
	for i, corner := range sheetCornerShapes {
		piece := ui.Icon(c, corner).FontSize(n).TextColor(tint).Absolute()
		switch i {
		case 0:
			piece.Top(0).Left(0)
		case 1:
			piece.Top(0).Right(0)
		case 2:
			piece.Bottom(0).Left(0)
		default:
			piece.Bottom(0).Right(0)
		}
	}
}

// sheetReach is how far a corner of the sheet reaches along each edge, in
// radii: 1 for a circular corner and 1.528665 for the continuous corner macOS
// draws.
var sheetReach = map[bool]float32{false: 1, true: 1.528665}[runtime.GOOS == "darwin"]

// sheetCornerShapes are the four pieces that cover what lies outside the
// rounded corners of the sheet: top left, top right, bottom left and bottom
// right.
var sheetCornerShapes = [4]*ui.SVG{
	cornerShape(false, false), cornerShape(true, false), cornerShape(false, true), cornerShape(true, true),
}

// cornerShape draws the area outside one rounded corner: the corner's square
// less the quarter of the sheet that stays. Its curve follows the sheet's, a
// circle or the continuous corner's Bézier segments.
func cornerShape(right, bottom bool) *ui.SVG {
	reach := 100 * float64(sheetReach)
	point := func(x, y float64) string {
		if right {
			x = reach - x
		}
		if bottom {
			y = reach - y
		}
		return strconv.FormatFloat(x, 'f', 3, 64) + " " + strconv.FormatFloat(y, 'f', 3, 64)
	}
	path := "M" + point(0, 0)
	if runtime.GOOS == "darwin" {
		// The segments of the corner macOS draws, in units of its radius.
		path += "L" + point(reach, 0) +
			"C" + point(108.849323, 0) + " " + point(86.840694, 0) + " " + point(63.149399, 7.491139) +
			"C" + point(37.282383, 16.905956) + " " + point(16.905956, 37.282383) + " " + point(7.491139, 63.149399) +
			"C" + point(0, 86.840694) + " " + point(0, 108.849323) + " " + point(0, reach)
	} else {
		const k = 0.5522847498 // a quarter circle as one Bézier curve
		path += "L" + point(100, 0) + "C" + point(100*(1-k), 0) + " " + point(0, 100*(1-k)) + " " + point(0, 100)
	}
	path += "Z"
	size := strconv.FormatFloat(reach, 'f', 3, 64)
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + size + ` ` + size + `" fill="currentColor"><path d="` + path + `"/></svg>`))
}
