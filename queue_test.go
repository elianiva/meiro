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

func TestQueueRemoveKeepsItsPlace(t *testing.T) {
	q := newQueue()
	chosen := tracks("a", "b", "c", "d")
	q.replace(chosen[2], chosen, 2, "")
	q.appendGenerated(tracks("r1", "r2"))
	q.insert(youtube.MusicItem{VideoID: "n"}, true, true) // a b c n d r1 r2

	if q.remove(q.index) {
		t.Error("removed the track playing")
	}
	if !q.remove(0) || q.current().VideoID != "c" || q.recStart != 4 {
		t.Errorf("before the current: %v at %d, recStart %d", ids(q.items), q.index, q.recStart)
	}
	q.remove(q.find("n"))
	if q.playNext != "" {
		t.Errorf("play-next mark %q outlived its track", q.playNext)
	}
	q.remove(q.find("r1"))
	if _, ok := q.generated["r1"]; ok || q.recStart != 3 || q.at(q.recStart).VideoID != "r2" {
		t.Errorf("generated: %v, recStart %d", ids(q.items), q.recStart)
	}
	q.remove(q.find("r2"))
	if q.recStart != -1 || len(q.generated) != 0 {
		t.Errorf("no generated tracks left, recStart %d", q.recStart)
	}
	if got := ids(q.items); !slices.Equal(got, []string{"b", "c", "d"}) || q.current().VideoID != "c" {
		t.Errorf("queue %v", got)
	}
}

func TestQueueShiftAndClear(t *testing.T) {
	q := newQueue()
	chosen := tracks("a", "b", "c")
	q.replace(chosen[0], chosen, 0, "")
	q.appendGenerated(tracks("r1", "r2"))

	if !q.shift(1, 1) || !slices.Equal(ids(q.items), []string{"a", "c", "b", "r1", "r2"}) {
		t.Fatalf("down: %v", ids(q.items))
	}
	if q.shift(0, -1) || q.shift(4, 1) {
		t.Error("moved past an end")
	}
	// Down past the divider makes the track the first of the generated ones'
	// neighbours: the boundary stays on the generated tracks.
	q.shift(2, 1)
	if q.at(q.recStart).VideoID != "r1" || q.current().VideoID != "a" {
		t.Errorf("across the divider: %v, recStart %d", ids(q.items), q.recStart)
	}
	if !q.shift(q.recStart, -1) || q.at(q.recStart).VideoID != "r1" {
		t.Errorf("up across the divider: %v, recStart %d", ids(q.items), q.recStart)
	}

	if !q.clearUpcoming() || !slices.Equal(ids(q.items), []string{"a"}) || q.recStart != -1 || len(q.generated) != 0 {
		t.Errorf("clear: %v", ids(q.items))
	}
	if q.clearUpcoming() {
		t.Error("cleared an empty queue")
	}
}

func TestQueueDragKeepsPlayNextAndBoundary(t *testing.T) {
	q := newQueue()
	chosen := tracks("a", "b", "c", "d")
	q.replace(chosen[0], chosen, 0, "")
	q.appendGenerated(tracks("r1", "r2"))
	q.insert(youtube.MusicItem{VideoID: "n"}, true, true) // a n b c d r1 r2

	// Drag d, the last chosen track, to the top of the upcoming tracks.
	if !q.moveRows([]int{q.rowOf(4)}, q.rowOf(1)) || !slices.Equal(ids(q.items), []string{"a", "d", "n", "b", "c", "r1", "r2"}) {
		t.Fatalf("drag: %v", ids(q.items))
	}
	if q.playNext != "n" {
		t.Errorf("play-next mark %q, want n", q.playNext)
	}
	if next, ok := q.next(true, repeatOff); !ok || q.at(next).VideoID != "n" {
		t.Errorf("next = %d, %v; play-next must still play first", next, ok)
	}
	if q.at(q.recStart).VideoID != "r1" || q.index != 0 {
		t.Errorf("boundary on %q, place %d", q.at(q.recStart).VideoID, q.index)
	}

	// A generated track dragged among the chosen ones stays generated.
	if !q.moveRows([]int{q.rowOf(6)}, 1) || q.hasDivider() == false {
		t.Fatalf("drag generated: %v, recStart %d", ids(q.items), q.recStart)
	}
	if _, ok := q.generated["r2"]; !ok || q.at(q.recStart).VideoID != "r2" {
		t.Errorf("after: %v, recStart %d", ids(q.items), q.recStart)
	}
}
