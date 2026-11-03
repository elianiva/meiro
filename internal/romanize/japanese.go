package romanize

import (
	"strings"
	"sync"

	"github.com/ikawaha/kagome-dict/ipa"
	"github.com/ikawaha/kagome/v2/tokenizer"
)

// japaneseMu guards the tokenizer. Kagome's dictionary is safe to read from
// several lyrics lookups at once once it has been built.
var (
	japaneseOnce sync.Once
	japaneseTok  *tokenizer.Tokenizer
)

func japaneseTokenizer() *tokenizer.Tokenizer {
	japaneseOnce.Do(func() {
		tok, err := tokenizer.New(ipa.Dict(), tokenizer.OmitBosEos())
		if err != nil {
			return
		}
		japaneseTok = tok
	})
	return japaneseTok
}

// japaneseReading turns kana and kanji into spaced Hepburn. A particle は,
// へ or を is read as it is said. Kanji take the dictionary reading, which
// can differ from the reading a song uses. Latin and punctuation stay.
func japaneseReading(text string) string {
	tok := japaneseTokenizer()
	if tok == nil {
		return hepburn(text)
	}
	var parts []string
	for _, token := range tok.Tokenize(text) {
		if token.Class == tokenizer.DUMMY {
			continue
		}
		surface := token.Surface
		if surface == "" {
			continue
		}
		if !hasJapanese(surface) {
			parts = append(parts, surface)
			continue
		}
		reading := surface
		features := token.Features()
		if len(features) > 7 && features[7] != "" && features[7] != "*" {
			reading = features[7]
		}
		if len(features) > 0 && features[0] == "助詞" {
			switch surface {
			case "は":
				reading = "ワ"
			case "へ":
				reading = "エ"
			case "を":
				reading = "オ"
			}
		}
		latin := strings.TrimSpace(hepburn(reading))
		if latin == "" {
			continue
		}
		parts = append(parts, latin)
	}
	return strings.Join(parts, " ")
}

// hepburn spells kana in ASCII Hepburn. Kanji and anything else are copied.
// A small tsu doubles the following consonant, and a long-vowel mark repeats
// the vowel it lengthens.
func hepburn(text string) string {
	runes := []rune(text)
	var b strings.Builder
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r == 'っ' || r == 'ッ' {
			next := ""
			if i+1 < len(runes) {
				next, i = digraph(runes, i+1)
			}
			b.WriteString(sokuon(next))
			b.WriteString(next)
			continue
		}
		if r == 'ー' {
			b.WriteString(lastVowel(b.String()))
			continue
		}
		piece, next := digraph(runes, i)
		if next != i {
			b.WriteString(piece)
			i = next
			continue
		}
		if latin, ok := kanaLatin[r]; ok {
			b.WriteString(latin)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func digraph(runes []rune, i int) (string, int) {
	if i+1 >= len(runes) {
		if latin, ok := kanaLatin[runes[i]]; ok {
			return latin, i
		}
		return string(runes[i]), i
	}
	if latin, ok := kanaDigraph[string(runes[i:i+2])]; ok {
		return latin, i + 1
	}
	if latin, ok := kanaLatin[runes[i]]; ok {
		return latin, i
	}
	return string(runes[i]), i
}

func sokuon(next string) string {
	switch {
	case strings.HasPrefix(next, "ch"):
		return "t"
	case next == "":
		return ""
	}
	return next[:1]
}

func lastVowel(soFar string) string {
	for i := len(soFar) - 1; i >= 0; i-- {
		switch soFar[i] {
		case 'a', 'i', 'u', 'e', 'o':
			return soFar[i : i+1]
		}
	}
	return ""
}

var kanaLatin = map[rune]string{
	'あ': "a", 'い': "i", 'う': "u", 'え': "e", 'お': "o",
	'か': "ka", 'き': "ki", 'く': "ku", 'け': "ke", 'こ': "ko",
	'さ': "sa", 'し': "shi", 'す': "su", 'せ': "se", 'そ': "so",
	'た': "ta", 'ち': "chi", 'つ': "tsu", 'て': "te", 'と': "to",
	'な': "na", 'に': "ni", 'ぬ': "nu", 'ね': "ne", 'の': "no",
	'は': "ha", 'ひ': "hi", 'ふ': "fu", 'へ': "he", 'ほ': "ho",
	'ま': "ma", 'み': "mi", 'む': "mu", 'め': "me", 'も': "mo",
	'や': "ya", 'ゆ': "yu", 'よ': "yo",
	'ら': "ra", 'り': "ri", 'る': "ru", 'れ': "re", 'ろ': "ro",
	'わ': "wa", 'ゐ': "wi", 'ゑ': "we", 'を': "o", 'ん': "n",
	'が': "ga", 'ぎ': "gi", 'ぐ': "gu", 'げ': "ge", 'ご': "go",
	'ざ': "za", 'じ': "ji", 'ず': "zu", 'ぜ': "ze", 'ぞ': "zo",
	'だ': "da", 'ぢ': "ji", 'づ': "zu", 'で': "de", 'ど': "do",
	'ば': "ba", 'び': "bi", 'ぶ': "bu", 'べ': "be", 'ぼ': "bo",
	'ぱ': "pa", 'ぴ': "pi", 'ぷ': "pu", 'ぺ': "pe", 'ぽ': "po",
	'ぁ': "a", 'ぃ': "i", 'ぅ': "u", 'ぇ': "e", 'ぉ': "o",
	'ゔ': "vu",
	'ア': "a", 'イ': "i", 'ウ': "u", 'エ': "e", 'オ': "o",
	'カ': "ka", 'キ': "ki", 'ク': "ku", 'ケ': "ke", 'コ': "ko",
	'サ': "sa", 'シ': "shi", 'ス': "su", 'セ': "se", 'ソ': "so",
	'タ': "ta", 'チ': "chi", 'ツ': "tsu", 'テ': "te", 'ト': "to",
	'ナ': "na", 'ニ': "ni", 'ヌ': "nu", 'ネ': "ne", 'ノ': "no",
	'ハ': "ha", 'ヒ': "hi", 'フ': "fu", 'ヘ': "he", 'ホ': "ho",
	'マ': "ma", 'ミ': "mi", 'ム': "mu", 'メ': "me", 'モ': "mo",
	'ヤ': "ya", 'ユ': "yu", 'ヨ': "yo",
	'ラ': "ra", 'リ': "ri", 'ル': "ru", 'レ': "re", 'ロ': "ro",
	'ワ': "wa", 'ヰ': "wi", 'ヱ': "we", 'ヲ': "o", 'ン': "n",
	'ガ': "ga", 'ギ': "gi", 'グ': "gu", 'ゲ': "ge", 'ゴ': "go",
	'ザ': "za", 'ジ': "ji", 'ズ': "zu", 'ゼ': "ze", 'ゾ': "zo",
	'ダ': "da", 'ヂ': "ji", 'ヅ': "zu", 'デ': "de", 'ド': "do",
	'バ': "ba", 'ビ': "bi", 'ブ': "bu", 'ベ': "be", 'ボ': "bo",
	'パ': "pa", 'ピ': "pi", 'プ': "pu", 'ペ': "pe", 'ポ': "po",
	'ァ': "a", 'ィ': "i", 'ゥ': "u", 'ェ': "e", 'ォ': "o",
	'ヴ': "vu",
}

var kanaDigraph = map[string]string{
	"きゃ": "kya", "きゅ": "kyu", "きょ": "kyo",
	"しゃ": "sha", "しゅ": "shu", "しょ": "sho",
	"ちゃ": "cha", "ちゅ": "chu", "ちょ": "cho",
	"にゃ": "nya", "にゅ": "nyu", "にょ": "nyo",
	"ひゃ": "hya", "ひゅ": "hyu", "ひょ": "hyo",
	"みゃ": "mya", "みゅ": "myu", "みょ": "myo",
	"りゃ": "rya", "りゅ": "ryu", "りょ": "ryo",
	"ぎゃ": "gya", "ぎゅ": "gyu", "ぎょ": "gyo",
	"じゃ": "ja", "じゅ": "ju", "じょ": "jo",
	"びゃ": "bya", "びゅ": "byu", "びょ": "byo",
	"ぴゃ": "pya", "ぴゅ": "pyu", "ぴょ": "pyo",
	"キャ": "kya", "キュ": "kyu", "キョ": "kyo",
	"シャ": "sha", "シュ": "shu", "ショ": "sho",
	"チャ": "cha", "チュ": "chu", "チョ": "cho",
	"ニャ": "nya", "ニュ": "nyu", "ニョ": "nyo",
	"ヒャ": "hya", "ヒュ": "hyu", "ヒョ": "hyo",
	"ミャ": "mya", "ミュ": "myu", "ミョ": "myo",
	"リャ": "rya", "リュ": "ryu", "リョ": "ryo",
	"ギャ": "gya", "ギュ": "gyu", "ギョ": "gyo",
	"ジャ": "ja", "ジュ": "ju", "ジョ": "jo",
	"ビャ": "bya", "ビュ": "byu", "ビョ": "byo",
	"ピャ": "pya", "ピュ": "pyu", "ピョ": "pyo",
}
