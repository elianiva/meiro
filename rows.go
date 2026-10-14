package main

import (
	"strconv"

	"github.com/elianiva/meiro/youtube"
)

// rowKind says what a row of a page's list holds.
type rowKind uint8

const (
	rowHero      rowKind = iota // the heading of an album, playlist or artist
	rowTopResult                // the card a search's results lead with
	rowHeading                  // the name of a section, with arrows for its shelf
	rowCards                    // a shelf of cards that scrolls sideways
	rowColumns                  // a shelf of songs in columns that scrolls sideways
	rowGrid                     // one line of cards in a grid that wraps across the page
	rowTrack                    // one song, album, artist or playlist in a column of rows
	rowMore                     // the button that asks for the next page
	rowLoading                  // the page is loading
	rowError                    // the page failed
	rowEmpty                    // the page has nothing on it
	rowStreaming                // the rest of the page is still loading
)

// row is one line of a page's list.
type row struct {
	kind  rowKind
	title string
	// body is the line under an empty page's title.
	body string
	// shelf names the carousel a heading steers, and a shelf draws.
	shelf string
	// items are the cards or songs of a shelf; item is the one of a track row.
	items []youtube.MusicItem
	item  youtube.MusicItem
	// queue is what playing from this row plays: the songs of its shelf.
	// Track rows play from the page's songs.
	queue []youtube.MusicItem
	// track is the place of a song among the page's songs, or -1.
	track    int
	position int
	// offline says an error row is the network being out of reach.
	offline bool
}

// key identifies a row across frames, so the list keeps its place when the
// rows change.
func (r row) key() any {
	switch r.kind {
	case rowTrack:
		return itemKey("track", r.item) + "\x00" + r.shelf + "#" + strconv.Itoa(r.position)
	case rowTopResult:
		return itemKey("top", r.item)
	case rowGrid:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf + "#" + strconv.Itoa(r.position)
	case rowCards, rowColumns, rowHeading:
		return strconv.Itoa(int(r.kind)) + ":" + r.shelf
	case rowHero, rowMore, rowLoading, rowError, rowEmpty, rowStreaming:
	}
	return strconv.Itoa(int(r.kind)) + ":" + r.title
}

// number is the place shown beside a song that has no artwork of its own.
func (r row) number() int {
	if r.track < 0 || r.item.Thumbnail != "" {
		return 0
	}
	return r.track + 1
}

// rowsKey summarises what setRows reads, cheaply. Comparing it each frame
// rebuilds the rows whenever the page's shape changes, even when a caller did
// not mark them stale; a content change within the same shape still marks
// them with rowsDirty.
type rowsKey struct {
	path    string
	detail  detail
	loading bool
	err     string
	offline bool
	more    string
	moreErr string
	// moreBusy says the next part of the page is on its way, which the list
	// shows a spinner for.
	moreBusy bool
	// shelves folds the sections' titles and item counts into one number, and
	// loose is how many items a page without sections holds.
	shelves uint64
	loose   int
	partial bool
	// columns is how many cards a line of the page's grid holds; it is zero
	// on a page without one.
	columns int
	// submitted is the search the rows show the results of, and top the card
	// those results lead with.
	submitted string
	top       youtube.MusicItem
}

// syncRows rebuilds the rows when the page's data changed since the last
// build, so an idle page reuses them frame after frame.
func (a *app) syncRows() {
	r, _ := a.route()
	s := a.pageState()
	key := rowsKey{
		path: a.path(), detail: a.page.detail,
		loading: s.loading, partial: s.partial, err: s.err, offline: s.offline, more: s.more, moreErr: s.moreErr,
		moreBusy: a.pageMore.busy(),
		shelves:  shelfShape(s.sections), loose: len(s.items),
		columns: a.gridColumns(r),
	}
	if r.id == routeSearch {
		key.submitted, key.top = a.search.submitted, a.search.top
	}
	if a.page.rows == nil || a.page.rowsDirty || key != a.page.rowsKey {
		a.page.rowsKey, a.page.rowsDirty = key, false
		// Each page owns its rows alone, so the new ones reuse their memory.
		a.page.rows, a.page.playable = buildRows(a.page.rows[:0], a.page.playable[:0], r, s, a.page.detail, key.top, key)
	}
}

// shelfShape folds each section's title and item count into one number, as
// FNV-1a: a cheap stand-in for the sections themselves.
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

// buildRows flattens a loaded page into the rows its list shows, appended to
// rows, and collects the songs that playing the page would queue, appended
// to playable. It reads nothing but its arguments, which key summarises. top
// is the card a search's results lead with, which no other page has.
func buildRows(rows []row, playable []youtube.MusicItem, r *route, s *pageState, heading detail, top youtube.MusicItem, key rowsKey) ([]row, []youtube.MusicItem) {
	if r.isDetail() {
		rows = append(rows, row{kind: rowHero, title: heading.title, track: -1})
	}
	switch {
	case s.loading:
		return append(rows, row{kind: rowLoading, track: -1}), playable
	case s.err != "":
		return append(rows, row{kind: rowError, title: s.err, offline: s.offline, track: -1}), playable
	}
	if top.Title != "" {
		rows = append(rows, row{kind: rowTopResult, item: top, track: -1})
	}

	// Songs on an album or playlist page carry no artwork of their own: the
	// page's is what they belong to, and what the player shows.
	collection := r.isDetail() && r.target != youtube.TargetArtist
	fallback := ""
	if collection {
		fallback = heading.art
	}
	sections := s.sections
	if len(sections) == 0 && len(s.items) > 0 {
		sections = []youtube.MusicSection{{Items: s.items}}
	}
	vertical := collection || r.id == routeSearch
	// placed says whether the library has led with its tile yet.
	placed := false
	for index, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		shelf := key.path + "#" + strconv.Itoa(index)
		songs := 0
		for _, item := range section.Items {
			if item.IsTrack() {
				songs++
			}
		}
		mostlySongs := songs*10 >= len(section.Items)*6
		if section.Title != "" {
			rows = append(rows, row{kind: rowHeading, title: section.Title, shelf: shelf, track: -1})
		}
		if vertical || (r.isDetail() && mostlySongs) {
			for position, item := range section.Items {
				rw := row{kind: rowTrack, shelf: shelf, item: item, track: -1, position: position}
				if item.IsTrack() {
					rw.track = len(playable)
					if item.Thumbnail == "" {
						item.Thumbnail = fallback
						rw.item = item
					}
					playable = append(playable, item)
				}
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
		if mostlySongs && len(section.Items) >= 3 && section.Items[0].Duration != "" {
			kind = rowColumns
		}
		items := section.Items
		if r.grid && kind == rowCards {
			kind = rowGrid
		}
		// The library leads with making a playlist, before the Liked Music
		// that the rest of it usually starts with.
		if r.id == routeLibrary && !placed && (kind == rowGrid || kind == rowCards) {
			items = append([]youtube.MusicItem{newPlaylistItem}, items...)
			placed = true
		}
		if kind == rowGrid {
			// A grid is laid out a line to a row, so the list builds only the
			// lines in view. Building the whole grid as one row put every card
			// of a library, and a request for its artwork, in every frame.
			perLine := max(key.columns, 1)
			for start := 0; start < len(items); start += perLine {
				line := items[start:min(start+perLine, len(items))]
				rows = append(rows, row{kind: rowGrid, shelf: shelf, items: line, queue: tracks, track: -1, position: start})
			}
			continue
		}
		rows = append(rows, row{kind: kind, shelf: shelf, items: items, queue: tracks, track: -1})
	}
	if s.partial {
		rows = append(rows, row{kind: rowStreaming, track: -1})
	}
	// A search asks for the next part of its results itself, as its list
	// reaches the end of what it has, the way YouTube Music does. Every other
	// page keeps the button that asks.
	switch {
	case s.moreErr != "":
		rows = append(rows, row{kind: rowMore, title: "Could not load more. Try again", track: -1})
	case key.moreBusy:
		rows = append(rows, row{kind: rowStreaming, title: "more", track: -1})
	case s.more != "" && r.id != routeSearch:
		rows = append(rows, row{kind: rowMore, title: "Show more", track: -1})
	}
	if len(rows) == 0 || (len(rows) == 1 && rows[0].kind == rowHero) {
		title, body := r.empty.title, r.empty.body
		if r.id == routeSearch {
			title, body = "No results for “"+key.submitted+"”", "Check the spelling, or try a different search."
		}
		rows = append(rows, row{kind: rowEmpty, title: title, body: body, track: -1})
	}
	return rows, playable
}
