package main

import (
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

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
	sc := m3.Of(c).Scheme
	c.Root().Background(sc.SurfaceContainerLow)

	// The router also moves on its own, by the keyboard's back and forward
	// keys, so the build is where a new location is noticed. It is guarded:
	// a location is acted on once.
	a.syncLocation()
	a.followArtwork()
	if a.notice != "" {
		c.Toast(a.notice)
		a.notice = ""
	}
	if !a.signIn.open && (a.signIn.generation != 0 || a.signIn.cancel != nil || a.signIn.cookie != "") {
		a.dismissSignIn()
	}

	bar := c.TitleBar()
	ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
		a.rail(c, bar)
		ui.Column(c).Grow(1).MinWidth(0).Padding(sheetInset, sheetInset, sheetInset, 0).Children(func() {
			a.sheet(c, bar)
		})
	})
	a.signInDialog(c)
	m3.Snackbars(c, 0)

	// The transport, wherever the keyboard focus is that does not want the
	// keys itself.
	if c.Shortcut(0, ui.KeySpace) {
		a.togglePlay()
	}
	if c.Shortcut(ui.Cmd, ui.KeyLeft) {
		a.previous()
	}
	if c.Shortcut(ui.Cmd, ui.KeyRight) {
		a.advance()
	}
	if c.Shortcut(ui.Cmd, ui.KeyK) || c.Shortcut(ui.Cmd, ui.KeyF) {
		a.focusSearch = true
		a.router.Push("/search")
	}
	if c.Shortcut(ui.Cmd, ui.KeyComma) {
		a.router.Push("/settings")
	}
	if a.npOpen && c.Shortcut(0, ui.KeyEscape) {
		a.npOpen = false
	}
}

// navPage returns the rail destination a path belongs to, and "" for a page
// the rail does not name, as an album or a playlist.
func navPage(path string) string {
	switch path {
	case "/home", "/explore", "/library", "/recap", "/search", "/settings":
		return path[1:]
	}
	return ""
}

func (a *app) rail(c *ui.Context, bar ui.TitleBar) {
	items := []m3.NavItem{
		{ID: pageHome, Label: "Home", Icon: m3.IconHome, Selected: m3.IconHomeFilled},
		{ID: pageSearch, Label: "Search", Icon: m3.IconSearch, Selected: m3.IconSearch},
		{ID: pageExplore, Label: "Explore", Icon: m3.IconExplore, Selected: m3.IconExploreFilled},
		{ID: pageLibrary, Label: "Library", Icon: m3.IconLibrary, Selected: m3.IconLibraryFilled},
	}
	if a.signedIn {
		// The recap is built from the account's play history, so it is only
		// offered once there is one.
		items = append(items, m3.NavItem{ID: pageRecap, Label: "Recap", Icon: m3.IconTrending, Selected: m3.IconTrending})
	}
	event := m3.Rail(c, m3.RailSpec{
		Items:    items,
		Footer:   []m3.NavItem{{ID: pageSettings, Label: "Settings", Icon: m3.IconSettings, Selected: m3.IconSettings}},
		Selected: navPage(a.router.Path()),
		Expanded: a.settings.RailExpanded,
		TopInset: bar.Height,
	})
	if event.Toggle {
		a.settings.RailExpanded = !a.settings.RailExpanded
		a.saveSettings()
	}
	if event.Item != "" && event.Item != navPage(a.router.Path()) {
		a.router.Push("/" + event.Item)
	}
}

// sheet is the rounded surface the pages live on. The player floats over its
// foot, and the full-screen player covers it.
func (a *app) sheet(c *ui.Context, bar ui.TitleBar) {
	sc := m3.Of(c).Scheme
	sheet := ui.Column(c).Key("sheet").Grow(1).MinHeight(0).Radius(m3.ExtraLarge).Clip().Background(sc.Surface)
	sheet.Children(func() {
		a.topBar(c, bar)
		ui.Column(c).Grow(1).MinHeight(0).Children(func() {
			a.page(c)
		})
		if a.current.VideoID != "" && !a.npOpen {
			a.playerBar(c)
		}
		if a.npOpen {
			a.nowPlaying(c)
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

// greeting names the part of the day.
func greeting(now time.Time) string {
	switch h := now.Hour(); {
	case h < 5:
		return "Up late?"
	case h < 12:
		return "Good morning"
	case h < 18:
		return "Good afternoon"
	}
	return "Good evening"
}

// pageTitle is what the top bar says for the page shown.
func (a *app) pageTitle(now time.Time) string {
	switch a.router.Path() {
	case "/home":
		return greeting(now)
	case "/explore":
		return "Explore"
	case "/library":
		return "Library"
	case "/recap":
		return "Recap"
	case "/search":
		return "Search"
	case "/settings":
		return "Settings"
	}
	return ""
}

// topBar names the page, with a way back on the pages a click opened, and
// the account at its end. It drags the window.
func (a *app) topBar(c *ui.Context, bar ui.TitleBar) {
	sc := m3.Of(c).Scheme
	back := isDetail(a.router.Path())
	padding := float32(pageGutter)
	if back {
		padding -= 8
	}
	topbar := ui.Row(c).Height(76).PaddingX(padding).Gap(8).AlignItems(ui.Center).Shrink(0).DragWindow()
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
		title := a.pageTitle(c.Now())
		m3.EmphasizedText(c, m3.HeadlineMedium, title).SingleLine().TextColor(sc.OnSurface)
		ui.Spacer(c)
		a.accountButton(c)
	})
}

// page builds the page the router shows.
func (a *app) page(c *ui.Context) {
	a.router.View(c, func(r *ui.Route) {
		switch {
		case r.Match("/home"):
			r.Title("Home")
			a.browsePage(c)
		case r.Match("/explore"):
			r.Title("Explore")
			a.browsePage(c)
		case r.Match("/library"):
			r.Title("Library")
			a.libraryPage(c)
		case r.Match("/recap"):
			r.Title("Recap")
			a.recapPage(c)
		case r.Match("/search"):
			r.Title("Search")
			a.searchPage(c)
		case r.Match("/settings"):
			r.Title("Settings")
			a.settingsPage(c)
		case r.Match("/album/{id}"):
			r.Title("Album")
			a.browsePage(c)
		case r.Match("/playlist/{id}"):
			r.Title("Playlist")
			a.browsePage(c)
		case r.Match("/artist/{id}"):
			r.Title("Artist")
			a.browsePage(c)
		default:
			r.Title("Not found")
			ui.Column(c).Fill().Center().Children(func() {
				a.message(c, m3.IconError, "Not found", "That page does not exist.", "", nil)
			})
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

// firstLine trims a long error to something a message can hold.
func firstLine(text string) string {
	text, _, _ = strings.Cut(text, "\n")
	return text
}
