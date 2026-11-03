package romanize

import (
	"strings"
	"testing"
)

func TestKoreanReadingFollowsTheRevisedRomanization(t *testing.T) {
	cases := []struct{ in, want string }{
		{"백마", "baengma"},
		{"종로", "jongno"},
		{"왕십리", "wangsimni"},
		{"별래", "byeollae"},
		{"신라", "silla"},
		{"학여울", "hangnyeoul"},
		{"알약", "allyak"},
		{"호랑이", "horangi"},
		{"빨간색이에요", "ppalgansaegieyo"},
		{"해돋이", "haedoji"},
		{"같이", "gachi"},
		{"굳히다", "guchida"},
		{"좋고", "joko"},
		{"놓다", "nota"},
		{"잡혀", "japyeo"},
		{"낳지", "nachi"},
		{"압구정", "apgujeong"},
		{"낙동강", "nakdonggang"},
		{"죽변", "jukbyeon"},
		{"낙성대", "nakseongdae"},
		{"합정", "hapjeong"},
		{"팔당", "paldang"},
		{"샛별", "saetbyeol"},
		{"울산", "ulsan"},
		{"구미", "gumi"},
		{"영동", "yeongdong"},
		{"백암", "baegam"},
		{"옥천", "okcheon"},
		{"합덕", "hapdeok"},
		{"호법", "hobeop"},
		{"월곶", "wolgot"},
		{"벚꽃", "beotkkot"},
		{"한밭", "hanbat"},
		{"구리", "guri"},
		{"설악", "seorak"},
		{"칠곡", "chilgok"},
		{"임실", "imsil"},
		{"울릉", "ulleung"},
		{"대관령", "daegwallyeong"},
		{"ㄱ", "g"},
		{"가나다라ㅁㅂㅅㅇ", "ganadarambs"},
		{"ㅏ", "a"},
		{"ㅘ", "wa"},
		{"안녕하세요.", "annyeonghaseyo."},
		{"한국어!", "hangugeo!"},
		{"", ""},
		{"!?/", "!?/"},
		{"안녕하세요 es-hangul", "annyeonghaseyo es-hangul"},
		{"한국은korea", "hangugeunkorea"},
		{"고양이는cat", "goyangineuncat"},
	}
	for _, tc := range cases {
		if got := koreanReading(tc.in); got != tc.want {
			t.Errorf("koreanReading(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestReadingSkipsLatinAndKeepsJapaneseAndKorean(t *testing.T) {
	if got := Reading("hello"); got != "" {
		t.Errorf("Reading(hello) = %q", got)
	}
	if got := Reading("안녕하세요"); got != "annyeonghaseyo" {
		t.Errorf("Reading(korean) = %q", got)
	}
	if got := Reading("♪"); got != "" {
		t.Errorf("Reading(empty lyric) = %q", got)
	}
	// A dictionary reading, spaced the way the line is said.
	if got := Reading("さくら"); got != "sakura" {
		t.Errorf("Reading(さくら) = %q", got)
	}
	if got := Reading("私は"); !strings.Contains(got, "wa") {
		t.Errorf("Reading(私は) = %q, want the particle は as wa", got)
	}
}
