package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
)

const playlistPrivacy = "PRIVATE"

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

var ErrAlreadyInPlaylist = errors.New("youtube: the video is already in the playlist")

func (c *Client) AddToPlaylist(ctx context.Context, playlistID, videoID string) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: AddToPlaylist requires cookie authentication")
	}
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), browsePlaylistPrefix)
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
		log.Printf("playlist: add refused playlist_id=%s video_id=%s status=%q body=%s", playlistID, videoID, response.Status, raw)
		if alreadyInPlaylist(raw, videoID) {
			return ErrAlreadyInPlaylist
		}
		if response.Status != "" {
			return fmt.Errorf("youtube: the playlist refused the video (%s)", response.Status)
		}
		return refusedWrite("browse/edit_playlist", raw)
	}
	return nil
}

func alreadyInPlaylist(raw json.RawMessage, videoID string) bool {
	retry := findRenderer(decodeResponse(raw), "playlistEditEndpoint")
	actions, _ := retry.get("actions").([]any)
	for _, value := range actions {
		action, _ := value.(*jsonObject)
		added, _ := action.get("addedVideoId").(string)
		option, _ := action.get("dedupeOption").(string)
		if added == videoID && option == "DEDUPE_OPTION_SKIP" {
			return true
		}
	}
	return false
}

func (c *Client) DeletePlaylist(ctx context.Context, playlistID string) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: DeletePlaylist requires cookie authentication")
	}
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), browsePlaylistPrefix)
	if playlistID == "" {
		return errors.New("youtube: DeletePlaylist needs a playlist")
	}
	raw, err := c.execute(ctx, "playlist/delete", map[string]any{"playlistId": playlistID})
	if err != nil {
		return err
	}
	var response struct {
		Actions []json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("youtube: decode playlist/delete response: %w", err)
	}
	if len(response.Actions) > 0 {
		log.Printf("playlist: delete refused playlist_id=%s body=%s", playlistID, raw)
		return refusedWrite("playlist/delete", raw)
	}
	return nil
}

func (c *Client) RemoveFromPlaylist(ctx context.Context, playlistID, playlistSetID string) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: RemoveFromPlaylist requires cookie authentication")
	}
	playlistID = strings.TrimPrefix(strings.TrimSpace(playlistID), browsePlaylistPrefix)
	playlistSetID = strings.TrimSpace(playlistSetID)
	switch {
	case playlistID == "":
		return errors.New("youtube: RemoveFromPlaylist needs a playlist")
	case playlistSetID == "":
		return errors.New("youtube: RemoveFromPlaylist needs a playlist entry")
	}
	raw, err := c.execute(ctx, "browse/edit_playlist", map[string]any{
		"playlistId": playlistID,
		"actions": []map[string]any{
			{"action": "ACTION_REMOVE_VIDEO", "setVideoId": playlistSetID},
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
		log.Printf("playlist: remove refused playlist_id=%s playlist_set_id=%s status=%q body=%s", playlistID, playlistSetID, response.Status, raw)
		if response.Status != "" {
			return fmt.Errorf("youtube: the playlist refused to remove the song (%s)", response.Status)
		}
		return refusedWrite("browse/edit_playlist", raw)
	}
	return nil
}

func videoIDs(videos []string) []string {
	var ids []string
	seen := make(map[string]struct{}, len(videos))
	for _, video := range videos {
		video = strings.TrimSpace(video)
		if video == "" {
			continue
		}
		if _, repeat := seen[video]; repeat {
			continue
		}
		seen[video] = struct{}{}
		ids = append(ids, video)
	}
	return ids
}

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
