package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// playlistPrivacy keeps a new playlist to the account that made it. YouTube
// Music's own client asks which of three to use; Meiro does not ask yet, and
// the one that publishes nothing is the one to take without asking.
const playlistPrivacy = "PRIVATE"

// CreatePlaylist makes an empty playlist named title, private to the account,
// and returns the ID YouTube gives it.
//
// A write counts only when YouTube answers with the new playlist's ID. An
// account it will not let make one is answered with a 200 and a panel that
// asks for the feature instead of an error, so a response without an ID is a
// refusal, not a playlist.
func (c *Client) CreatePlaylist(ctx context.Context, title string) (string, error) {
	if c.cookieAuth == nil {
		return "", errors.New("youtube: CreatePlaylist requires cookie authentication")
	}
	title, err := playlistTitle(title)
	if err != nil {
		return "", err
	}
	raw, err := c.execute(ctx, "playlist/create", map[string]any{
		"title":         title,
		"description":   "",
		"privacyStatus": playlistPrivacy,
	})
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
