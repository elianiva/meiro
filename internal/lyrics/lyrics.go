package lyrics

import (
	"context"
	"sort"
	"strings"
	"time"
)

type Word struct {
	At   time.Duration
	Text string
}

type Line struct {
	At    time.Duration
	Text  string
	Words []Word
}

// Query names the track a lyrics lookup is for.
type Query struct {
	Title, Artist, Album string
	Duration             time.Duration
}

// Result is what a lyrics source found. Synced is empty when the source has
// only plain text, and Source names the provider for the footer.
type Result struct {
	Synced []Line
	Plain  string
	Source string
}

// Source looks up the lyrics of one track.
type Source func(ctx context.Context, q Query) (Result, error)

func IndexAt(lines []Line, position time.Duration) int {
	return sort.Search(len(lines), func(i int) bool { return lines[i].At > position }) - 1
}

func IndexWordAt(words []Word, position time.Duration) int {
	return sort.Search(len(words), func(i int) bool { return words[i].At > position }) - 1
}

func Text(lines []Line) string {
	text := make([]string, len(lines))
	for i, line := range lines {
		text[i] = line.Text
	}
	return strings.TrimSpace(strings.Join(text, "\n"))
}
