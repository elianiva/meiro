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

// fontFamily is the name the font registers under.
const fontFamily = "Google Sans"

// uiFont is the typeface the theme asks for: empty until the font registers.
var uiFont string

func init() {
	if err := ui.RegisterFont(googleSans, fontFamily); err != nil {
		// The system font does the job, as it did before.
		log.Printf("registering %s: %v", fontFamily, err)
		return
	}
	uiFont = fontFamily
}
