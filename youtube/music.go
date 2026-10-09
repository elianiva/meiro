package youtube

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// SearchType limits search results to one music category. An empty value
// searches all categories.
type SearchType string

const (
	SearchAll       SearchType = ""
	SearchSongs     SearchType = "song"
	SearchVideos    SearchType = "video"
	SearchAlbums    SearchType = "album"
	SearchArtists   SearchType = "artist"
	SearchPlaylists SearchType = "playlist"
)

// SearchOptions controls a music search.
type SearchOptions struct {
	Type SearchType
}

// MusicItem is the common subset of YouTube Music song, video, album, artist,
// and playlist renderers.
type MusicItem struct {
	ID         string `json:"id,omitempty"`
	VideoID    string `json:"videoId,omitempty"`
	BrowseID   string `json:"browseId,omitempty"`
	PlaylistID string `json:"playlistId,omitempty"`
	Title      string `json:"title,omitempty"`
	Subtitle   string `json:"subtitle,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Duration   string `json:"duration,omitempty"`
	Thumbnail  string `json:"thumbnail,omitempty"`
}

// SearchResult contains the parsed music items of a search.
type SearchResult struct {
	Items             []MusicItem `json:"items"`
	ContinuationToken string      `json:"continuationToken,omitempty"`
}

// BrowseResult represents a Music browse page such as an artist, album,
// playlist, home feed, explore page, library, or account settings.
type BrowseResult struct {
	// Title and Thumbnail are the page's own heading, the name and picture it
	// puts over its content, as an artist's does.
	Title             string         `json:"title,omitempty"`
	Thumbnail         string         `json:"thumbnail,omitempty"`
	Items             []MusicItem    `json:"items"`
	Sections          []MusicSection `json:"sections,omitempty"`
	ContinuationToken string         `json:"continuationToken,omitempty"`
	// QueuePlaylistID is the playlist panel's ID, used to continue radio queues.
	QueuePlaylistID string `json:"queuePlaylistId,omitempty"`
}

// pageHeading reads the heading an artist, album or playlist puts over its
// content: the first header renderer with a name, and the picture in it. The
// renderers differ by page, which is why the one with a name is taken rather
// than a named one.
func pageHeading(root any) (title, thumbnail string) {
	object, ok := root.(*jsonObject)
	if !ok {
		return "", ""
	}
	header, ok := object.get("header").(*jsonObject)
	if !ok {
		return "", ""
	}
	for _, member := range header.members {
		renderer, ok := member.value.(*jsonObject)
		if !ok {
			continue
		}
		if title := rendererText(renderer.get("title")); title != "" {
			return title, rendererThumbnail(renderer.get("thumbnail"))
		}
	}
	return "", ""
}

// MusicSection is a shelf or grid in a browse response. ContinuationToken can
// be passed to ContinueBrowse to load more items in that section.
type MusicSection struct {
	Title             string      `json:"title,omitempty"`
	Kind              string      `json:"kind"`
	Items             []MusicItem `json:"items"`
	ContinuationToken string      `json:"continuationToken,omitempty"`
}

// AccountDetails contains the active account response.
type AccountDetails struct {
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// AccountChannel describes a channel in the account switcher.
type AccountChannel struct {
	Name       string `json:"name,omitempty"`
	Byline     string `json:"byline,omitempty"`
	Handle     string `json:"handle,omitempty"`
	ChannelID  string `json:"channelId,omitempty"`
	Thumbnail  string `json:"thumbnail,omitempty"`
	Selected   bool   `json:"selected,omitempty"`
	Disabled   bool   `json:"disabled,omitempty"`
	HasChannel bool   `json:"hasChannel,omitempty"`
}

// AccountList contains channels available to a cookie-authenticated account.
type AccountList struct {
	Items []AccountChannel `json:"items"`
}

// Lyrics contains the description shelf returned by YouTube Music for a track.
type Lyrics struct {
	Description string `json:"description,omitempty"`
	Footer      string `json:"footer,omitempty"`
}

// UpNextOptions identifies the playback queue whose next items are requested.
// PlaylistIndex is optional because a single-track radio request has no
// playlist position. Continuation is set when loading another page of the
// same queue.
type UpNextOptions struct {
	VideoID       string
	PlaylistID    string
	PlaylistIndex *int
	Continuation  string
}

// Search searches YouTube Music. Supported types are song, video, album,
// artist, playlist, and the empty value for all categories.
func (c *Client) Search(ctx context.Context, query string, options SearchOptions) (*SearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("youtube: search query is required")
	}
	payload := map[string]any{"query": query}
	if options.Type != SearchAll {
		params, err := musicSearchParams(options.Type)
		if err != nil {
			return nil, err
		}
		payload["params"] = url.QueryEscape(params)
	}
	raw, err := c.execute(ctx, "search", payload)
	if err != nil {
		return nil, err
	}
	return c.newSearchResult(raw), nil
}

func musicSearchParams(kind SearchType) (string, error) {
	field := map[SearchType]byte{
		SearchSongs: 1, SearchVideos: 2, SearchAlbums: 3,
		SearchArtists: 4, SearchPlaylists: 5,
	}[kind]
	if field == 0 {
		return "", fmt.Errorf("youtube: unsupported music search type %q", kind)
	}
	// SearchFilter{filters:{musicSearchType:{<field>:true}}}; protobuf field 17
	// of Filters holds the nested MusicSearchType message.
	inner := []byte{field << 3, 1}
	filters := append([]byte{0x8a, 0x01, byte(len(inner))}, inner...)
	message := append([]byte{0x12, byte(len(filters))}, filters...)
	return base64.StdEncoding.EncodeToString(message), nil
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

// GetAllLibrary loads every section and continuation page available from the
// library landing page. YouTube may still omit data based on account, region,
// or subscription access.
func (c *Client) GetAllLibrary(ctx context.Context) (*BrowseResult, error) {
	result, err := c.GetLibrary(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.drainBrowse(ctx, result, "library"); err != nil {
		return nil, err
	}
	return result, nil
}

// drainBrowse folds every continuation page reachable from result into it.
// label names the page in the error a runaway result produces.
func (c *Client) drainBrowse(ctx context.Context, result *BrowseResult, label string) error {
	queue := browseContinuations(result)
	seen := make(map[string]struct{})
	for len(queue) > 0 {
		token := queue[0]
		queue = queue[1:]
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		if len(seen) > 1000 {
			return fmt.Errorf("youtube: %s exceeded 1000 continuation pages", label)
		}
		page, err := c.ContinueBrowse(ctx, token)
		if err != nil {
			return err
		}
		result.Items = append(result.Items, page.Items...)
		result.Sections = append(result.Sections, page.Sections...)
		queue = append(queue, browseContinuations(page)...)
	}
	result.ContinuationToken = ""
	for index := range result.Sections {
		result.Sections[index].ContinuationToken = ""
	}
	return nil
}

// ContinueBrowse requests the next page for a browse or library section.
func (c *Client) ContinueBrowse(ctx context.Context, token string) (*BrowseResult, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("youtube: browse continuation token is required")
	}
	raw, err := c.execute(ctx, "browse", map[string]any{"continuation": token})
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

// ContinueSearch requests the next page from SearchResult.ContinuationToken.
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

// GetAllPlaylist loads every browse continuation available for a playlist.
// YouTube may still omit tracks based on account, region, or access.
func (c *Client) GetAllPlaylist(ctx context.Context, playlistID string) (*BrowseResult, error) {
	result, err := c.GetPlaylist(ctx, playlistID)
	if err != nil {
		return nil, err
	}
	if err := c.drainBrowse(ctx, result, "playlist"); err != nil {
		return nil, err
	}
	return result, nil
}

// GetAccountDetails returns the active signed-in account. It requires cookie
// authentication.
func (c *Client) GetAccountDetails(ctx context.Context) (*AccountDetails, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountDetails requires cookie authentication")
	}
	raw, err := c.execute(ctx, "account/account_menu", map[string]any{})
	if err != nil {
		return nil, err
	}
	details := &AccountDetails{}
	root := decodeResponse(raw)
	// The account menu names the active account, its email, and its photo in
	// one header renderer.
	if header := findRenderer(root, "activeAccountHeaderRenderer"); header != nil {
		details.Name = rendererText(header.get("accountName"))
		details.Email = rendererText(header.get("email"))
		details.Thumbnail = rendererThumbnail(header.get("accountPhoto"))
	}
	// The header carries no channel ID; the menu's account list does.
	for _, channel := range extractAccountChannels(root) {
		if !channel.Selected {
			continue
		}
		details.ChannelID = channel.ChannelID
		if details.Thumbnail == "" {
			details.Thumbnail = channel.Thumbnail
		}
		break
	}
	return details, nil
}

// GetAccounts lists all channels available to a cookie-authenticated account.
func (c *Client) GetAccounts(ctx context.Context) (*AccountList, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccounts requires cookie authentication")
	}
	raw, err := c.executeForClient(ctx, "account/accounts_list", map[string]any{
		"requestType":      "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER",
		"callCircumstance": "SWITCHING_USERS_FULL",
	}, webClient)
	if err != nil {
		return nil, err
	}
	return &AccountList{Items: extractAccountChannels(decodeResponse(raw))}, nil
}

// GetUpNext fetches the read-only queue for a track. Set PlaylistID and
// PlaylistIndex when the track plays from a playlist, and Continuation to
// extend the same queue with another /next page.
func (c *Client) GetUpNext(ctx context.Context, options UpNextOptions) (*BrowseResult, error) {
	if strings.TrimSpace(options.VideoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	payload := map[string]any{"videoId": options.VideoID}
	if options.PlaylistID != "" {
		payload["playlistId"] = options.PlaylistID
	}
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

// newUpNextResult reads a /next response. The response is parsed once, and its
// items, queue ID, and continuation are all read off that one tree.
func (c *Client) newUpNextResult(raw json.RawMessage) *BrowseResult {
	return upNextResult(decodeResponse(raw))
}

// upNextResult reads a decoded /next response.
func upNextResult(root any) *BrowseResult {
	items, sections := browseParts(root, true)
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

// ContinueUpNext requests another page of a queue returned by GetUpNext. The
// video ID and optional playlist fields preserve the context used to create
// that queue.
func (c *Client) ContinueUpNext(ctx context.Context, options UpNextOptions, continuation string) (*BrowseResult, error) {
	if strings.TrimSpace(continuation) == "" {
		return nil, errors.New("youtube: up-next continuation token is required")
	}
	options.Continuation = continuation
	return c.GetUpNext(ctx, options)
}

// GetSearchSuggestions returns the query completions YouTube Music offers for
// a partial query, in the order it offers them.
func (c *Client) GetSearchSuggestions(ctx context.Context, input string) ([]string, error) {
	if strings.TrimSpace(input) == "" {
		return nil, errors.New("youtube: suggestion input is required")
	}
	raw, err := c.execute(ctx, "music/get_search_suggestions", map[string]any{"input": input})
	if err != nil {
		return nil, err
	}
	return searchSuggestions(raw), nil
}

// searchSuggestions reads the completed text of every suggestion renderer in a
// suggestion response, in order and without repeats.
func searchSuggestions(raw json.RawMessage) []string {
	root := decodeResponse(raw)
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

// GetLyrics loads the lyrics tab for a track. Availability depends on the
// track and YouTube Music account/region.
func (c *Client) GetLyrics(ctx context.Context, videoID string) (*Lyrics, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_LYRICS")
	if err != nil {
		return nil, err
	}
	lyrics := &Lyrics{}
	root := decodeResponse(raw)
	if renderer := findRenderer(root, "musicDescriptionShelfRenderer"); renderer != nil {
		lyrics.Description = rendererText(renderer.get("description"))
		lyrics.Footer = rendererText(renderer.get("footer"))
	}
	return lyrics, nil
}

// GetRelated loads the related music tab for a track.
func (c *Client) GetRelated(ctx context.Context, videoID string) (*BrowseResult, error) {
	raw, err := c.getTrackTab(ctx, videoID, "MUSIC_PAGE_TYPE_TRACK_RELATED")
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}

// GetRecap loads the listening-review page for the signed-in account.
func (c *Client) GetRecap(ctx context.Context) (*BrowseResult, error) {
	return c.browse(ctx, "FEmusic_listening_review")
}

func (c *Client) getTrackTab(ctx context.Context, videoID, pageType string) (json.RawMessage, error) {
	if strings.TrimSpace(videoID) == "" {
		return nil, errors.New("youtube: video ID is required")
	}
	raw, err := c.execute(ctx, "next", map[string]any{"videoId": videoID})
	if err != nil {
		return nil, err
	}
	root := decodeResponse(raw)
	browseID := musicTabBrowseID(root, pageType)
	if browseID == "" {
		return nil, fmt.Errorf("youtube: track response has no %s tab", pageType)
	}
	return c.execute(ctx, "browse", map[string]any{"browseId": browseID})
}

func musicTabBrowseID(root any, pageType string) string {
	var walk func(any, []any) string
	walk = func(value any, parents []any) string {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if id := walk(child, parents); id != "" {
					return id
				}
			}
		case *jsonObject:
			if node.get("pageType") == pageType {
				for index := len(parents) - 1; index >= 0; index-- {
					if id := navigationID(parents[index]); id != "" {
						return id
					}
				}
				return navigationID(node)
			}
			parents = append(parents, node)
			for _, member := range node.members {
				if id := walk(member.value, parents); id != "" {
					return id
				}
			}
		}
		return ""
	}
	return walk(root, nil)
}

func findRenderer(root any, rendererName string) *jsonObject {
	var walk func(any) *jsonObject
	walk = func(value any) *jsonObject {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				if renderer := walk(child); renderer != nil {
					return renderer
				}
			}
		case *jsonObject:
			if renderer, ok := node.get(rendererName).(*jsonObject); ok {
				return renderer
			}
			for _, member := range node.members {
				if renderer := walk(member.value); renderer != nil {
					return renderer
				}
			}
		}
		return nil
	}
	return walk(root)
}

// newBrowseResult reads a browse response. The response is parsed once, and
// everything taken from it is read off that one tree.
func (c *Client) newBrowseResult(raw json.RawMessage) *BrowseResult {
	return browseResult(decodeResponse(raw))
}

// browseResult reads a decoded browse response into the page's items, sections
// and continuation.
func browseResult(root any) *BrowseResult {
	items, sections := browseParts(root, true)
	title, thumbnail := pageHeading(root)
	return &BrowseResult{
		Title: title, Thumbnail: thumbnail,
		Items: items, Sections: sections,
		ContinuationToken: continuationToken(root),
	}
}

// newSearchResult reads a search response, as newBrowseResult does a browse.
func (c *Client) newSearchResult(raw json.RawMessage) *SearchResult {
	root := decodeResponse(raw)
	items, _ := browseParts(root, false)
	return &SearchResult{
		Items: items, ContinuationToken: continuationToken(root),
	}
}

func browseContinuations(result *BrowseResult) []string {
	tokens := make([]string, 0, len(result.Sections)+1)
	if result.ContinuationToken != "" {
		tokens = append(tokens, result.ContinuationToken)
	}
	for _, section := range result.Sections {
		if section.ContinuationToken != "" {
			tokens = append(tokens, section.ContinuationToken)
		}
	}
	return tokens
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

// browseParts walks a response once, in document order, and returns the items
// and sections it holds. Every item renderer is parsed once, whether it sits
// inside a section or outside one, and a section's items are the ones it
// itself listed.
func browseParts(root any, wantSections bool) ([]MusicItem, []MusicSection) {
	var items []MusicItem
	var sections []MusicSection
	seen := make(map[string]struct{})
	var walk func(any, *sectionItems)
	walk = func(value any, section *sectionItems) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child, section)
			}
		case *jsonObject:
			for _, member := range node.members {
				if section == nil && wantSections && isSectionRenderer(member.key) {
					if renderer, ok := member.value.(*jsonObject); ok {
						current := &sectionItems{seen: make(map[string]struct{})}
						walk(renderer, current)
						sections = append(sections, MusicSection{
							Title: rendererTitle(renderer), Kind: member.key,
							Items: current.items, ContinuationToken: continuationToken(renderer),
						})
						continue
					}
				}
				if item, renderer, ok := parseMusicRenderer(member.key, member.value); ok {
					appendMusicItem(&items, seen, item, renderer)
					if section != nil {
						appendMusicItem(&section.items, section.seen, item, renderer)
					}
				}
				walk(member.value, section)
			}
		}
	}
	walk(root, nil)
	return items, sections
}

// sectionItems collects one section's items, which dedupe against each other
// rather than against the whole page.
type sectionItems struct {
	items []MusicItem
	seen  map[string]struct{}
}

// parseMusicRenderer reads the item a key and value name, when the key names an
// item renderer.
func parseMusicRenderer(key string, value any) (MusicItem, *jsonObject, bool) {
	renderer, ok := value.(*jsonObject)
	if !ok {
		return MusicItem{}, nil, false
	}
	if key == "musicCardShelfRenderer" {
		// A search's top result is a card, not a list entry.
		item, ok := parseMusicCardShelf(renderer)
		return item, renderer, ok
	}
	kind, ok := rendererKind(key)
	if !ok {
		return MusicItem{}, nil, false
	}
	return parseMusicItem(kind, renderer), renderer, true
}

// isSectionRenderer reports whether a key names a shelf or grid the page shows
// as its own section.
func isSectionRenderer(key string) bool {
	switch key {
	case "musicShelfRenderer", "musicPlaylistShelfRenderer", "playlistVideoListRenderer", "gridRenderer", "musicCarouselShelfRenderer":
		return true
	default:
		return false
	}
}

func extractAccountChannels(root any) []AccountChannel {
	var channels []AccountChannel
	seen := make(map[string]struct{})
	appendChannel := func(channel AccountChannel) {
		identity := channel.ChannelID + channel.Name + channel.Handle
		if identity == "" {
			return
		}
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		channels = append(channels, channel)
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case *jsonObject:
			if hasAccountChannelFields(node) {
				appendChannel(parseAccountChannel(node))
				return
			}
			for _, member := range node.members {
				child, ok := member.value.(*jsonObject)
				if !ok {
					walk(member.value)
					continue
				}
				if member.key == "accountItemRenderer" || member.key == "accountItem" {
					appendChannel(parseAccountChannel(child))
					continue
				}
				if hasAccountChannelFields(child) {
					appendChannel(parseAccountChannel(child))
					continue
				}
				walk(child)
			}
		}
	}
	walk(root)
	return channels
}

func parseAccountChannel(renderer *jsonObject) AccountChannel {
	channel := AccountChannel{
		Name:       rendererText(renderer.get("accountName")),
		Byline:     rendererText(renderer.get("accountByline")),
		Handle:     rendererText(renderer.get("channelHandle")),
		Thumbnail:  rendererThumbnail(renderer.get("accountPhoto")),
		Selected:   rendererBool(renderer.get("isSelected")),
		Disabled:   rendererBool(renderer.get("isDisabled")),
		HasChannel: rendererBool(renderer.get("hasChannel")),
		ChannelID:  navigationID(renderer.get("endpoint")),
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer.get("account_name"))
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer.get("name"))
	}
	if channel.ChannelID == "" {
		channel.ChannelID, _ = renderer.get("channelId").(string)
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer.get("handle").(string)
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer.get("channel_handle").(string)
	}
	if channel.Byline == "" {
		channel.Byline = rendererText(renderer.get("account_byline"))
	}
	if channel.Thumbnail == "" {
		channel.Thumbnail = rendererThumbnail(renderer.get("account_photo"))
	}
	return channel
}

func hasAccountChannelFields(value *jsonObject) bool {
	for _, key := range []string{"channelId", "accountName", "account_name", "channelHandle", "channel_handle"} {
		if value.get(key) != nil {
			return true
		}
	}
	return false
}

func rendererBool(value any) bool {
	result, _ := value.(bool)
	return result
}

// appendMusicItem keeps a parsed item unless its renderer held nothing to
// identify it by. A playlist may hold a song twice; its entries differ by
// the ID the playlist gave each, which keeps both.
func appendMusicItem(items *[]MusicItem, seen map[string]struct{}, item MusicItem, renderer *jsonObject) {
	identity := strings.Join([]string{
		item.VideoID, item.BrowseID, item.PlaylistID, item.Title,
		nestedString(renderer.get("playlistItemData"), "playlistSetVideoId"),
	}, "\x00")
	if identity == "\x00\x00\x00\x00" {
		return
	}
	if _, exists := seen[identity]; exists {
		return
	}
	seen[identity] = struct{}{}
	*items = append(*items, item)
}

// parseMusicCardShelf reads a search's top result. It carries its media in
// the card itself, outside any list: the title run navigates to the video,
// the artist's browse endpoint sits in the subtitle runs, and the length
// trails the subtitle.
func parseMusicCardShelf(renderer *jsonObject) (MusicItem, bool) {
	item := MusicItem{Kind: "video"}
	item.Title = rendererText(renderer.get("title"))
	item.Subtitle = rendererText(renderer.get("subtitle"))
	if duration := trailingDuration(item.Subtitle); duration != "" {
		item.Duration = duration
		item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+duration)
	}
	item.VideoID = nestedString([]any{renderer.get("title"), renderer.get("onTap"), renderer.get("thumbnailOverlay")}, "videoId")
	if item.VideoID == "" {
		item.VideoID = rendererVideoID(renderer)
	}
	item.BrowseID = cardArtistBrowseID(renderer)
	item.Thumbnail = rendererThumbnail(renderer.get("thumbnail"))
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer)
	}
	if item.VideoID != "" {
		item.ID = item.VideoID
	} else if item.BrowseID != "" {
		item.ID = item.BrowseID
	}
	if item.VideoID == "" || item.Title == "" {
		return MusicItem{}, false
	}
	return item, true
}

// cardArtistBrowseID reads the artist a card's subtitle names. navigationID
// cannot: it would return the mix queue of the card's menu entries first.
func cardArtistBrowseID(renderer *jsonObject) string {
	subtitle, ok := renderer.get("subtitle").(*jsonObject)
	if !ok {
		return ""
	}
	runs, ok := subtitle.get("runs").([]any)
	if !ok {
		return ""
	}
	for _, value := range runs {
		run, ok := value.(*jsonObject)
		if !ok {
			continue
		}
		endpoint, ok := run.get("navigationEndpoint").(*jsonObject)
		if !ok {
			continue
		}
		if browse, ok := endpoint.get("browseEndpoint").(*jsonObject); ok {
			if id, _ := browse.get("browseId").(string); id != "" {
				return id
			}
		}
	}
	return ""
}

// rendererArtistBrowseID reads an artist browse endpoint from a track's
// subtitle or second flex column, without treating album links as artists.
func rendererArtistBrowseID(renderer *jsonObject) string {
	if id := artistBrowseEndpoint(renderer.get("subtitle")); id != "" {
		return id
	}
	columns, ok := renderer.get("flexColumns").([]any)
	if !ok || len(columns) < 2 {
		return ""
	}
	return artistBrowseEndpoint(columns[1])
}

func artistBrowseEndpoint(value any) string {
	switch node := value.(type) {
	case []any:
		for _, child := range node {
			if id := artistBrowseEndpoint(child); id != "" {
				return id
			}
		}
	case *jsonObject:
		if endpoint, ok := node.get("browseEndpoint").(*jsonObject); ok {
			if id, _ := endpoint.get("browseId").(string); strings.HasPrefix(id, "UC") || strings.Contains(id, "privately_owned_artist") {
				return id
			}
		}
		for _, member := range node.members {
			if id := artistBrowseEndpoint(member.value); id != "" {
				return id
			}
		}
	}
	return ""
}

// trailingDuration reads a track length from the end of a card's subtitle,
// where it trails the artist as "Video • Artist • 8.4M views • 2:05".
func trailingDuration(subtitle string) string {
	for _, part := range strings.Split(subtitle, " • ") {
		if isDurationText(strings.TrimSpace(part)) {
			return strings.TrimSpace(part)
		}
	}
	return ""
}

func rendererKind(key string) (string, bool) {
	switch key {
	case "musicResponsiveListItemRenderer":
		return "track", true
	case "musicTwoRowItemRenderer":
		return "music_item", true
	case "musicVideoRenderer":
		return "video", true
	case "videoRenderer":
		return "video", true
	case "playlistVideoRenderer", "playlistPanelVideoRenderer":
		return "video", true
	case "gridPlaylistRenderer":
		return "playlist", true
	case "gridAlbumRenderer":
		return "album", true
	case "gridArtistRenderer":
		return "artist", true
	default:
		return "", false
	}
}

func parseMusicItem(kind string, renderer *jsonObject) MusicItem {
	item := MusicItem{Kind: kind}
	item.Title = rendererText(renderer.get("title"))
	if item.Title == "" {
		item.Title = rendererText(renderer.get("headline"))
	}
	if item.Title == "" {
		item.Title = rendererColumnText(renderer, "flexColumns", 0)
	}
	item.Subtitle = rendererText(renderer.get("subtitle"))
	if item.Subtitle == "" {
		item.Subtitle = rendererColumnText(renderer, "flexColumns", 1)
	}
	item.Duration = rendererText(renderer.get("lengthText"))
	if item.Duration == "" {
		item.Duration = rendererColumnText(renderer, "fixedColumns", 0)
	}
	if item.Duration == "" {
		// Search and library rows keep the length at the end of their last
		// flex column, as "Artist • Album • 3:42".
		item.Duration = rendererFlexDuration(renderer)
		if item.Duration != "" {
			item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+item.Duration)
		}
	}
	item.VideoID, _ = renderer.get("videoId").(string)
	if item.VideoID == "" {
		item.VideoID = rendererVideoID(renderer)
	}
	item.PlaylistID, _ = renderer.get("playlistId").(string)
	item.BrowseID = navigationID(renderer.get("navigationEndpoint"))
	if kind == "track" || kind == "video" {
		if artistID := rendererArtistBrowseID(renderer); artistID != "" {
			item.BrowseID = artistID
		}
	}
	if item.VideoID != "" {
		item.ID = item.VideoID
	} else if item.BrowseID != "" {
		item.ID = item.BrowseID
	} else {
		item.ID = item.PlaylistID
	}
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer.get("thumbnail"))
	}
	if item.Thumbnail == "" {
		// Two-row items keep their art under thumbnailRenderer instead of
		// thumbnail.
		item.Thumbnail = rendererThumbnail(renderer)
	}
	return item
}

func rendererText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	object, ok := value.(*jsonObject)
	if !ok {
		return ""
	}
	if text, ok := object.get("simpleText").(string); ok {
		return text
	}
	runs, ok := object.get("runs").([]any)
	if !ok {
		return ""
	}
	var builder strings.Builder
	for _, value := range runs {
		if run, ok := value.(*jsonObject); ok {
			if text, ok := run.get("text").(string); ok {
				builder.WriteString(text)
			}
		}
	}
	return builder.String()
}

func rendererColumnText(renderer *jsonObject, columnName string, index int) string {
	columns, ok := renderer.get(columnName).([]any)
	if !ok || index >= len(columns) {
		return ""
	}
	column, ok := columns[index].(*jsonObject)
	if !ok {
		return ""
	}
	// A flex column holds a musicResponsiveListItemFlexColumnRenderer and a
	// fixed column a musicResponsiveListItemFixedColumnRenderer.
	for _, key := range []string{"musicResponsiveListItemFlexColumnRenderer", "musicResponsiveListItemFixedColumnRenderer"} {
		if columnRenderer, ok := column.get(key).(*jsonObject); ok {
			return rendererText(columnRenderer.get("text"))
		}
	}
	return ""
}

// rendererVideoID finds a renderer's video ID where a track's own taps
// keep it: its data, its play overlay and its columns. The menu stays out:
// its mix and shuffle entries carry queue IDs, not the track's.
func rendererVideoID(renderer *jsonObject) string {
	for _, key := range []string{"playlistItemData", "overlay", "flexColumns", "thumbnailOverlay", "navigationEndpoint", "title", "onTap"} {
		value := renderer.get(key)
		if value == nil {
			continue
		}
		if id := nestedString(value, "videoId"); id != "" {
			return id
		}
	}
	return ""
}

// rendererTitle reads a shelf's title, which newer responses put in the
// shelf's header instead of the shelf itself.
func rendererTitle(renderer *jsonObject) string {
	if title := rendererText(renderer.get("title")); title != "" {
		return title
	}
	header, ok := renderer.get("header").(*jsonObject)
	if !ok {
		return ""
	}
	for _, member := range header.members {
		headerRenderer, ok := member.value.(*jsonObject)
		if !ok {
			continue
		}
		if title := rendererText(headerRenderer.get("title")); title != "" {
			return title
		}
	}
	return ""
}

// nestedString returns the first non-empty string stored under name anywhere
// in value. It visits members in document order, so the result follows the
// response.
func nestedString(value any, name string) string {
	var result string
	var walk func(any)
	walk = func(current any) {
		if result != "" {
			return
		}
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case *jsonObject:
			if text, ok := node.get(name).(string); ok && text != "" {
				result = text
				return
			}
			for _, member := range node.members {
				walk(member.value)
			}
		}
	}
	walk(value)
	return result
}

// rendererFlexDuration reads a track length from the trailing run of a
// musicResponsiveListItemRenderer's last flex column, where search and
// library rows keep it.
func rendererFlexDuration(renderer *jsonObject) string {
	columns, ok := renderer.get("flexColumns").([]any)
	if !ok {
		return ""
	}
	for index := len(columns) - 1; index >= 0; index-- {
		column, ok := columns[index].(*jsonObject)
		if !ok {
			continue
		}
		columnRenderer, ok := column.get("musicResponsiveListItemFlexColumnRenderer").(*jsonObject)
		if !ok {
			continue
		}
		text, ok := columnRenderer.get("text").(*jsonObject)
		if !ok {
			continue
		}
		runs, ok := text.get("runs").([]any)
		if !ok {
			continue
		}
		for runIndex := len(runs) - 1; runIndex >= 0; runIndex-- {
			run, ok := runs[runIndex].(*jsonObject)
			if !ok {
				continue
			}
			if run.has("navigationEndpoint") {
				continue
			}
			if text, _ := run.get("text").(string); isDurationText(text) {
				return text
			}
		}
	}
	return ""
}

// isDurationText reports whether text reads as a track length, as "3:42" or
// "1:02:03".
func isDurationText(text string) bool {
	parts := strings.Split(text, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 2 {
			return false
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return false
			}
		}
	}
	return true
}

func navigationID(value any) string {
	var result string
	var walk func(any)
	walk = func(current any) {
		if result != "" {
			return
		}
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case *jsonObject:
			// Queue taps name a watch endpoint, not a page: their playlist
			// is the mix being offered, and the track itself carries the
			// video ID. navigationID only names browsable pages.
			if node.has("watchEndpoint") {
				return
			}
			for _, name := range []string{"browseId", "playlistId"} {
				if value, ok := node.get(name).(string); ok && value != "" {
					if name == "playlistId" && !isBrowsablePlaylist(value) {
						continue
					}
					result = value
					return
				}
			}
			for _, member := range node.members {
				walk(member.value)
			}
		}
	}
	walk(value)
	return result
}

// isBrowsablePlaylist reports whether an ID names a page the app can open.
// Mix and radio queues ride along in taps and menus, but browsing one is
// empty: only library, community and product playlists open.
func isBrowsablePlaylist(id string) bool {
	return strings.HasPrefix(id, "VL") || strings.HasPrefix(id, "PL")
}

func rendererThumbnail(value any) string {
	var result string
	var walk func(any)
	walk = func(current any) {
		if result != "" {
			return
		}
		switch node := current.(type) {
		case []any:
			for _, child := range node {
				walk(child)
			}
		case *jsonObject:
			if thumbnails, ok := node.get("thumbnails").([]any); ok && len(thumbnails) > 0 {
				if thumbnail, ok := thumbnails[len(thumbnails)-1].(*jsonObject); ok {
					result, _ = thumbnail.get("url").(string)
				}
			}
			for _, member := range node.members {
				walk(member.value)
			}
		}
	}
	walk(value)
	return result
}
