package main

import (
	_ "embed"
	"log"

	"github.com/egoist/mygo/ui"
)

// googleSans is Google Sans, the typeface of Material 3 Expressive, as one
// variable font whose weight, optical size and grade the text engine picks
// along as the type scale asks. It is licensed under the SIL Open Font
// License: see fonts/OFL.txt.
//
//go:embed fonts/GoogleSans.ttf
var googleSans []byte

// meiroHangul is Noto Sans KR cut to the Hangul syllables, the jamo and the
// punctuation Korean text uses. Google Sans holds no Hangul, so without it a
// Korean title draws as nothing when no installed font covers the script, as
// on a minimal Linux. It is licensed under the SIL Open Font License: see
// fonts/OFL-MeiroHangul.txt.
//
//go:embed fonts/MeiroHangul.ttf
var meiroHangul []byte

// fontFamily is the name the font registers under, and hangulFamily the name
// the Korean fallback registers under.
const (
	fontFamily   = "Google Sans"
	hangulFamily = "Meiro Hangul"
)

// uiFont is the typeface the theme asks for: empty until the font registers.
var uiFont string

func init() {
	if err := ui.RegisterFont(googleSans, fontFamily); err != nil {
		// The system font does the job, as it did before.
		log.Printf("registering %s: %v", fontFamily, err)
		return
	}
	uiFont = fontFamily
	if err := ui.RegisterFont(meiroHangul, hangulFamily); err != nil {
		// The system's Korean font does the job, when it has one.
		log.Printf("registering %s: %v", hangulFamily, err)
		return
	}
	// A family list, so text takes Latin from Google Sans and Hangul from the
	// bundled font, whichever fonts the system has.
	uiFont = fontFamily + ", " + hangulFamily
}
