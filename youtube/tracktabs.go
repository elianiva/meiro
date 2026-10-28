package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// Lyrics contains the description shelf returned by YouTube Music for a track.
type Lyrics struct {
	Description string `json:"description,omitempty"`
	Footer      string `json:"footer,omitempty"`
	// Timed is the lyrics line by line with when each is sung, when YouTube
	// Music has them timed. It is empty when it has only the plain text.
	Timed []TimedLine `json:"timed,omitempty"`
}

// TimedLine is one line of lyrics and the moment it starts.
type TimedLine struct {
	Start time.Duration `json:"start"`
	Text  string        `json:"text"`
}

// GetLyrics loads the lyrics tab for a track. Availability depends on the
// track and YouTube Music account/region. The web client gives the lyrics as
// plain text; the timed ones, which only the Android app is given, are asked
// for as well, and a track that has none, or a failure to read them, leaves
// the plain text alone.
func (c *Client) GetLyrics(ctx context.Context, videoID string) (*Lyrics, error) {
	browseID, err := c.trackTabBrowseID(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_LYRICS")
	if err != nil {
		log.Printf("lyrics: youtube video_id=%s: no lyrics tab: %v", videoID, err)
		return nil, err
	}
	raw, err := c.execute(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		log.Printf("lyrics: youtube video_id=%s browse_id=%s: web browse failed: %v", videoID, browseID, err)
		return nil, err
	}
	lyrics := &Lyrics{}
	root := decodeResponse(raw)
	if renderer := findRenderer(root, "musicDescriptionShelfRenderer"); renderer != nil {
		lyrics.Description = rendererText(renderer.get("description"))
		lyrics.Footer = rendererText(renderer.get("footer"))
	}
	timed, err := c.executeAs(ctx, androidMusicClient, false, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		log.Printf("lyrics: youtube video_id=%s browse_id=%s: android browse failed: %v", videoID, browseID, err)
	} else {
		lyrics.Timed = parseTimedLyrics(decodeResponse(timed))
	}
	log.Printf("lyrics: youtube video_id=%s browse_id=%s plain_chars=%d timed_lines=%d", videoID, browseID, len(lyrics.Description), len(lyrics.Timed))
	return lyrics, nil
}

// parseTimedLyrics reads the lines of a timedLyricsModel, which the Android
// client's lyrics page buries in its element tree.
func parseTimedLyrics(root any) []TimedLine {
	var walk func(any) []TimedLine
	walk = func(value any) []TimedLine {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if lines := walk(child); lines != nil {
					return lines
				}
			}
		case *jsonObject:
			if data, ok := node.get("timedLyricsData").([]any); ok {
				var lines []TimedLine
				for _, item := range data {
					entry, _ := item.(*jsonObject)
					cue, _ := entry.get("cueRange").(*jsonObject)
					text, _ := entry.get("lyricLine").(string)
					start, ok := milliseconds(cue.get("startTimeMilliseconds"))
					if ok {
						lines = append(lines, TimedLine{Start: start, Text: strings.TrimSpace(text)})
					}
				}
				return lines
			}
			for _, member := range node.members {
				if lines := walk(member.value); lines != nil {
					return lines
				}
			}
		}
		return nil
	}
	return walk(root)
}

// milliseconds reads a time that YouTube sends as a number or as a string.
func milliseconds(value any) (time.Duration, bool) {
	switch v := value.(type) {
	case float64:
		return time.Duration(v) * time.Millisecond, true
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		return time.Duration(n) * time.Millisecond, err == nil
	}
	return 0, false
}

// EpisodeDetails is what the Details tab of a podcast episode shows, where a
// song has its lyrics: a line of views, date and length, the title, the show
// and the episode's description.
type EpisodeDetails struct {
	Strapline     string `json:"strapline,omitempty"`
	Title         string `json:"title,omitempty"`
	Show          string `json:"show,omitempty"`
	ShowID        string `json:"showId,omitempty"`
	ShowThumbnail string `json:"showThumbnail,omitempty"`
	// Description is the description in runs, as it is written: plain text,
	// links and the timestamps of its chapters.
	Description []DescriptionRun `json:"description,omitempty"`
}

// DescriptionRun is a run of a description. A link has its URL; a timestamp
// of the episode itself has Timestamp set and At, where in it it seeks to.
type DescriptionRun struct {
	Text      string        `json:"text"`
	URL       string        `json:"url,omitempty"`
	At        time.Duration `json:"at,omitempty"`
	Timestamp bool          `json:"timestamp,omitempty"`
}

// GetEpisodeDetails loads the Details tab of a podcast episode.
func (c *Client) GetEpisodeDetails(ctx context.Context, videoID string) (*EpisodeDetails, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_NON_MUSIC_AUDIO_DETAILS")
	if err != nil {
		return nil, err
	}
	shelf := findRenderer(decodeResponse(raw), "musicDescriptionShelfRenderer")
	if shelf == nil {
		return nil, errors.New("youtube: episode details have no description")
	}
	details := &EpisodeDetails{
		Strapline:     rendererText(shelf.get("strapline")),
		Title:         rendererText(shelf.get("header")),
		Show:          rendererText(shelf.get("subheader")),
		ShowID:        navigationID(shelf.get("subheader")),
		ShowThumbnail: rendererThumbnail(shelf.get("subheaderThumbnail")),
	}
	runs, _ := nestedObject(shelf, "description")
	list, _ := runs.get("runs").([]any)
	for _, value := range list {
		run, ok := value.(*jsonObject)
		if !ok {
			continue
		}
		text, _ := run.get("text").(string)
		part := DescriptionRun{Text: text}
		endpoint, _ := run.get("navigationEndpoint").(*jsonObject)
		if link, ok := nestedObject(endpoint, "urlEndpoint"); ok {
			part.URL, _ = link.get("url").(string)
		}
		if watch, ok := nestedObject(endpoint, "watchEndpoint"); ok && watch.get("videoId") == videoID {
			seconds, _ := watch.get("startTimeSeconds").(float64)
			part.At, part.Timestamp = time.Duration(seconds)*time.Second, true
		}
		details.Description = append(details.Description, part)
	}
	return details, nil
}

// GetRelated loads the related tab for a track, or for a podcast episode,
// whose tab is of a page type of its own.
func (c *Client) GetRelated(ctx context.Context, videoID string) (*BrowseResult, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_RELATED", "MUSIC_PAGE_TYPE_NON_MUSIC_AUDIO_RELATED")
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

func (c *Client) getTrackTab(ctx context.Context, videoID string, pageTypes ...string) (json.RawMessage, error) {
	browseID, err := c.trackTabBrowseID(ctx, videoID, pageTypes...)
	if err != nil {
		return nil, err
	}
	return c.execute(ctx, "browse", map[string]any{"browseId": browseID})
}

// trackTabBrowseID finds the page of a track's tab, such as its lyrics: the
// first of the page types the track has a tab of.
func (c *Client) trackTabBrowseID(ctx context.Context, videoID string, pageTypes ...string) (string, error) {
	if strings.TrimSpace(videoID) == "" {
		return "", errors.New("youtube: video ID is required")
	}
	raw, err := c.execute(ctx, "next", map[string]any{"videoId": videoID})
	if err != nil {
		return "", err
	}
	root := decodeResponse(raw)
	for _, pageType := range pageTypes {
		if browseID := musicTabBrowseID(root, pageType); browseID != "" {
			return browseID, nil
		}
	}
	return "", fmt.Errorf("youtube: track response has no %s tab", strings.Join(pageTypes, " or "))
}

func musicTabBrowseID(root any, pageType string) string {
	var walk func(any, []any) string
	walk = func(value any, parents []any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if id := walk(child, parents); id != "" {
					return id
				}
			}
		case *jsonObject:
			if node.get("pageType") == pageType {
				for index := len(parents) - 1; index >= 0; index-- {
					if id := navigationID(parents[index]); id != "" {
						return id
					}
				}
				return navigationID(node)
			}
			parents = append(parents, node)
			for _, member := range node.members {
				if id := walk(member.value, parents); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(root, nil)
}
