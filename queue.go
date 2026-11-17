package main

import (
	"math/rand/v2"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/elianiva/meiro/youtube"
)

const autoplayQueueLimit = 100

type queue struct {
	items        []youtube.MusicItem
	index        int
	recStart     int
	generated    map[string]struct{}
	playNext     string
	shuffleKept  []youtube.MusicItem
	source       string
	playlistMore string
}

const noRecommendations = -1

func (q *queue) hasRecommendations() bool { return q.recStart != noRecommendations }

func newQueue() queue { return queue{recStart: noRecommendations, generated: map[string]struct{}{}} }

func (q *queue) len() int { return len(q.items) }

func (q *queue) current() youtube.MusicItem {
	if q.index < 0 || q.index >= len(q.items) {
		return youtube.MusicItem{}
	}
	return q.items[q.index]
}

func (q *queue) each(fn func(*youtube.MusicItem)) {
	for i := range q.items {
		fn(&q.items[i])
	}
}

func (q *queue) at(index int) youtube.MusicItem { return q.items[index] }

func (q *queue) remaining() int { return len(q.items) - q.index - 1 }

func (q *queue) replace(item youtube.MusicItem, tracks []youtube.MusicItem, index int, source string) {
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

func (q *queue) limit(autoPlay bool) int {
	if !autoPlay && q.hasRecommendations() {
		return q.recStart
	}
	return len(q.items)
}

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

func (q *queue) nextEligible(autoPlay bool, repeat repeatMode, skipDisliked bool) (int, bool) {
	if !skipDisliked {
		return q.next(autoPlay, repeat)
	}
	if q.playNext != "" {
		for i := max(q.index+1, 0); i < len(q.items); i++ {
			if q.items[i].VideoID == q.playNext && q.items[i].Like != youtube.Disliked {
				return i, true
			}
		}
	}
	limit := q.limit(autoPlay)
	if limit == 0 || q.index >= limit {
		return 0, false
	}
	for i := max(q.index+1, 0); i < limit; i++ {
		if q.items[i].Like != youtube.Disliked {
			return i, true
		}
	}
	if repeat == repeatQueue {
		for i := 0; i <= q.index && i < limit; i++ {
			if q.items[i].Like != youtube.Disliked {
				return i, true
			}
		}
	}
	return 0, false
}

func (q *queue) moveTo(index int) youtube.MusicItem {
	q.index = index
	q.recStart = q.firstGeneratedFrom(q.index + 1)
	if q.items[index].VideoID == q.playNext {
		q.playNext = ""
	}
	return q.items[index]
}

func (q *queue) firstGeneratedFrom(from int) int {
	for index := max(from, 0); index < len(q.items); index++ {
		if _, generated := q.generated[q.items[index].VideoID]; generated {
			return index
		}
	}
	return -1
}

func (q *queue) find(videoID string) int {
	return slices.IndexFunc(q.items, func(t youtube.MusicItem) bool { return t.VideoID == videoID })
}

func (q *queue) insert(item youtube.MusicItem, playNext, autoPlay bool) {
	position := len(q.items)
	if q.hasRecommendations() {
		if q.index < q.recStart {
			position = q.recStart
		} else {
			position = q.index + 1
			for position < len(q.items) {
				if _, generated := q.generated[q.items[position].VideoID]; generated {
					break
				}
				position++
			}
		}
	}
	if playNext {
		position = q.index + 1
	}
	q.insertAt(item, position)
	if playNext {
		q.playNext = item.VideoID
		if !autoPlay && q.hasRecommendations() && position >= q.recStart {
			q.recStart = position + 1
		}
	}
}

func (q *queue) appendPlaylist(items []youtube.MusicItem, shuffle bool) {
	if shuffle && len(q.shuffleKept) == 0 {
		q.shuffleKept = slices.Clone(q.items[:q.limit(false)])
	}
	for _, item := range items {
		position := q.limit(false)
		if shuffle {
			q.shuffleKept = append(q.shuffleKept, item)
			if position > q.index {
				position = q.index + 1 + rand.IntN(position-q.index)
			}
		}
		q.insertAt(item, position)
	}
}

func (q *queue) insertAt(item youtube.MusicItem, position int) {
	position = min(max(position, 0), len(q.items))
	q.items = slices.Insert(q.items, position, item)
	if position <= q.index {
		q.index++
	}
	if q.hasRecommendations() && position <= q.recStart {
		q.recStart++
	}
}

func (q *queue) remove(index int) bool {
	if index < 0 || index >= len(q.items) || index == q.index {
		return false
	}
	id := q.items[index].VideoID
	q.items = slices.Delete(q.items, index, index+1)
	if index < q.index {
		q.index--
	}
	if id == q.playNext {
		q.playNext = ""
	}
	delete(q.generated, id)
	if q.hasRecommendations() {
		if index < q.recStart {
			q.recStart--
		}
		if q.recStart >= len(q.items) {
			q.recStart = noRecommendations
		}
	}
	return true
}

func (q *queue) dropMarkedAI(exclude func(youtube.MusicItem) bool) bool {
	removed := false
	for index := len(q.items) - 1; index > q.index; index-- {
		if exclude != nil && exclude(q.items[index]) {
			removed = q.remove(index) || removed
		}
	}
	return removed
}

func (q *queue) clearUpcoming() bool {
	q.playlistMore = ""
	if q.remaining() < 1 {
		return false
	}
	q.items = slices.Clone(q.items[:q.index+1])
	q.playNext = ""
	q.recStart = noRecommendations
	q.generated = map[string]struct{}{}
	return true
}

func (q *queue) shift(index, delta int) bool {
	target := index + delta
	if index < 0 || index >= len(q.items) || target < 0 || target >= len(q.items) {
		return false
	}
	to := q.rowOf(target)
	if delta > 0 {
		to++
	}
	return q.moveRows([]int{q.rowOf(index)}, to)
}

func (q *queue) regenerate() {
	q.generated = map[string]struct{}{}
	if !q.hasRecommendations() {
		return
	}
	for _, item := range q.items[q.recStart:] {
		if item.VideoID != "" {
			q.generated[item.VideoID] = struct{}{}
		}
	}
}

const sameSongTolerance = 3 * time.Second

type songID struct{ title, artist string }

func songIDOf(item youtube.MusicItem) songID {
	return songID{normalizeName(item.Title), normalizeName(item.Artist)}
}

func normalizeName(text string) string {
	var b strings.Builder
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func noteVersion(versions map[songID][]time.Duration, item youtube.MusicItem) {
	if key := songIDOf(item); key.title != "" {
		versions[key] = append(versions[key], parseDuration(item.Duration))
	}
}

func hasVersion(versions map[songID][]time.Duration, item youtube.MusicItem) bool {
	key := songIDOf(item)
	if key.title == "" {
		return false
	}
	d := parseDuration(item.Duration)
	return slices.ContainsFunc(versions[key], func(o time.Duration) bool {
		return d <= 0 || o <= 0 || (d-o).Abs() <= sameSongTolerance
	})
}

func (q *queue) appendGenerated(items []youtube.MusicItem) {
	seen := make(map[string]struct{}, len(q.items)+min(len(items), autoplayQueueLimit+1))
	versions := make(map[songID][]time.Duration, len(q.items))
	for _, queued := range q.items {
		if queued.VideoID != "" {
			seen[queued.VideoID] = struct{}{}
		}
		noteVersion(versions, queued)
	}
	fresh := make([]youtube.MusicItem, 0, min(len(items), autoplayQueueLimit+1))
	for _, item := range items {
		if item.VideoID == "" || item.Kind == youtube.KindVideo {
			continue
		}
		if _, exists := seen[item.VideoID]; exists {
			continue
		}
		seen[item.VideoID] = struct{}{}
		if hasVersion(versions, item) {
			continue
		}
		noteVersion(versions, item)
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
		if q.hasRecommendations() {
			q.recStart = max(0, q.recStart-played)
		}
	}
	for _, item := range fresh {
		if len(q.generated) >= autoplayQueueLimit {
			break
		}
		if !q.hasRecommendations() {
			q.recStart = len(q.items)
		}
		q.items = append(q.items, item)
		q.generated[item.VideoID] = struct{}{}
	}
}

func (q *queue) shuffleQueue() {
	if _, generated := q.generated[q.current().VideoID]; generated {
		return
	}
	limit := len(q.items)
	if q.hasRecommendations() && q.recStart < limit {
		limit = q.recStart
	}
	if limit <= 1 || q.index < 0 || q.index >= limit {
		return
	}
	q.shuffleKept = append(q.shuffleKept[:0], q.items[:limit]...)
	current := q.items[q.index]
	rest := make([]youtube.MusicItem, 0, limit-1)
	rest = append(rest, q.items[:q.index]...)
	rest = append(rest, q.items[q.index+1:limit]...)
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	ordered := make([]youtube.MusicItem, 0, limit)
	ordered = append(ordered, current)
	ordered = append(ordered, rest...)
	copy(q.items[:limit], ordered)
	q.index = 0
	if q.hasRecommendations() {
		q.recStart = limit
	}
}

func (q *queue) restoreQueue() {
	if len(q.shuffleKept) == 0 {
		return
	}
	limit := len(q.items)
	if q.hasRecommendations() && q.recStart < limit {
		limit = q.recStart
	}
	if limit == 0 {
		return
	}
	playing := ""
	if q.index >= 0 && q.index < limit {
		playing = q.items[q.index].VideoID
	}
	left := slices.Clone(q.items[:limit])
	ordered := make([]youtube.MusicItem, 0, limit)
	take := func(want youtube.MusicItem) {
		for i, queued := range left {
			if queued.VideoID == want.VideoID {
				ordered = append(ordered, queued)
				left = slices.Delete(left, i, i+1)
				return
			}
		}
	}
	for _, item := range q.shuffleKept {
		take(item)
	}
	ordered = append(ordered, left...)
	copy(q.items[:limit], ordered)
	q.shuffleKept = q.shuffleKept[:0]
	if playing == "" {
		return
	}
	for i, item := range q.items[:limit] {
		if item.VideoID == playing {
			q.index = i
			break
		}
	}
}

func (q *queue) hasDivider() bool { return q.recStart > 0 && q.recStart < len(q.items) }

func (q *queue) rows() int {
	if q.hasDivider() {
		return len(q.items) + 1
	}
	return len(q.items)
}

func (q *queue) isDivider(row int) bool { return q.hasDivider() && row == q.recStart }

func (q *queue) trackAt(row int) (int, bool) {
	if q.isDivider(row) {
		return 0, false
	}
	if q.hasDivider() && row > q.recStart {
		row--
	}
	return row, row >= 0 && row < len(q.items)
}

func (q *queue) rowOf(index int) int {
	if q.hasDivider() && index >= q.recStart {
		return index + 1
	}
	return index
}

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
	q.recStart = q.firstGeneratedFrom(q.index + 1)
	return true
}

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
	insert := to
	for index := range at {
		if index < to {
			insert--
		}
	}
	insert = min(max(insert, 0), len(rest))
	return slices.Concat(rest[:insert], moving, rest[insert:])
}

type repeatMode uint8

const (
	repeatOff repeatMode = iota
	repeatQueue
	repeatTrack
)

func (m repeatMode) next() repeatMode { return (m + 1) % (repeatTrack + 1) }
