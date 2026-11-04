package romaji

import (
	"testing"
)

func TestToRomaji(t *testing.T) {
	for _, c := range []struct{ kana, want string }{
		{"きょう", "kyou"},
		{"コーヒー", "kōhī"},
		{"ずっと", "zutto"},
		{"こっち", "kotchi"},
		{"しんぶん", "shimbun"},
		{"しんや", "shin'ya"},
		{"ヴァイオリン", "vaiorin"},
		{"サッカー", "sakkā"},
		{"パーティー", "pātī"},
		{"ありがとう", "arigatou"},
		{"を", "o"},
		{"ん", "n"},
	} {
		if got := toRomaji(c.kana); got != c.want {
			t.Errorf("toRomaji(%q) = %q, want %q", c.kana, got, c.want)
		}
	}
}

func TestCanonicalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"sō , sore .", "sō, sore."},
		{`" arigatō "`, `"arigatō"`},
		{"( a )", "(a)"},
		{"mol - 74", "mol-74"},
		{"  one   two  ", "one two"},
		{"sore wa , sore", "sore wa, sore"},
	} {
		if got := canonicalize(c.in); got != c.want {
			t.Errorf("canonicalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRomanize(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ line, want string }{
		{"夜に駆ける", "yoru ni kakeru"},
		{"今日はいい天気ですね", "kyō wa ii tenki desu ne"},
		{"君の名前を呼ぶよ", "kimi no namae o yobu yo"},
		{"ありがとう", "arigatō"},
		{"東京", "tōkyō"},
		{"コーヒーを飲む", "kōhī o nomu"},
		{"すもももももももものうち", "sumomo mo momo mo momo no uchi"},
		{"Hello 世界", "Hello sekai"},
		{"「そうか」と彼は言った。", `"sō ka" to kare wa itta.`},
		{"行った", "itta"},
		{"ずっと", "zutto"},
		{"しんぶん", "shimbun"},
		{"日本語の歌詞をローマ字にする", "nihongo no kashi o rōmaji ni suru"},
		{"こんにちは", "konnichiwa"},
	} {
		if got := r.Romanize(c.line); got != c.want {
			t.Errorf("Romanize(%q) = %q, want %q", c.line, got, c.want)
		}
	}
}

func TestLinesOnlyRomanizesJapaneseLyrics(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the dictionary")
	}
	chinese, err := Lines([]string{"你好世界", "我们走吧", "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	for line, want := range map[string]string{"你好世界": "你好世界", "我们走吧": "我们走吧", "Hello": "Hello"} {
		if got := chinese[line]; got != want {
			t.Errorf("Chinese line %q = %q, want %q", line, got, want)
		}
	}
	japanese, err := Lines([]string{"夜に駆ける", "Hello", "東京"})
	if err != nil {
		t.Fatal(err)
	}
	for line, want := range map[string]string{"夜に駆ける": "yoru ni kakeru", "Hello": "Hello", "東京": "tōkyō"} {
		if got := japanese[line]; got != want {
			t.Errorf("Japanese line %q = %q, want %q", line, got, want)
		}
	}
}
