// Package romaji turns Japanese lyrics into Latin letters the way pear-desktop
// does for its Japanese lyrics: a Kuromoji-style tokenizer reads the kanji, and
// the readings become Hepburn romanization. The tokenizer is Kagome, a Go port
// of Kuromoji, with the IPADIC dictionary Kuromoji uses, so Meiro needs no
// JavaScript or dictionary download at runtime.
package romaji

import (
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

// Romanizer romanizes Japanese lines. It holds the dictionary, which is large,
// so a caller builds one and keeps it.
type Romanizer struct {
	tok *tokenizer.Tokenizer
	// mu serializes tokenization: the tokenizer is read-only, but its lattice
	// is not documented to be safe for concurrent use.
	mu sync.Mutex
}

// New reads the IPADIC dictionary embedded in the app and returns a Romanizer.
// It takes a moment and some memory, so call it off the main thread and keep
// the result.
func New() (*Romanizer, error) {
	tok, err := tokenizer.New(ipa.Dict())
	if err != nil {
		return nil, err
	}
	return &Romanizer{tok: tok}, nil
}

var (
	sharedOnce sync.Once
	shared     *Romanizer
	sharedErr  error
)

// Lines romanizes the lines of a song and returns each line's Latin spelling.
// A line that is not Japanese maps to itself, so a caller can look every line
// up and show only the ones that changed.
//
// A song counts as Japanese when any line has kana. Lyrics with none are left
// alone rather than guessed at, so Chinese lyrics are not read as Japanese.
func Lines(lines []string) (map[string]string, error) {
	sharedOnce.Do(func() { shared, sharedErr = New() })
	if sharedErr != nil {
		return nil, sharedErr
	}
	return shared.Lines(lines), nil
}

// Lines romanizes the lines of one song. See the package-level Lines.
func (r *Romanizer) Lines(lines []string) map[string]string {
	japanese := false
	for _, line := range lines {
		if hasKana(line) {
			japanese = true
			break
		}
	}
	out := make(map[string]string, len(lines))
	for _, line := range lines {
		if _, done := out[line]; done {
			continue
		}
		switch {
		case line == "":
			out[line] = ""
		case japanese && hasJapanese(line):
			out[line] = r.Romanize(line)
		default:
			out[line] = line
		}
	}
	return out
}

// Romanize returns one line in Hepburn romanization, with the words spaced.
// A line without Japanese is returned as it is.
func (r *Romanizer) Romanize(line string) string {
	if line == "" || !hasJapanese(line) {
		return line
	}
	r.mu.Lock()
	words := r.words(line)
	r.mu.Unlock()
	parts := make([]string, len(words))
	for i, word := range words {
		kana := word.surface
		if hasJapanese(word.surface) {
			kana = word.reading
			if word.hasPronunciation && word.pronunciation != "" {
				kana = word.pronunciation
			}
		}
		parts[i] = toRomaji(kana)
	}
	return canonicalize(strings.Join(parts, " "))
}

// word is a token as the romanization needs it. Kuromoji and Kagome both
// report a reading for a kanji and a pronunciation for how it sounds, which
// turns a written は into wa and a long vowel into ー.
type word struct {
	surface          string
	reading          string
	pronunciation    string
	hasPronunciation bool
	pos              string
}

// words tokenizes a line and patches the tokens as Kuroshiro does before it
// romanizes them: a trailing っ of a verb or adjective joins what follows, so
// 行った doubles the t, and a う that follows a verb joins it, so 思う ends in
// ou rather than a separate u.
func (r *Romanizer) words(line string) []word {
	tokens := r.tok.Tokenize(line)
	words := make([]word, 0, len(tokens))
	for _, t := range tokens {
		if t.Class == tokenizer.DUMMY || t.Surface == "" {
			continue
		}
		reading, _ := t.Reading()
		pronunciation, hasPronunciation := t.Pronunciation()
		w := word{
			surface:          t.Surface,
			reading:          reading,
			pronunciation:    pronunciation,
			hasPronunciation: hasPronunciation,
		}
		if pos := t.POS(); len(pos) > 0 {
			w.pos = pos[0]
		}
		switch {
		case !hasJapanese(w.surface):
			w.reading = w.surface
		case w.reading == "" && allKana(w.surface):
			w.reading = toKatakana(w.surface)
		case w.reading == "":
			w.reading = w.surface
		case hasHiragana(w.reading):
			w.reading = toKatakana(w.reading)
		}
		words = append(words, w)
	}
	for i := 0; i < len(words); i++ {
		if words[i].surface != "う" && words[i].surface != "ウ" {
			continue
		}
		if words[i].pos != "助動詞" || i == 0 || words[i-1].pos != "動詞" {
			continue
		}
		previous := &words[i-1]
		previous.surface += "う"
		if previous.hasPronunciation {
			previous.pronunciation += "ー"
		} else {
			previous.pronunciation, previous.hasPronunciation = previous.reading+"ー", true
		}
		previous.reading += "ウ"
		words = append(words[:i], words[i+1:]...)
		i--
	}
	for i := 0; i < len(words); i++ {
		w := &words[i]
		if w.pos != "動詞" && w.pos != "形容詞" {
			continue
		}
		if utf8.RuneCountInString(w.surface) < 2 || !strings.HasSuffix(w.surface, "っ") && !strings.HasSuffix(w.surface, "ッ") {
			continue
		}
		if i+1 >= len(words) {
			continue
		}
		next := words[i+1]
		w.surface += next.surface
		if w.hasPronunciation {
			w.pronunciation += next.pronunciation
		} else {
			w.pronunciation, w.hasPronunciation = w.reading+next.reading, true
		}
		w.reading += next.reading
		words = append(words[:i+1], words[i+2:]...)
		i--
	}
	return words
}
