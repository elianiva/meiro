package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// The interface asks for Google Sans and the Korean font beside it, so a
// Korean title draws whether or not the system has a font for it.
func TestTheInterfaceFontCarriesKorean(t *testing.T) {
	if uiFont == "" {
		t.Fatal("the interface font never registered")
	}
	if !strings.Contains(uiFont, fontFamily) || !strings.Contains(uiFont, hangulFamily) {
		t.Fatalf("the interface font is %q, want %s and %s", uiFont, fontFamily, hangulFamily)
	}
	for _, family := range ui.FontFamilies() {
		if family.Name == hangulFamily {
			if !family.Registered {
				t.Errorf("%s is the system's font, not the bundled one", hangulFamily)
			}
			return
		}
	}
	t.Errorf("no family called %s is registered", hangulFamily)
}
