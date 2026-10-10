package main

import (
	"context"
	"testing"
	"time"

	"github.com/elianiva/meiro/youtube"
)

func TestHoverResolvesOnlyWhatThePointerRestsOn(t *testing.T) {
	a := newTestApp()
	var resolved []string
	a.streams = newStreamCache(func(_ context.Context, id, _ string) (string, time.Duration, error) {
		resolved = append(resolved, id)
		return "https://media.invalid/" + id, 0, nil
	})
	var pending func()
	cancelled := 0
	a.schedule = func(_ time.Duration, work func()) func() {
		pending = work
		return func() { cancelled++; pending = nil }
	}
	song := func(id string) youtube.MusicItem { return youtube.MusicItem{VideoID: id} }

	// Sweeping over a row and leaving it starts nothing.
	a.hoverSong("row-a", song("a"), true)
	a.hoverSong("row-a", song("a"), false)
	if pending != nil || cancelled != 1 {
		t.Fatalf("leaving the row did not cancel its lookup (pending=%v, cancelled=%d)", pending != nil, cancelled)
	}
	// Another row leaving does not cancel the one the pointer is on.
	a.hoverSong("row-b", song("b"), true)
	a.hoverSong("row-a", song("a"), false)
	if pending == nil {
		t.Fatal("an unrelated row cancelled the hovered row's lookup")
	}
	fire := pending
	pending = nil
	fire()
	if len(resolved) != 1 || resolved[0] != "b" {
		t.Fatalf("resolved %v, want [b]", resolved)
	}
	// Staying on the row does not resolve it a second time.
	a.hoverSong("row-b", song("b"), true)
	if pending != nil {
		t.Error("a second lookup was scheduled for the same row")
	}
}

func TestHoverSkipsWhatIsPlayingOrBusy(t *testing.T) {
	a := newTestApp()
	calls := 0
	a.streams = newStreamCache(func(context.Context, string, string) (string, time.Duration, error) {
		calls++
		return "url", 0, nil
	})
	a.current = youtube.MusicItem{VideoID: "now"}
	a.hoverKey = "k"
	a.resolveHovered("k", "now")
	a.resolving = true
	a.resolveHovered("k", "other")
	a.resolving = false
	a.hoverBusy = true
	a.resolveHovered("k", "other")
	a.hoverBusy = false
	a.resolveHovered("stale", "other")
	if calls != 0 {
		t.Errorf("resolved %d times, want none", calls)
	}
	a.resolveHovered("k", "other")
	if calls != 1 {
		t.Errorf("resolved %d times, want 1", calls)
	}
	if a.hoverBusy {
		t.Error("hoverBusy was left set")
	}
}
