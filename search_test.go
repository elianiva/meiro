package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Typing in the search field must not search: only Enter does.
func TestSearchRunsOnSubmitOnly(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if err := tt.Click("Search songs, albums, artists"); err != nil {
		t.Fatal(err)
	}
	tt.Type("ambient")
	tt.Frame()
	if n := searches.Load(); n != 0 || tt.HasText("Search Result Song") {
		t.Fatalf("typing ran %d searches: %q", n, tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	if n := searches.Load(); n != 1 {
		t.Fatalf("Enter ran %d searches, want 1", n)
	}
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the search results are missing: %q", tt.Texts())
	}
	if len(a.settings.Recent) != 1 || a.settings.Recent[0] != "ambient" {
		t.Errorf("the search was not remembered: %v", a.settings.Recent)
	}
	// Picking another filter searches again for what was submitted, not for
	// whatever is half typed.
	tt.Type("zzz")
	if err := tt.Click("Songs"); err != nil {
		t.Fatal(err)
	}
	if n := searches.Load(); n != 2 || a.search.submitted != "ambient" {
		t.Errorf("a filter ran %d searches for %q", n, a.search.submitted)
	}
}

// Typing shows the completions YouTube Music offers; choosing one searches it.
func TestSearchSuggestionsRunTheChosenSearch(t *testing.T) {
	a := newTestApp()
	a.router.Push("/search")
	tt := ui.NewTester(a.view, 1000, 700)
	searches.Store(0)
	if err := tt.Click("Search songs, albums, artists"); err != nil {
		t.Fatal(err)
	}
	tt.Type("yor")
	tt.Frame()
	if !tt.HasText("Yorushika songs") {
		t.Fatalf("suggestions are missing: %q", tt.Texts())
	}
	if n := searches.Load(); n != 0 {
		t.Fatalf("showing suggestions ran %d searches, want 0", n)
	}
	if err := tt.Click("Yorushika songs"); err != nil {
		t.Fatal(err)
	}
	if a.search.submitted != "Yorushika songs" || searches.Load() != 1 {
		t.Fatalf("choosing a suggestion submitted %q after %d searches", a.search.submitted, searches.Load())
	}
	if tt.HasText("Yorushika songs") {
		t.Errorf("suggestions stayed after the search ran: %q", tt.Texts())
	}
	if !tt.HasText("Search Result Song") {
		t.Fatalf("the chosen search has no results: %q", tt.Texts())
	}
}
