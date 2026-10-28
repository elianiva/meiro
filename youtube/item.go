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
	// KindEpisode is an episode of a podcast. It plays as a track does.
	KindEpisode ItemKind = "episode"
)

// TargetKind is what an item opens when it is chosen.
type TargetKind uint8

const (
	// TargetNone is an item that opens nothing.
	TargetNone TargetKind = iota
	// TargetTrack plays the item.
	TargetTrack
	// TargetAlbum, TargetPlaylist, TargetArtist and TargetPodcast open the
	// item's page.
	TargetAlbum
	TargetPlaylist
	TargetArtist
	TargetPodcast
)

// Target is what an item opens, and the ID that names it: a video ID for a
// track, a browse ID for an album, a playlist, an artist or a podcast.
type Target struct {
	Kind TargetKind
	ID   string
}

// Opens reports whether the target opens a page of its own.
func (t Target) Opens() bool {
	return t.Kind == TargetAlbum || t.Kind == TargetPlaylist || t.Kind == TargetArtist || t.Kind == TargetPodcast
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
	case isPodcastID(browse):
		return Target{TargetPodcast, browse}
	case isArtistID(browse):
		return Target{TargetArtist, browse}
	case item.VideoID != "":
		return Target{TargetTrack, item.VideoID}
	}
	return Target{}
}

// IsTrack reports whether choosing the item plays it.
func (item MusicItem) IsTrack() bool { return item.Target().Kind == TargetTrack }

// IsEpisode reports whether the item is an episode of a podcast.
func (item MusicItem) IsEpisode() bool { return item.Kind == KindEpisode }

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

// BrowseURL is the public link to an album, a playlist, an artist or a
// podcast page.
func BrowseURL(browseID string) string {
	return "https://music.youtube.com/browse/" + url.PathEscape(browseID)
}

func isAlbumID(id string) bool {
	return strings.HasPrefix(id, "MPR") || strings.Contains(id, "privately_owned_release")
}

// isPodcastID reports whether an ID names a podcast show's page. Its
// episodes are a playlist, but the show's page is not the playlist's.
func isPodcastID(id string) bool { return strings.HasPrefix(id, "MPSPP") }

// episodeVideoIDFromBrowseID reads the video ID carried by an episode page's
// browse ID, such as MPEDUZ83quirqLA.
func episodeVideoIDFromBrowseID(id string) string {
	videoID, ok := strings.CutPrefix(id, "MPED")
	if !ok || len(videoID) != 11 {
		return ""
	}
	return videoID
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

// MusicItem is the common subset of YouTube Music song, video, album, artist,
// and playlist renderers.
type MusicItem struct {
	ID            string   `json:"id,omitempty"`
	VideoID       string   `json:"videoId,omitempty"`
	BrowseID      string   `json:"browseId,omitempty"`
	PlaylistID    string   `json:"playlistId,omitempty"`
	PlaylistSetID string   `json:"playlistSetVideoId,omitempty"`
	Title         string   `json:"title,omitempty"`
	Subtitle      string   `json:"subtitle,omitempty"`
	Kind          ItemKind `json:"kind,omitempty"`
	Duration      string   `json:"duration,omitempty"`
	Thumbnail     string   `json:"thumbnail,omitempty"`
	// Description is what a show's page says of an episode, on one line. It
	// is not saved with the item: it runs long, and only the show's page
	// shows it.
	Description string `json:"-"`
	// ProgressPercent is the account's reported episode progress, when the
	// show page includes it. It is a hint for resume, not saved with the item.
	ProgressPercent int `json:"-"`
	// Like is whether the account has liked the track, when the page that
	// held it said. It is not saved with the item: a like changes elsewhere.
	Like LikeStatus `json:"-"`
	// Library is whether the account has saved the album or the playlist, when
	// the item's menu said, and nil when it did not. It is not saved with the
	// item, for the same reason, and it is a pointer so that the items that
	// have none, which are nearly all of them, stay small: a page's rows copy
	// them. Nothing changes what it points to; a change gives the item another.
	Library *LibraryState `json:"-"`
	// Artist is the first artist the item credits, which the scrobble and the
	// lyrics search take, Album the album, and Artists every artist. They are
	// read from the links of the byline rather than from the flattened
	// Subtitle, which also holds the kind, the year and the length. They are
	// empty when the byline links none, and nothing reconstructs them from the
	// Subtitle. They are strings, not a list, so that an item stays comparable.
	Artist string `json:"artist,omitempty"`
	// Artists is every artist the item credits, in the order its byline links
	// them, joined as "First, Second". It is empty for an item whose byline links
	// none. Artist is the first of them.
	Artists string `json:"artists,omitempty"`
	Album   string `json:"album,omitempty"`
	// Byline preserves the text runs and browse IDs for player navigation.
	// It is immutable after parsing; a pointer keeps MusicItem comparable.
	Byline *TrackByline `json:"byline,omitempty"`
}

// TrackByline keeps the original separators and non-link metadata alongside
// artist and album links. It is saved with the queue for offline restoration.
type TrackByline struct {
	Parts []BylinePart `json:"parts"`
}

type BylinePart struct {
	Text     string `json:"text"`
	BrowseID string `json:"browseId,omitempty"`
}

// Target accepts only artist and album destinations, including saved data.
func (p BylinePart) Target() Target {
	switch {
	case isArtistID(p.BrowseID):
		return Target{Kind: TargetArtist, ID: p.BrowseID}
	case isAlbumID(p.BrowseID):
		return Target{Kind: TargetAlbum, ID: p.BrowseID}
	}
	return Target{}
}

// PlayerByline also supports older queues that saved a single artist ID.
// Only a whole metadata segment matching the credited artist becomes a link.
func (item MusicItem) PlayerByline() []BylinePart {
	if item.Byline != nil {
		return item.Byline.Parts
	}
	if id := item.ArtistID(); id != "" && item.Artist != "" {
		segments := strings.Split(item.Subtitle, " • ")
		matches := 0
		for _, text := range segments {
			if text == item.Artist {
				matches++
			}
		}
		// Old queues cannot distinguish an artist called "Song" from the
		// kind label when both appear. Leave ambiguous text unlinked.
		if matches != 1 {
			return []BylinePart{{Text: item.Subtitle}}
		}
		var parts []BylinePart
		for i, text := range segments {
			if i > 0 {
				parts = append(parts, BylinePart{Text: " • "})
			}
			part := BylinePart{Text: text}
			if text == item.Artist {
				part.BrowseID = id
			}
			parts = append(parts, part)
		}
		return parts
	}
	return []BylinePart{{Text: item.Subtitle}}
}

// setCredits makes item credit the artists a byline links and the album.
// Artist names the first of them, which the scrobble and the lyrics search
// take, and Artists names every one, the line the presence shows.
func (item *MusicItem) setCredits(artists []string, album string) {
	item.Album = album
	item.Artists = strings.Join(artists, ", ")
	item.Artist = ""
	if len(artists) > 0 {
		item.Artist = artists[0]
	}
}
