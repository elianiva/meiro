package main

import (
	"slices"

	"github.com/elianiva/meiro/youtube"
)

// reorderQueue moves the queue tracks at indices rows to before index to,
// and reports where the current track landed. Rows must be queue indices in
// any order; to is a queue index in the queue as it stands before the move.
// Items keep their identity: only positions change, so the player, which
// holds no pointers into the queue, keeps playing what it plays.
func reorderQueue(queue []youtube.MusicItem, rows []int, to int, currentID string) ([]youtube.MusicItem, int) {
	picked := slices.Clone(rows)
	slices.Sort(picked)
	picked = slices.Compact(picked)
	moving := make([]youtube.MusicItem, 0, len(picked))
	rest := make([]youtube.MusicItem, 0, len(queue))
	at := make(map[int]struct{}, len(picked))
	for _, row := range picked {
		if row >= 0 && row < len(queue) {
			at[row] = struct{}{}
		}
	}
	for i, item := range queue {
		if _, ok := at[i]; ok {
			moving = append(moving, item)
		} else {
			rest = append(rest, item)
		}
	}
	// Rows removed before the destination pull it back by one each.
	insert := to
	for row := range at {
		if row < to {
			insert--
		}
	}
	insert = min(max(insert, 0), len(rest))
	moved := append(slices.Clone(rest[:insert]), append(moving, rest[insert:]...)...)
	index := slices.IndexFunc(moved, func(item youtube.MusicItem) bool { return item.VideoID == currentID })
	return moved, index
}

// moveQueue carries out a list reorder of the queue: it maps the list rows,
// which include the recommendation divider, onto queue indices, moves them,
// and keeps the current track, the play-next mark and the recommendation
// boundary right.
func (a *app) moveQueue(rows []int, to int) {
	header := a.recommendationStart > 0 && a.recommendationStart < len(a.queue)
	toQueue := to
	if header && to > a.recommendationStart {
		toQueue--
	}
	indices := make([]int, 0, len(rows))
	for _, row := range rows {
		index := row
		if header && row > a.recommendationStart {
			index--
		}
		if header && row == a.recommendationStart {
			continue // the divider is not a track
		}
		if index >= 0 && index < len(a.queue) {
			indices = append(indices, index)
		}
	}
	if len(indices) == 0 {
		return
	}
	moved, index := reorderQueue(a.queue, indices, toQueue, a.current.VideoID)
	if index < 0 {
		return // the current track left the queue; keep the state as it is
	}
	a.queue, a.index = moved, index
	a.recommendationStart = recommendationBoundary(moved, a.recommendationIDs)
	a.queueFollow = "" // the list scrolls to the current track again
	a.syncSystemMedia()
}

// recommendationBoundary is the first generated track of the queue, or -1
// when none of the generated tracks are in it anymore.
func recommendationBoundary(queue []youtube.MusicItem, generated map[string]struct{}) int {
	if len(generated) == 0 {
		return -1
	}
	for i, item := range queue {
		if _, ok := generated[item.VideoID]; ok {
			return i
		}
	}
	return -1
}
