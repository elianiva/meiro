package main

import (
	"strconv"
	"strings"

	"github.com/elianiva/meiro/youtube"
)

const (
	noTrack            = -1
	minSongsForColumns = 3
)

type rowKind uint8

const (
	rowHero rowKind = iota
	rowTopResult
	rowHeading
	rowCards
	rowColumns
	rowGrid
	rowTrack
	rowMore
	rowSimilar
	rowLoading
	rowError
	rowEmpty
	rowStreaming
)

type row struct {
	title    string
	body     string
	shelf    string
	items    []youtube.MusicItem
	queue    []youtube.MusicItem
	calls    rowCalls
	cards    []cardView
	track    int
	position int
	kind     rowKind
	numbered bool
	offline  bool
	playAll  bool
}

func (r row) key() any {
	switch r.kind {
	case rowTrack:
		if r.calls.key != "" {
			return r.calls.key
		}
		return "track:" + r.title + "\x00" + r.shelf + "#" + strconv.Itoa(r.position)
	case rowTopResult:
		if len(r.items) > 0 {
			return itemKey("top", r.items[0])
		}
	case rowGrid:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf + "#" + strconv.Itoa(r.position)
	case rowCards, rowColumns, rowHeading:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf
	case rowHero, rowMore, rowLoading, rowError, rowEmpty, rowStreaming, rowSimilar:
	}
	return strconv.Itoa(int(r.kind)) + ":" + r.title
}

func prepareTrackRow(r *row, item youtube.MusicItem) {
	number := 0
	if r.track != noTrack && item.Thumbnail == "" {
		number = r.track + 1
	}
	r.numbered = number > 0
	r.calls = newRowCalls(item, songKey(item, songOptions{number: number, list: r.shelf, position: r.position}), r.shelf)
}

func (r row) trackItem(queue []youtube.MusicItem) (youtube.MusicItem, bool) {
	if r.track != noTrack && r.track < len(queue) {
		return queue[r.track], true
	}
	if len(r.items) > 0 {
		return r.items[0], true
	}
	return youtube.MusicItem{}, false
}

func (r row) number() int {
	if !r.numbered {
		return 0
	}
	return r.track + 1
}

type rowsKey struct {
	path         string
	detail       detail
	loading      bool
	err          string
	offline      bool
	more         string
	moreErr      string
	moreBusy     bool
	restBusy     bool
	similar      int
	similarShown int
	shelves      uint64
	loose        int
	partial      bool
	columns      int
	submitted    string
	top          youtube.MusicItem
	aiVersion    uint64
	excludeAI    bool
	blocked      uint64
	filter       string
	sort         playlistSort
}

func (a *app) syncRows() {
	r, _ := a.route()
	if r.id == routeDownloads && a.location == a.router.Location() {
		a.syncDownloadsPage()
	}
	s := a.pageState()
	if a.page.rowsDirty {
		s.shaped = false
	}
	key := rowsKey{
		path: a.path(), detail: a.page.detail,
		loading: s.loading, partial: s.partial, err: s.err, offline: s.offline, more: s.more, moreErr: s.moreErr,
		moreBusy: a.pageMore.busy(), similar: len(s.similar), similarShown: s.similarShown,
		shelves: s.shelfShape(), loose: len(s.items),
		columns: a.gridColumns(r), aiVersion: a.aiFilter.generation(), excludeAI: a.settings.ExcludeAIGenerated, blocked: a.blockedVersion,
	}
	if r.id == routeSearch {
		key.submitted, key.top = a.search.submitted, a.search.top
	}
	if r.id == routePlaylist {
		key.filter, key.sort = a.detailFilter, a.detailSort
		key.restBusy = a.pageSimilar.busy()
	}
	if a.page.rows == nil || a.page.rowsDirty || key != a.page.rowsKey {
		a.page.rowsKey, a.page.rowsDirty = key, false
		exclude := a.excludesItem
		if r.keepsBlockedSongs() {
			exclude = a.aiExcludes
		}
		a.page.rows, a.page.playable = buildRows(a.page.rows[:0], a.page.playable[:0], r, s, a.page.detail, key.top, key, exclude)
	}
}

func (s *pageState) shelfShape() uint64 {
	if !s.shaped {
		s.shape, s.shaped = shelfShape(s.sections), true
	}
	return s.shape
}

func shelfShape(sections []youtube.MusicSection) uint64 {
	shape := uint64(14695981039346656037)
	for _, section := range sections {
		for _, r := range section.Title {
			shape = (shape ^ uint64(r)) * 1099511628211
		}
		shape = (shape ^ uint64(len(section.Items))) * 1099511628211
		shape = (shape ^ '|') * 1099511628211
	}
	return shape
}

func buildRows(rows []row, playable []youtube.MusicItem, r *route, s *pageState, heading detail, top youtube.MusicItem, key rowsKey, exclude func(youtube.MusicItem) bool) ([]row, []youtube.MusicItem) {
	if r.isDetail() {
		rows = append(rows, row{kind: rowHero, title: heading.title, track: noTrack})
	}
	switch {
	case s.loading:
		return append(rows, row{kind: rowLoading, track: noTrack}), playable
	case s.err != "":
		return append(rows, row{kind: rowError, title: s.err, offline: s.offline, track: noTrack}), playable
	}
	hiddenAI := false
	keep := func(item youtube.MusicItem) bool {
		if exclude != nil && exclude(item) {
			hiddenAI = true
			return false
		}
		return true
	}
	if top.Title != "" && keep(top) {
		rows = append(rows, row{kind: rowTopResult, items: []youtube.MusicItem{top}, track: noTrack})
	}

	collection := r.isDetail() && r.target != youtube.TargetArtist
	fallback := ""
	if collection {
		fallback = heading.art
	}
	sections := s.sections
	if len(sections) == 0 && len(s.items) > 0 {
		sections = []youtube.MusicSection{{Items: s.items}}
	}
	if exclude != nil {
		visible := make([]youtube.MusicSection, 0, len(sections))
		for _, section := range sections {
			items := make([]youtube.MusicItem, 0, len(section.Items))
			for _, item := range section.Items {
				if keep(item) {
					items = append(items, item)
				}
			}
			if len(items) > 0 {
				section.Items = items
				visible = append(visible, section)
			}
		}
		sections = visible
	}
	if r.id == routePlaylist && key.sort.ordered() {
		sections = sortPlaylistSections(sections, key.sort)
	}
	if r.id == routePlaylist {
		hasTracks := false
		for _, section := range sections {
			for _, item := range section.Items {
				hasTracks = hasTracks || item.IsTrack()
			}
		}
		if hasTracks {
			rows = append(rows, row{kind: rowHeading, title: "Songs", shelf: "playlist-sort-header", track: noTrack})
		}
	}
	vertical := collection || r.id == routeSearch || r.id == routeDownloads
	searching := r.id == routePlaylist && key.filter != ""
	needle := strings.ToLower(strings.TrimSpace(key.filter))
	placed := false
	firstShelf := true
	for index, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		topShelf := firstShelf
		shelf := key.path + "#" + strconv.Itoa(index)
		shown := section.Items
		var places []int
		if searching {
			shown = make([]youtube.MusicItem, 0, len(section.Items))
			for _, item := range section.Items {
				if !item.IsTrack() {
					continue
				}
				if item.Thumbnail == "" {
					item.Thumbnail = fallback
				}
				if matchesSearch(item, needle) {
					shown = append(shown, item)
					places = append(places, len(playable))
				}
				playable = append(playable, item)
			}
			if len(shown) == 0 {
				continue
			}
		}
		firstShelf = false
		songs := 0
		for _, item := range shown {
			if item.IsTrack() {
				songs++
			}
		}
		mostlySongs := songs*10 >= len(shown)*6
		heading := -1
		if section.Title != "" {
			heading = len(rows)
			rows = append(rows, row{kind: rowHeading, title: section.Title, shelf: shelf, track: noTrack})
		}
		if vertical || (r.isDetail() && mostlySongs) {
			for position, item := range shown {
				rw := row{kind: rowTrack, shelf: shelf, title: item.Title, track: noTrack, position: position}
				if item.IsTrack() {
					if item.Thumbnail == "" {
						item.Thumbnail = fallback
					}
					if searching {
						rw.track = places[position]
					} else {
						rw.track = len(playable)
						playable = append(playable, item)
					}
				} else {
					rw.items = []youtube.MusicItem{item}
				}
				prepareTrackRow(&rw, item)
				rows = append(rows, rw)
			}
			continue
		}
		var tracks []youtube.MusicItem
		for _, item := range section.Items {
			if item.IsTrack() {
				tracks = append(tracks, item)
			}
		}
		kind := rowCards
		if mostlySongs && len(section.Items) >= minSongsForColumns && section.Items[0].Duration != "" {
			kind = rowColumns
		}
		if topShelf && r.id == routeHome && songs >= minSongsForColumns && songs == len(shown) {
			kind = rowColumns
		}
		items := section.Items
		if r.grid && kind == rowCards {
			kind = rowGrid
		}
		if r.id == routeLibrary && !placed && (kind == rowGrid || kind == rowCards) {
			items = append([]youtube.MusicItem{newPlaylistItem}, items...)
			placed = true
		}
		if kind == rowGrid {
			perLine := max(key.columns, 1)
			for start := 0; start < len(items); start += perLine {
				line := items[start:min(start+perLine, len(items))]
				cards := make([]cardView, len(line))
				for i := range line {
					cards[i] = newCardView(line[i], shelf, start+i)
				}
				rows = append(rows, row{kind: rowGrid, shelf: shelf, items: line, cards: cards, queue: tracks, track: noTrack, position: start})
			}
			continue
		}
		if heading >= 0 && topShelf && kind == rowColumns {
			rows[heading].playAll = true
			rows[heading].queue = tracks
		}
		var cards []cardView
		if kind == rowCards {
			cards = make([]cardView, len(items))
			for i := range items {
				cards[i] = newCardView(items[i], shelf, i)
			}
		}
		rows = append(rows, row{kind: kind, shelf: shelf, items: items, cards: cards, queue: tracks, track: noTrack})
	}
	if s.partial {
		rows = append(rows, row{kind: rowStreaming, track: noTrack})
	}
	similar := s.similar
	if exclude != nil {
		similar = make([]youtube.MusicItem, 0, len(s.similar))
		for _, item := range s.similar {
			if keep(item) {
				similar = append(similar, item)
			}
		}
	}
	if r.id == routePlaylist && len(similar) > 0 && key.filter == "" {
		rows = append(rows, row{kind: rowSimilar, title: "Similar songs", track: noTrack})
		for i, item := range similar[:min(s.similarShown, len(similar))] {
			rw := row{kind: rowTrack, title: item.Title, queue: similar, track: i}
			prepareTrackRow(&rw, item)
			rows = append(rows, rw)
		}
		if s.similarShown < len(similar) || s.similarMore != "" {
			rows = append(rows, row{kind: rowMore, title: "Show more", shelf: "similar", track: noTrack})
		}
	}
	switch {
	case s.moreErr != "":
		rows = append(rows, row{kind: rowMore, title: "Could not load more. Try again", track: noTrack})
	case key.moreBusy || key.restBusy:
		rows = append(rows, row{kind: rowStreaming, title: "more", track: noTrack})
	case s.more != "" && r.id != routeSearch && r.id != routePlaylist:
		rows = append(rows, row{kind: rowMore, title: "Show more", track: noTrack})
	}
	if len(rows) == 0 || (len(rows) == 1 && rows[0].kind == rowHero) ||
		(searching && len(rows) == 2 && rows[0].kind == rowHero && rows[1].shelf == "playlist-sort-header") {
		title, body := r.empty.title, r.empty.body
		switch {
		case hiddenAI:
			title, body = "No non-AI tracks found", "YouTube marked every song here as made with AI, or the page has nothing else to show."
		case r.id == routeSearch:
			title, body = "No results for “"+key.submitted+"”", "Check the spelling, or try a different search."
		case searching:
			title, body = "No songs match “"+key.filter+"”", ""
		}
		rows = append(rows, row{kind: rowEmpty, title: title, body: body, track: noTrack})
	}
	return rows, playable
}

func matchesSearch(item youtube.MusicItem, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(item.Title), needle) ||
		strings.Contains(strings.ToLower(item.Subtitle), needle)
}
