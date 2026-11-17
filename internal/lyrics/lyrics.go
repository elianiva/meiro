package lyrics

import (
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
