package main

import (
	"cmp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/elianiva/meiro/youtube"
)

// playlistSortField is the value the playlist page orders its tracks by.
type playlistSortField uint8

const (
	playlistSortOriginal playlistSortField = iota
	playlistSortTitle
	playlistSortArtist
	playlistSortDuration
)

// playlistSort is one playlist page's ordering choice. Original is the zero
// value, so an untouched page keeps YouTube Music's order.
type playlistSort struct {
	field playlistSortField
	desc  bool
}

func (s playlistSort) active() bool { return s.field != playlistSortOriginal }

// ordered reports whether the page shows its tracks in an order other than the
// one YouTube Music gave: a field is sorted by, or the original order is read
// backwards.
func (s playlistSort) ordered() bool { return s.active() || s.desc }

func (s playlistSort) label() string {
	switch s.field {
	case playlistSortOriginal:
		return "Original order"
	case playlistSortTitle:
		return "Title"
	case playlistSortArtist:
		return "Artist"
	case playlistSortDuration:
		return "Duration"
	}
	return "Original order"
}

// choose picks the field the page orders its tracks by. The direction belongs
// to the button beside the control, so picking a field keeps it.
func (s *playlistSort) choose(field playlistSortField) {
	s.field = field
}

// flip reverses the direction, of the original order as well as a field's.
func (s *playlistSort) flip() {
	s.desc = !s.desc
}

// sortPlaylistSections sorts track slots independently within each section.
// Section metadata and non-track items stay where YouTube placed them.
func sortPlaylistSections(sections []youtube.MusicSection, s playlistSort) []youtube.MusicSection {
	if !s.ordered() {
		return sections
	}
	sorted := make([]youtube.MusicSection, len(sections))
	for i, section := range sections {
		sorted[i] = section
		sorted[i].Items = append([]youtube.MusicItem(nil), section.Items...)
	}
	// The original order has nothing to compare, so its descending direction
	// reads the whole playlist backwards: the tracks reverse across the
	// sections, and the headings and the non-track items keep their places.
	if !s.active() {
		tracks := make([]youtube.MusicItem, 0)
		for _, section := range sorted {
			for _, item := range section.Items {
				if item.IsTrack() {
					tracks = append(tracks, item)
				}
			}
		}
		slices.Reverse(tracks)
		track := 0
		for i := range sorted {
			for j, item := range sorted[i].Items {
				if item.IsTrack() {
					sorted[i].Items[j] = tracks[track]
					track++
				}
			}
		}
		return sorted
	}
	for i, section := range sorted {
		tracks := make([]youtube.MusicItem, 0, len(section.Items))
		for _, item := range section.Items {
			if item.IsTrack() {
				tracks = append(tracks, item)
			}
		}
		// A field sorts the tracks within their own section: a section is a
		// shelf YouTube Music placed, and its heading names what is in it.
		sort.SliceStable(tracks, func(i, j int) bool {
			order := comparePlaylistItems(tracks[i], tracks[j], s.field)
			if s.desc {
				return order > 0
			}
			return order < 0
		})
		track := 0
		for j, item := range sorted[i].Items {
			if item.IsTrack() {
				sorted[i].Items[j] = tracks[track]
				track++
			}
		}
	}
	return sorted
}

func comparePlaylistItems(left, right youtube.MusicItem, field playlistSortField) int {
	var order int
	switch field {
	case playlistSortOriginal:
		return 0
	case playlistSortTitle:
		order = strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
	case playlistSortArtist:
		order = strings.Compare(strings.ToLower(playlistArtist(left)), strings.ToLower(playlistArtist(right)))
	case playlistSortDuration:
		order = cmp.Compare(durationSeconds(left.Duration), durationSeconds(right.Duration))
	}
	if order != 0 {
		return order
	}
	return strings.Compare(strings.ToLower(left.Title), strings.ToLower(right.Title))
}

// playlistArtist follows the text convention already used by the parser:
// artist, album and duration are separated by " • ".
func playlistArtist(item youtube.MusicItem) string {
	artist, _, _ := strings.Cut(item.Subtitle, " • ")
	return strings.TrimSpace(artist)
}

func durationSeconds(value string) int {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return 0
	}
	seconds := 0
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0
		}
		seconds = seconds*60 + n
	}
	return seconds
}
