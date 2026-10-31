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

// BrowseResult represents a Music browse page such as an artist, album,
// playlist, home feed, explore page, library, or account settings.
type BrowseResult struct {
	// Title and Thumbnail are the page's own heading, the name and picture it
	// puts over its content, as an artist's does.
	Title     string `json:"title,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
	// Description is what a podcast show says of itself, on one line.
	Description       string         `json:"description,omitempty"`
	Items             []MusicItem    `json:"items"`
	Sections          []MusicSection `json:"sections,omitempty"`
	ContinuationToken string         `json:"continuationToken,omitempty"`
	// Library is the save button of an album's or a playlist's header: whether
	// the page is in the library, and what to write to change it. Its ID is
	// empty for a page that offers none.
	Library LibraryState `json:"-"`
	// Subscription is the subscribe button of an artist's header: whether the
	// account follows the artist, and the channel to write. Its ChannelID is
	// empty for a page that offers none.
	Subscription Subscription `json:"-"`
	// QueuePlaylistID is the playlist panel's ID, used to continue radio queues.
	QueuePlaylistID string `json:"queuePlaylistId,omitempty"`
}

// pageHeading reads the heading an artist, album or playlist puts over its
// content. Older pages put it at the top level; current albums put a responsive
// header in the primary tab. Only the header's thumbnail belongs to the page,
// not its artist portrait or a picture from the tracks and recommendations.
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

// MusicSection is a shelf or grid in a browse response. A continuation response
// without an owning shelf is represented as a synthetic section with an empty
// Title and its continuation renderer as Kind. ContinuationToken can be passed
// to ContinueBrowse to load more items in that section.

type MusicSection struct {
	Title             string      `json:"title,omitempty"`
	Kind              string      `json:"kind"`
	Items             []MusicItem `json:"items"`
	ContinuationToken string      `json:"continuationToken,omitempty"`
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

// StreamLibrary loads the library landing page and every continuation page
// YouTube offers, and calls progress
// with what it has so far: once for the landing page, then after each level
// of continuation pages. Each call gets a snapshot that the caller may keep
// and read while the rest loads. The final result is the last snapshot, with
// the tokens that asked for more cleared; progress is not called again for it.
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

// snapshot copies what a later page could still change: the lists of items
// and sections, though not the items themselves, which only ever grow at their
// end.
func (r *BrowseResult) snapshot() *BrowseResult {
	copied := *r
	copied.Items = slices.Clip(r.Items)
	copied.Sections = slices.Clone(r.Sections)
	for index := range copied.Sections {
		copied.Sections[index].Items = slices.Clip(copied.Sections[index].Items)
	}
	return &copied
}

// drainConcurrency is how many continuation pages are asked for at once.
const drainConcurrency = 4

// drainBrowse folds every continuation page reachable from result into it.
// label names the page in the error a runaway result produces.
//
// The pages are read a level at a time: those a level names are asked for
// together, and folded in the order they were named, so the result is the one
// a walk that asked for one page after another would give. A library's shelves
// each have their own chain of pages, and asking for them one by one made the
// page wait for every round trip in turn.
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

// continueAll asks for the pages of several continuations at once, a few at a
// time, and gives each answer or error at the place of its continuation.
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

// ContinueBrowse requests the next page for a browse or library section.
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
	if !strings.HasPrefix(playlistID, "VL") {
		playlistID = "VL" + playlistID
	}
	return c.browse(ctx, playlistID)
}

// GetPodcast reads a podcast show's page: its episodes, newest first, in one
// shelf. The page's continuation reads the next of them, as ContinuePodcast
// does.
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
	// A show puts its heading among its contents, not over them.
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

// ContinuePodcast reads the next episodes of a show from a token GetPodcast or
// ContinuePodcast returned.
func (c *Client) ContinuePodcast(ctx context.Context, token string) (*BrowseResult, error) {
	result, err := c.ContinueBrowse(ctx, token)
	if err != nil {
		return nil, err
	}
	return episodePage(result), nil
}

// episodePage makes the continuation of a show's episode shelf the page's
// own. The shelf is what goes on; the token the page holds besides reloads
// the shelf under one of its filters.
func episodePage(result *BrowseResult) *BrowseResult {
	result.ContinuationToken = ""
	if len(result.Sections) > 0 {
		result.ContinuationToken = result.Sections[len(result.Sections)-1].ContinuationToken
	}
	return result
}

// newBrowseResult reads a browse response. The response is parsed once, and
// everything taken from it is read off that one tree.
func (c *Client) newBrowseResult(raw json.RawMessage) *BrowseResult {
	return browseResult(decodeResponse(raw))
}

// browseResult reads a decoded browse response into the page's items, sections
// and continuation.
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

// pageContinuationToken returns only continuations that extend the whole
// browse response. Shelf continuations are owned by their renderer and must
// remain available on the corresponding MusicSection instead.
func pageContinuationToken(root any) string {
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
