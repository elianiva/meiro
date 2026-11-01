package lastfm

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

var song = Track{Artist: "A", Title: "T"}

// player plays a track against a fake clock, sampling every 250 ms as the app
// does, and records the events its samples made true.
type player struct {
	l        *Listen
	total    time.Duration
	now      time.Time
	position time.Duration
	events   []Event
}

func newPlayer(total time.Duration, track Track, chosen bool) *player {
	return &player{l: NewListen(track, chosen), total: total, now: t0}
}

func (p *player) sample() {
	if e := p.l.Observe(p.now, p.position, p.total); e != Nothing {
		p.events = append(p.events, e)
	}
}

// play lets d of sound go by, sampled every 250 ms.
func (p *player) play(d time.Duration) {
	for end := p.now.Add(d); p.now.Before(end); {
		p.now = p.now.Add(250 * time.Millisecond)
		p.position += 250 * time.Millisecond
		p.sample()
	}
}

// wait lets d of the clock go by with no sound and no samples: paused.
func (p *player) wait(d time.Duration) {
	p.now = p.now.Add(d)
	p.l.Break()
}

// stall lets d go by sampled but silent: buffering with the position fixed.
func (p *player) stall(d time.Duration) {
	for end := p.now.Add(d); p.now.Before(end); {
		p.now = p.now.Add(250 * time.Millisecond)
		p.sample()
	}
}

// seek moves the position, which the app marks with a break.
func (p *player) seek(to time.Duration) {
	p.position = to
	p.l.Break()
}

func count(events []Event, want Event) int {
	n := 0
	for _, e := range events {
		if e == want {
			n++
		}
	}
	return n
}

func TestListenQualifiesAtHalf(t *testing.T) {
	p := newPlayer(3*time.Minute, song, true)
	p.play(time.Second)
	if len(p.events) != 1 || p.events[0] != Started {
		t.Fatalf("events = %v, want just Started", p.events)
	}
	p.play(88 * time.Second) // 88 s listened in all, short of 90
	if count(p.events, Qualified) != 0 {
		t.Fatalf("scrobbled at %s of 180 s: %v", p.l.Listened(), p.events)
	}
	p.play(3 * time.Second)
	if count(p.events, Qualified) != 1 {
		t.Fatalf("events = %v, want Qualified once past half", p.events)
	}
	p.play(time.Minute)
	if count(p.events, Qualified) != 1 {
		t.Errorf("a play scrobbles once: %v", p.events)
	}
	if got := p.l.Started(); !got.Equal(t0.Add(250 * time.Millisecond)) {
		t.Errorf("Started = %s, want the first confirmed sample", got)
	}
}

func TestListenLongTrackQualifiesAtFourMinutes(t *testing.T) {
	p := newPlayer(20*time.Minute, song, false)
	p.play(3*time.Minute + 55*time.Second)
	if count(p.events, Qualified) != 0 {
		t.Fatal("scrobbled before four minutes")
	}
	p.play(15 * time.Second)
	if count(p.events, Qualified) != 1 {
		t.Fatalf("events = %v", p.events)
	}
}

func TestListenShortTrackNeverQualifies(t *testing.T) {
	p := newPlayer(30*time.Second, song, true)
	p.play(30 * time.Second)
	if count(p.events, Qualified) != 0 || count(p.events, Started) != 1 {
		t.Errorf("a 30 s track: events = %v, want Started only", p.events)
	}
	unknown := newPlayer(0, song, true)
	unknown.play(10 * time.Minute)
	if count(unknown.events, Qualified) != 0 {
		t.Error("a track of unknown length was scrobbled")
	}
}

func TestListenQualifiesOnceTheLengthIsKnown(t *testing.T) {
	p := newPlayer(0, song, true)
	p.play(2 * time.Minute)
	p.total = 3 * time.Minute
	p.play(time.Second)
	if count(p.events, Qualified) != 1 {
		t.Errorf("events = %v", p.events)
	}
}

func TestListenPausedTimeDoesNotCount(t *testing.T) {
	p := newPlayer(3*time.Minute, song, true)
	p.play(60 * time.Second)
	p.wait(30 * time.Minute) // paused
	p.play(20 * time.Second)
	if count(p.events, Qualified) != 0 {
		t.Fatalf("scrobbled with 80 s listened: %v (%s)", p.events, p.l.Listened())
	}
	p.play(11 * time.Second)
	if count(p.events, Qualified) != 1 {
		t.Errorf("events = %v (%s listened)", p.events, p.l.Listened())
	}
}

func TestListenPauseWithoutBreakStillDoesNotCount(t *testing.T) {
	// A gap in the samples is a pause even when nobody said so.
	p := newPlayer(3*time.Minute, song, true)
	p.play(60 * time.Second)
	p.now = p.now.Add(10 * time.Minute)
	p.play(29 * time.Second)
	if count(p.events, Qualified) != 0 || p.l.Listened() > 90*time.Second {
		t.Errorf("listened %s, events %v", p.l.Listened(), p.events)
	}
}

func TestListenBufferingDoesNotCount(t *testing.T) {
	p := newPlayer(3*time.Minute, song, true)
	p.play(60 * time.Second)
	p.stall(10 * time.Minute) // the clock runs, the position does not
	p.play(20 * time.Second)
	if count(p.events, Qualified) != 0 {
		t.Errorf("listened %s: scrobbled through buffering", p.l.Listened())
	}
}

func TestListenSeekForwardDoesNotCount(t *testing.T) {
	p := newPlayer(3*time.Minute, song, true)
	p.play(10 * time.Second)
	p.seek(150 * time.Second)
	p.play(5 * time.Second)
	if count(p.events, Qualified) != 0 {
		t.Fatalf("a seek past the half way point scrobbled: listened %s", p.l.Listened())
	}
	// A jump the app did not mark is caught by the position alone.
	before := p.l.Listened()
	p.position += 20 * time.Second
	p.play(time.Second)
	if got := p.l.Listened() - before; got > 1500*time.Millisecond {
		t.Errorf("an unmarked jump counted: %s credited", got)
	}
}

func TestListenSeekBackKeepsWhatWasHeard(t *testing.T) {
	p := newPlayer(3*time.Minute, song, true)
	p.play(80 * time.Second)
	p.seek(0)
	p.play(15 * time.Second)
	if count(p.events, Qualified) != 1 {
		t.Errorf("listening on after a seek back: events %v, listened %s", p.events, p.l.Listened())
	}
}

func TestListenWithoutClearMetadataIsSilent(t *testing.T) {
	for _, track := range []Track{{Title: "T"}, {Artist: "A"}, {Artist: "  ", Title: "T"}} {
		p := newPlayer(3*time.Minute, track, true)
		p.play(3 * time.Minute)
		if len(p.events) != 0 {
			t.Errorf("%+v: events %v, want none", track, p.events)
		}
	}
	var l *Listen
	if l.Observe(t0, 0, time.Minute) != Nothing {
		t.Error("no listen, no events")
	}
}
