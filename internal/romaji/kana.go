package romaji

import (
	"regexp"
	"strings"
)

// hepburn maps hiragana, one syllable or a combination at a time, to the
// Hepburn romanization Kuroshiro uses. The values and the long vowel marks
// below follow Kuroshiro's table, so a line reads in Meiro as it does in
// pear-desktop. Katakana is folded to hiragana before the lookup; a sokuon and
// a syllabic n need their neighbours, so toRomaji handles them apart.
var hepburn = map[string]string{
	"ぁ": "a", "あ": "a", "ぃ": "i", "い": "i", "ぅ": "u", "う": "u",
	"ぇ": "e", "え": "e", "ぉ": "o", "お": "o",
	"か": "ka", "き": "ki", "く": "ku", "け": "ke", "こ": "ko",
	"が": "ga", "ぎ": "gi", "ぐ": "gu", "げ": "ge", "ご": "go",
	"さ": "sa", "し": "shi", "す": "su", "せ": "se", "そ": "so",
	"ざ": "za", "じ": "ji", "ず": "zu", "ぜ": "ze", "ぞ": "zo",
	"た": "ta", "ち": "chi", "つ": "tsu", "て": "te", "と": "to",
	"だ": "da", "ぢ": "ji", "づ": "zu", "で": "de", "ど": "do",
	"な": "na", "に": "ni", "ぬ": "nu", "ね": "ne", "の": "no",
	"は": "ha", "ひ": "hi", "ふ": "fu", "へ": "he", "ほ": "ho",
	"ば": "ba", "び": "bi", "ぶ": "bu", "べ": "be", "ぼ": "bo",
	"ぱ": "pa", "ぴ": "pi", "ぷ": "pu", "ぺ": "pe", "ぽ": "po",
	"ま": "ma", "み": "mi", "む": "mu", "め": "me", "も": "mo",
	"や": "ya", "ゆ": "yu", "よ": "yo",
	"ら": "ra", "り": "ri", "る": "ru", "れ": "re", "ろ": "ro",
	"わ": "wa", "ゐ": "i", "ゑ": "e", "を": "o", "ん": "n",
	"ゔ": "vu", "ゕ": "ka", "ゖ": "ke",
	"ゃ": "ya", "ゅ": "yu", "ょ": "yo", "ゎ": "wa",
	"きゃ": "kya", "きゅ": "kyu", "きょ": "kyo", "きぇ": "kye",
	"ぎゃ": "gya", "ぎゅ": "gyu", "ぎょ": "gyo",
	"しゃ": "sha", "しゅ": "shu", "しょ": "sho", "しぇ": "she",
	"じゃ": "ja", "じゅ": "ju", "じょ": "jo", "じぇ": "je",
	"ちゃ": "cha", "ちゅ": "chu", "ちょ": "cho", "ちぇ": "che",
	"ぢゃ": "ja", "ぢゅ": "ju", "ぢょ": "jo",
	"にゃ": "nya", "にゅ": "nyu", "にょ": "nyo", "にぇ": "nye",
	"ひゃ": "hya", "ひゅ": "hyu", "ひょ": "hyo", "ひぇ": "hye",
	"びゃ": "bya", "びゅ": "byu", "びょ": "byo",
	"ぴゃ": "pya", "ぴゅ": "pyu", "ぴょ": "pyo",
	"みゃ": "mya", "みゅ": "myu", "みょ": "myo",
	"りゃ": "rya", "りゅ": "ryu", "りょ": "ryo",
	"ふぁ": "fa", "ふぃ": "fi", "ふぇ": "fe", "ふぉ": "fo", "ふゅ": "fyu", "ふょ": "fyo",
	"つぁ": "tsa", "つぃ": "tsi", "つぇ": "tse", "つぉ": "tso",
	"てぃ": "ti", "てゅ": "tyu", "でぃ": "di", "でゅ": "dyu",
	"とぅ": "tu", "どぅ": "du",
	"いぇ": "ye", "うぃ": "wi", "うぇ": "we", "うぉ": "wo",
	"くぁ": "kwa", "くぃ": "kwi", "くぇ": "kwe", "くぉ": "kwo", "くゎ": "kwa",
	"ぐぁ": "gwa", "ぐぃ": "gwi", "ぐぇ": "gwe", "ぐぉ": "gwo", "ぐゎ": "gwa",
	"ゔぁ": "va", "ゔぃ": "vi", "ゔぇ": "ve", "ゔぉ": "vo", "ゔゅ": "vyu", "ゔょ": "vyo",
	"・": "･",
}

// toRomaji returns a kana string in Hepburn romanization. It keeps the sokuon,
// the syllabic n and the long vowel mark for its own pass, because each
// depends on the sound after or before it.
func toRomaji(kana string) string {
	runes := []rune(kana)
	for i, r := range runes {
		if r >= 0x30a1 && r <= 0x30f6 {
			runes[i] = r - 0x60
		}
	}
	var out strings.Builder
	out.Grow(len(runes))
	for i := 0; i < len(runes); i++ {
		switch r := runes[i]; r {
		case 'っ':
			// A sokuon doubles the consonant that follows it, and stands for
			// tsu when nothing does.
			if i+1 < len(runes) {
				if next, _ := syllable(runes[i+1:]); next != "" && isConsonant(next[0]) {
					out.WriteByte(next[0])
					continue
				}
			}
			out.WriteString("tsu")
		case 'ん':
			out.WriteString("n")
			// Before a vowel or a y, the n is its own syllable and takes an
			// apostrophe, as in shin'ya.
			if i+1 < len(runes) && needsApostrophe(runes[i+1]) {
				out.WriteByte('\'')
			}
		default:
			if next, n := syllable(runes[i:]); n > 0 {
				out.WriteString(next)
				i += n - 1
			} else {
				out.WriteString(punctuation(r))
			}
		}
	}
	result := out.String()
	// Hepburn writes っち as tchi, where the doubled c would read hard.
	result = strings.ReplaceAll(result, "cc", "tc")
	// The syllabic n sounds as m before b, m and p.
	for _, pair := range [][2]string{{"nm", "mm"}, {"nb", "mb"}, {"np", "mp"}} {
		result = strings.ReplaceAll(result, pair[0], pair[1])
	}
	return strings.NewReplacer(
		"aー", "ā", "iー", "ī", "uー", "ū", "eー", "ē", "oー", "ō",
	).Replace(result)
}

// syllable returns the romanization of the kana at the start of runes and how
// many runes it used, preferring a combination such as きゃ over き alone.
func syllable(runes []rune) (string, int) {
	if len(runes) >= 2 {
		if s, ok := hepburn[string(runes[:2])]; ok {
			return s, 2
		}
	}
	if s, ok := hepburn[string(runes[:1])]; ok {
		return s, 1
	}
	return "", 0
}

func isConsonant(b byte) bool {
	return strings.ContainsRune("bcdfghjklmnpqrstvwxyz", rune(b))
}

func needsApostrophe(r rune) bool {
	switch r {
	case 'あ', 'い', 'う', 'え', 'お', 'や', 'ゆ', 'よ', 'ゃ', 'ゅ', 'ょ',
		'ぁ', 'ぃ', 'ぅ', 'ぇ', 'ぉ':
		return true
	}
	return false
}

// punctuation folds the full-width marks lyrics use into the Latin ones a
// romanization reads with, as Kuroshiro does before it joins the words.
func punctuation(r rune) string {
	switch r {
	case '、':
		return ","
	case '。':
		return "."
	case '！':
		return "!"
	case '？':
		return "?"
	case '〜', '～':
		return "~"
	case '「', '」', '『', '』', '“', '”':
		return `"`
	case '（':
		return "("
	case '）':
		return ")"
	case '：':
		return ":"
	case '；':
		return ";"
	case '’':
		return "'"
	case '　':
		return " "
	case '０', '１', '２', '３', '４', '５', '６', '７', '８', '９':
		return string('0' + (r - '０'))
	}
	return string(r)
}

func hasKana(s string) bool {
	for _, r := range s {
		if isKana(r) {
			return true
		}
	}
	return false
}

func hasHiragana(s string) bool {
	for _, r := range s {
		if r >= 0x3041 && r <= 0x3096 {
			return true
		}
	}
	return false
}

func hasJapanese(s string) bool {
	for _, r := range s {
		if isKana(r) || isKanji(r) {
			return true
		}
	}
	return false
}

func allKana(s string) bool {
	for _, r := range s {
		if !isKana(r) && r != 'ー' {
			return false
		}
	}
	return s != ""
}

func isKana(r rune) bool {
	return r >= 0x3041 && r <= 0x3096 ||
		r >= 0x30a1 && r <= 0x30fa ||
		r >= 0x30fd && r <= 0x30ff
}

func isKanji(r rune) bool {
	return r >= 0x3400 && r <= 0x4dbf ||
		r >= 0x4e00 && r <= 0x9fff ||
		r >= 0xf900 && r <= 0xfaff ||
		r == '々' || r == '〆'
}

func toKatakana(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0x3041 && r <= 0x3096 {
			return r + 0x60
		}
		return r
	}, s)
}

var (
	spaceBeforePunctuation = regexp.MustCompile(`\s+([,\.!\?\)\]\}])`)
	spaceAfterOpening      = regexp.MustCompile(`([\(\[\{])\s+`)
	spacedHyphen           = regexp.MustCompile(`(\S) - (\S)`)
	doubleQuoted           = regexp.MustCompile(`"([^"]*)"`)
)

// canonicalize tidies the spaces a word-by-word join leaves around punctuation
// and inside quotes, as Kuroshiro callers do. It also trims the line.
func canonicalize(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = spacedHyphen.ReplaceAllString(s, "$1-$2")
	s = spaceBeforePunctuation.ReplaceAllString(s, "$1")
	s = spaceAfterOpening.ReplaceAllString(s, "$1")
	s = doubleQuoted.ReplaceAllStringFunc(s, func(m string) string {
		return `"` + strings.TrimSpace(m[1:len(m)-1]) + `"`
	})
	return strings.TrimSpace(s)
}
