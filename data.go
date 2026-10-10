package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

// What a page, or the thing an item opens, is.
const (
	pageTrack    = "track"
	pageAlbum    = "album"
	pagePlaylist = "playlist"
	pageArtist   = "artist"
	pageHome     = "home"
	pageExplore  = "explore"
	pageLibrary  = "library"
	pageRecap    = "recap"
	pageSearch   = "search"
	pageSettings = "settings"
)

const detailStateLimit = pageCacheLimit // one detail per retained route

// searchKinds are the filters of the search page, in the order its chips show
// them.
var searchKinds = []struct {
	name string
	kind youtube.SearchType
}{
	{"All", youtube.SearchAll},
	{"Songs", youtube.SearchSongs},
	{"Albums", youtube.SearchAlbums},
	{"Artists", youtube.SearchArtists},
	{"Playlists", youtube.SearchPlaylists},
	{"Videos", youtube.SearchVideos},
}

// pageState is what a page has loaded, whichever page it is.
type pageState struct {
	loading  bool
	err      string
	sections []youtube.MusicSection
	items    []youtube.MusicItem
	more     string
	// moreErr is why the last request for more failed. The page keeps what it
	// has, and the button to ask again.
	moreErr string
}

// searchState is the search page. Typing only edits query; a search runs when
// the user submits it, and submitted is what the results are for.
type searchState struct {
	pageState
	query     string
	submitted string
	kind      int
	// suggestions are the completions of the text being typed. suggestSeq
	// numbers the request that asked for them, so a slow answer to an earlier
	// query is dropped, and suggestCancel stops a pending request.
	suggestions   []string
	suggestSeq    int
	suggestCancel func()
}

// rowKind says what a row of a page's list holds.
type rowKind int

const (
	rowHero    rowKind = iota // the heading of an album, playlist or artist
	rowHeading                // the name of a section, with arrows for its shelf
	rowCards                  // a shelf of cards that scrolls sideways
	rowColumns                // a shelf of songs in columns that scrolls sideways
	rowTrack                  // one song, album, artist or playlist in a column of rows
	rowMore                   // the button that loads the next page
	rowLoading                // the page is loading
	rowError                  // the page failed
	rowEmpty                  // the page has nothing on it
)

// row is one line of a page's list.
type row struct {
	kind  rowKind
	title string
	// shelf names the carousel a heading steers, and a shelf draws.
	shelf string
	// items are the cards or songs of a shelf; item is the one of a track row.
	items []youtube.MusicItem
	item  youtube.MusicItem
	// queue is what playing from this row plays: the songs of its shelf, or
	// of the page.
	queue []youtube.MusicItem
	// track is the place of a song among the page's songs, or -1.
	track    int
	position int
}

// key identifies a row across frames, so the list keeps its place when the
// rows change.
func (r row) key() any {
	switch r.kind {
	case rowTrack:
		return itemKey("track", r.item) + "\x00" + r.shelf + "#" + strconv.Itoa(r.position)
	case rowCards, rowColumns, rowHeading:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf
	}
	return strconv.Itoa(int(r.kind)) + ":" + r.title
}

// onNavigate restores a fresh route from the page cache, or loads it when it
// is missing or stale. The heading is kept for an album, playlist or artist
// page, which the action that opened it already described.
func (a *app) onNavigate() {
	a.cacheCurrentPage()
	a.nextJob()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	location, path := a.router.Location(), a.router.Path()
	page, cached := a.restorePage(location)
	if cached && (path == "/search" || page.fresh(time.Now())) {
		a.npOpen = false
		return
	}
	a.feed = pageState{}
	a.rows, a.playable = nil, nil
	a.rowsDirty = true
	a.pageLoadedAt = time.Time{}
	// The list is shared by every page, so a new page starts at its top.
	a.list = ui.ListState{}
	a.list.ScrollTo(0, ui.Start)
	a.npOpen = false

	if path != "/search" {
		a.cancelSuggestions()
	}
	switch {
	case path == "/home":
		a.detail = detail{}
		a.loadFeed(pageHome)
	case path == "/explore":
		a.detail = detail{}
		a.loadFeed(pageExplore)
	case path == "/library":
		a.detail = detail{}
		if a.signedIn {
			a.loadFeed(pageLibrary)
		}
	case path == "/recap":
		a.detail = detail{}
		if a.signedIn {
			a.loadFeed(pageRecap)
		}
	case path == "/search":
		a.detail = detail{}
		a.focusSearch = a.search.submitted == ""
	case path == "/settings":
		a.detail = detail{}
	case strings.HasPrefix(path, "/album/"):
		a.detail = a.detailFor(path)
		a.detail.kind = pageAlbum
		a.loadDetail(pageAlbum, pathArg(path))
	case strings.HasPrefix(path, "/playlist/"):
		a.detail = a.detailFor(path)
		a.detail.kind = pagePlaylist
		a.loadDetail(pagePlaylist, pathArg(path))
	case strings.HasPrefix(path, "/artist/"):
		a.detail = a.detailFor(path)
		a.detail.kind = pageArtist
		a.loadDetail(pageArtist, pathArg(path))
	}
}

func (a *app) detailFor(path string) detail {
	detail := a.details[path]
	if detail.title != "" || detail.subtitle != "" || detail.art != "" || detail.kind != "" {
		a.detailOrder = append(removeKey(a.detailOrder, path), path)
	}
	return detail
}

func (a *app) rememberDetail(path string, detail detail) {
	a.details[path] = detail
	a.detailOrder = append(removeKey(a.detailOrder, path), path)
	for len(a.detailOrder) > detailStateLimit {
		oldest := a.detailOrder[0]
		a.detailOrder = a.detailOrder[1:]
		delete(a.details, oldest)
	}
}

// pathArg returns the ID at the end of a page's path.
func pathArg(path string) string {
	_, id, _ := strings.Cut(path, "/")
	_, id, _ = strings.Cut(id, "/")
	if decoded, err := url.PathUnescape(id); err == nil {
		return decoded
	}
	return id
}

// loadFeed fetches one of the pages that need no argument.
func (a *app) loadFeed(page string) {
	a.fetch(func(ctx context.Context, client *youtube.Client) (*youtube.BrowseResult, error) {
		switch page {
		case pageHome:
			return client.GetHomeFeed(ctx)
		case pageExplore:
			return client.GetExplore(ctx)
		case pageRecap:
			return client.GetRecap(ctx)
		default:
			return client.GetAllLibrary(ctx)
		}
	})
}

// loadDetail fetches an album, a playlist or an artist page.
func (a *app) loadDetail(kind, id string) {
	a.fetch(func(ctx context.Context, client *youtube.Client) (*youtube.BrowseResult, error) {
		switch kind {
		case pageAlbum:
			return client.GetAlbum(ctx, id)
		case pagePlaylist:
			return client.GetPlaylist(ctx, id)
		default:
			return client.GetArtist(ctx, id)
		}
	})
}

// fetch runs a page load off the main thread and applies it, dropping the
// result when a newer load has replaced it. The client is the one in use when
// the load began, handed to it because the app's own may change meanwhile.
func (a *app) fetch(load func(ctx context.Context, client *youtube.Client) (*youtube.BrowseResult, error)) {
	client := a.client()
	if client == nil {
		return
	}
	a.feed.loading, a.feed.err = true, ""
	a.rowsDirty = true
	job := a.nextJob()
	location := a.router.Location()
	ctx := a.jobContext()
	a.run(func() {
		result, err := load(ctx, client)
		a.update(func() {
			if job != a.job || location != a.router.Location() {
				return
			}
			a.rowsDirty = true
			a.feed.loading = false
			if err != nil {
				a.feed.err = err.Error()
				return
			}
			a.feed.sections = result.Sections
			// Items are the page's songs, cards and rows all together, which
			// its sections already hold; they are only a page by themselves
			// when it has no sections.
			a.feed.items = nil
			if len(result.Sections) == 0 {
				a.feed.items = result.Items
			}
			a.feed.more = result.ContinuationToken
			a.pageLoadedAt = time.Now()
		})
	})
}

// nextJob numbers a new load, so one that lands after a newer one began is
// dropped.
func (a *app) nextJob() int {
	a.job++
	return a.job
}

// jobContext cancels the load in flight and returns the context of the next.
func (a *app) jobContext() context.Context {
	if a.cancel != nil {
		a.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel
	return ctx
}

// runSearch searches for what the user submitted, with the chosen filter. It
// is the only way a search starts: typing in the field never does.
func (a *app) runSearch(query string) {
	query = strings.TrimSpace(query)
	a.cancelSuggestions()
	if query == "" {
		a.search = searchState{kind: a.search.kind}
		a.rowsDirty = true
		return
	}
	a.search.query, a.search.submitted = query, query
	a.settings.remember(query)
	a.saveSettings()
	a.search.sections = nil
	client := a.client()
	if client == nil {
		return
	}
	a.search.loading, a.search.err, a.search.more = true, "", ""
	a.search.items = nil
	a.rowsDirty = true
	a.list.ScrollTo(0, ui.Start)
	job := a.nextJob()
	ctx := a.jobContext()
	kind := searchKinds[a.search.kind].kind
	a.run(func() {
		result, err := client.Search(ctx, query, youtube.SearchOptions{Type: kind})
		a.update(func() {
			if job != a.job {
				return
			}
			a.rowsDirty = true
			a.search.loading = false
			if err != nil {
				a.search.err = err.Error()
				return
			}
			a.search.items = result.Items
			a.search.more = result.ContinuationToken
		})
	})
}

// loadMore appends the next page of items to the one shown.
func (a *app) loadMore() {
	s, client := a.pageState(), a.client()
	if s.more == "" || client == nil {
		return
	}
	token, searching := s.more, a.router.Path() == "/search"
	s.more, s.moreErr = "", ""
	a.rowsDirty = true
	// A page load that begins meanwhile, or a navigation, makes this result
	// stale; asking for more must not itself cancel one in flight.
	job := a.job
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var items []youtube.MusicItem
		var next string
		var err error
		if searching {
			var result *youtube.SearchResult
			if result, err = client.ContinueSearch(ctx, token); err == nil {
				items, next = result.Items, result.ContinuationToken
			}
		} else {
			var result *youtube.BrowseResult
			if result, err = client.ContinueBrowse(ctx, token); err == nil {
				items, next = result.Items, result.ContinuationToken
			}
		}
		a.update(func() {
			if job != a.job {
				return
			}
			a.rowsDirty = true
			if err != nil {
				s.more, s.moreErr = token, err.Error()
				return
			}
			if len(s.sections) > 0 {
				last := &s.sections[len(s.sections)-1]
				last.Items = append(last.Items, items...)
			} else {
				s.items = append(s.items, items...)
			}
			s.more = next
		})
	})
}

// pageState returns the state of the page the router shows.
func (a *app) pageState() *pageState {
	if a.router.Path() == "/search" {
		return &a.search.pageState
	}
	return &a.feed
}

// isDetail reports whether the page is an album, a playlist or an artist.
func isDetail(path string) bool {
	return strings.HasPrefix(path, "/album/") || strings.HasPrefix(path, "/playlist/") || strings.HasPrefix(path, "/artist/")
}

// rowsKey summarises what setRows reads, cheaply. Comparing it each frame
// rebuilds the rows whenever the page's shape changes, even when a caller did
// not mark them stale; a content change within the same shape still marks
// them with rowsDirty.
type rowsKey struct {
	path    string
	detail  detail
	loading bool
	err     string
	more    string
	moreErr string
	// shelves folds the sections' titles and item counts into one number, and
	// loose is how many items a page without sections holds.
	shelves uint64
	loose   int
}

// rowsKeyOf describes the page the rows are built from.
func (a *app) rowsKeyOf() rowsKey {
	s := a.pageState()
	return rowsKey{
		path: a.router.Path(), detail: a.detail,
		loading: s.loading, err: s.err, more: s.more, moreErr: s.moreErr,
		shelves: shelfShape(s.sections), loose: len(s.items),
	}
}

// shelfShape folds each section's title and item count into one number, as
// FNV-1a: a cheap stand-in for the sections themselves.
func shelfShape(sections []youtube.MusicSection) uint64 {
	shape := uint64(14695981039346656037)
	for _, section := range sections {
		for _, r := range section.Title {
			shape = (shape ^ uint64(r)) * 1099511628211
		}
		shape = (shape ^ uint64(len(section.Items))) * 1099511628211
		shape = (shape ^ '|') * 1099511628211
	}
	return shape
}

// syncRows rebuilds the rows when the page's data changed since the last
// build, so an idle page reuses them frame after frame.
func (a *app) syncRows() {
	key := a.rowsKeyOf()
	if a.rows == nil || a.rowsDirty || key != a.rowsKey {
		a.rowsKey, a.rowsDirty = key, false
		a.setRows()
	}
}

// setRows flattens the loaded page into the rows its list shows, and
// collects the songs that playing the page would queue.
func (a *app) setRows() {
	path := a.router.Path()
	s := a.pageState()
	a.rows, a.playable = a.rows[:0], a.playable[:0]
	onDetail := isDetail(path)
	if onDetail {
		a.rows = append(a.rows, row{kind: rowHero, title: a.detail.title, track: -1})
	}
	switch {
	case s.loading:
		a.rows = append(a.rows, row{kind: rowLoading, track: -1})
		return
	case s.err != "":
		a.rows = append(a.rows, row{kind: rowError, title: s.err, track: -1})
		return
	}

	// Songs on an album or playlist page carry no artwork of their own: the
	// page's is what they belong to, and what the player shows.
	fallback := ""
	if onDetail && a.detail.kind != pageArtist {
		fallback = a.detail.art
	}
	sections := s.sections
	if len(sections) == 0 && len(s.items) > 0 {
		sections = []youtube.MusicSection{{Items: s.items}}
	}
	vertical := onDetail && a.detail.kind != pageArtist || path == "/search"
	for index, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		shelf := path + "#" + strconv.Itoa(index)
		songs := 0
		for _, item := range section.Items {
			if kind, _ := targetOf(item); kind == pageTrack {
				songs++
			}
		}
		if section.Title != "" {
			a.rows = append(a.rows, row{kind: rowHeading, title: section.Title, shelf: shelf, track: -1})
		}
		switch {
		case vertical || (onDetail && songs*10 >= len(section.Items)*6):
			for position, item := range section.Items {
				r := row{kind: rowTrack, shelf: shelf, item: item, track: -1, position: position}
				if kind, _ := targetOf(item); kind == pageTrack {
					r.track = len(a.playable)
					if item.Thumbnail == "" {
						item.Thumbnail = fallback
						r.item = item
					}
					a.playable = append(a.playable, item)
				}
				a.rows = append(a.rows, r)
			}
		default:
			var queue []youtube.MusicItem
			for _, item := range section.Items {
				if kind, _ := targetOf(item); kind == pageTrack {
					queue = append(queue, item)
				}
			}
			kind := rowCards
			if songs*10 >= len(section.Items)*6 && len(section.Items) >= 3 && section.Items[0].Duration != "" {
				kind = rowColumns
			}
			a.rows = append(a.rows, row{kind: kind, shelf: shelf, items: section.Items, queue: queue, track: -1})
		}
	}
	if s.more != "" {
		title := "Show more"
		if s.moreErr != "" {
			title = "Could not load more. Try again"
		}
		a.rows = append(a.rows, row{kind: rowMore, title: title, track: -1})
	}
	if len(a.rows) == 0 || (len(a.rows) == 1 && a.rows[0].kind == rowHero) {
		title := "Nothing here yet"
		if path == "/search" {
			title = "No results for “" + a.search.submitted + "”"
		}
		a.rows = append(a.rows, row{kind: rowEmpty, title: title, track: -1})
	}
}

// activate opens what a song, album, artist or playlist is: a song plays from
// its place in queue, and the others open their page.
func (a *app) activate(item youtube.MusicItem, queue []youtube.MusicItem) {
	kind, id := targetOf(item)
	switch kind {
	case pageTrack:
		index := 0
		for i, q := range queue {
			if q.VideoID == item.VideoID {
				index = i
				break
			}
		}
		options, source := a.playbackQueueOptions(index)
		a.playWithOptions(item, queue, index, options, source)
	case pageAlbum, pagePlaylist, pageArtist:
		path := "/" + kind + "/" + url.PathEscape(id)
		a.rememberDetail(path, detail{title: item.Title, subtitle: item.Subtitle, art: item.Thumbnail, kind: kind})
		a.router.Push(path)
	}
}

// playbackQueueOptions adds the playlist position when a track is selected
// from a playlist page. The page title is shown as the queue's source.
func (a *app) playbackQueueOptions(index int) (youtube.UpNextOptions, string) {
	options := youtube.UpNextOptions{}
	path := a.router.Path()
	if strings.HasPrefix(path, "/playlist/") {
		playlistIndex := index
		options.PlaylistID = pathArg(path)
		options.PlaylistIndex = &playlistIndex
	}
	source := a.detail.title
	if source == "" {
		switch path {
		case "/home":
			source = "Home"
		case "/explore":
			source = "Explore"
		case "/library":
			source = "Your library"
		case "/recap":
			source = "Your recap"
		case "/search":
			source = "Search results"
		}
	}
	return options, source
}

// playCollection plays an album or a playlist from its first song, without
// opening its page, as the play button over its card does.
func (a *app) playCollection(item youtube.MusicItem, list string, position int) {
	kind, id := targetOf(item)
	if kind == pageTrack {
		a.play(item, []youtube.MusicItem{item}, 0)
		return
	}
	client := a.client()
	if kind != pageAlbum && kind != pagePlaylist || client == nil {
		return
	}
	key := itemKey("open", item) + "\x00" + list + "#" + strconv.Itoa(position)
	if a.opening == key {
		return
	}
	a.opening = key
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var result *youtube.BrowseResult
		var err error
		if kind == pageAlbum {
			result, err = client.GetAlbum(ctx, id)
		} else {
			result, err = client.GetAllPlaylist(ctx, id)
		}
		var songs []youtube.MusicItem
		if err == nil {
			all := result.Items
			if len(result.Sections) > 0 {
				all = nil
				for _, section := range result.Sections {
					all = append(all, section.Items...)
				}
			}
			for _, song := range all {
				if k, _ := targetOf(song); k == pageTrack {
					if song.Thumbnail == "" {
						song.Thumbnail = item.Thumbnail
					}
					songs = append(songs, song)
				}
			}
		}
		a.update(func() {
			if a.opening == key {
				a.opening = ""
			}
			if len(songs) > 0 {
				options := youtube.UpNextOptions{}
				if kind == pagePlaylist {
					playlistIndex := 0
					options.PlaylistID, options.PlaylistIndex = id, &playlistIndex
				}
				a.playWithOptions(songs[0], songs, 0, options, item.Title)
			}
		})
	})
}

// targetOf says what an item opens. A video ID always wins: rows name their
// artist beside the track itself, and the artist must not capture the tap.
// Album and playlist IDs route only items with no video; the artist only
// items with neither video nor collection.
func targetOf(item youtube.MusicItem) (kind, id string) {
	browse, playlist := item.BrowseID, item.PlaylistID
	if playlist == "" {
		playlist = browse
	}
	isAlbum := strings.HasPrefix(browse, "MPR") || strings.Contains(browse, "privately_owned_release")
	isPlaylist := strings.HasPrefix(playlist, "VL") || strings.HasPrefix(playlist, "PL")
	isArtist := strings.HasPrefix(browse, "UC") || strings.Contains(browse, "privately_owned_artist")
	switch {
	case item.VideoID != "" && !isAlbum && !isPlaylist:
		return pageTrack, item.VideoID
	case isAlbum:
		return pageAlbum, browse
	case isPlaylist:
		return pagePlaylist, playlist
	case isArtist:
		return pageArtist, browse
	case item.VideoID != "":
		return pageTrack, item.VideoID
	}
	return "", ""
}

func isVideo(item youtube.MusicItem) bool {
	return item.Kind == "video"
}

// retry loads the page shown again.
func (a *app) retry() {
	if a.router.Path() == "/search" {
		a.runSearch(a.search.submitted)
		return
	}
	a.reloadPage()
}

// reloadPage bypasses the cached entry for the location the router shows.
func (a *app) reloadPage() {
	location := a.router.Location()
	a.forgetPage(location)
	a.location = ""
	a.onNavigate()
	a.location = location
}
