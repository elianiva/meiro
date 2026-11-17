package youtube

import (
	"context"
	"errors"
	"strings"
)

type UpNextOptions struct {
	VideoID       string
	PlaylistID    string
	PlaylistIndex *int
	Continuation  string
}

const automixPlaylistPrefix = "RDAMVM"

func (c *Client) GetUpNext(ctx context.Context, options UpNextOptions) (*BrowseResult, error) {
	if strings.TrimSpace(options.VideoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	payload := map[string]any{"videoId": options.VideoID}
	playlistID := options.PlaylistID
	if playlistID == "" {
		playlistID = automixPlaylistPrefix + options.VideoID
	}
	payload["playlistId"] = playlistID
	if options.PlaylistIndex != nil {
		payload["playlistIndex"] = *options.PlaylistIndex
	}
	if options.Continuation != "" {
		payload["continuation"] = options.Continuation
	}
	raw, err := c.execute(ctx, "next", payload)
	if err != nil {
		return nil, err
	}
	root := decodeResponse(raw)
	result := upNextResult(root)
	if options.Continuation != "" || result.QueuePlaylistID != "" {
		return result, nil
	}
	playlistPayload := automixPlaylistPayload(root)
	if playlistPayload == nil {
		return result, nil
	}
	playlistPayload["videoId"] = options.VideoID
	raw, err = c.execute(ctx, "next", playlistPayload)
	if err != nil {
		return nil, err
	}
	return upNextResult(decodeResponse(raw)), nil
}

func upNextResult(root any) *BrowseResult {
	items, sections, _ := browseParts(root, false)
	return &BrowseResult{
		Items: items, Sections: sections,
		ContinuationToken: upNextContinuationToken(root),
		QueuePlaylistID:   upNextPlaylistID(root),
	}
}

func upNextPlaylistID(root any) string {
	var walk func(any) string
	walk = func(value any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if id := walk(child); id != "" {
					return id
				}
			}
		case *jsonObject:
			for _, key := range []string{"playlistPanelRenderer", "playlistPanelContinuation"} {
				if panel, ok := node.get(key).(*jsonObject); ok {
					if id, ok := panel.get("playlistId").(string); ok && id != "" {
						return id
					}
				}
			}
			for _, member := range node.members {
				if id := walk(member.value); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(root)
}

func upNextContinuationToken(root any) string {
	tokenFromPanel := func(panel *jsonObject) string {
		if token, ok := panel.get("continuation").(string); ok && token != "" {
			return token
		}
		continuations, _ := panel.get("continuations").([]any)
		for _, value := range continuations {
			continuation, _ := value.(*jsonObject)
			for _, key := range []string{"nextRadioContinuationData", "nextContinuationData"} {
				if data, ok := continuation.get(key).(*jsonObject); ok {
					if token, ok := data.get("continuation").(string); ok && token != "" {
						return token
					}
				}
			}
		}
		return ""
	}
	var walk func(any) string
	walk = func(value any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if token := walk(child); token != "" {
					return token
				}
			}
		case *jsonObject:
			for _, key := range []string{"playlistPanelRenderer", "playlistPanelContinuation"} {
				if panel, ok := node.get(key).(*jsonObject); ok {
					if token := tokenFromPanel(panel); token != "" {
						return token
					}
				}
			}
			for _, member := range node.members {
				if token := walk(member.value); token != "" {
					return token
				}
			}
		}
		return ""
	}
	return walk(root)
}

func automixPlaylistPayload(root any) map[string]any {
	var walk func(any) map[string]any
	walk = func(value any) map[string]any {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if payload := walk(child); payload != nil {
					return payload
				}
			}
		case *jsonObject:
			if preview, ok := node.get("automixPreviewVideoRenderer").(*jsonObject); ok {
				content, _ := preview.get("content").(*jsonObject)
				automix, _ := content.get("automixPlaylistVideoRenderer").(*jsonObject)
				navigation, _ := automix.get("navigationEndpoint").(*jsonObject)
				if endpoint, ok := navigation.get("watchPlaylistEndpoint").(*jsonObject); ok {
					payload := make(map[string]any, len(endpoint.members))
					for _, member := range endpoint.members {
						payload[member.key] = member.value
					}
					return payload
				}
			}
			for _, member := range node.members {
				if payload := walk(member.value); payload != nil {
					return payload
				}
			}
		}
		return nil
	}
	return walk(root)
}

func (c *Client) ContinueUpNext(ctx context.Context, options UpNextOptions, continuation string) (*BrowseResult, error) {
	if strings.TrimSpace(continuation) == "" {
		return nil, errors.New("youtube: up-next continuation token is required")
	}
	options.Continuation = continuation
	return c.GetUpNext(ctx, options)
}
