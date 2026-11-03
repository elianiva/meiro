// Package romanize spells a lyric line in Latin when it is Japanese or
// Korean, so the line can be shown with a reading under it. A line that is
// already Latin, or that has no Japanese or Korean in it, has no reading.
package romanize

import (
	"strings"
	"sync"
	"unicode"
)

// Reading is the Latin reading of line. It is empty when line has nothing
// to add under it: no Japanese or Korean, or a reading that only repeats the
// line.
func Reading(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || line == "♪" {
		return ""
	}
	readingMu.Lock()
	defer readingMu.Unlock()
	if got, ok := readings[line]; ok {
		return got
	}
	got := reading(line)
	readings[line] = got
	return got
}

var (
	readingMu sync.Mutex
	readings  = map[string]string{}
)

func reading(line string) string {
	var b strings.Builder
	for _, run := range scriptRuns(line) {
		switch run.kind {
		case scriptKorean:
			b.WriteString(koreanReading(run.text))
		case scriptJapanese:
			b.WriteString(japaneseReading(run.text))
		case scriptOther:
			b.WriteString(run.text)
		}
	}
	got := strings.Join(strings.Fields(b.String()), " ")
	if got == "" || sameReading(got, line) {
		return ""
	}
	return got
}

func sameReading(reading, line string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(reading), ""), strings.Join(strings.Fields(line), ""))
}

type scriptKind int

const (
	scriptOther scriptKind = iota
	scriptKorean
	scriptJapanese
)

type scriptRun struct {
	kind scriptKind
	text string
}

// scriptRuns keeps a line in the order it is written, grouping a stretch of
// one script together so a Korean word and a Japanese word in the same line
// are each read in their own way.
func scriptRuns(line string) []scriptRun {
	var runs []scriptRun
	var b strings.Builder
	kind := scriptOther
	flush := func() {
		if b.Len() == 0 {
			return
		}
		runs = append(runs, scriptRun{kind: kind, text: b.String()})
		b.Reset()
	}
	for _, r := range line {
		next := scriptOf(r)
		// A space inside a Japanese or Korean stretch stays there, so
		// "안녕 하세요" is read as one phrase rather than two.
		if next == scriptOther && isJoiner(r) && kind != scriptOther {
			b.WriteRune(r)
			continue
		}
		if next != kind {
			flush()
			kind = next
		}
		b.WriteRune(r)
	}
	flush()
	return runs
}

func isJoiner(r rune) bool {
	return r == ' ' || r == '\t' || r == '　' || unicode.Is(unicode.Pd, r)
}

func scriptOf(r rune) scriptKind {
	if isHangul(r) {
		return scriptKorean
	}
	if isKana(r) || isKanji(r) {
		return scriptJapanese
	}
	return scriptOther
}

func isHangul(r rune) bool {
	return (r >= 0xAC00 && r <= 0xD7A3) || (r >= 0x1100 && r <= 0x11FF) || (r >= 0x3130 && r <= 0x318F)
}

func isKana(r rune) bool {
	return (r >= 0x3040 && r <= 0x30FF) || (r >= 0x31F0 && r <= 0x31FF)
}

func isKanji(r rune) bool {
	return unicode.Is(unicode.Han, r)
}

func hasJapanese(text string) bool {
	for _, r := range text {
		if isKana(r) || isKanji(r) {
			return true
		}
	}
	return false
}
