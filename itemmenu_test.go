package main

import (
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

func TestItemMenuOpensAndClosesOnItsItem(t *testing.T) {
	var m itemMenu
	if m.opened("a") || m.owns("a") {
		t.Fatal("a fresh menu belongs to an item")
	}
	m.show("a", false, 5, 6)
	if !m.opened("a") || m.opened("b") || !m.owns("a") {
		t.Fatalf("menu not open on a only: %+v", m)
	}
	m.close()
	if m.opened("a") || !m.owns("a") {
		t.Fatalf("closing should keep the item for its exit: %+v", m)
	}
}

func TestItemMenuAnchorsAtThePointerOnlyWhenPointerOpened(t *testing.T) {
	var m itemMenu
	m.show("a", true, 12, 34)
	if !m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("pointer anchor lost: %+v", m)
	}
	// A later menu-button open on another item forgets the pointer, and keeps
	// the old coordinates from leaking into its anchor choice.
	m.show("b", false, 99, 99)
	if m.atPointer || m.x != 12 || m.y != 34 {
		t.Fatalf("button open kept pointer anchor: %+v", m)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("button-opened menu has a pointer anchor")
		}
		m.show("a", true, 1, 2)
		if _, ok := m.pointerAnchor(c, "b"); ok {
			t.Error("pointer anchor offered to another item")
		}
		if _, ok := m.pointerAnchor(c, "a"); !ok {
			t.Error("pointer anchor missing on its item")
		}
	}, 100, 100)
	tt.Frame()
}

func TestThemesAreIsolatedPerWindow(t *testing.T) {
	var wg sync.WaitGroup
	for i, seed := range []string{"#d81b78", "#1b78d8", "#78d81b", "#6750a4"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			color, _ := parseSeed(seed)
			want := m3.New(m3.Config{Seed: color, Font: "Font" + string(rune('A'+i))}, false)
			var got *m3.Theme
			tt := ui.NewTester(func(c *ui.Context) {
				m3.Provide(c, want)
				got = m3.Of(c)
			}, 100, 100)
			for range 20 {
				tt.Frame()
				if got != want || got.UI(ui.LightTheme()).Font != want.Font {
					t.Errorf("window %d saw another window's theme", i)
					return
				}
			}
		}()
	}
	wg.Wait()
}
