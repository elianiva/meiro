package youtube

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestParseTimedLyrics(t *testing.T) {
	root := decodeResponse(json.RawMessage(`{"contents":{"elementRenderer":{"newElement":{"type":{"componentType":{"model":{"timedLyricsModel":{"lyricsData":{"timedLyricsData":[
		{"lyricLine":"Hello ","cueRange":{"startTimeMilliseconds":"9200","endTimeMilliseconds":"10630"}},
		{"lyricLine":"World","cueRange":{"startTimeMilliseconds":12540}},
		{"lyricLine":"no cue"}]}}}}}}}}}`))
	got := parseTimedLyrics(root)
	want := []TimedLine{{9200 * time.Millisecond, "Hello"}, {12540 * time.Millisecond, "World"}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	if parseTimedLyrics(decodeResponse(json.RawMessage(`{"a":[]}`))) != nil {
		t.Fatal("a page without timed lyrics gave lines")
	}
}
