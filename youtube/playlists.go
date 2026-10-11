package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// playlistPrivacy keeps a new playlist to the account that made it. YouTube
// Music's own client asks which of three to use; Meiro does not ask yet, and
// the one that publishes nothing is the one to take without asking.
const playlistPrivacy = "PRIVATE"

// CreatePlaylist makes a playlist named title, private to the account, holding
// the videos given, and returns the ID YouTube gives it.
//
// A write counts only when YouTube answers with the new playlist's ID. An
// account it will not let make one is answered with a 200 and a panel that
// asks for the feature instead of an error, so a response without an ID is a
// refusal, not a playlist.
func (c *Client) CreatePlaylist(ctx context.Context, title string, videos ...string) (string, error) {
	if c.cookieAuth == nil {
		return "", errors.New("youtube: CreatePlaylist requires cookie authentication")
	}
	title, err := playlistTitle(title)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"title":         title,
		"description":   "",
		"privacyStatus": playlistPrivacy,
	}
	// Seeding the videos in the same write spares a fresh playlist the first
	// edit, which YouTube refuses until the playlist settles.
	if seeded := videoIDs(videos); len(seeded) > 0 {
		payload["videoIds"] = seeded
	}
	raw, err := c.execute(ctx, "playlist/create", payload)
	if err != nil {
		return "", err
	}
	var response struct {
		PlaylistID string `json:"playlistId"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("youtube: decode playlist/create response: %w", err)
	}
	if response.PlaylistID == "" {
		return "", refusedWrite("playlist/create", raw)
	}
	return response.PlaylistID, nil
}

// AddToPlaylist adds one video to a playlist. playlistID is taken with or
// without the "VL" prefix a browse ID carries, because a write wants it
// without: reads add that prefix, writes take it off.
//
// YouTube refuses the whole write when the video is already in the playlist,
// and says so in the status of an otherwise ordinary response, so a status
// that does not say it succeeded is a refusal.
func (c *Client) AddToPlaylist(ctx context.Context, playlistID, videoID string) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: AddToPlaylist requires cookie authentication")
	}
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), "VL")
	videoID = strings.TrimSpace(videoID)
	switch {
	case playlistID == "":
		return errors.New("youtube: AddToPlaylist needs a playlist")
	case videoID == "":
		return errors.New("youtube: AddToPlaylist needs a video")
	}
	raw, err := c.execute(ctx, "browse/edit_playlist", map[string]any{
		"playlistId": playlistID,
		"actions": []map[string]any{
			{"action": "ACTION_ADD_VIDEO", "addedVideoId": videoID},
		},
	})
	if err != nil {
		return err
	}
	var response struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("youtube: decode browse/edit_playlist response: %w", err)
	}
	if !strings.Contains(response.Status, "SUCCEEDED") {
		if response.Status != "" {
			return fmt.Errorf("youtube: the playlist refused the video (%s)", response.Status)
		}
		return refusedWrite("browse/edit_playlist", raw)
	}
	return nil
}

// videoIDs returns the videos of a write, without the blanks and the repeats.
func videoIDs(videos []string) []string {
	var ids []string
	for _, video := range videos {
		video = strings.TrimSpace(video)
		if video == "" || slices.Contains(ids, video) {
			continue
		}
		ids = append(ids, video)
	}
	return ids
}

// playlistTitle checks a name YouTube Music will take, and returns it without
// the whitespace around it. It refuses the two characters its own client
// refuses, which name an element rather than a playlist.
func playlistTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return "", errors.New("youtube: a playlist needs a name")
	case strings.ContainsAny(title, "<>"):
		return "", errors.New("youtube: a playlist name cannot contain < or >")
	}
	return title, nil
}

// refusedWrite reports a write YouTube answered without making. A refusal can
// name the feature the account is asked for first, in a panel it shows
// instead; one that names nothing is still a refusal.
func refusedWrite(endpoint string, raw json.RawMessage) error {
	var response struct {
		Actions []struct {
			ShowEngagementPanelEndpoint struct {
				Identifier struct {
					Tag string `json:"tag"`
				} `json:"identifier"`
			} `json:"showEngagementPanelEndpoint"`
		} `json:"actions"`
	}
	wanted := ""
	if err := json.Unmarshal(raw, &response); err == nil {
		for _, action := range response.Actions {
			if tag := action.ShowEngagementPanelEndpoint.Identifier.Tag; tag != "" {
				wanted = tag
				break
			}
		}
	}
	if wanted == "" {
		return fmt.Errorf("youtube: %s made no change", endpoint)
	}
	return fmt.Errorf("youtube: %s made no change: the account is asked for %s first", endpoint, wanted)
}
