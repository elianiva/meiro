package main

import (
	"slices"
	"testing"

	"github.com/elianiva/meiro/youtube"
)

func TestSortPlaylistSections(t *testing.T) {
	items := []youtube.MusicItem{
		{VideoID: "long", Title: "Bravo", Subtitle: "Beta • Album"},
		{BrowseID: "MPRalbum", Title: "Album tile"},
		{VideoID: "short", Title: "alpha", Subtitle: "Alpha • Album", Duration: "2:00"},
		{VideoID: "duplicate", Title: "Alpha", Subtitle: "Alpha • Album", Duration: "2:00"},
	}
	sections := []youtube.MusicSection{
		{Title: "Songs", Kind: "playlist-shelf", ContinuationToken: "next", Items: items},
		{Title: "More songs", Kind: "playlist-shelf", Items: []youtube.MusicItem{{VideoID: "later", Title: "Charlie"}}},
	}

	tests := []struct {
		name string
		sort playlistSort
		want []string
	}{
		{"title ascending", playlistSort{field: playlistSortTitle}, []string{"short", "duplicate", "long"}},
		{"title descending", playlistSort{field: playlistSortTitle, desc: true}, []string{"long", "short", "duplicate"}},
		{"artist ascending", playlistSort{field: playlistSortArtist}, []string{"short", "duplicate", "long"}},
		{"duration ascending", playlistSort{field: playlistSortDuration}, []string{"long", "short", "duplicate"}},
		{"duration descending", playlistSort{field: playlistSortDuration, desc: true}, []string{"short", "duplicate", "long"}},
		{"original descending", playlistSort{desc: true}, []string{"later", "duplicate", "short"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSections := sortPlaylistSections(sections, tt.sort)
			got := gotSections[0].Items
			ids := make([]string, 0)
			for _, item := range got {
				if item.IsTrack() {
					ids = append(ids, item.VideoID)
				}
			}
			if !slices.Equal(ids, tt.want) {
				t.Fatalf("got %v, want %v", ids, tt.want)
			}
		})
	}
	got := sortPlaylistSections(sections, playlistSort{field: playlistSortTitle})
	if len(got) != len(sections) || got[0].Title != sections[0].Title || got[0].Kind != sections[0].Kind || got[0].ContinuationToken != sections[0].ContinuationToken || got[1].Title != sections[1].Title {
		t.Fatalf("section metadata changed: %#v", got)
	}
	if got[0].Items[1] != items[1] || got[0].Items[1].Target().Kind != youtube.TargetAlbum {
		t.Fatalf("non-track item moved or disappeared: %#v", got[0].Items)
	}
	if got[1].Items[0].VideoID != "later" {
		t.Fatalf("tracks crossed section boundary: %#v", got[1].Items)
	}
	if got := sortPlaylistSections(sections, playlistSort{desc: true}); got[0].Items[1] != items[1] || got[0].Title != sections[0].Title || got[1].Title != sections[1].Title {
		t.Fatalf("the reversed original order moved a heading or a non-track item: %#v", got)
	}
	if got := sortPlaylistSections(sections, playlistSort{}); !slices.Equal(got[0].Items, items) {
		t.Fatalf("original order changed: %v", got[0].Items)
	}
}
