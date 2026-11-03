package romanize

// Korean readings follow the Revised Romanization of Korean: a line is
// rewritten as it is pronounced, then each syllable is spelled in Latin.
// The pronunciation changes are the National Institute of Korean Language's
// standard rules for liaison, assimilation, palatalization and aspiration.
// Tensification is left unwritten, which is what the romanization standard
// asks for. A handful of fixed exceptions (잎 compounds, 전역) are listed
// rather than guessed.

import "strings"

var (
	choseong  = []string{"ㄱ", "ㄲ", "ㄴ", "ㄷ", "ㄸ", "ㄹ", "ㅁ", "ㅂ", "ㅃ", "ㅅ", "ㅆ", "ㅇ", "ㅈ", "ㅉ", "ㅊ", "ㅋ", "ㅌ", "ㅍ", "ㅎ"}
	jungseong = []string{"ㅏ", "ㅐ", "ㅑ", "ㅒ", "ㅓ", "ㅔ", "ㅕ", "ㅖ", "ㅗ", "ㅘ", "ㅙ", "ㅚ", "ㅛ", "ㅜ", "ㅝ", "ㅞ", "ㅟ", "ㅠ", "ㅡ", "ㅢ", "ㅣ"}
	// jongseong spells a batchim as one or two jamo. An empty string is no batchim.
	jongseong = []string{"", "ㄱ", "ㄲ", "ㄱㅅ", "ㄴ", "ㄴㅈ", "ㄴㅎ", "ㄷ", "ㄹ", "ㄹㄱ", "ㄹㅁ", "ㄹㅂ", "ㄹㅅ", "ㄹㅌ", "ㄹㅍ", "ㄹㅎ", "ㅁ", "ㅂ", "ㅂㅅ", "ㅅ", "ㅆ", "ㅇ", "ㅈ", "ㅊ", "ㅋ", "ㅌ", "ㅍ", "ㅎ"}
)

var (
	choIndex  = indexOf(choseong)
	jungIndex = indexOf(jungseong)
	jongIndex = indexOf(jongseong)
)

func indexOf(list []string) map[string]int {
	out := make(map[string]int, len(list))
	for i, s := range list {
		out[s] = i
	}
	return out
}

// Letters of the alphabet said as words, and the reading of that name.
var letterNames = map[string]string{
	"기역": "기역", "니은": "니은", "리을": "리을", "미음": "미음", "비읍": "비읍", "시옷": "시옷", "이응": "이응",
	"디귱": "디귱", "지읋": "지읋", "치읏": "치읏", "키읍": "키읍", "티읏": "티읏", "피읍": "피읍", "히읏": "히읏",
}

// specialLetter is how ㄷ, ㅈ, ㅊ, ㅋ, ㅌ, ㅍ, ㅎ are said when they are the
// batchim of their own letter-name.
var specialLetter = map[string]string{"ㄷ": "ㅅ", "ㅈ": "ㅅ", "ㅊ": "ㅅ", "ㅌ": "ㅅ", "ㅎ": "ㅅ", "ㅋ": "ㄱ", "ㅍ": "ㅂ"}

var palatalize = map[string]string{"ㄷ": "ㅈ", "ㅌ": "ㅊ", "ㄹㅌ": "ㅊ"}

var (
	insertVowels    = setOf("ㅏ", "ㅐ", "ㅓ", "ㅔ", "ㅗ", "ㅜ", "ㅟ")
	insertNext      = setOf("ㅑ", "ㅕ", "ㅛ", "ㅠ", "ㅣ", "ㅒ", "ㅖ")
	insertNieun     = setOf("ㄱ", "ㄴ", "ㄷ", "ㅁ", "ㅂ", "ㅇ")
	nasalIeung      = setOf("ㄱ", "ㄲ", "ㅋ", "ㄱㅅ", "ㄹㄱ")
	nasalNieun      = setOf("ㄷ", "ㅅ", "ㅆ", "ㅈ", "ㅊ", "ㅌ", "ㅎ")
	nasalMieum      = setOf("ㅂ", "ㅍ", "ㄹㅂ", "ㄹㅍ", "ㅂㅅ")
	rieulToNieun    = setOf("ㅁ", "ㅇ", "ㄱ", "ㅂ")
	hieutBatchim    = setOf("ㅎ", "ㄴㅎ", "ㄹㅎ")
	aspirateNext    = map[string]string{"ㄱ": "ㅋ", "ㄷ": "ㅌ", "ㅈ": "ㅊ", "ㅅ": "ㅆ"}
	aspirateFrom    = map[string]string{"ㄱ": "ㅋ", "ㄹㄱ": "ㅋ", "ㄷ": "ㅌ", "ㅂ": "ㅍ", "ㄹㅂ": "ㅍ", "ㅈ": "ㅊ", "ㄴㅈ": "ㅊ"}
	aspirateFromSet = setOf("ㄱ", "ㄹㄱ", "ㄷ", "ㅂ", "ㄹㅂ", "ㅈ", "ㄴㅈ")
	digeutBeforeH   = setOf("ㅅ", "ㅆ", "ㅊ", "ㅌ")
	representative  = map[string]string{
		"ㄲ": "ㄱ", "ㅋ": "ㄱ", "ㄱㅅ": "ㄱ", "ㄹㄱ": "ㄱ",
		"ㅅ": "ㄷ", "ㅆ": "ㄷ", "ㅈ": "ㄷ", "ㅊ": "ㄷ", "ㅌ": "ㄷ",
		"ㅍ": "ㅂ", "ㅂㅅ": "ㅂ", "ㄹㅍ": "ㅂ",
		"ㄴㅈ": "ㄴ", "ㄹㅂ": "ㄹ", "ㄹㅅ": "ㄹ", "ㄹㅌ": "ㄹ", "ㄹㅁ": "ㅁ",
	}
	clusterSimplify = map[string]string{
		"ㄱㅅ": "ㄱ", "ㄴㅈ": "ㄴ", "ㄴㅎ": "ㄴ", "ㄹㄱ": "ㄱ", "ㄹㅁ": "ㅁ",
		"ㄹㅂ": "ㄹ", "ㄹㅅ": "ㄹ", "ㄹㅌ": "ㄹ", "ㄹㅍ": "ㄹ", "ㄹㅎ": "ㄹ", "ㅂㅅ": "ㅂ",
	}
)

// Words whose pronunciation the rules above do not reach.
var fixedReading = map[string]string{
	"베갯잇":  "베갴닏",
	"깍잎":   "깨닙",
	"나뭇잎":  "나문닙",
	"도리깍열": "도리깨녘",
	"뒷윳":   "들닻",
	"전역":   "저녁",
}

func setOf(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

type syllable struct {
	cho, jung, jong string
}

// koreanReading romanizes Hangul and leaves every other character as it is.
func koreanReading(text string) string {
	if text == "" {
		return ""
	}
	parts := strings.Split(text, " ")
	for i, part := range parts {
		parts[i] = romanizePhrase(part)
	}
	return strings.Join(parts, " ")
}

func romanizePhrase(phrase string) string {
	pronounced := standardize(phrase)
	var b strings.Builder
	runes := []rune(pronounced)
	for i, r := range runes {
		b.WriteString(romanizeRune(runes, i, r))
	}
	return b.String()
}

func romanizeRune(runes []rune, i int, r rune) string {
	if s, ok := decompose(r); ok {
		cho := choseongLatin[s.cho]
		if s.cho == "ㄹ" && i > 0 {
			if prev, ok := decompose(runes[i-1]); ok && prev.jong == "ㄹ" {
				cho = "l"
			}
		}
		return cho + jungseongLatin[s.jung] + jongseongLatin[s.jong]
	}
	if latin, ok := jungseongLatin[string(r)]; ok {
		return latin
	}
	if latin, ok := choseongLatin[string(r)]; ok {
		return latin
	}
	return string(r)
}

var choseongLatin = map[string]string{
	"ㄱ": "g", "ㄲ": "kk", "ㅋ": "k",
	"ㄷ": "d", "ㄸ": "tt", "ㅌ": "t",
	"ㅂ": "b", "ㅃ": "pp", "ㅍ": "p",
	"ㅈ": "j", "ㅉ": "jj", "ㅊ": "ch",
	"ㅅ": "s", "ㅆ": "ss", "ㅎ": "h",
	"ㄴ": "n", "ㅁ": "m", "ㅇ": "",
	"ㄹ": "r",
}

var jungseongLatin = map[string]string{
	"ㅏ": "a", "ㅓ": "eo", "ㅗ": "o", "ㅜ": "u", "ㅡ": "eu", "ㅣ": "i",
	"ㅐ": "ae", "ㅔ": "e", "ㅚ": "oe", "ㅟ": "wi",
	"ㅑ": "ya", "ㅕ": "yeo", "ㅛ": "yo", "ㅠ": "yu", "ㅒ": "yae", "ㅖ": "ye",
	"ㅘ": "wa", "ㅙ": "wae", "ㅝ": "wo", "ㅞ": "we", "ㅢ": "ui",
}

var jongseongLatin = map[string]string{
	"": "", "ㄱ": "k", "ㄴ": "n", "ㄷ": "t", "ㄹ": "l", "ㅁ": "m", "ㅂ": "p", "ㅇ": "ng",
}

func standardize(phrase string) string {
	if phrase == "" {
		return ""
	}
	if fixed, ok := fixedReading[phrase]; ok {
		return fixed
	}
	var kept []keptRune
	var syllables []syllable
	for i, r := range []rune(phrase) {
		if s, ok := decompose(r); ok {
			syllables = append(syllables, s)
			continue
		}
		kept = append(kept, keptRune{i, string(r)})
	}
	for i := range syllables {
		var next *syllable
		if i+1 < len(syllables) {
			next = &syllables[i+1]
		}
		cur := syllables[i]
		cur, next = applyRules(cur, next, phrase, i)
		syllables[i] = cur
		if next != nil {
			syllables[i+1] = *next
		}
	}
	out := make([]string, 0, len(syllables)+len(kept))
	for _, s := range syllables {
		out = append(out, compose(s))
	}
	for _, k := range kept {
		out = insertAt(out, k.index, k.text)
	}
	return strings.Join(out, "")
}

type keptRune struct {
	index int
	text  string
}

func insertAt(list []string, index int, text string) []string {
	if index >= len(list) {
		return append(list, text)
	}
	list = append(list, "")
	copy(list[index+1:], list[index:])
	list[index] = text
	return list
}

func applyRules(current syllable, next *syllable, phrase string, index int) (syllable, *syllable) {
	if next != nil {
		current, *next = rule16(current, *next, phrase, index)
		current, *next = rule17(current, *next)
		*next = rule19(current, *next)
		current, *next = nieunRieulInsertion(current, *next)
		current = rule18(current, *next)
		current, *next = rule20(current, *next)
	}
	current, next = rule12(current, next)
	if next != nil {
		current, *next = rule13(current, *next)
	}
	current = rule9(current, next)
	return current, next
}

func rule16(current, next syllable, phrase string, index int) (syllable, syllable) {
	if current.jong == "" || next.cho != "ㅇ" {
		return current, next
	}
	runes := []rune(phrase)
	combined := ""
	if index > 0 && index < len(runes) {
		combined = string(runes[index-1]) + string(runes[index])
	}
	if _, ok := letterNames[combined]; !ok {
		return current, next
	}
	if reading, ok := specialLetter[current.jong]; ok {
		next.cho = reading
		current.jong = ""
		return current, next
	}
	if current.jong != "" {
		next.cho = current.jong
		if current.jong != "ㅇ" {
			current.jong = ""
		}
	}
	return current, next
}

func rule17(current, next syllable) (syllable, syllable) {
	if next.jung != "ㅣ" {
		return current, next
	}
	if next.cho == "ㅇ" {
		if to, ok := palatalize[current.jong]; ok {
			next.cho = to
			if current.jong == "ㄹㅌ" {
				current.jong = "ㄹ"
			} else {
				current.jong = ""
			}
		}
	}
	if next.cho == "ㅎ" && current.jong == "ㄷ" {
		next.cho = "ㅊ"
		current.jong = ""
	}
	return current, next
}

func rule19(current, next syllable) syllable {
	if rieulToNieun[current.jong] && next.cho == "ㄹ" {
		next.cho = "ㄴ"
	}
	return next
}

func nieunRieulInsertion(current, next syllable) (syllable, syllable) {
	if current.jong == "" || next.cho != "ㅇ" || !insertNext[next.jung] {
		return current, next
	}
	// 이 with nothing after it takes the batchim by liaison instead of
	// growing an extra ㄴ.
	if next.cho == "ㅇ" && next.jung == "ㅣ" && next.jong == "" && clusterSimplify[current.jong] == "" && !isCluster(current.jong) {
		return current, next
	}
	if insertVowels[current.jung] {
		if insertNieun[current.jong] {
			if current.jong == "ㄱ" {
				current.jong = "ㅇ"
			}
			next.cho = "ㄴ"
		}
		if current.jong == "ㄹ" {
			next.cho = "ㄹ"
		}
		return current, next
	}
	if simplified, ok := clusterSimplify[current.jong]; ok {
		current.jong = simplified
		return current, next
	}
	next.cho = current.jong
	return current, next
}

func isCluster(jong string) bool {
	_, ok := clusterSimplify[jong]
	return ok
}

func rule18(current, next syllable) syllable {
	if current.jong == "" || (next.cho != "ㄴ" && next.cho != "ㅁ") {
		return current
	}
	switch {
	case nasalIeung[current.jong]:
		current.jong = "ㅇ"
	case nasalNieun[current.jong]:
		current.jong = "ㄴ"
	case nasalMieum[current.jong]:
		current.jong = "ㅁ"
	}
	return current
}

func rule20(current, next syllable) (syllable, syllable) {
	if current.jong == "ㄴ" && next.cho == "ㄹ" {
		current.jong = "ㄹ"
	}
	if next.cho == "ㄴ" && (current.jong == "ㄹ" || current.jong == "ㄹㅎ" || current.jong == "ㄹㅌ") {
		next.cho = "ㄹ"
	}
	return current, next
}

func rule12(current syllable, next *syllable) (syllable, *syllable) {
	if current.jong != "" && hieutBatchim[current.jong] {
		if next != nil {
			if to, ok := aspirateNext[next.cho]; ok {
				next.cho = to
				current.jong = strings.ReplaceAll(current.jong, "ㅎ", "")
			}
			if next.cho == "ㄴ" && (current.jong == "ㄴㅎ" || current.jong == "ㄹㅎ") {
				current.jong = strings.ReplaceAll(current.jong, "ㅎ", "")
			}
			if next.cho == "ㅇ" {
				if current.jong == "ㄴㅎ" || current.jong == "ㄹㅎ" {
					current.jong = strings.ReplaceAll(current.jong, "ㅎ", "")
				} else {
					current.jong = ""
				}
			} else {
				current.jong = strings.ReplaceAll(current.jong, "ㅎ", "")
			}
		} else {
			current.jong = strings.ReplaceAll(current.jong, "ㅎ", "")
		}
	}
	if next != nil && next.cho == "ㅎ" {
		if to, ok := aspirateFrom[current.jong]; ok && aspirateFromSet[current.jong] {
			next.cho = to
			if len([]rune(current.jong)) == 1 {
				current.jong = ""
			} else {
				current.jong = string([]rune(current.jong)[0])
			}
		} else if digeutBeforeH[current.jong] {
			next.cho = "ㅌ"
			current.jong = ""
		}
	}
	return current, next
}

func rule13(current, next syllable) (syllable, syllable) {
	if current.jong == "" || next.cho != "ㅇ" {
		return current, next
	}
	runes := []rune(current.jong)
	switch {
	case current.jong != "ㅇ" && len(runes) == 1:
		next.cho = current.jong
		current.jong = ""
	case len(runes) == 2 && runes[0] != runes[1]:
		second := string(runes[1])
		if second == "ㅅ" {
			next.cho = "ㅆ"
		} else {
			next.cho = second
		}
		current.jong = string(runes[0])
	}
	return current, next
}

func rule9(current syllable, next *syllable) syllable {
	if current.jong == "" {
		return current
	}
	atEnd := next == nil
	beforeConsonant := next != nil && next.cho != "ㅇ"
	if atEnd || beforeConsonant {
		if to, ok := representative[current.jong]; ok {
			current.jong = to
		}
	}
	return current
}

func decompose(r rune) (syllable, bool) {
	if r < 0xAC00 || r > 0xD7A3 {
		return syllable{}, false
	}
	n := int(r - 0xAC00)
	cho := n / (21 * 28)
	jung := (n % (21 * 28)) / 28
	jong := n % 28
	return syllable{cho: choseong[cho], jung: jungseong[jung], jong: jongseong[jong]}, true
}

func compose(s syllable) string {
	cho, okC := choIndex[s.cho]
	jung, okJ := jungIndex[s.jung]
	jong, okN := jongIndex[s.jong]
	if !okC || !okJ || !okN {
		return ""
	}
	return string(rune(0xAC00 + (cho*21+jung)*28 + jong))
}
