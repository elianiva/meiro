package youtube

import "strings"

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

func browseParts(root any, search bool) ([]MusicItem, []MusicSection, MusicItem) {
	var items []MusicItem
	var sections []MusicSection
	var top MusicItem
	seen := make(map[itemIdentity]struct{})
	var walk func(any, *sectionItems)
	walk = func(value any, section *sectionItems) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child, section)
			}
		case *jsonObject:
			for _, member := range node.members {
				if section == nil && !search && isSectionRenderer(member.key) {
					if renderer, ok := member.value.(*jsonObject); ok {
						current := &sectionItems{seen: make(map[itemIdentity]struct{})}
						walk(renderer, current)
						sections = append(sections, MusicSection{
							Title: rendererTitle(renderer), Kind: member.key,
							Items: current.items, ContinuationToken: continuationToken(renderer),
							MoreID: sectionMoreID(member.key, renderer),
						})
						continue
					}
				}
				if item, renderer, ok := parseMusicRenderer(member.key, member.value); ok {
					if search && member.key == "musicCardShelfRenderer" {
						if top.Title == "" {
							top = item
						}
					} else {
						appendMusicItem(&items, seen, item, renderer)
						if section != nil {
							appendMusicItem(&section.items, section.seen, item, renderer)
						}
					}
				}
				walk(member.value, section)
			}
		}
	}
	walk(root, nil)
	return items, sections, top
}

type sectionItems struct {
	items []MusicItem
	seen  map[itemIdentity]struct{}
}

const audioTrackVideoType = "MUSIC_VIDEO_TYPE_ATV"

func parseMusicRenderer(key string, value any) (MusicItem, *jsonObject, bool) {
	renderer, ok := value.(*jsonObject)
	if !ok {
		return MusicItem{}, nil, false
	}
	if key == "musicCardShelfRenderer" {
		item, ok := parseMusicCardShelf(renderer)
		return item, renderer, ok
	}
	kind, ok := rendererKind(key)
	if !ok {
		return MusicItem{}, nil, false
	}
	item := parseMusicItem(kind, renderer)
	if key == "playlistPanelVideoRenderer" {
		if videoType := nestedString(renderer.get("navigationEndpoint"), "musicVideoType"); videoType == "" || videoType == audioTrackVideoType {
			item.Kind = KindTrack
		}
	}
	return item, renderer, true
}

func isSectionRenderer(key string) bool {
	switch key {
	case "musicShelfRenderer", "musicShelfContinuation", "musicPlaylistShelfRenderer", "musicPlaylistShelfContinuation", "playlistVideoListRenderer", "playlistVideoListContinuation", "gridRenderer", "gridContinuation", "musicCarouselShelfRenderer":
		return true
	default:
		return false
	}
}

func appendMusicItem(items *[]MusicItem, seen map[itemIdentity]struct{}, item MusicItem, renderer *jsonObject) {
	identity := itemIdentity{
		videoID: item.VideoID, browseID: item.BrowseID, playlistID: item.PlaylistID, title: item.Title,
		setVideoID: nestedString(renderer.get("playlistItemData"), "playlistSetVideoId"),
	}
	if identity == (itemIdentity{}) {
		return
	}
	if _, exists := seen[identity]; exists {
		return
	}
	seen[identity] = struct{}{}
	*items = append(*items, item)
}

type itemIdentity struct {
	videoID, browseID, playlistID, title, setVideoID string
}

func parseMusicCardShelf(renderer *jsonObject) (MusicItem, bool) {
	item := MusicItem{Kind: KindMusicItem}
	item.Title = rendererText(renderer.get("title"))
	if item.Title == "" {
		return MusicItem{}, false
	}
	item.Subtitle = rendererText(renderer.get("subtitle"))
	if duration := trailingDuration(item.Subtitle); duration != "" {
		item.Duration = duration
		item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+duration)
	}
	item.BrowseID, item.VideoID = cardTarget(renderer)
	if item.VideoID == "" {
		item.VideoID = episodeVideoIDFromBrowseID(item.BrowseID)
		if item.VideoID != "" {
			item.Kind = KindEpisode
		}
	}
	item.setCredits(bylineCredits(renderer.get("subtitle")))
	item.setDisplayByline(renderer)
	if item.VideoID != "" && item.Kind != KindEpisode {
		item.Kind = KindVideo
	}
	if item.VideoID != "" {
		item.ID = item.VideoID
	} else {
		item.ID = item.BrowseID
	}
	item.Thumbnail = rendererThumbnail(renderer.get("thumbnail"))
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer)
	}
	return item, true
}

func cardTarget(renderer *jsonObject) (browseID, videoID string) {
	for _, value := range []any{renderer.get("title"), renderer.get("onTap")} {
		if browseID == "" {
			browseID = navigationID(value)
		}
		if videoID == "" {
			videoID = nestedString(value, "videoId")
		}
	}
	if browseID == "" && videoID == "" {
		videoID = rendererVideoID(renderer)
	}
	if browseID == "" && videoID == "" {
		browseID = cardArtistBrowseID(renderer)
	}
	return browseID, videoID
}

func nestedObject(value any, name string) (*jsonObject, bool) {
	object, ok := value.(*jsonObject)
	if !ok {
		return nil, false
	}
	child, ok := object.get(name).(*jsonObject)
	return child, ok
}

func trailingDuration(subtitle string) string {
	for _, part := range strings.Split(subtitle, " • ") {
		if isDurationText(strings.TrimSpace(part)) {
			return strings.TrimSpace(part)
		}
	}
	return ""
}

func rendererKind(key string) (ItemKind, bool) {
	switch key {
	case "musicResponsiveListItemRenderer":
		return KindTrack, true
	case "musicTwoRowItemRenderer":
		return KindMusicItem, true
	case "musicVideoRenderer", "videoRenderer", "playlistVideoRenderer", "playlistPanelVideoRenderer":
		return KindVideo, true
	case "gridPlaylistRenderer":
		return KindPlaylist, true
	case "gridAlbumRenderer":
		return KindAlbum, true
	case "gridArtistRenderer":
		return KindArtist, true
	case "musicMultiRowListItemRenderer":
		return KindEpisode, true
	default:
		return "", false
	}
}

func parseMusicItem(kind ItemKind, renderer *jsonObject) MusicItem {
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
	if item.Subtitle == "" {
		item.Subtitle = rendererText(renderer.get("longBylineText"))
		if item.Subtitle == "" {
			item.Subtitle = rendererText(renderer.get("shortBylineText"))
		}
	}
	item.Duration = rendererText(renderer.get("lengthText"))
	if item.Duration == "" {
		item.Duration = rendererColumnText(renderer, "fixedColumns", 0)
	}
	if item.Duration == "" {
		item.Duration = rendererFlexDuration(renderer)
		if item.Duration != "" {
			item.Subtitle = strings.TrimSuffix(item.Subtitle, " • "+item.Duration)
		}
	}
	item.VideoID, _ = renderer.get("videoId").(string)
	if item.VideoID == "" {
		item.VideoID = rendererVideoID(renderer)
	}
	item.BrowseID = navigationID(renderer.get("navigationEndpoint"))
	if item.BrowseID == "" {
		item.BrowseID = navigationID(renderer.get("title"))
	}
	episodeVideoID := episodeVideoIDFromBrowseID(item.BrowseID)
	if item.VideoID == "" {
		item.VideoID = episodeVideoID
	}
	if item.VideoID != "" && (isEpisode(renderer) || episodeVideoID != "") {
		item.Kind = KindEpisode
	}
	if item.Kind == KindEpisode {
		item.Description = strings.Join(strings.Fields(rendererText(renderer.get("description"))), " ")
		item.ProgressPercent = episodeProgress(renderer)
	}
	if length := episodeLength(renderer); length != "" && item.Duration == "" {
		item.Subtitle = strings.TrimPrefix(item.Subtitle+" • "+length, " • ")
	}
	if kind == KindTrack || kind == KindVideo || item.VideoID != "" {
		item.setCredits(rendererCredits(renderer))
	}
	item.setDisplayByline(renderer)
	item.PlaylistID, _ = renderer.get("playlistId").(string)
	item.PlaylistSetID = nestedString(renderer.get("playlistItemData"), "playlistSetVideoId")
	item.Like = likeStatus(renderer)
	item.Library = itemLibraryState(renderer)
	if item.BrowseID == "" && (kind == KindTrack || kind == KindVideo) {
		item.BrowseID = rendererArtistBrowseID(renderer)
	}
	switch {
	case item.VideoID != "":
		item.ID = item.VideoID
	case item.BrowseID != "":
		item.ID = item.BrowseID
	default:
		item.ID = item.PlaylistID
	}
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer.get("thumbnail"))
	}
	if item.Thumbnail == "" {
		item.Thumbnail = rendererThumbnail(renderer)
	}
	return item
}

func isEpisode(renderer *jsonObject) bool {
	for _, key := range []string{"navigationEndpoint", "onTap", "overlay"} {
		if nestedString(renderer.get(key), "musicVideoType") == "MUSIC_VIDEO_TYPE_PODCAST_EPISODE" {
			return true
		}
	}
	return false
}

func episodeLength(renderer *jsonObject) string {
	progress, ok := nestedObject(renderer.get("playbackProgress"), "musicPlaybackProgressRenderer")
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(rendererText(progress.get("durationText")), " • "))
}

func episodeProgress(renderer *jsonObject) int {
	progress, ok := nestedObject(renderer.get("playbackProgress"), "musicPlaybackProgressRenderer")
	if !ok {
		return 0
	}
	value, ok := progress.get("playbackProgressPercentage").(float64)
	if !ok {
		return 0
	}
	return min(max(int(value), 0), 100)
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
	for _, key := range []string{"musicResponsiveListItemFlexColumnRenderer", "musicResponsiveListItemFixedColumnRenderer"} {
		if columnRenderer, ok := column.get(key).(*jsonObject); ok {
			return rendererText(columnRenderer.get("text"))
		}
	}
	return ""
}

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

func sectionMoreID(kind string, renderer *jsonObject) string {
	if kind != "musicShelfRenderer" {
		return ""
	}
	id := navigationID(renderer.get("title"))
	if !strings.HasPrefix(id, browsePlaylistPrefix) {
		return ""
	}
	return id
}
