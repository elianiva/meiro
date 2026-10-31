package youtube

import (
	"context"
	"errors"
	"strings"
)

// AIDisclosure asks the watch page whether YouTube marks videoID as altered
// or generated with AI. It speaks as the regular WEB client because YouTube
// Music's player data does not carry the label.
func (c *Client) AIDisclosure(ctx context.Context, videoID string) (AIDisclosure, error) {
	if strings.TrimSpace(videoID) == "" {
		return AIUnknown, errors.New("youtube: AI disclosure requires a video ID")
	}
	raw, err := c.executeAs(ctx, webClient, false, "next", map[string]any{"videoId": videoID})
	if err != nil {
		return AIUnknown, err
	}
	return aiDisclosure(decodeResponse(raw)), nil
}

// aiDisclosure reads the badge on the video the watch page is about. A page
// without a primary renderer is unknown; a primary renderer without the badge
// says YouTube supplied no AI label for this track.
func aiDisclosure(root any) AIDisclosure {
	primary := findRenderer(root, "videoPrimaryInfoRenderer")
	if primary == nil {
		return AIUnknown
	}
	badges, _ := primary.get("badges").([]any)
	for _, value := range badges {
		badge, ok := value.(*jsonObject)
		if !ok {
			continue
		}
		renderer, ok := badge.get("metadataBadgeRenderer").(*jsonObject)
		if !ok {
			continue
		}
		icon := nestedString(renderer.get("icon"), "iconType")
		label, _ := renderer.get("label").(string)
		accessibility := nestedString(renderer.get("accessibilityData"), "label")
		if icon == "INFO" && (strings.EqualFold(strings.TrimSpace(label), "AI") ||
			strings.HasPrefix(strings.ToUpper(accessibility), "AI:")) {
			return AIAlteredOrGenerated
		}
	}
	return AINone
}
