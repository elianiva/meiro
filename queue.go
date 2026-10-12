package main

import (
	"math/rand/v2"
	"slices"

	"github.com/elianiva/meiro/youtube"
)

// autoplayQueueLimit bounds the generated tracks a queue holds, the current
// one and those to come; played history is dropped to stay under it.
const autoplayQueueLimit = 100

// queue is what plays: the tracks the user chose, followed by the tracks
// auto-play generated after them, and the place of the one playing.
//
// Its fields are only changed by its methods, which keep them consistent:
// index is a place in items whenever items is not empty, recStart is -1 or
// a place in items, and every generated track in items is in generated.
type queue struct {
	items []youtube.MusicItem
	index int
	// recStart is the place of the first generated track, or -1 when the
	// queue holds none.
	recStart  int
	generated map[string]struct{}
	// playNext is the track the user asked to play next, which plays before
	// the rest even after the queue is reordered.
	playNext string
	// source names where the chosen tracks came from, for the queue's
	// heading.
	source string
}

func newQueue() queue { return queue{recStart: -1, generated: map[string]struct{}{}} }

// len is how many tracks the queue holds.
func (q *queue) len() int { return len(q.items) }

// current is the track at the queue's place, or the zero item when the
// queue is empty.
func (q *queue) current() youtube.MusicItem {
	if q.index < 0 || q.index >= len(q.items) {
		return youtube.MusicItem{}
	}
	return q.items[q.index]
}

// at is the track at a place in the queue.
func (q *queue) at(index int) youtube.MusicItem { return q.items[index] }

// remaining is how many tracks follow the current one.
func (q *queue) remaining() int { return len(q.items) - q.index - 1 }

// replace makes the queue tracks, chosen from source, with item at its
// place: index when that is where item is, or else wherever item first is.
// A queue without item in it holds item alone. Generated tracks and the
// play-next mark of the queue before are dropped.
func (q *queue) replace(item youtube.MusicItem, tracks []youtube.MusicItem, index int, source string) {
	// The page that offered tracks rebuilds its own list on every frame, so
	// the queue must not share its backing array.
	*q = newQueue()
	q.items, q.source = slices.Clone(tracks), source
	if index < 0 || index >= len(q.items) || q.items[index].VideoID != item.VideoID {
		index = slices.IndexFunc(q.items, func(t youtube.MusicItem) bool { return t.VideoID == item.VideoID })
	}
	if index < 0 {
		q.items, index = []youtube.MusicItem{item}, 0
	}
	q.index = index
}

// limit is the end of what may play: the whole queue, or only the chosen
// tracks when auto-play is off.
func (q *queue) limit(autoPlay bool) int {
	if !autoPlay && q.recStart >= 0 {
		return q.recStart
	}
	return len(q.items)
}

// next is the place of the track that follows the current one, and false
// when the queue is over.
func (q *queue) next(autoPlay bool, repeat repeatMode) (int, bool) {
	if q.playNext != "" {
		for i := max(q.index+1, 0); i < len(q.items); i++ {
			if q.items[i].VideoID == q.playNext {
				return i, true
			}
		}
	}
	limit := q.limit(autoPlay)
	switch {
	case limit == 0 || q.index >= limit:
		return 0, false
	case q.index+1 < limit:
		return q.index + 1, true
	case repeat == repeatQueue:
		return 0, true
	}
	return 0, false
}

// moveTo makes the track at index the current one.
func (q *queue) moveTo(index int) youtube.MusicItem {
	q.index = index
	if q.items[index].VideoID == q.playNext {
		q.playNext = ""
	}
	return q.items[index]
}

// find is the place of a track in the queue, or -1.
func (q *queue) find(videoID string) int {
	return slices.IndexFunc(q.items, func(t youtube.MusicItem) bool { return t.VideoID == videoID })
}

// insert adds a chosen track: after the current one when playNext is set,
// and otherwise at the end of the chosen tracks, ahead of the generated ones.
func (q *queue) insert(item youtube.MusicItem, playNext, autoPlay bool) {
	position := len(q.items)
	if q.recStart >= 0 {
		position = q.recStart
	}
	if playNext {
		position = q.index + 1
	}
	position = min(max(position, 0), len(q.items))
	q.items = slices.Insert(q.items, position, item)
	if position <= q.index {
		q.index++
	}
	if q.recStart >= 0 && position <= q.recStart {
		q.recStart++
	}
	if playNext {
		q.playNext = item.VideoID
		// With auto-play off the queue stops at the generated tracks, so a
		// track to play next placed among them moves the boundary past it.
		if !autoPlay && q.recStart >= 0 && position >= q.recStart {
			q.recStart = position + 1
		}
	}
}

// regenerate counts the generated tracks again from the boundary, for a
// queue that goes back to auto-play.
func (q *queue) regenerate() {
	q.generated = map[string]struct{}{}
	if q.recStart < 0 {
		return
	}
	for _, item := range q.items[q.recStart:] {
		if item.VideoID != "" {
			q.generated[item.VideoID] = struct{}{}
		}
	}
}

// appendGenerated adds the recommendations YouTube Music returned that the
// queue does not already hold, keeping at most autoplayQueueLimit generated
// tracks: played tracks are dropped first to make room, without touching
// the one playing.
func (q *queue) appendGenerated(items []youtube.MusicItem) {
	seen := make(map[string]struct{}, len(q.items)+min(len(items), autoplayQueueLimit+1))
	for _, queued := range q.items {
		if queued.VideoID != "" {
			seen[queued.VideoID] = struct{}{}
		}
	}
	fresh := make([]youtube.MusicItem, 0, min(len(items), autoplayQueueLimit+1))
	for _, item := range items {
		if item.VideoID == "" {
			continue
		}
		if _, exists := seen[item.VideoID]; exists {
			continue
		}
		seen[item.VideoID] = struct{}{}
		fresh = append(fresh, item)
		if len(fresh) > autoplayQueueLimit {
			break
		}
	}
	if len(q.generated)+len(fresh) > autoplayQueueLimit && q.index > 0 {
		played := q.index
		for _, queued := range q.items[:played] {
			delete(q.generated, queued.VideoID)
		}
		q.items = slices.Clone(q.items[played:])
		q.index = 0
		if q.recStart >= 0 {
			q.recStart = max(0, q.recStart-played)
		}
	}
	for _, item := range fresh {
		if len(q.generated) >= autoplayQueueLimit {
			break
		}
		if q.recStart < 0 {
			q.recStart = len(q.items)
		}
		q.items = append(q.items, item)
		q.generated[item.VideoID] = struct{}{}
	}
}

// shuffleUpcoming arranges the chosen tracks still to play in a random
// order. The track playing, those played and the generated tracks stay
// where they are.
func (q *queue) shuffleUpcoming() {
	limit := len(q.items)
	if q.recStart >= 0 && q.recStart < limit {
		limit = q.recStart
	}
	start := q.index + 1
	if start < 0 || start >= limit {
		return
	}
	rand.Shuffle(limit-start, func(i, j int) {
		q.items[start+i], q.items[start+j] = q.items[start+j], q.items[start+i]
	})
}

// The queue's list shows a divider between the chosen and the generated
// tracks, when there are both. Rows of that list are not places in the
// queue: these translate between the two.

// hasDivider reports whether the list shows the divider.
func (q *queue) hasDivider() bool { return q.recStart > 0 && q.recStart < len(q.items) }

// rows is how many rows the list shows.
func (q *queue) rows() int {
	if q.hasDivider() {
		return len(q.items) + 1
	}
	return len(q.items)
}

// isDivider reports whether a row of the list is the divider.
func (q *queue) isDivider(row int) bool { return q.hasDivider() && row == q.recStart }

// trackAt is the place in the queue a row of the list shows, and false for
// the divider or a row past the end.
func (q *queue) trackAt(row int) (int, bool) {
	if q.isDivider(row) {
		return 0, false
	}
	if q.hasDivider() && row > q.recStart {
		row--
	}
	return row, row >= 0 && row < len(q.items)
}

// rowOf is the row of the list that shows a place in the queue.
func (q *queue) rowOf(index int) int {
	if q.hasDivider() && index >= q.recStart {
		return index + 1
	}
	return index
}

// moveRows carries out a reorder of the list: the tracks the rows show move
// to before the row to, as the list stood before the move. The current
// track, the play-next mark and the boundary of the generated tracks follow.
// It reports whether anything moved.
func (q *queue) moveRows(rows []int, to int) bool {
	indices := make([]int, 0, len(rows))
	for _, row := range rows {
		if index, ok := q.trackAt(row); ok {
			indices = append(indices, index)
		}
	}
	if len(indices) == 0 {
		return false
	}
	toIndex := to
	if q.hasDivider() && to > q.recStart {
		toIndex--
	}
	current := q.current()
	moved := reorder(q.items, indices, toIndex)
	index := slices.IndexFunc(moved, func(item youtube.MusicItem) bool { return item.VideoID == current.VideoID })
	if index < 0 {
		return false
	}
	q.items, q.index = moved, index
	q.recStart = -1
	if len(q.generated) > 0 {
		q.recStart = slices.IndexFunc(moved, func(item youtube.MusicItem) bool {
			_, ok := q.generated[item.VideoID]
			return ok
		})
	}
	return true
}

// reorder moves the items at indices, in any order, to before index to, a
// place in items as they stand before the move, and returns the result in a
// new slice.
func reorder(items []youtube.MusicItem, indices []int, to int) []youtube.MusicItem {
	at := make(map[int]struct{}, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(items) {
			at[index] = struct{}{}
		}
	}
	moving := make([]youtube.MusicItem, 0, len(at))
	rest := make([]youtube.MusicItem, 0, len(items))
	for i, item := range items {
		if _, ok := at[i]; ok {
			moving = append(moving, item)
		} else {
			rest = append(rest, item)
		}
	}
	// Each item taken from before the destination pulls it back by one.
	insert := to
	for index := range at {
		if index < to {
			insert--
		}
	}
	insert = min(max(insert, 0), len(rest))
	return slices.Concat(rest[:insert], moving, rest[insert:])
}

// repeatMode is what happens when a track, or the queue, ends.
type repeatMode uint8

const (
	repeatOff   repeatMode = iota // play on, and stop at the end of the queue
	repeatQueue                   // go back to the first track after the last
	repeatTrack                   // play the same track again
)

// next is the mode a press of the repeat button goes to.
func (m repeatMode) next() repeatMode { return (m + 1) % (repeatTrack + 1) }
