package main

import (
	"hash/fnv"
	"slices"
	"time"

	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/youtube"
)

// persistInterval is how often the queue and position are looked at for a
// change worth saving.
const persistInterval = 5 * time.Second

// positionStep is how far the position moves before it is saved again; a
// queue that changes is saved at once, whatever the position.
const positionStep = 5 * time.Second

// restoreEndGuard is how near the end of a track a saved position must be to
// start the track over instead: resuming there would end it at once.
const restoreEndGuard = 2 * time.Second

// playbackSave keeps the queue and the position between runs. Saving is
// debounced by its writer.
type playbackSave struct {
	writer playstate.Writer
	// last is what was last handed to the writer, to skip a save that would
	// write what is already there.
	last   queueMark
	lastAt time.Duration
	saved  bool
}

// queueMark is what makes one queue differ from another: the tracks in their
// order, and the places in them.
type queueMark struct {
	tracks   uint64
	index    int
	recStart int
	playNext string
	source   string
}

// mark summarises the queue. It reads every track's ID, which is cheap
// beside the period it is asked at.
func (q *queue) mark() queueMark {
	h := fnv.New64a()
	for _, item := range q.items {
		_, _ = h.Write([]byte(item.VideoID))
		_, _ = h.Write([]byte{0})
	}
	return queueMark{h.Sum64(), q.index, q.recStart, q.playNext, q.source}
}

// state is the queue as it is saved, with the position in the current track.
func (q *queue) state(position time.Duration) playstate.State {
	return playstate.State{
		Items: q.items, Index: q.index, RecStart: q.recStart,
		PlayNext: q.playNext, Source: q.source, Position: position,
	}.Window()
}

// restore makes the queue what s holds, which playstate.Load has checked.
func (q *queue) restore(s playstate.State) {
	*q = newQueue()
	q.items = slices.Clone(s.Items)
	q.index, q.recStart, q.playNext, q.source = s.Index, s.RecStart, s.PlayNext, s.Source
	q.regenerate()
}

// restorePlayback brings back the queue and the track the last run left,
// paused where it was. Nothing plays until the user presses play.
func (a *app) restorePlayback(path string) {
	a.playSave.writer.Path = path
	s := playstate.Load(path)
	if s.Empty() {
		return
	}
	a.queue.restore(s)
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

// playbackPosition is how far into the current track playback is.
func (a *app) playbackPosition() time.Duration {
	switch {
	case a.player.Active():
		return a.player.Position()
	case a.resumeID != "" && a.resumeID == a.current.VideoID:
		return time.Duration(a.scrub * float64(time.Second))
	}
	return 0
}

// persistPlayback hands the queue and position to the writer when they
// changed since the last time. It runs on the main thread.
func (a *app) persistPlayback() { a.savePlayback(positionStep) }

// savePlayback hands them over when the queue changed or the position moved
// by step or more. A step of 0 saves any change, as at exit, where the last
// seconds of listening are worth keeping.
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
	s.writer.Save(a.queue.state(position))
}
