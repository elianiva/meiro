package youtube

import (
	"net/url"
	"strings"
)

type ItemKind string

const (
	KindTrack     ItemKind = "track"
	KindMusicItem ItemKind = "music_item"
	KindVideo     ItemKind = "video"
	KindPlaylist  ItemKind = "playlist"
	KindAlbum     ItemKind = "album"
	KindArtist    ItemKind = "artist"
	KindEpisode   ItemKind = "episode"
)

type AIDisclosure uint8

const (
	AIUnknown AIDisclosure = iota
	AINone
	AIAlteredOrGenerated
)

type TargetKind uint8

const (
	TargetNone TargetKind = iota
	TargetTrack
	TargetAlbum
	TargetPlaylist
	TargetArtist
	TargetPodcast
)

type Target struct {
	Kind TargetKind
	ID   string
}

func (t Target) Opens() bool {
	return t.Kind == TargetAlbum || t.Kind == TargetPlaylist || t.Kind == TargetArtist || t.Kind == TargetPodcast
}

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

func (item MusicItem) IsTrack() bool { return item.Target().Kind == TargetTrack }

func (item MusicItem) IsEpisode() bool { return item.Kind == KindEpisode }

func (item MusicItem) IsVideo() bool { return item.Kind == KindVideo }

func (item MusicItem) ArtistID() string {
	if item.VideoID != "" && isArtistID(item.BrowseID) {
		return item.BrowseID
	}
	return ""
}

// PrimaryArtist returns the first artist credited on a track: the one the
// song is by, not an artist it features. It is empty when the row names no
// linked artist.
func (item MusicItem) PrimaryArtist() (id, name string) {
	if id := item.ArtistID(); id != "" {
		return id, item.Artist
	}
	if item.VideoID == "" || item.Byline == nil {
		return "", ""
	}
	for _, part := range item.Byline.Parts {
		if target := part.Target(); target.Kind == TargetArtist {
			return target.ID, strings.TrimSpace(part.Text)
		}
	}
	return "", ""
}

func WatchURL(videoID string) string {
	return "https://music.youtube.com/watch?v=" + url.QueryEscape(videoID)
}

func BrowseURL(browseID string) string {
	return "https://music.youtube.com/browse/" + url.PathEscape(browseID)
}

func isAlbumID(id string) bool {
	return strings.HasPrefix(id, "MPR") || strings.Contains(id, "privately_owned_release")
}

func isPodcastID(id string) bool { return strings.HasPrefix(id, "MPSPP") }

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

const browsePlaylistPrefix = "VL"

func isBrowsablePlaylist(id string) bool {
	return strings.HasPrefix(id, browsePlaylistPrefix) || strings.HasPrefix(id, "PL")
}

type MusicItem struct {
	ID              string        `json:"id,omitempty"`
	VideoID         string        `json:"videoId,omitempty"`
	BrowseID        string        `json:"browseId,omitempty"`
	PlaylistID      string        `json:"playlistId,omitempty"`
	PlaylistSetID   string        `json:"playlistSetVideoId,omitempty"`
	Title           string        `json:"title,omitempty"`
	Subtitle        string        `json:"subtitle,omitempty"`
	Kind            ItemKind      `json:"kind,omitempty"`
	AIDisclosure    AIDisclosure  `json:"aiDisclosure,omitempty"`
	Duration        string        `json:"duration,omitempty"`
	Thumbnail       string        `json:"thumbnail,omitempty"`
	Description     string        `json:"-"`
	ProgressPercent int           `json:"-"`
	Like            LikeStatus    `json:"-"`
	Library         *LibraryState `json:"-"`
	Artist          string        `json:"artist,omitempty"`
	Artists         string        `json:"artists,omitempty"`
	Album           string        `json:"album,omitempty"`
	Byline          *TrackByline  `json:"byline,omitempty"`
}

type TrackByline struct {
	Parts []BylinePart `json:"parts"`
}

type BylinePart struct {
	Text     string `json:"text"`
	BrowseID string `json:"browseId,omitempty"`
}

func (p BylinePart) Target() Target {
	switch {
	case isArtistID(p.BrowseID):
		return Target{Kind: TargetArtist, ID: p.BrowseID}
	case isAlbumID(p.BrowseID):
		return Target{Kind: TargetAlbum, ID: p.BrowseID}
	}
	return Target{}
}

func (item MusicItem) PlayerByline() []BylinePart {
	if item.Byline != nil {
		parts, _ := dropKindPrefix(item.Byline.Parts)
		return parts
	}
	if id := item.ArtistID(); id != "" && item.Artist != "" {
		segments := strings.Split(item.Subtitle, " • ")
		matches := 0
		for _, text := range segments {
			if text == item.Artist {
				matches++
			}
		}
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
		parts, _ = dropKindPrefix(parts)
		return parts
	}
	return []BylinePart{{Text: item.Subtitle}}
}

func (item *MusicItem) setCredits(artists []string, album string) {
	item.Album = album
	item.Artists = strings.Join(artists, ", ")
	item.Artist = ""
	if len(artists) > 0 {
		item.Artist = artists[0]
	}
}
