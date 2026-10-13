package main

import (
	"strings"
	"testing"

	"github.com/elianiva/meiro/youtube"
)

// Every page has exactly one route, and every route's path leads back to it.
func TestRouteTableIsComplete(t *testing.T) {
	seen := map[routeID]bool{}
	patterns := map[string]bool{}
	for _, r := range routes {
		if seen[r.id] {
			t.Errorf("route %d is listed twice", r.id)
		}
		if patterns[r.pattern] {
			t.Errorf("pattern %s is listed twice", r.pattern)
		}
		seen[r.id], patterns[r.pattern] = true, true
		if r.heading == nil && !r.isDetail() {
			t.Errorf("%s has no heading", r.pattern)
		}
		id := ""
		if r.isDetail() {
			id = "MPREb a/b" // escaped in the path, and back
		}
		got, gotID := matchRoute(r.path(id))
		if got.id != r.id || gotID != id {
			t.Errorf("%s leads to %s with id %q", r.path(id), got.pattern, gotID)
		}
	}
	for id := routeNotFound + 1; id <= routeArtist; id++ {
		if !seen[id] {
			t.Errorf("route %d has no entry in routes", id)
		}
	}
	for _, kind := range []youtube.TargetKind{youtube.TargetAlbum, youtube.TargetPlaylist, youtube.TargetArtist} {
		if _, ok := detailRoute(kind); !ok {
			t.Errorf("target kind %d has no page", kind)
		}
	}
}

func TestMatchRouteRejectsWhatNoPageShows(t *testing.T) {
	for _, path := range []string{"/", "/nowhere", "/album", "/album/", "/album/a/b", "/home/x"} {
		if r, _ := matchRoute(path); r.id != routeNotFound {
			t.Errorf("%s matched %s", path, r.pattern)
		}
	}
	if r, id := matchRoute("/playlist/VLPL1/"); r.id != routePlaylist || id != "VLPL1" {
		t.Errorf("a trailing slash: %s %q", r.pattern, id)
	}
}

// Matching the route of a page allocates nothing: the view asks for it
// several times a frame.
func TestMatchRouteAllocatesNothing(t *testing.T) {
	if n := testing.AllocsPerRun(100, func() { matchRoute("/artist/UCabc") }); n != 0 {
		t.Errorf("matchRoute allocates %v times", n)
	}
}

// The rail lists its destinations in the order of routes, with Settings at
// its foot.
func TestRailComesFromRoutes(t *testing.T) {
	var items, footer []string
	for _, r := range routes {
		if r.rail == nil {
			continue
		}
		if r.rail.footer {
			footer = append(footer, r.rail.label)
		} else {
			items = append(items, r.rail.label)
		}
	}
	if got, want := strings.Join(items, ","), "Home,Search,Explore,Library,Recap"; got != want {
		t.Errorf("rail destinations: %s, want %s", got, want)
	}
	if len(footer) != 1 || footer[0] != "Settings" {
		t.Errorf("rail footer: %v", footer)
	}
}
