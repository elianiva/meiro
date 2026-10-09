package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func TestPageCacheEvictsTheLeastRecentlyUsedRoute(t *testing.T) {
	a := newTestApp()
	for i := range pageCacheLimit + 1 {
		a.location = fmt.Sprintf("/page/%d", i)
		a.feed = pageState{more: fmt.Sprintf("token-%d", i)}
		a.cacheCurrentPage()
	}

	if len(a.pageCache) != pageCacheLimit {
		t.Fatalf("cached routes = %d, want %d", len(a.pageCache), pageCacheLimit)
	}
	if _, ok := a.pageCache["/page/0"]; ok {
		t.Fatal("least recently used route was not evicted")
	}
	if _, ok := a.pageCache[fmt.Sprintf("/page/%d", pageCacheLimit)]; !ok {
		t.Fatal("most recently used route was evicted")
	}
}

func TestPageCacheUsesTheWholeLocationAsItsKey(t *testing.T) {
	a := newTestApp()
	for _, test := range []struct {
		location string
		more     string
	}{
		{location: "/home?section=for-you", more: "for-you"},
		{location: "/home?section=charts", more: "charts"},
	} {
		a.location = test.location
		a.feed = pageState{more: test.more}
		a.cacheCurrentPage()
	}

	for _, test := range []struct {
		location string
		more     string
	}{
		{location: "/home?section=for-you", more: "for-you"},
		{location: "/home?section=charts", more: "charts"},
	} {
		if page, ok := a.restorePage(test.location); !ok || page.feed.more != test.more {
			t.Errorf("cache entry for %q = %+v, found %t", test.location, page, ok)
		}
	}
}

func TestCachedPageFreshnessRejectsStaleAndFailedLoads(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name string
		page cachedPage
		want bool
	}{
		{name: "fresh", page: cachedPage{loadedAt: now}, want: true},
		{name: "stale", page: cachedPage{loadedAt: now.Add(-pageCacheTTL)}, want: false},
		{name: "failed", page: cachedPage{loadedAt: now, feed: pageState{err: "failed"}}, want: false},
		{name: "loading", page: cachedPage{loadedAt: now, feed: pageState{loading: true}}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.page.fresh(now); got != test.want {
				t.Errorf("fresh = %t, want %t", got, test.want)
			}
		})
	}
}

func TestForgetPageInvalidatesOnlyTheRequestedLocation(t *testing.T) {
	a := newTestApp()
	for _, location := range []string{"/home", "/explore"} {
		a.location = location
		a.cacheCurrentPage()
	}
	a.forgetPage("/home")
	if _, ok := a.pageCache["/home"]; ok {
		t.Fatal("invalidated page remains cached")
	}
	if _, ok := a.pageCache["/explore"]; !ok {
		t.Fatal("invalidating one page removed another page")
	}
}

func TestLateLoadDoesNotOverwriteTheRouteNowShown(t *testing.T) {
	a := newTestApp()
	var work []func()
	a.run = func(fn func()) { work = append(work, fn) }
	tt := ui.NewTester(a.view, 1000, 700)
	if len(work) != 1 || a.router.Path() != "/home" {
		t.Fatalf("initial route: path %q, queued loads %d", a.router.Path(), len(work))
	}

	a.router.Push("/explore")
	tt.Frame()
	if len(work) != 2 || a.router.Path() != "/explore" {
		t.Fatalf("explore route: path %q, queued loads %d", a.router.Path(), len(work))
	}
	work[0]()
	if !a.feed.loading || len(a.feed.sections) != 0 {
		t.Fatalf("late Home load changed Explore state: %+v", a.feed)
	}
	work[1]()
	if a.feed.loading || a.feed.err != "" || len(a.feed.sections) == 0 {
		t.Fatalf("Explore load did not apply: %+v", a.feed)
	}
}

func TestBackRestoresTheHomeListScroll(t *testing.T) {
	a := newTestApp()
	tt := ui.NewTester(a.view, 1000, 300)
	tt.Scroll(500, 200, 0, 480)
	first, _ := a.list.Visible()
	if first == 0 {
		t.Fatal("the Home list did not scroll")
	}

	a.router.Push("/explore")
	tt.Frame()
	a.router.Back()
	tt.Frame()
	if restored, _ := a.list.Visible(); restored != first {
		t.Errorf("restored Home list starts at row %d, want %d", restored, first)
	}
}
