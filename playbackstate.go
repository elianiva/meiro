package main

import (
	"hash/fnv"
	"slices"
	"time"

	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/youtube"
)

const persistInterval = 5 * time.Second

const positionStep = 5 * time.Second

const restoreEndGuard = 2 * time.Second

type playbackSave struct {
	writer playstate.Writer
	last   queueMark
	lastAt time.Duration
	saved  bool
}

type queueMark struct {
	tracks       uint64
	index        int
	recStart     int
	playNext     string
	source       string
	playlistMore string
}

func (q *queue) mark() queueMark {
	h := fnv.New64a()
	for _, item := range q.items {
		_, _ = h.Write([]byte(item.VideoID))
		_, _ = h.Write([]byte{0})
	}
	return queueMark{h.Sum64(), q.index, q.recStart, q.playNext, q.source, q.playlistMore}
}

func (q *queue) state(position time.Duration) playstate.State {
	return playstate.State{
		Items: q.items, Index: q.index, RecStart: q.recStart,
		PlayNext: q.playNext, Source: q.source, Position: position, PlaylistMore: q.playlistMore,
	}.Window()
}

func (q *queue) restore(s playstate.State) {
	*q = newQueue()
	q.items = slices.Clone(s.Items)
	q.index, q.recStart, q.playNext, q.source = s.Index, s.RecStart, s.PlayNext, s.Source
	q.playlistMore = s.PlaylistMore
	q.regenerate()
	for i := len(q.items) - 1; i > q.index; i-- {
		item := q.items[i]
		if _, generated := q.generated[item.VideoID]; generated && item.Kind == youtube.KindVideo {
			q.remove(i)
		}
	}
	q.recStart = q.firstGeneratedFrom(q.index + 1)
}

func (a *app) restorePlayback(path string) {
	a.playSave.writer.Path = path
	s := playstate.Load(path)
	a.episodes = s.Episodes
	if s.Empty() {
		return
	}
	a.queue.restore(s)
	if a.settings.ExcludeAIGenerated {
		a.queue.dropMarkedAI(a.excludesItem)
	}
	item := a.queue.current()
	a.current = item
	a.total = parseDuration(item.Duration)
	position := s.Position
	if a.total > 0 && position >= a.total-restoreEndGuard {
		position = 0
	}
	a.scrub, a.resumeID = position.Seconds(), item.VideoID
	a.resetUpNext(youtube.UpNextOptions{VideoID: item.VideoID})
	a.playSave.last, a.playSave.lastAt, a.playSave.saved = a.queue.mark(), position, true
}

func (a *app) playbackPosition() time.Duration {
	switch {
	case a.player.Active():
		return a.player.Position()
	case a.resumeID != "" && a.resumeID == a.current.VideoID:
		return time.Duration(a.scrub * float64(time.Second))
	}
	return 0
}

func (a *app) persistPlayback() { a.savePlayback(positionStep) }

func (a *app) savePlayback(step time.Duration) {
	if a.current.VideoID == "" || a.queue.len() == 0 {
		return
	}
	s := &a.playSave
	mark, position := a.queue.mark(), a.playbackPosition()
	moved := position - s.lastAt
	if s.saved && mark == s.last && moved > -step && moved < step {
		return
	}
	s.last, s.lastAt, s.saved = mark, position, true
	state := a.queue.state(position)
	state.Episodes = a.episodes
	s.writer.Save(state)
}

const episodeStartGuard = 30 * time.Second

const episodeEndGuard = time.Minute
