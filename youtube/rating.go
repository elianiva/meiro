package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LikeStatus is whether the signed-in account has liked a track, as YouTube's
// like buttons name it. It is a byte, since every item of every page carries
// one.
type LikeStatus uint8

const (
	// LikeUnknown is a track whose page did not say.
	LikeUnknown LikeStatus = iota
	// Liked is a track the account has given a thumbs-up.
	Liked
	// Indifferent is a track the account has rated neither way.
	Indifferent
	// Disliked is a track the account has given a thumbs-down. Meiro reads it
	// but does not write it.
	Disliked
)

// parseLikeStatus reads the name YouTube's like button gives a status.
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

// RateTrack likes a track or takes the like back, for the account the client
// speaks for. It is the call behind the thumb of YouTube Music's player:
// like/like with the video as the target, and like/removelike to undo it.
func (c *Client) RateTrack(ctx context.Context, videoID string, like bool) error {
	if c.cookieAuth == nil {
		return errors.New("youtube: RateTrack requires cookie authentication")
	}
	videoID = strings.TrimSpace(videoID)
	if videoID == "" {
		return errors.New("youtube: RateTrack needs a video")
	}
	return c.rate(ctx, map[string]any{"videoId": videoID}, like)
}

// rate likes or un-likes whatever target names: a video, or a playlist or an
// album to put in the library, which YouTube keeps as a like as well.
func (c *Client) rate(ctx context.Context, target map[string]any, like bool) error {
	endpoint := "like/removelike"
	if like {
		endpoint = "like/like"
	}
	raw, err := c.execute(ctx, endpoint, map[string]any{"target": target})
	if err != nil {
		return err
	}
	return writeFailure(endpoint, raw)
}

// writeFailure reports an answer that says the write failed in its body.
// YouTube answers a write it accepted with an ordinary response and one it
// refused, in some cases, with a 200 and an "error" object in place of the
// result.
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

// likeStatus reads the like a row or a panel item carries in its menu: the
// like button of the menu's top-level buttons names it. The result is "" for an
// item whose menu has none, which is every item of a page that is not about
// what plays.
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
