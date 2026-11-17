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
	pageCacheLimit = 12
	pageCacheTTL   = 5 * time.Minute
)

type detail struct {
	title       string
	subtitle    string
	art         string
	description string
}

type pageState struct {
	loading      bool
	err          string
	offline      bool
	sections     []youtube.MusicSection
	items        []youtube.MusicItem
	more         string
	moreErr      string
	partial      bool
	similar      []youtube.MusicItem
	similarShown int
	similarMore  string
	shape        uint64
	shaped       bool
}

type pageData struct {
	feed         pageState
	detail       detail
	library      youtube.LibraryState
	subscription youtube.Subscription
	loadedAt     time.Time
	rows         []row
	playable     []youtube.MusicItem
	playlistMore string
	rowsDirty    bool
	rowsKey      rowsKey
	downloads    int
	list         *ui.ListState
	scroll       *ui.ScrollState
}

func (p *pageData) fresh(now time.Time) bool {
	return !p.loadedAt.IsZero() && now.Sub(p.loadedAt) < pageCacheTTL && !p.feed.loading && !p.feed.partial && p.feed.err == ""
}

type routed struct {
	location string
	path     string
	route    *route
	id       string
}

func (a *app) route() (*route, string) {
	if location := a.router.Location(); a.routed.route == nil || a.routed.location != location {
		path := a.router.Path()
		r, id := matchRoute(path)
		a.routed = routed{location: location, path: path, route: r, id: id}
	}
	return a.routed.route, a.routed.id
}

func (a *app) path() string {
	a.route()
	return a.routed.path
}

func (a *app) syncLocation() {
	if location := a.router.Location(); location != a.location {
		a.onNavigate()
		a.location = location
	}
}

func (a *app) onNavigate() {
	if a.location != "" {
		a.pages.Put(a.location, a.page)
	}
	a.detailFilter, a.detailSearchOpen, a.detailSearchHadFocus, a.detailSort, a.detailSortOpen = "", false, false, playlistSort{}, false
	a.detailCompact = false
	a.closeSettingsSearch()
	a.pageLoad.stop()
	a.pageMore.stop()
	a.nowPlaying.open, a.nowPlaying.closing = false, false
	a.nowPlaying.navigateToSet = false
	a.pull = pullState{}
	location := a.router.Location()
	r, id := a.route()
	if r.open != nil {
		r.open(a)
	}
	a.cancelSuggestions()
	if r.id == routeSearch {
		if a.search.submitted == "" {
			a.searchField.Focus()
		}
	}
	if page, ok := a.pages.Get(location); ok && (r.id == routeSearch || r.load == nil || page.fresh(time.Now())) {
		a.page, a.list = page, page.list
		return
	}
	a.list = &ui.ListState{}
	a.list.ScrollTo(0, ui.Start)
	a.page = &pageData{rowsDirty: true, list: a.list, scroll: &ui.ScrollState{}}
	a.pages.Delete(location)
	if r.isDetail() {
		a.page.detail, _ = a.details.Get(a.path())
	}
	if r.load != nil && (r.needsAccount == nil || a.signedIn()) {
		a.loadPage(r, id)
	}
}

func (a *app) openTarget(target youtube.Target, heading detail) {
	path, ok := targetPath(target)
	if !ok {
		return
	}
	a.details.Put(path, heading)
	a.router.Push(path)
	if a.nowPlaying.miniOpen {
		a.setMiniShown(false)
		a.desktop.closeMini()
		a.desktop.bringToFront()
	}
}

func (a *app) loadPage(r *route, id string) {
	client := a.client()
	path := a.path()
	p := a.page
	p.feed.loading, p.feed.err, p.feed.offline = true, "", false
	p.rowsDirty = true
	a.pageMore.stop()
	startProgress(a, &a.pageLoad, noTimeout, func(ctx context.Context, progress func(func())) (*youtube.BrowseResult, error) {
		return r.load(ctx, client, id, func(partial *youtube.BrowseResult) {
			progress(func() {
				if p.feed.loading || p.feed.partial {
					a.applyPage(p, r, path, client, partial, true)
				}
			})
		})
	}, func(result *youtube.BrowseResult, err error) {
		feed := &p.feed
		if err != nil && feed.partial && result != nil {
			log.Printf("loading the rest of %s: %v", path, err)
			a.applyPage(p, r, path, client, result, false)
			feed.more, feed.moreErr = "", ""
			return
		}
		if downloaded, ok := a.offlinePage(id); ok && r.isDetail() && errors.Is(err, youtube.ErrUnreachable) {
			a.applyPage(p, r, path, client, downloaded, false)
			return
		}
		if err != nil {
			p.rowsDirty = true
			feed.loading, feed.partial = false, false
			feed.err, feed.offline = failure(err)
			if cur := a.account.session; errors.Is(err, youtube.ErrUnauthorized) && cur != nil && cur.client == client {
				a.checkSession()
			}
			return
		}
		a.applyPage(p, r, path, client, result, false)
		a.syncCollectionDownload(p, r, id)
		if r.id == routePlaylist {
			token, suggestions := playlistContinuations(feed.sections, feed.more)
			p.playlistMore = token
			p.feed.more = ""
			a.loadPlaylistRest(p, r, id, client, token, suggestions)
		}
	})
}

func (a *app) loadPlaylistRest(p *pageData, r *route, id string, client *youtube.Client, token, suggestions string) {
	if token == "" || a.pageSimilar.busy() {
		return
	}
	startTask(a, &a.pageSimilar, moreTimeout, func(ctx context.Context) (moreItems, error) {
		more, err := readPlaylistRest(ctx, token, client.ContinueBrowse)
		if err != nil || len(more.sections) > 0 || suggestions == "" || suggestions == token {
			return more, err
		}
		rest, err := readPlaylistRest(ctx, suggestions, client.ContinueBrowse)
		more.items = append(more.items, rest.items...)
		more.sections, more.next = rest.sections, rest.next
		return more, err
	}, func(more moreItems, err error) {
		p.rowsDirty = true
		if err != nil {
			log.Printf("loading the rest of a playlist: %v", err)
			return
		}
		p.playlistMore = ""
		feed := &p.feed
		if len(more.items) > 0 {
			switch {
			case len(feed.sections) > 0:
				last := &feed.sections[len(feed.sections)-1]
				last.Items = append(last.Items, more.items...)
			default:
				feed.items = append(feed.items, more.items...)
			}
		}
		if len(more.sections) > 0 {
			for _, section := range more.sections {
				feed.similar = append(feed.similar, section.Items...)
			}
			feed.similarMore = more.next
			feed.similarShown = min(suggestionsShown, len(feed.similar))
		}
		a.syncCollectionDownload(p, r, id)
	})
}

func playlistContinuations(sections []youtube.MusicSection, pageToken string) (songs, suggestions string) {
	for _, section := range sections {
		if (section.Kind == "musicPlaylistShelfRenderer" || section.Kind == "playlistVideoListRenderer") && section.ContinuationToken != "" {
			return section.ContinuationToken, pageToken
		}
	}
	return pageToken, ""
}

func readPlaylistRest(ctx context.Context, token string, continueBrowse func(context.Context, string) (*youtube.BrowseResult, error)) (moreItems, error) {
	var tracks []youtube.MusicItem
	seen := make(map[string]struct{})
	for token != "" {
		if _, tokenRepeated := seen[token]; tokenRepeated {
			break
		}
		seen[token] = struct{}{}
		result, err := continueBrowse(ctx, token)
		if err != nil {
			return moreItems{items: tracks}, err
		}

		var playlistPage bool
		var nextPlaylist string
		var suggestions []youtube.MusicSection
		for _, section := range result.Sections {
			if isPlaylistContinuation(section.Kind) {
				playlistPage = true
				tracks = append(tracks, section.Items...)
				if section.ContinuationToken != "" {
					nextPlaylist = section.ContinuationToken
				}
				continue
			}
			suggestions = append(suggestions, section)
		}
		if len(suggestions) > 0 {
			next := result.ContinuationToken
			for _, section := range suggestions {
				if section.ContinuationToken != "" {
					next = section.ContinuationToken
					break
				}
			}
			return moreItems{items: tracks, sections: suggestions, next: next}, nil
		}
		if playlistPage {
			token = nextPlaylist
			if token == "" {
				token = result.ContinuationToken
			}
			continue
		}
		tracks = append(tracks, result.Items...)
		token = result.ContinuationToken
	}
	return moreItems{items: tracks}, nil
}

func isPlaylistContinuation(kind string) bool {
	switch kind {
	case "playlistVideoListContinuation", "musicPlaylistShelfContinuation":
		return true
	default:
		return false
	}
}

func (a *app) applyPage(p *pageData, r *route, path string, client *youtube.Client, result *youtube.BrowseResult, partial bool) {
	p.rowsDirty = true
	p.feed.loading, p.feed.partial = false, partial
	if r.isDetail() {
		if result.Title != "" {
			p.detail.title = result.Title
		}
		if result.Thumbnail != "" {
			p.detail.art = result.Thumbnail
		}
		if result.Description != "" {
			p.detail.description = result.Description
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

func (a *app) pageState() *pageState {
	if r, _ := a.route(); r.id == routeSearch {
		return &a.search.pageState
	}
	return &a.page.feed
}

const moreTimeout = time.Minute

type moreItems struct {
	items    []youtube.MusicItem
	sections []youtube.MusicSection
	next     string
}

func (a *app) loadMore() {
	s := a.pageState()
	r, _ := a.route()
	suggestions := r.id == routePlaylist && len(s.similar) > 0
	token := s.more
	if suggestions {
		token = s.similarMore
	}
	if token == "" || a.pageMore.busy() {
		return
	}
	searching, home, podcast := r.id == routeSearch, r.id == routeHome, r.id == routePodcast
	s.more, s.moreErr = "", ""
	if suggestions {
		s.similarMore = ""
	}
	p := a.page
	p.rowsDirty = true
	client := a.client()
	startTask(a, &a.pageMore, moreTimeout, func(ctx context.Context) (moreItems, error) {
		if searching {
			result, err := client.ContinueSearch(ctx, token)
			if err != nil {
				return moreItems{}, err
			}
			return moreItems{items: result.Items, next: result.ContinuationToken}, nil
		}
		continueBrowse := client.ContinueBrowse
		if podcast {
			continueBrowse = client.ContinuePodcast
		}
		result, err := continueBrowse(ctx, token)
		if err != nil {
			return moreItems{}, err
		}
		return moreItems{items: result.Items, sections: result.Sections, next: result.ContinuationToken}, nil
	}, func(more moreItems, err error) {
		p.rowsDirty = true
		if err != nil {
			s.more, s.moreErr = token, err.Error()
			return
		}
		if len(more.items) == 0 && len(more.sections) == 0 {
			s.more, s.moreErr = token, "The continuation returned no content."
			return
		}
		switch {
		case home && len(more.sections) > 0:
			s.sections = append(s.sections, more.sections...)
		case r.id == routePlaylist && len(more.sections) > 0:
			for _, section := range more.sections {
				s.similar = append(s.similar, section.Items...)
			}
			s.similarMore = more.next
			s.similarShown = max(s.similarShown, min(suggestionsShown, len(s.similar)))
		case len(s.sections) > 0:
			last := &s.sections[len(s.sections)-1]
			last.Items = append(last.Items, more.items...)
		default:
			s.items = append(s.items, more.items...)
		}
		if !suggestions {
			s.more = more.next
		}
	})
}

const suggestionsShown = 5

func (a *app) moreSimilar() {
	s := a.pageState()
	if s.similarShown < len(s.similar) {
		s.similarShown = min(s.similarShown+suggestionsShown, len(s.similar))
		a.page.rowsDirty = true
		return
	}
	a.loadMore()
}

func failure(err error) (msg string, offline bool) {
	return firstLine(err.Error()), errors.Is(err, youtube.ErrUnreachable)
}

func (a *app) retry() {
	if r, _ := a.route(); r.id == routeSearch {
		a.runSearch(a.search.submitted, a.search.params)
		return
	}
	a.reloadPage()
}

func (a *app) reloadPage() {
	location := a.router.Location()
	a.pages.Delete(location)
	a.location = ""
	a.onNavigate()
	a.location = location
}

func (a *app) activate(item youtube.MusicItem, tracks []youtube.MusicItem) {
	a.activateAt(item, tracks, indexOf(tracks, item.VideoID))
}

func (a *app) activateAt(item youtube.MusicItem, tracks []youtube.MusicItem, index int) {
	target := item.Target()
	switch target.Kind {
	case youtube.TargetTrack:
		index = max(index, 0)
		options, source := a.queueContext(index)
		a.playFrom(item, tracks, index, options, source)
	case youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist, youtube.TargetPodcast:
		a.openTarget(target, detail{title: item.Title, subtitle: item.Subtitle, art: item.Thumbnail})
	case youtube.TargetNone:
	}
}

func indexOf(tracks []youtube.MusicItem, videoID string) int {
	for i := range tracks {
		if tracks[i].VideoID == videoID {
			return i
		}
	}
	return -1
}

func firstLine(text string) string {
	text, _, _ = strings.Cut(text, "\n")
	return text
}
