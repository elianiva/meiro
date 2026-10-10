package main

import (
	"time"

	"github.com/elianiva/meiro/youtube"
)

const (
	pageCacheLimit = 12
	pageCacheTTL   = 5 * time.Minute
)

// cachedPage is app-owned route state. MyGo retains element state such as
// scroll and focus for history entries; this keeps the data those elements
// render, without retaining frame-scoped UI objects.
type cachedPage struct {
	feed      pageState
	rows      []row
	playable  []youtube.MusicItem
	rowsDirty bool
	rowsKey   rowsKey
	detail    detail
	loadedAt  time.Time
}

func (p cachedPage) fresh(now time.Time) bool {
	return !p.loadedAt.IsZero() && now.Sub(p.loadedAt) < pageCacheTTL && !p.feed.loading && p.feed.err == ""
}

func (a *app) cacheCurrentPage() {
	if a.location == "" {
		return
	}
	page := a.pageCache[a.location]
	if page == nil {
		page = &cachedPage{}
		a.pageCache[a.location] = page
	}
	page.feed = a.feed
	page.rows = a.rows
	page.playable = a.playable
	page.rowsDirty = a.rowsDirty
	page.rowsKey = a.rowsKey
	page.detail = a.detail
	page.loadedAt = a.pageLoadedAt
	a.touchPage(a.location)
}

func (a *app) restorePage(location string) (*cachedPage, bool) {
	page := a.pageCache[location]
	if page == nil {
		return nil, false
	}
	a.feed = page.feed
	a.rows = page.rows
	a.playable = page.playable
	a.rowsDirty = page.rowsDirty
	a.rowsKey = page.rowsKey
	a.detail = page.detail
	a.pageLoadedAt = page.loadedAt
	a.touchPage(location)
	return page, true
}

func (a *app) touchPage(location string) {
	a.pageCacheOrder = append(removeKey(a.pageCacheOrder, location), location)
	for len(a.pageCacheOrder) > pageCacheLimit {
		oldest := a.pageCacheOrder[0]
		a.pageCacheOrder = a.pageCacheOrder[1:]
		delete(a.pageCache, oldest)
	}
}

func removeKey(keys []string, key string) []string {
	for index, candidate := range keys {
		if candidate == key {
			return append(keys[:index], keys[index+1:]...)
		}
	}
	return keys
}

func (a *app) forgetPage(location string) {
	delete(a.pageCache, location)
	a.pageCacheOrder = removeKey(a.pageCacheOrder, location)
}

func (a *app) forgetPages() {
	clear(a.pageCache)
	a.pageCacheOrder = nil
}
