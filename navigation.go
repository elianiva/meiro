package main

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/youtube"
)

const (
	// pageCacheLimit is how many recent pages are kept, with what they
	// loaded, so going back to one shows it at once.
	pageCacheLimit = 12
	// pageCacheTTL is how long a kept page is shown before it loads again.
	pageCacheTTL = 5 * time.Minute
)

// detail is the heading of the album, playlist or artist page shown. The
// action that opens such a page seeds it with what it had at hand; the page
// replaces it with its own once it loads.
type detail struct {
	title    string
	subtitle string
	art      string
}

// pageState is what a page has loaded, whichever page it is.
type pageState struct {
	loading bool
	err     string
	// offline says err is the network being out of reach, not a page that
	// failed: the message tells the user to check the connection.
	offline  bool
	sections []youtube.MusicSection
	items    []youtube.MusicItem
	more     string
	// moreErr is why the last request for more failed. The page keeps what it
	// has, and the button to ask again.
	moreErr string
	// partial says the page shows what has loaded of it so far, and more of
	// it is still on the way.
	partial bool
}

// pageData is everything the app holds for a page: what it loaded, its
// heading, and the rows built from them. The page cache keeps it whole, so a
// field added here is cached and restored with the rest.
type pageData struct {
	feed   pageState
	detail detail
	// library is whether an album or a playlist page is in the library.
	library youtube.LibraryState
	// subscription is whether an artist's page is followed.
	subscription youtube.Subscription
	loadedAt     time.Time
	rows         []row
	playable     []youtube.MusicItem
	// rowsDirty marks the rows stale. A change of the page's data sets it,
	// and the next frame rebuilds them; otherwise they are reused. rowsKey
	// is the shape they were built from, so a change a caller did not mark
	// still rebuilds them.
	rowsDirty bool
	rowsKey   rowsKey
}

// fresh reports whether a kept page can be shown as it is.
func (p *pageData) fresh(now time.Time) bool {
	return !p.loadedAt.IsZero() && now.Sub(p.loadedAt) < pageCacheTTL && !p.feed.loading && !p.feed.partial && p.feed.err == ""
}

// routed is the route of a location, kept until the router moves on.
type routed struct {
	location string
	path     string
	route    *route
	id       string
}

// route is the route of the page the router shows, and the ID its path
// names. The view asks for it several times a frame, and the router only
// moves between frames, so the match is kept until the location changes.
// The location is compared rather than the path, which the router parses
// out of it on every call.
func (a *app) route() (*route, string) {
	if location := a.router.Location(); a.routed.route == nil || a.routed.location != location {
		path := a.router.Path()
		r, id := matchRoute(path)
		a.routed = routed{location: location, path: path, route: r, id: id}
	}
	return a.routed.route, a.routed.id
}

// path is the path of the page the router shows.
func (a *app) path() string {
	a.route()
	return a.routed.path
}

// syncLocation notices a new location, once: the router also moves on its
// own, by the keyboard's back and forward keys, so the view checks it every
// frame.
func (a *app) syncLocation() {
	if location := a.router.Location(); location != a.location {
		a.onNavigate()
		a.location = location
	}
}

// onNavigate keeps the page being left, and shows the new one: from the
// page cache when it is fresh there, or loaded anew.
func (a *app) onNavigate() {
	if a.location != "" {
		a.pages.Put(a.location, a.page)
	}
	// A playlist page opens unfiltered, with its search closed: the search
	// belongs to the visit, not the page. A page kept from before rebuilds
	// its rows, since the search is part of the rows' key.
	a.detailFilter, a.detailSearchOpen, a.detailFocus = "", false, false
	a.pageLoad.stop()
	a.pageMore.stop()
	a.nowPlaying.open, a.nowPlaying.closing = false, false
	location := a.router.Location()
	r, id := a.route()
	if r.open != nil {
		r.open(a)
	}
	// The suggestions follow the text being typed, wherever the user is: a
	// navigation closes the panel they open, and the search page focuses the
	// bar only when it has no results to show.
	a.cancelSuggestions()
	if r.id == routeSearch {
		a.focusSearch = a.search.submitted == ""
	}
	if page, ok := a.pages.Get(location); ok && (r.id == routeSearch || page.fresh(time.Now())) {
		a.page = page
		return
	}
	a.page = &pageData{rowsDirty: true}
	a.pages.Delete(location)
	if r.isDetail() {
		a.page.detail, _ = a.details.Get(a.path())
	}
	// The list is shared by every page, so a new page starts at its top.
	// A new state, not a reset one: the frame of the old one is still read by
	// the accessibility tree built from the last frame.
	a.list = &ui.ListState{}
	a.list.ScrollTo(0, ui.Start)
	if r.load != nil && (r.needsAccount == nil || a.signedIn()) {
		a.loadPage(r, id)
	}
}

// openTarget opens the page of an album, a playlist or an artist, with the
// heading the item that opened it had.
func (a *app) openTarget(target youtube.Target, heading detail) {
	path, ok := targetPath(target)
	if !ok {
		return
	}
	a.details.Put(path, heading)
	a.router.Push(path)
}

// loadPage loads the page shown with its route's loader. The client is the
// one in use when the load began, since the app's own may change meanwhile.
func (a *app) loadPage(r *route, id string) {
	client := a.client()
	path := a.path()
	// The result goes to the page that asked for it, wherever the router
	// has moved since.
	p := a.page
	p.feed.loading, p.feed.err, p.feed.offline = true, "", false
	p.rowsDirty = true
	a.pageMore.stop()
	startProgress(a, &a.pageLoad, noTimeout, func(ctx context.Context, progress func(func())) (*youtube.BrowseResult, error) {
		return r.load(ctx, client, id, func(partial *youtube.BrowseResult) {
			progress(func() {
				// A page that finished ignores what was still on its way.
				if p.feed.loading || p.feed.partial {
					a.applyPage(p, r, path, client, partial, true)
				}
			})
		})
	}, func(result *youtube.BrowseResult, err error) {
		feed := &p.feed
		if err != nil && feed.partial && result != nil {
			// Some of the page is already there: keep it, and say the rest
			// did not come.
			log.Printf("loading the rest of %s: %v", path, err)
			a.applyPage(p, r, path, client, result, false)
			feed.more, feed.moreErr = "", ""
			return
		}
		if err != nil {
			p.rowsDirty = true
			feed.loading, feed.partial = false, false
			feed.err, feed.offline = failure(err)
			// A rejected session is not a page that failed: ask whether the
			// account is still there, and say so when it is not.
			if cur := a.account.session; errors.Is(err, youtube.ErrUnauthorized) && cur != nil && cur.client == client {
				a.checkSession()
			}
			return
		}
		a.applyPage(p, r, path, client, result, false)
	})
}

// applyPage shows a result on page p. A partial one is the start of a page
// that is still loading: it is shown at once, without the button that asks
// for more, since the rest is already on its way.
func (a *app) applyPage(p *pageData, r *route, path string, client *youtube.Client, result *youtube.BrowseResult, partial bool) {
	p.rowsDirty = true
	p.feed.loading, p.feed.partial = false, partial
	// A page over its own content takes the name and the picture from what
	// it loaded; what the caller seeded is only what it had at hand when the
	// page was opened.
	if r.isDetail() {
		if result.Title != "" {
			p.detail.title = result.Title
		}
		if result.Thumbnail != "" {
			p.detail.art = result.Thumbnail
		}
		a.details.Put(path, p.detail)
		if result.Library.ID != "" {
			p.library = result.Library
		}
		if result.Subscription.ChannelID != "" {
			p.subscription = result.Subscription
		}
	}
	p.feed.sections = result.Sections
	// Items are the page's songs, cards and rows all together, which its
	// sections already hold; they are only a page by themselves when it has
	// no sections.
	p.feed.items = nil
	if len(result.Sections) == 0 {
		p.feed.items = result.Items
	}
	p.feed.more = result.ContinuationToken
	if partial {
		p.feed.more = ""
	}
	p.loadedAt = time.Now()
	if !partial && r.id == routeLibrary {
		a.knownPlaylists = knownPlaylists{client: client, items: libraryPlaylists(result)}
	}
}

// pageState returns what the page the router shows has loaded.
func (a *app) pageState() *pageState {
	if r, _ := a.route(); r.id == routeSearch {
		return &a.search.pageState
	}
	return &a.page.feed
}

// moreTimeout bounds a request for the next part of a page.
const moreTimeout = time.Minute

// moreItems is the next part of a page, and the token for the part after.
type moreItems struct {
	items []youtube.MusicItem
	next  string
}

// loadMore appends the next part of the page shown. A page load that begins
// meanwhile, or a navigation, drops it; asking for more does not itself
// cancel a load in flight.
func (a *app) loadMore() {
	s := a.pageState()
	if s.more == "" {
		return
	}
	r, _ := a.route()
	token, searching := s.more, r.id == routeSearch
	s.more, s.moreErr = "", ""
	p := a.page
	p.rowsDirty = true
	client := a.client()
	startTask(a, &a.pageMore, moreTimeout, func(ctx context.Context) (moreItems, error) {
		if searching {
			result, err := client.ContinueSearch(ctx, token)
			if err != nil {
				return moreItems{}, err
			}
			return moreItems{result.Items, result.ContinuationToken}, nil
		}
		result, err := client.ContinueBrowse(ctx, token)
		if err != nil {
			return moreItems{}, err
		}
		return moreItems{result.Items, result.ContinuationToken}, nil
	}, func(more moreItems, err error) {
		p.rowsDirty = true
		if err != nil {
			s.more, s.moreErr = token, err.Error()
			return
		}
		if len(s.sections) > 0 {
			last := &s.sections[len(s.sections)-1]
			last.Items = append(last.Items, more.items...)
		} else {
			s.items = append(s.items, more.items...)
		}
		s.more = more.next
	})
}

// failure is how a failed load is shown: the first line of what went wrong,
// and whether it was the network being out of reach.
func failure(err error) (msg string, offline bool) {
	return firstLine(err.Error()), errors.Is(err, youtube.ErrUnreachable)
}

// retry loads the page shown again.
func (a *app) retry() {
	if r, _ := a.route(); r.id == routeSearch {
		a.runSearch(a.search.submitted, a.search.params)
		return
	}
	a.reloadPage()
}

// reloadPage loads the page shown again, bypassing the page cache.
func (a *app) reloadPage() {
	location := a.router.Location()
	a.pages.Delete(location)
	a.location = ""
	a.onNavigate()
	a.location = location
}

// activate opens what an item is: a song plays from its place in tracks,
// and the others open their page.
func (a *app) activate(item youtube.MusicItem, tracks []youtube.MusicItem) {
	target := item.Target()
	switch target.Kind {
	case youtube.TargetTrack:
		index := max(indexOf(tracks, item.VideoID), 0)
		options, source := a.queueContext(index)
		a.playFrom(item, tracks, index, options, source)
	case youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist:
		a.openTarget(target, detail{title: item.Title, subtitle: item.Subtitle, art: item.Thumbnail})
	case youtube.TargetNone:
	}
}

// indexOf is the place of a track among tracks, or -1.
func indexOf(tracks []youtube.MusicItem, videoID string) int {
	for i := range tracks {
		if tracks[i].VideoID == videoID {
			return i
		}
	}
	return -1
}

// firstLine trims a long error to something a message can hold.
func firstLine(text string) string {
	text, _, _ = strings.Cut(text, "\n")
	return text
}
