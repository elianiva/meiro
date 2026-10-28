package youtube

import "strings"

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
// subtitle, byline or second flex column, without treating album links as artists.
func rendererArtistBrowseID(renderer *jsonObject) string {
	for _, byline := range []string{"subtitle", "longBylineText", "shortBylineText"} {
		if id := artistBrowseEndpoint(renderer.get(byline)); id != "" {
			return id
		}
	}
	columns, ok := renderer.get("flexColumns").([]any)
	if !ok || len(columns) < 2 {
		return ""
	}
	return artistBrowseEndpoint(columns[1])
}

// rendererCredits reads the artists and the album a track's byline links: the
// subtitle of a two-row item, the second flex column of a list row, and the
// bylines of a queue's panel item. It returns the first source that names an
// artist. Only linked runs count, so text that is merely shown beside the
// artist, such as the kind of item or a year, is never taken for a name.
func rendererCredits(renderer *jsonObject) (artists []string, album string) {
	sources := []any{renderer.get("subtitle"), renderer.get("longBylineText"), renderer.get("shortBylineText")}
	if columns, ok := renderer.get("flexColumns").([]any); ok && len(columns) >= 2 {
		if column, ok := columns[1].(*jsonObject); ok {
			if flex, ok := column.get("musicResponsiveListItemFlexColumnRenderer").(*jsonObject); ok {
				sources = append(sources, flex.get("text"))
			}
		}
	}
	for _, source := range sources {
		found, sourceAlbum := bylineCredits(source)
		if len(found) > 0 {
			return found, sourceAlbum
		}
		album = sourceAlbum
	}
	return nil, album
}

// bylineCredits reads the artists and the album linked from the runs of a
// byline, in the order it links them. A separator run links nothing, so it is
// not a name.
func bylineCredits(byline any) (artists []string, album string) {
	object, ok := byline.(*jsonObject)
	if !ok {
		return nil, ""
	}
	runs, _ := object.get("runs").([]any)
	for _, value := range runs {
		run, ok := value.(*jsonObject)
		if !ok {
			continue
		}
		text, _ := run.get("text").(string)
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		id := ""
		if browse, ok := nestedObject(run.get("navigationEndpoint"), "browseEndpoint"); ok {
			id, _ = browse.get("browseId").(string)
		}
		switch {
		case isArtistID(id):
			artists = append(artists, text)
		case isAlbumID(id) && album == "":
			album = text
		}
	}
	return artists, album
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

// setPlayerByline preserves the displayed runs instead of matching artist
// names inside a flattened string. An album may live in a separate column.
func (item *MusicItem) setPlayerByline(renderer *jsonObject) {
	if item.VideoID == "" || item.IsEpisode() {
		return
	}
	sources := []any{renderer.get("subtitle")}
	var columns []any
	if flex, ok := renderer.get("flexColumns").([]any); ok {
		for _, value := range flex[min(1, len(flex)):] {
			if column, ok := nestedObject(value, "musicResponsiveListItemFlexColumnRenderer"); ok {
				columns = append(columns, column.get("text"))
			}
		}
	}
	if len(columns) > 0 {
		sources = append(sources, columns[0])
	}
	sources = append(sources, renderer.get("longBylineText"), renderer.get("shortBylineText"))
	parts := []BylinePart{{Text: item.Subtitle}}
	for _, source := range sources {
		text := rendererText(source)
		if text == "" || item.Subtitle == "" {
			continue
		}
		if text == item.Subtitle || strings.TrimSuffix(text, " • "+item.Duration) == item.Subtitle {
			parts = bylineParts(source, len(item.Subtitle))
			break
		}
	}
	// Some list renderers put the album in their own flex column, while the
	// visible subtitle contains only artists. Keep its real destination too.
	hasAlbum := false
	for _, part := range parts {
		hasAlbum = hasAlbum || part.Target().Kind == TargetAlbum
	}
	if !hasAlbum {
		for _, source := range append(sources, columns...) {
			for _, part := range bylineParts(source, len(rendererText(source))) {
				if part.Target().Kind == TargetAlbum {
					if item.Subtitle != "" {
						parts = append(parts, BylinePart{Text: " • "})
					}
					parts = append(parts, part)
					if item.Album == "" {
						item.Album = part.Text
					}
					hasAlbum = true
					break
				}
			}
			if hasAlbum {
				break
			}
		}
	}
	for _, part := range parts {
		if part.Target().Opens() {
			item.Byline = &TrackByline{Parts: parts}
			return
		}
	}
}

// limit excludes a trailing duration already removed from Subtitle.
func bylineParts(source any, limit int) []BylinePart {
	object, ok := source.(*jsonObject)
	if !ok {
		return nil
	}
	runs, _ := object.get("runs").([]any)
	var parts []BylinePart
	if len(runs) == 0 {
		text := rendererText(source)
		return []BylinePart{{Text: text[:min(len(text), limit)]}}
	}
	for _, value := range runs {
		run, ok := value.(*jsonObject)
		if !ok || limit <= 0 {
			continue
		}
		text, _ := run.get("text").(string)
		text = text[:min(len(text), limit)]
		limit -= len(text)
		if text == "" {
			continue
		}
		part := BylinePart{Text: text}
		if endpoint, ok := nestedObject(run.get("navigationEndpoint"), "browseEndpoint"); ok {
			part.BrowseID, _ = endpoint.get("browseId").(string)
			if !part.Target().Opens() {
				part.BrowseID = ""
			}
		}
		parts = append(parts, part)
	}
	return parts
}
