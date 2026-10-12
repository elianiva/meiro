package main

import (
	"slices"
	"testing"

	"github.com/elianiva/meiro/youtube"
)

// The queue's invariants hold through what playback does to it: the place
// stays on the current track, and the boundary of the generated tracks stays
// on the first of them.
func TestQueueKeepsItsPlaceThroughChanges(t *testing.T) {
	q := newQueue()
	chosen := tracks("a", "b", "c")
	q.replace(chosen[1], chosen, 1, "Album")
	if cur := q.current(); cur.VideoID != "b" || q.recStart != -1 {
		t.Fatalf("replace: current %q, recStart %d", cur.VideoID, q.recStart)
	}

	q.appendGenerated(tracks("c", "r1", "r2")) // c is already queued
	if got := ids(q.items); !slices.Equal(got, []string{"a", "b", "c", "r1", "r2"}) || q.recStart != 3 {
		t.Fatalf("generated: %v, recStart %d", got, q.recStart)
	}

	// A chosen track goes ahead of the generated ones; play-next goes next.
	q.insert(youtube.MusicItem{VideoID: "x"}, false, true)
	q.insert(youtube.MusicItem{VideoID: "y"}, true, true)
	if got := ids(q.items); !slices.Equal(got, []string{"a", "b", "y", "c", "x", "r1", "r2"}) || q.recStart != 5 {
		t.Fatalf("insert: %v, recStart %d", got, q.recStart)
	}
	if next, ok := q.next(true, repeatOff); !ok || q.at(next).VideoID != "y" {
		t.Fatalf("next = %d, %v", next, ok)
	}

	// With auto-play off the queue ends before the generated tracks.
	q.moveTo(4) // x
	if _, ok := q.next(false, repeatOff); ok {
		t.Error("auto-play off played into the generated tracks")
	}
	if next, ok := q.next(false, repeatQueue); !ok || next != 0 {
		t.Errorf("repeating the queue went to %d, %v", next, ok)
	}

	// Dragging the current track keeps it current, and the divider row is
	// not a track.
	before := q.rows()
	if !q.moveRows([]int{q.rowOf(4)}, 0) {
		t.Fatal("the drag moved nothing")
	}
	if cur := q.current(); cur.VideoID != "x" || q.index != 0 || q.rows() != before {
		t.Errorf("after the drag: current %q at %d", cur.VideoID, q.index)
	}
	if !q.isDivider(q.recStart) || q.moveRows([]int{q.recStart}, 0) {
		t.Error("the divider row is draggable")
	}
	if first := q.at(q.recStart).VideoID; first != "r1" {
		t.Errorf("the boundary is on %q, want r1", first)
	}
}

// A queue that would hold more generated tracks than the limit drops played
// tracks first, and never more than the limit.
func TestQueueBoundsGeneratedTracks(t *testing.T) {
	q := newQueue()
	q.replace(tracks("a")[0], tracks("a"), 0, "")
	many := make([]string, autoplayQueueLimit*2)
	for i := range many {
		many[i] = "g" + string(rune('A'+i/26)) + string(rune('a'+i%26))
	}
	q.appendGenerated(tracks(many[:autoplayQueueLimit-1]...))
	q.moveTo(q.len() - 1)
	q.appendGenerated(tracks(many[autoplayQueueLimit:]...))
	if len(q.generated) > autoplayQueueLimit {
		t.Errorf("%d generated tracks, limit %d", len(q.generated), autoplayQueueLimit)
	}
	if cur := q.current(); cur.VideoID != many[autoplayQueueLimit-2] {
		t.Errorf("the current track changed to %q", cur.VideoID)
	}
}

func TestReplaceWithATrackOutsideTheTracksPlaysItAlone(t *testing.T) {
	q := newQueue()
	q.replace(youtube.MusicItem{VideoID: "solo"}, tracks("a", "b"), 0, "")
	if got := ids(q.items); !slices.Equal(got, []string{"solo"}) || q.index != 0 {
		t.Errorf("queue %v at %d", got, q.index)
	}
}
