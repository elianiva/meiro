package main

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/systemmedia"
)

type countingSession struct{ updates atomic.Int32 }

func (s *countingSession) Update(systemmedia.State) { s.updates.Add(1) }
func (s *countingSession) Close() error             { return nil }

// Building the view describes state: with a track current, the side panel on
// the lyrics tab and a thumbnail on screen, repeated frames start no work.
func TestRebuildingTheViewStartsNoSideEffects(t *testing.T) {
	a := newTestApp()
	session := &countingSession{}
	a.systemMedia = session
	var jobs int
	a.run = func(work func()) { jobs++ }
	a.current.VideoID, a.current.Title = "vid", "Song"
	a.queue = append(a.queue, a.current)
	tt := ui.NewTester(a.view, 1000, 700)
	a.npOpen, a.npTab = true, 1
	baseline := jobs // the home feed
	for range 5 {
		tt.Frame()
	}
	if jobs != baseline {
		t.Errorf("rebuilds started %d background jobs", jobs-baseline)
	}
	if got := session.updates.Load(); got != 0 {
		t.Errorf("rebuilds published %d system media updates", got)
	}
	if a.lyrics.videoID != "" {
		t.Errorf("rebuilds loaded lyrics for %q", a.lyrics.videoID)
	}
}

// Choosing the lyrics or related tab loads once per track, however often the
// panel is asked and rebuilt.
func TestPanelLoadsOncePerTrack(t *testing.T) {
	a := newTestApp()
	var jobs int
	a.run = func(work func()) { jobs++ }
	a.current.VideoID = "vid"
	tt := ui.NewTester(a.view, 1000, 700)
	a.npOpen, a.npTab = true, 1
	base := jobs
	for range 3 {
		a.loadPanel()
		tt.Frame()
	}
	if jobs != base+1 {
		t.Errorf("lyrics: %d jobs, want 1", jobs-base)
	}
	a.npTab = 2
	for range 3 {
		a.loadPanel()
		tt.Frame()
	}
	if jobs != base+2 {
		t.Errorf("related: %d jobs in total, want 2", jobs-base)
	}
	a.npOpen = false
	a.current.VideoID = "other"
	a.loadPanel()
	if jobs != base+2 {
		t.Error("a closed panel loaded")
	}
}

// A location is acted on once, however many frames show it.
func TestLocationIsLoadedOnce(t *testing.T) {
	a := newTestApp()
	var jobs int
	a.run = func(work func()) { jobs++ }
	tt := ui.NewTester(a.view, 1000, 700)
	base := jobs
	a.router.Push("/explore")
	for range 4 {
		tt.Frame()
	}
	if jobs != base+1 {
		t.Errorf("/explore started %d loads, want 1", jobs-base)
	}
}

// The ticker owns the periodic work and ends when told to.
func TestTickerStopsAndPollIsIdleWithoutAStream(t *testing.T) {
	a := newTestApp()
	session := &countingSession{}
	a.systemMedia = session
	a.poll()
	a.poll()
	if session.updates.Load() != 0 {
		t.Error("polling without a stream published state")
	}
	stop := make(chan struct{})
	a.startTicker(stop)
	a.stopTicking()
	a.stopTicking() // closing twice is harmless
	close(stop)
	time.Sleep(2 * pollInterval)
	if session.updates.Load() != 0 {
		t.Error("the ticker published state without a stream")
	}
}
