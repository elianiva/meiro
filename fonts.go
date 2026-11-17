package main

import (
	_ "embed"
	"log"

	"github.com/egoist/mygo/ui"
)

//go:embed fonts/GoogleSans.ttf
var googleSans []byte

const fontFamily = "Google Sans"

var uiFont string

func init() {
	if err := ui.RegisterFont(googleSans, fontFamily); err != nil {
		log.Printf("registering %s: %v", fontFamily, err)
		return
	}
	uiFont = fontFamily
}
