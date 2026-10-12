package youtube

import (
	"net/url"
	"strings"
)

// ItemKind is the renderer an item was parsed from. It says how YouTube drew
// the item, not what it opens: see Target for that.
type ItemKind string

const (
	KindTrack     ItemKind = "track"
	KindMusicItem ItemKind = "music_item"
	KindVideo     ItemKind = "video"
	KindPlaylist  ItemKind = "playlist"
	KindAlbum     ItemKind = "album"
	KindArtist    ItemKind = "artist"
)

// TargetKind is what an item opens when it is chosen.
type TargetKind uint8

const (
	// TargetNone is an item that opens nothing.
	TargetNone TargetKind = iota
	// TargetTrack plays the item.
	TargetTrack
	// TargetAlbum, TargetPlaylist and TargetArtist open the item's page.
	TargetAlbum
	TargetPlaylist
	TargetArtist
)

// Target is what an item opens, and the ID that names it: a video ID for a
// track, a browse ID for an album, a playlist or an artist.
type Target struct {
	Kind TargetKind
	ID   string
}

// Opens reports whether the target opens a page of its own.
func (t Target) Opens() bool {
	return t.Kind == TargetAlbum || t.Kind == TargetPlaylist || t.Kind == TargetArtist
}

// Target says what the item opens. A video ID always wins: rows name their
// artist beside the track itself, and the artist must not capture the tap.
// Album and playlist IDs route only items with no video; the artist only
// items with neither video nor collection.
func (item MusicItem) Target() Target {
	browse, playlist := item.BrowseID, item.PlaylistID
	if playlist == "" {
		playlist = browse
	}
	album, list := isAlbumID(browse), isBrowsablePlaylist(playlist)
	switch {
	case item.VideoID != "" && !album && !list:
		return Target{TargetTrack, item.VideoID}
	case album:
		return Target{TargetAlbum, browse}
	case list:
		return Target{TargetPlaylist, playlist}
	case isArtistID(browse):
		return Target{TargetArtist, browse}
	case item.VideoID != "":
		return Target{TargetTrack, item.VideoID}
	}
	return Target{}
}

// IsTrack reports whether choosing the item plays it.
func (item MusicItem) IsTrack() bool { return item.Target().Kind == TargetTrack }

// IsVideo reports whether the item is a music video rather than a song.
func (item MusicItem) IsVideo() bool { return item.Kind == KindVideo }

// ArtistID is the artist a track names beside itself, or "" when it names
// none.
func (item MusicItem) ArtistID() string {
	if item.VideoID != "" && isArtistID(item.BrowseID) {
		return item.BrowseID
	}
	return ""
}

// WatchURL is the public link to a track.
func WatchURL(videoID string) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(videoID)
}

// BrowseURL is the public link to an album, a playlist or an artist page.
func BrowseURL(browseID string) string {
	return "https://music.youtube.com/browse/" + url.PathEscape(browseID)
}

func isAlbumID(id string) bool {
	return strings.HasPrefix(id, "MPR") || strings.Contains(id, "privately_owned_release")
}

func isArtistID(id string) bool {
	return strings.HasPrefix(id, "UC") || strings.Contains(id, "privately_owned_artist")
}

// isBrowsablePlaylist reports whether an ID names a page the app can open.
// Mix and radio queues ride along in taps and menus, but browsing one is
// empty: only library, community and product playlists open.
func isBrowsablePlaylist(id string) bool {
	return strings.HasPrefix(id, "VL") || strings.HasPrefix(id, "PL")
}
