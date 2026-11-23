package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

type SearchFilter struct {
	Label  string `json:"label,omitempty"`
	Params string `json:"params,omitempty"`
}

const (
	SearchSongsParams     = "EgWKAQIIAWoSEAUQAxAJ"
	SearchVideosParams    = "EgWKAQIQAWoSEAUQAxAJ"
	SearchAlbumsParams    = "EgWKAQIYAWoSEAUQAxAJ"
	SearchPlaylistsParams = "EgWKAQIoAWoSEAUQAxAJ"
	SearchArtistsParams   = "EgWKAQIgAWoSEAUQAxAJ"
)

type SearchOptions struct {
	Params string
}

type SearchResult struct {
	Items             []MusicItem    `json:"items"`
	ContinuationToken string         `json:"continuationToken,omitempty"`
	TopResult         MusicItem      `json:"topResult,omitempty"`
	Filters           []SearchFilter `json:"filters,omitempty"`
}

type SearchSuggestions struct {
	Queries []string    `json:"queries"`
	Items   []MusicItem `json:"items,omitempty"`
}

func (c *Client) Search(ctx context.Context, query string, options SearchOptions) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("youtube: search query is required")
	}
	payload := map[string]any{"query": query}
	if options.Params != "" {
		payload["params"] = options.Params
	}
	raw, err := c.execute(ctx, "search", payload)
	if err != nil {
		return nil, err
	}
	return c.newSearchResult(raw), nil
}

func (c *Client) ContinueSearch(ctx context.Context, token string) (*SearchResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("youtube: search continuation token is required")
	}
	raw, err := c.execute(ctx, "search", map[string]any{"continuation": token})
	if err != nil {
		return nil, err
	}
	return c.newSearchResult(raw), nil
}

func (c *Client) GetSearchSuggestions(ctx context.Context, input string) (*SearchSuggestions, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("youtube: suggestion input is required")
	}
	raw, err := c.execute(ctx, "music/get_search_suggestions", map[string]any{"input": input})
	if err != nil {
		return nil, err
	}
	root := decodeResponse(raw)
	items, _, _ := browseParts(root, false)
	return &SearchSuggestions{Queries: searchSuggestions(root), Items: items}, nil
}

func searchSuggestions(root any) []string {
	var suggestions []string
	seen := make(map[string]struct{})
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case *jsonObject:
			if renderer, ok := node.get("searchSuggestionRenderer").(*jsonObject); ok {
				if text := rendererText(renderer.get("suggestion")); text != "" {
					if _, exists := seen[text]; !exists {
						seen[text] = struct{}{}
						suggestions = append(suggestions, text)
					}
				}
			}
			for _, member := range node.members {
				walk(member.value)
			}
		}
	}
	walk(root)
	return suggestions
}

func (c *Client) newSearchResult(raw json.RawMessage) *SearchResult {
	root := decodeResponse(raw)
	items, _, top := browseParts(root, true)
	return &SearchResult{
		Items: items, TopResult: top, Filters: searchFilters(root),
		ContinuationToken: continuationToken(root),
	}
}

func searchFilters(root any) []SearchFilter {
	cloud := findRenderer(root, "chipCloudRenderer")
	if cloud == nil {
		return nil
	}
	chips, _ := cloud.get("chips").([]any)
	filters := make([]SearchFilter, 0, len(chips))
	for _, value := range chips {
		chip, ok := value.(*jsonObject)
		if !ok {
			continue
		}
		renderer, ok := chip.get("chipCloudChipRenderer").(*jsonObject)
		if !ok {
			continue
		}
		filters = append(filters, SearchFilter{
			Label:  rendererText(renderer.get("text")),
			Params: searchEndpointParams(renderer),
		})
	}
	return filters
}

func searchEndpointParams(renderer *jsonObject) string {
	endpoint, ok := renderer.get("navigationEndpoint").(*jsonObject)
	if !ok {
		return ""
	}
	search, ok := endpoint.get("searchEndpoint").(*jsonObject)
	if !ok {
		return ""
	}
	params, _ := search.get("params").(string)
	return params
}
