package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

// routeID names a page of the app.
type routeID uint8

const (
	routeNotFound routeID = iota
	routeHome
	routeExplore
	routeLibrary
	routeRecap
	routeSearch
	routeSettings
	routeStats
	routeAlbum
	routePlaylist
	routeArtist
)

// loader reads a page's content. partial, when the loader can use it, shows
// the start of a page that is still loading.
type loader func(ctx context.Context, client *youtube.Client, id string, partial func(*youtube.BrowseResult)) (*youtube.BrowseResult, error)

// signInPrompt is what a page that needs an account shows without one.
type signInPrompt struct {
	icon        *ui.SVG
	title, body string
}

// route is everything the app knows about one page. A page is added by
// adding its route to routes, and nowhere else: the router's view, the rail,
// the top bar, loading, reloading on a change of account and the name of a
// queue played from the page all read it from here.
type route struct {
	id routeID
	// pattern is the page's path, with {id} standing for the ID of what a
	// detail page shows.
	pattern string
	// title names the page in the router's history.
	title string
	// heading is what the top bar says; detail pages leave it to their hero.
	heading func(now time.Time) string
	// nav is the rail destination the page belongs to, if any.
	nav routeID
	// source names a queue played from the page; detail pages use the title
	// of what they show.
	source string
	// target is the kind of item a detail page shows, and TargetNone for the
	// other pages. Detail pages get a hero, a back button and a heading of
	// their own.
	target youtube.TargetKind
	// load reads the page's content as it opens; pages without content of
	// their own leave it nil.
	load loader
	// open, when set, runs each time the page is navigated to: the place to
	// start work the page needs, since building its view must start none.
	open func(a *app)
	// view builds the page; nil builds the page's list of rows.
	view func(a *app, c *ui.Context)
	// needsAccount, when set, is shown instead of the page to a signed-out
	// user, and the page loads nothing until one signs in.
	needsAccount *signInPrompt
	// grid lays the page's cards out in lines that wrap, rather than in
	// shelves that scroll sideways.
	grid bool
}

func heading(text string) func(time.Time) string {
	return func(time.Time) string { return text }
}

// routes is every page of the app. The order is the order patterns are
// matched in. It is filled in init, because the views it names reach back to
// it.
var routes []route

func init() { routes = appRoutes() }

func appRoutes() []route {
	return []route{
		{
			id: routeHome, pattern: "/home", title: "Home", heading: greeting, nav: routeHome, source: "Home",
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetHomeFeed(ctx)
			},
		},
		{
			id: routeExplore, pattern: "/explore", title: "Explore", heading: heading("Explore"), nav: routeExplore, source: "Explore",
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetExplore(ctx)
			},
		},
		{
			id: routeLibrary, pattern: "/library", title: "Library", heading: heading("Library"), nav: routeLibrary, source: "Your library", grid: true,
			load: func(ctx context.Context, c *youtube.Client, _ string, partial func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.StreamLibrary(ctx, partial)
			},
			needsAccount: &signInPrompt{m3.IconLibraryFilled, "Your library lives here", "Sign in to see your songs, albums and playlists."},
		},
		{
			id: routeRecap, pattern: "/recap", title: "Recap", heading: heading("Recap"), nav: routeRecap, source: "Your recap",
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetRecap(ctx)
			},
			// The recap is built from the account's play history.
			needsAccount: &signInPrompt{m3.IconTrending, "Your recap lives here", "Sign in to see the music you have had on repeat."},
		},
		{
			id: routeSearch, pattern: "/search", title: "Search", heading: heading("Search"), nav: routeSearch, source: "Search results",
			view: (*app).searchPage,
		},
		{
			id: routeSettings, pattern: "/settings", title: "Settings", heading: heading("Settings"), nav: routeSettings,
			view: (*app).settingsPage,
		},
		{
			id: routeStats, pattern: "/stats", title: "Stats", heading: heading("Listening stats"), nav: routeSettings,
			view: (*app).statsPage, open: (*app).loadHistory,
		},
		{
			id: routeAlbum, pattern: "/album/{id}", title: "Album", target: youtube.TargetAlbum,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetAlbum(ctx, id)
			},
		},
		{
			id: routePlaylist, pattern: "/playlist/{id}", title: "Playlist", target: youtube.TargetPlaylist,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetPlaylist(ctx, id)
			},
		},
		{
			id: routeArtist, pattern: "/artist/{id}", title: "Artist", target: youtube.TargetArtist,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetArtist(ctx, id)
			},
		},
	}
}

// notFound is the page of a path no route matches.
var notFound = route{
	id: routeNotFound, title: "Not found", heading: heading(""),
	view: func(a *app, c *ui.Context) {
		ui.Column(c).Fill().Center().Children(func() {
			a.message(c, m3.IconError, "Not found", "That page does not exist.", "", nil)
		})
	},
}

// routeByID returns the route with an ID. Every ID but routeNotFound has one.
func routeByID(id routeID) *route {
	for i := range routes {
		if routes[i].id == id {
			return &routes[i]
		}
	}
	return &notFound
}

// matchRoute returns the route a path shows, and the ID a detail page's path
// names.
func matchRoute(path string) (*route, string) {
	for i := range routes {
		if id, ok := matchPattern(routes[i].pattern, path); ok {
			return &routes[i], id
		}
	}
	return &notFound, ""
}

// matchPattern matches a path against a route pattern of fixed parts and at
// most one {id}, as the router's own Route.Match does, and returns the ID,
// unescaped. It allocates nothing unless the ID is escaped: the view asks
// for the route several times a frame.
func matchPattern(pattern, path string) (string, bool) {
	pattern, path = strings.Trim(pattern, "/"), strings.Trim(path, "/")
	id := ""
	for {
		want, patternRest, morePattern := strings.Cut(pattern, "/")
		part, pathRest, morePath := strings.Cut(path, "/")
		switch {
		case want == "{id}" && part != "":
			id = part
		case want != part:
			return "", false
		}
		if morePattern != morePath {
			return "", false
		}
		if !morePattern {
			break
		}
		pattern, path = patternRest, pathRest
	}
	if decoded, err := url.PathUnescape(id); err == nil {
		id = decoded
	}
	return id, true
}

// path is where a route's page is: its pattern, with id for a detail page.
func (r *route) path(id string) string {
	return strings.Replace(r.pattern, "{id}", url.PathEscape(id), 1)
}

// isDetail reports whether the page shows one album, playlist or artist.
func (r *route) isDetail() bool { return r.target != youtube.TargetNone }

// detailRoute is the page that shows an item of a kind, if it has one.
func detailRoute(kind youtube.TargetKind) (*route, bool) {
	for i := range routes {
		if routes[i].target == kind && kind != youtube.TargetNone {
			return &routes[i], true
		}
	}
	return nil, false
}

// targetPath is the page that shows an album, a playlist or an artist.
func targetPath(target youtube.Target) (string, bool) {
	r, ok := detailRoute(target.Kind)
	if !ok || target.ID == "" {
		return "", false
	}
	return r.path(target.ID), true
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
