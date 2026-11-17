package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

type BrowseResult struct {
	Title             string         `json:"title,omitempty"`
	Thumbnail         string         `json:"thumbnail,omitempty"`
	Description       string         `json:"description,omitempty"`
	Items             []MusicItem    `json:"items"`
	Sections          []MusicSection `json:"sections,omitempty"`
	ContinuationToken string         `json:"continuationToken,omitempty"`
	Library           LibraryState   `json:"-"`
	Subscription      Subscription   `json:"-"`
	QueuePlaylistID   string         `json:"queuePlaylistId,omitempty"`
}

func pageHeading(root any) (title, thumbnail string) {
	object, ok := root.(*jsonObject)
	if !ok {
		return "", ""
	}
	if header, ok := object.get("header").(*jsonObject); ok {
		for _, member := range header.members {
			renderer, ok := member.value.(*jsonObject)
			if !ok {
				continue
			}
			if title := rendererText(renderer.get("title")); title != "" {
				return title, rendererThumbnail(renderer.get("thumbnail"))
			}
		}
	}
	contents, _ := nestedObject(root, "contents")
	page, _ := nestedObject(contents, "twoColumnBrowseResultsRenderer")
	tabs, _ := page.get("tabs").([]any)
	for _, tab := range tabs {
		renderer, _ := nestedObject(tab, "tabRenderer")
		content, _ := nestedObject(renderer, "content")
		sections, _ := nestedObject(content, "sectionListRenderer")
		items, _ := sections.get("contents").([]any)
		for _, item := range items {
			header, _ := nestedObject(item, "musicResponsiveHeaderRenderer")
			if title := rendererText(header.get("title")); title != "" {
				return title, rendererThumbnail(header.get("thumbnail"))
			}
		}
	}
	return "", ""
}

type MusicSection struct {
	Title             string      `json:"title,omitempty"`
	Kind              string      `json:"kind"`
	Items             []MusicItem `json:"items"`
	ContinuationToken string      `json:"continuationToken,omitempty"`
	MoreID            string      `json:"moreId,omitempty"`
}

func (c *Client) GetHomeFeed(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_home")
}

func (c *Client) GetExplore(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_explore")
}

func (c *Client) GetLibrary(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_library_landing")
}

func (c *Client) StreamLibrary(ctx context.Context, progress func(*BrowseResult)) (*BrowseResult, error) {
	result, err := c.GetLibrary(ctx)
	if err != nil {
		return nil, err
	}
	if progress != nil && (result.ContinuationToken != "" || slices.ContainsFunc(result.Sections, func(s MusicSection) bool { return s.ContinuationToken != "" })) {
		progress(result.snapshot())
	}
	if err := c.drainBrowse(ctx, result, "library", progress); err != nil {
		return result, err
	}
	return result, nil
}

func (r *BrowseResult) snapshot() *BrowseResult {
	copied := *r
	copied.Items = slices.Clip(r.Items)
	copied.Sections = slices.Clone(r.Sections)
	for index := range copied.Sections {
		copied.Sections[index].Items = slices.Clip(copied.Sections[index].Items)
	}
	return &copied
}

const drainConcurrency = 4

func (c *Client) drainBrowse(ctx context.Context, result *BrowseResult, label string, progress func(*BrowseResult)) error {
	queue := browseContinuationQueue(result)
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		var level []browseContinuation
		exceeded := false
		for _, continuation := range queue {
			if _, exists := seen[continuation.token]; exists {
				continue
			}
			seen[continuation.token] = struct{}{}
			if len(seen) > 1000 {
				exceeded = true
				break
			}
			level = append(level, continuation)
		}
		queue = nil
		pages, errs := c.continueAll(ctx, level)
		for i, continuation := range level {
			if errs[i] != nil {
				return errs[i]
			}
			page := pages[i]
			result.Items = append(result.Items, page.Items...)
			if continuation.section >= 0 {
				result.Sections[continuation.section].Items = append(result.Sections[continuation.section].Items, page.Items...)
			} else {
				result.Sections = append(result.Sections, page.Sections...)
			}
			if page.ContinuationToken != "" {
				queue = append(queue, browseContinuation{token: page.ContinuationToken, section: -1})
			}
			for index, section := range page.Sections {
				if section.ContinuationToken == "" {
					continue
				}
				sectionIndex := continuation.section
				if sectionIndex < 0 {
					sectionIndex = len(result.Sections) - len(page.Sections) + index
				}
				queue = append(queue, browseContinuation{token: section.ContinuationToken, section: sectionIndex})
			}
		}
		if exceeded {
			return fmt.Errorf("youtube: %s exceeded 1000 continuation pages", label)
		}
		if progress != nil && len(queue) > 0 {
			progress(result.snapshot())
		}
	}
	result.ContinuationToken = ""
	for index := range result.Sections {
		result.Sections[index].ContinuationToken = ""
	}
	return nil
}

func (c *Client) continueAll(ctx context.Context, continuations []browseContinuation) ([]*BrowseResult, []error) {
	pages := make([]*BrowseResult, len(continuations))
	errs := make([]error, len(continuations))
	if len(continuations) == 1 {
		pages[0], errs[0] = c.ContinueBrowse(ctx, continuations[0].token)
		return pages, errs
	}
	var group sync.WaitGroup
	slots := make(chan struct{}, drainConcurrency)
	for i, continuation := range continuations {
		slots <- struct{}{}
		group.Add(1)
		go func() {
			defer func() { <-slots; group.Done() }()
			pages[i], errs[i] = c.ContinueBrowse(ctx, continuation.token)
		}()
	}
	group.Wait()
	return pages, errs
}

func (c *Client) ContinueBrowse(ctx context.Context, token string) (*BrowseResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("youtube: browse continuation token is required")
	}
	raw, err := c.executeAs(ctx, musicClient, false, "browse", map[string]any{"continuation": token})
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

func (c *Client) GetArtist(ctx context.Context, artistID string) (*BrowseResult, error) {
	if !strings.HasPrefix(artistID, "UC") && !strings.HasPrefix(artistID, "FEmusic_library_privately_owned_artist") {
		return nil, fmt.Errorf("youtube: invalid artist ID %q", artistID)
	}
	return c.browse(ctx, artistID)
}

func (c *Client) GetAlbum(ctx context.Context, albumID string) (*BrowseResult, error) {
	if !strings.HasPrefix(albumID, "MPR") && !strings.HasPrefix(albumID, "FEmusic_library_privately_owned_release") {
		return nil, fmt.Errorf("youtube: invalid album ID %q", albumID)
	}
	return c.browse(ctx, albumID)
}

func (c *Client) GetPlaylist(ctx context.Context, playlistID string) (*BrowseResult, error) {
	if playlistID == "" {
		return nil, errors.New("youtube: playlist ID is required")
	}
	if !strings.HasPrefix(playlistID, browsePlaylistPrefix) {
		playlistID = browsePlaylistPrefix + playlistID
	}
	return c.browse(ctx, playlistID)
}

func (c *Client) GetPodcast(ctx context.Context, showID string) (*BrowseResult, error) {
	if !isPodcastID(showID) {
		return nil, fmt.Errorf("youtube: invalid podcast ID %q", showID)
	}
	raw, err := c.execute(ctx, "browse", map[string]any{"browseId": showID})
	if err != nil {
		return nil, err
	}
	root := decodeResponse(raw)
	result := episodePage(browseResult(root))
	if header := findRenderer(root, "musicResponsiveHeaderRenderer"); header != nil {
		if result.Title == "" {
			result.Title = rendererText(header.get("title"))
			result.Thumbnail = rendererThumbnail(header.get("thumbnail"))
		}
		if shelf, ok := nestedObject(header.get("description"), "musicDescriptionShelfRenderer"); ok {
			result.Description = strings.Join(strings.Fields(rendererText(shelf.get("description"))), " ")
		}
	}
	return result, nil
}

func (c *Client) ContinuePodcast(ctx context.Context, token string) (*BrowseResult, error) {
	result, err := c.ContinueBrowse(ctx, token)
	if err != nil {
		return nil, err
	}
	return episodePage(result), nil
}

func episodePage(result *BrowseResult) *BrowseResult {
	result.ContinuationToken = ""
	if len(result.Sections) > 0 {
		result.ContinuationToken = result.Sections[len(result.Sections)-1].ContinuationToken
	}
	return result
}

func (c *Client) newBrowseResult(raw json.RawMessage) *BrowseResult {
	return browseResult(decodeResponse(raw))
}

func browseResult(root any) *BrowseResult {
	items, sections, _ := browseParts(root, false)
	title, thumbnail := pageHeading(root)
	return &BrowseResult{
		Title: title, Thumbnail: thumbnail,
		Items: items, Sections: sections,
		ContinuationToken: pageContinuationToken(root),
		Library:           headerLibraryState(root),
		Subscription:      headerSubscription(root),
	}
}

type browseContinuation struct {
	token   string
	section int
}

func browseContinuationQueue(result *BrowseResult) []browseContinuation {
	continuations := make([]browseContinuation, 0, len(result.Sections)+1)
	if result.ContinuationToken != "" {
		continuations = append(continuations, browseContinuation{token: result.ContinuationToken, section: -1})
	}
	for index, section := range result.Sections {
		if section.ContinuationToken != "" {
			continuations = append(continuations, browseContinuation{token: section.ContinuationToken, section: index})
		}
	}
	return continuations
}

func pageContinuationToken(root any) string {
	if token := appendedContinuationToken(root); token != "" {
		return token
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
			if token, ok := node.get("continuation").(string); ok && token != "" {
				return token
			}
			for _, member := range node.members {
				if isSectionRenderer(member.key) {
					continue
				}
				if token := walk(member.value); token != "" {
					return token
				}
			}
		}
		return ""
	}
	return walk(root)
}

func appendedContinuationToken(root any) string {
	object, ok := root.(*jsonObject)
	if !ok {
		return ""
	}
	actions, _ := object.get("onResponseReceivedActions").([]any)
	for _, action := range actions {
		appended, _ := nestedObject(action, "appendContinuationItemsAction")
		items, _ := appended.get("continuationItems").([]any)
		for index := len(items) - 1; index >= 0; index-- {
			renderer, ok := nestedObject(items[index], "continuationItemRenderer")
			if !ok {
				continue
			}
			if token := continuationToken(renderer); token != "" {
				return token
			}
		}
	}
	return ""
}

func continuationToken(root any) string {
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
			if command, ok := node.get("continuationCommand").(*jsonObject); ok {
				if token, ok := command.get("token").(string); ok && token != "" {
					return token
				}
			}
			if next, ok := node.get("nextContinuationData").(*jsonObject); ok {
				if token, ok := next.get("continuation").(string); ok && token != "" {
					return token
				}
			}
			if token, ok := node.get("continuation").(string); ok && token != "" {
				return token
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

func (c *Client) browse(ctx context.Context, browseID string) (*BrowseResult, error) {
	raw, err := c.execute(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}
