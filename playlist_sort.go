package main

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/elianiva/meiro/youtube"
)

type playlistSortField uint8

const (
	playlistSortOriginal playlistSortField = iota
	playlistSortTitle
	playlistSortArtist
	playlistSortDuration
)

type playlistSort struct {
	field playlistSortField
	desc  bool
}

func (s playlistSort) active() bool { return s.field != playlistSortOriginal }

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

func (s *playlistSort) choose(field playlistSortField) {
	s.field = field
}

func (s *playlistSort) flip() {
	s.desc = !s.desc
}

func sortPlaylistSections(sections []youtube.MusicSection, s playlistSort) []youtube.MusicSection {
	if !s.ordered() {
		return sections
	}
	sorted := make([]youtube.MusicSection, len(sections))
	for i, section := range sections {
		sorted[i] = section
		sorted[i].Items = append([]youtube.MusicItem(nil), section.Items...)
	}
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
	var keys []sortKey
	for i, section := range sections {
		keys = keys[:0]
		for j, item := range section.Items {
			if item.IsTrack() {
				keys = append(keys, newSortKey(item, s.field, j))
			}
		}
		slices.SortStableFunc(keys, func(x, y sortKey) int {
			if s.desc {
				return y.compare(x)
			}
			return x.compare(y)
		})
		track := 0
		for j, item := range section.Items {
			if item.IsTrack() {
				sorted[i].Items[j] = section.Items[keys[track].at]
				track++
			}
		}
	}
	return sorted
}

type sortKey struct {
	field   string
	seconds time.Duration
	title   string
	at      int
}

func newSortKey(item youtube.MusicItem, field playlistSortField, at int) sortKey {
	k := sortKey{title: strings.ToLower(item.Title), at: at}
	switch field {
	case playlistSortArtist:
		k.field = strings.ToLower(playlistArtist(item))
	case playlistSortDuration:
		k.seconds = parseDuration(item.Duration)
	case playlistSortOriginal, playlistSortTitle:
	}
	return k
}

func (k sortKey) compare(other sortKey) int {
	return cmp.Or(
		strings.Compare(k.field, other.field),
		cmp.Compare(k.seconds, other.seconds),
		strings.Compare(k.title, other.title),
	)
}

func playlistArtist(item youtube.MusicItem) string {
	artist, _, _ := strings.Cut(item.Subtitle, " • ")
	return strings.TrimSpace(artist)
}
