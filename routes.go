package main

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/keymap"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/youtube"
)

type routeID uint8

const (
	routeNotFound routeID = iota
	routeHome
	routeExplore
	routeLibrary
	routeHistory
	routeDownloads
	routeSearch
	routeSettings
	routeStats
	routeAlbum
	routePlaylist
	routeArtist
	routePodcast
)

type loader func(ctx context.Context, client *youtube.Client, id string, partial func(*youtube.BrowseResult)) (*youtube.BrowseResult, error)

type signInPrompt struct {
	icon        *ui.SVG
	title, body string
	what        string
}

type route struct {
	id           routeID
	pattern      string
	title        string
	heading      func(now time.Time) string
	rail         *railEntry
	nav          routeID
	source       string
	target       youtube.TargetKind
	load         loader
	open         func(a *app)
	view         func(a *app, c *ui.Context)
	empty        emptyMessage
	needsAccount *signInPrompt
	grid         bool
	appOwned     bool
}

func (r route) refreshable() bool { return r.load != nil && !r.isDetail() }

type emptyMessage struct{ title, body string }

type railEntry struct {
	label        string
	icon, filled *ui.SVG
	footer       bool
	withAccount  bool
	action       keymap.Action
}

func heading(text string) func(time.Time) string {
	return func(time.Time) string { return text }
}

var routes []route

func init() { routes = appRoutes() }

func appRoutes() []route {
	return []route{
		{
			id: routeHome, empty: emptyMessage{"Nothing to show yet", "YouTube Music returned an empty home page."}, pattern: "/home", title: "Home", rail: &railEntry{label: "Home", action: keymap.Home, icon: m3.IconHome, filled: m3.IconHomeFilled}, heading: greeting, nav: routeHome, source: "Home",
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetHomeFeed(ctx)
			},
		},
		{
			id: routeSearch, pattern: "/search", title: "Search", rail: &railEntry{label: "Search", action: keymap.Search, icon: m3.IconSearch, filled: m3.IconSearch}, heading: heading("Search"), nav: routeSearch, source: "Search results",
			view: (*app).searchPage,
		},
		{
			id: routeExplore, empty: emptyMessage{"Nothing to explore yet", "YouTube Music returned an empty page."}, pattern: "/explore", title: "Explore", rail: &railEntry{label: "Explore", action: keymap.Explore, icon: m3.IconExplore, filled: m3.IconExploreFilled}, heading: heading("Explore"), nav: routeExplore, source: "Explore",
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetExplore(ctx)
			},
		},
		{
			id: routeLibrary, empty: emptyMessage{"Your library is empty", "Songs, albums and playlists you save will show up here."}, pattern: "/library", title: "Library", rail: &railEntry{label: "Library", action: keymap.Library, icon: m3.IconLibrary, filled: m3.IconLibraryFilled}, heading: heading("Library"), nav: routeLibrary, source: "Your library", grid: true,
			load: func(ctx context.Context, c *youtube.Client, _ string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.StreamLibrary(ctx, nil)
			},
			needsAccount: &signInPrompt{m3.IconLibraryFilled, "Your library lives here", "Sign in to see your songs, albums and playlists.", "your songs, albums and playlists"},
		},
		{
			id: routeHistory, empty: emptyMessage{"No listening history yet", "Songs you play in Meiro show up here."}, pattern: "/history", title: "History", rail: &railEntry{label: "History", icon: m3.IconHistory, filled: m3.IconHistory}, heading: heading("History"), nav: routeHistory, source: "History", appOwned: true,
			open: (*app).openHistory,
		},
		{
			id: routeDownloads, empty: emptyMessage{"No downloads yet", "Download songs, albums and playlists to play them offline."}, pattern: "/downloads", title: "Downloads", rail: &railEntry{label: "Downloads", icon: m3.IconDownload, filled: m3.IconDownloadFilled}, heading: heading("Downloads"), nav: routeDownloads, source: "Downloads", appOwned: true,
		},
		{
			id: routeSettings, pattern: "/settings", title: "Settings", rail: &railEntry{label: "Settings", action: keymap.Settings, icon: m3.IconSettings, filled: m3.IconSettings, footer: true}, heading: heading("Settings"), nav: routeSettings, appOwned: true,
			view: (*app).settingsPage, open: (*app).loadToolVersions,
		},
		{
			id: routeStats, pattern: "/stats", title: "Stats", heading: heading("Listening stats"), nav: routeSettings, appOwned: true,
			view: (*app).statsPage, open: (*app).loadHistory,
		},
		{
			id: routeAlbum, empty: emptyMessage{"This album is empty", "YouTube Music returned no songs for it."}, pattern: "/album/{id}", title: "Album", target: youtube.TargetAlbum,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetAlbum(ctx, id)
			},
		},
		{
			id: routePlaylist, empty: emptyMessage{"This playlist is empty", "YouTube Music returned no songs for it."}, pattern: "/playlist/{id}", title: "Playlist", target: youtube.TargetPlaylist,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetPlaylist(ctx, id)
			},
		},
		{
			id: routeArtist, empty: emptyMessage{"Nothing here yet", "YouTube Music returned nothing for this artist."}, pattern: "/artist/{id}", title: "Artist", target: youtube.TargetArtist,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetArtist(ctx, id)
			},
		},
		{
			id: routePodcast, empty: emptyMessage{"This podcast has no episodes", "YouTube Music returned no episodes for it."}, pattern: "/podcast/{id}", title: "Podcast", target: youtube.TargetPodcast,
			load: func(ctx context.Context, c *youtube.Client, id string, _ func(*youtube.BrowseResult)) (*youtube.BrowseResult, error) {
				return c.GetPodcast(ctx, id)
			},
		},
	}
}

var notFound = route{
	id: routeNotFound, title: "Not found", heading: heading(""),
	view: func(a *app, c *ui.Context) {
		ui.Column(c).Fill().Center().Children(func() {
			a.message(c, m3.IconError, "Not found", "That page does not exist.", "", nil)
		})
	},
}

func routeByID(id routeID) *route {
	for i := range routes {
		if routes[i].id == id {
			return &routes[i]
		}
	}
	return &notFound
}

func matchRoute(path string) (*route, string) {
	for i := range routes {
		if id, ok := matchPattern(routes[i].pattern, path); ok {
			return &routes[i], id
		}
	}
	return &notFound, ""
}

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

func (r *route) path(id string) string {
	return strings.Replace(r.pattern, "{id}", url.PathEscape(id), 1)
}

func (r *route) isDetail() bool { return r.target != youtube.TargetNone }

// keepsBlockedSongs reports whether the page lists a blocked artist's songs
// greyed out, so a whole album or playlist still reads as it is, instead of
// leaving them out as the other lists do.
func (r *route) keepsBlockedSongs() bool {
	return r.id == routeAlbum || r.id == routePlaylist || r.id == routeArtist
}

func detailRoute(kind youtube.TargetKind) (*route, bool) {
	for i := range routes {
		if routes[i].target == kind && kind != youtube.TargetNone {
			return &routes[i], true
		}
	}
	return nil, false
}

func targetPath(target youtube.Target) (string, bool) {
	r, ok := detailRoute(target.Kind)
	if !ok || target.ID == "" {
		return "", false
	}
	return r.path(target.ID), true
}

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
