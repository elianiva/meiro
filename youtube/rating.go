package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type LikeStatus uint8

const (
	LikeUnknown LikeStatus = iota
	Liked
	Indifferent
	Disliked
)

func parseLikeStatus(name string) LikeStatus {
	switch name {
	case "LIKE":
		return Liked
	case "INDIFFERENT":
		return Indifferent
	case "DISLIKE":
		return Disliked
	}
	return LikeUnknown
}

func (c *Client) RateTrack(ctx context.Context, videoID string, status LikeStatus) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: RateTrack requires cookie authentication")
	}
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return errors.New("youtube: RateTrack needs a video")
	}
	var endpoint string
	switch status {
	case Liked:
		endpoint = "like/like"
	case Disliked:
		endpoint = "like/dislike"
	case Indifferent:
		endpoint = "like/removelike"
	case LikeUnknown:
	}
	if endpoint == "" {
		return errors.New("youtube: RateTrack needs a known rating")
	}
	return c.rateEndpoint(ctx, map[string]any{"videoId": videoID}, endpoint)
}

func (c *Client) rate(ctx context.Context, target map[string]any, like bool) error {
	endpoint := "like/removelike"
	if like {
		endpoint = "like/like"
	}
	return c.rateEndpoint(ctx, target, endpoint)
}

func (c *Client) rateEndpoint(ctx context.Context, target map[string]any, endpoint string) error {
	raw, err := c.execute(ctx, endpoint, map[string]any{"target": target})
	if err != nil {
		return err
	}
	return writeFailure(endpoint, raw)
}

func writeFailure(endpoint string, raw json.RawMessage) error {
	var response struct {
		Error *struct {
			Message string `json:"message"`
			Status  string `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return fmt.Errorf("youtube: decode %s response: %w", endpoint, err)
	}
	if response.Error == nil {
		return nil
	}
	reason := response.Error.Message
	if reason == "" {
		reason = response.Error.Status
	}
	if reason == "" {
		return fmt.Errorf("youtube: %s was refused", endpoint)
	}
	return fmt.Errorf("youtube: %s was refused: %s", endpoint, reason)
}

func likeStatus(renderer *jsonObject) LikeStatus {
	menu, _ := renderer.get("menu").(*jsonObject)
	menuRenderer, _ := menu.get("menuRenderer").(*jsonObject)
	buttons, _ := menuRenderer.get("topLevelButtons").([]any)
	for _, value := range buttons {
		button, _ := value.(*jsonObject)
		like, _ := button.get("likeButtonRenderer").(*jsonObject)
		name, _ := like.get("likeStatus").(string)
		if status := parseLikeStatus(name); status != LikeUnknown {
			return status
		}
	}
	return LikeUnknown
}
