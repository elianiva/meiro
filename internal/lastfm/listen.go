package lastfm

import "time"

const (
	MinTrack     = 30 * time.Second
	MaxThreshold = 4 * time.Minute

	maxGap = 2 * time.Second
	slack  = 500 * time.Millisecond
)

func Threshold(total time.Duration) (time.Duration, bool) {
	if total <= MinTrack {
		return 0, false
	}
	return min(total/2, MaxThreshold), true
}

type Event uint8

const (
	Nothing Event = iota
	Started
	Qualified
)

type Listen struct {
	Track  Track
	Chosen bool

	confirmed bool
	started   time.Time
	listened  time.Duration
	scrobbled bool

	haveLast bool
	lastAt   time.Time
	lastPos  time.Duration
}

func NewListen(track Track, chosen bool) *Listen {
	return &Listen{Track: track, Chosen: chosen}
}

func (l *Listen) Break() {
	if l != nil {
		l.haveLast = false
	}
}

func (l *Listen) Listened() time.Duration { return l.listened }

func (l *Listen) Observe(at time.Time, position, total time.Duration) Event {
	if l == nil || !l.Track.Valid() {
		return Nothing
	}
	if !l.confirmed {
		l.confirmed, l.started = true, at
		l.setLast(at, position)
		return Started
	}
	if l.haveLast {
		wait, moved := at.Sub(l.lastAt), position-l.lastPos
		if wait > 0 && wait <= maxGap && moved >= 0 && moved <= wait+slack {
			l.listened += moved
		}
	}
	l.setLast(at, position)
	if l.scrobbled {
		return Nothing
	}
	if need, ok := Threshold(total); ok && l.listened >= need {
		l.scrobbled = true
		return Qualified
	}
	return Nothing
}

func (l *Listen) setLast(at time.Time, position time.Duration) {
	l.haveLast, l.lastAt, l.lastPos = true, at, position
}

func (l *Listen) Started() time.Time { return l.started }

func (l *Listen) Scrobble(total time.Duration) Scrobble {
	track := l.Track
	track.Duration = total
	return Scrobble{Track: track, Started: l.started, Chosen: l.Chosen}
}
