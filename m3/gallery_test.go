package m3_test

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// gallery lays every component out on one page, for looking at. It writes
// PNGs when MEIRO_SNAPSHOTS names a directory.
type gallery struct {
	cfg     m3.Config
	group   int
	on      bool
	off     bool
	vol     float64
	hue     float64
	query   string
	toolbar string
}

func (g *gallery) view(c *ui.Context) {
	th := m3.New(g.cfg, c.Theme().Dark)
	m3.Provide(c, th)
	sc := th.Scheme
	c.Root().Background(sc.Surface)
	ui.Scroll(c).Fill().Padding(24).Gap(20).Children(func() {
		ui.Row(c).Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
			m3.Button(c, m3.ButtonSpec{Label: "Filled", Icon: m3.IconPlay, Key: "a"})
			m3.Button(c, m3.ButtonSpec{Label: "Tonal", Kind: m3.Tonal, Key: "b"})
			m3.Button(c, m3.ButtonSpec{Label: "Elevated", Kind: m3.Elevated, Key: "c"})
			m3.Button(c, m3.ButtonSpec{Label: "Outlined", Kind: m3.Outlined, Key: "d"})
			m3.Button(c, m3.ButtonSpec{Label: "Text", Kind: m3.TextOnly, Key: "e"})
			m3.Button(c, m3.ButtonSpec{Label: "Disabled", Disabled: true, Key: "f"})
			m3.Button(c, m3.ButtonSpec{Label: "Square", Square: true, Kind: m3.Tonal, Key: "g"})
			m3.Button(c, m3.ButtonSpec{Label: "Toggled", Toggle: true, Selected: true, Kind: m3.Tonal, Key: "h"})
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			m3.Button(c, m3.ButtonSpec{Label: "XS", Size: m3.ExtraSmall32, Key: "s0"})
			m3.Button(c, m3.ButtonSpec{Label: "Small", Size: m3.Small40, Key: "s1"})
			m3.Button(c, m3.ButtonSpec{Label: "Medium", Size: m3.Medium56, Key: "s2"})
			m3.Button(c, m3.ButtonSpec{Label: "Large", Size: m3.Large96, Icon: m3.IconAdd, Key: "s3"})
			m3.Button(c, m3.ButtonSpec{Label: "XL", Size: m3.ExtraLarge136, Key: "s4"})
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFavorite, Label: "Like", Key: "i0"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFavorite, Label: "Like", Kind: m3.FilledIcon, Key: "i1"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFavorite, Label: "Like", Kind: m3.TonalIcon, Key: "i2"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconFavorite, Label: "Like", Kind: m3.OutlinedIcon, Key: "i3"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconShuffle, Label: "Shuffle", Toggle: true, Selected: true, Key: "i4"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconPause, Label: "Pause", Kind: m3.FilledIcon, Dimension: 72, Morph: true, Selected: true, Key: "i5"})
			m3.IconButton(c, m3.IconButtonSpec{Icon: m3.IconPlay, Label: "Play", Kind: m3.FilledIcon, Dimension: 72, Morph: true, Key: "i6"})
			m3.FAB(c, m3.FABSpec{Icon: m3.IconAdd, Key: "f0"})
			m3.FAB(c, m3.FABSpec{Icon: m3.IconPlay, Label: "Play all", Tone: m3.FABTertiaryContainer, Key: "f1"})
			m3.FAB(c, m3.FABSpec{Icon: m3.IconAdd, Size: m3.FABLarge, Tone: m3.FABSecondaryContainer, Key: "f2"})
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			m3.Chip(c, "All", true, "c0")
			m3.Chip(c, "Songs", false, "c1")
			m3.Chip(c, "Albums", false, "c2")
			m3.ButtonGroup(c, "group", &g.group, []string{"System", "Light", "Dark"}, nil)
			m3.Switch(c, &g.on, "on")
			m3.Switch(c, &g.off, "off")
			m3.LoadingIndicator(c, 48, false)
			m3.LoadingIndicator(c, 48, true)
			m3.Equalizer(c, 24, true, sc.Primary)
			m3.Avatar(c, "Dicha", nil, 40)
		})
		ui.Column(c).Gap(4).Children(func() {
			v := 40.0
			ui.Row(c).Children(func() { m3.Slider(c, &v, 0, 100, m3.SliderSpec{Wavy: true, Waving: true, Key: "w1"}) })
			ui.Row(c).Children(func() { m3.Slider(c, &v, 0, 100, m3.SliderSpec{Wavy: true, Key: "w2"}) })
			ui.Row(c).Children(func() { m3.Slider(c, &g.vol, 0, 100, m3.SliderSpec{Thickness: 16, Key: "w3"}) })
			ui.Row(c).Children(func() { m3.Slider(c, &g.hue, 0, 360, m3.SliderSpec{Thickness: 16, Hue: true, Key: "w4"}) })
		})
		ui.Row(c).Children(func() {
			m3.SearchBar(c, m3.SearchSpec{Value: &g.query, Placeholder: "Search songs", Key: "s", MaxWidth: 520})
			m3.SearchBar(c, m3.SearchSpec{Value: &g.toolbar, Placeholder: "Search in a toolbar", Key: "st", Height: 48, MaxWidth: 520})
		})
		ui.Row(c).Gap(6).Children(func() {
			for _, col := range []ui.Color{
				sc.Primary, sc.PrimaryContainer, sc.Secondary, sc.SecondaryContainer, sc.Tertiary, sc.TertiaryContainer,
				sc.Error, sc.Surface, sc.SurfaceContainerLow, sc.SurfaceContainer, sc.SurfaceContainerHigh, sc.SurfaceContainerHighest, sc.OnSurface, sc.Outline,
			} {
				ui.Box(c).Size(52, 52).Radius(14).Background(col).Border(1, sc.OutlineVariant)
			}
		})
	})
}

func TestGallery(t *testing.T) {
	// Every component the gallery shows is findable by its label, so a
	// component that stops rendering fails this test rather than only showing
	// up in a snapshot nobody opens. The gallery runs in every appearance.
	components := []string{
		"Filled", "Tonal", "Elevated", "Outlined", "Text", "Disabled", "Square", "Toggled",
		"XS", "Small", "Medium", "Large", "XL",
		"Like", "Shuffle", "Pause", "Play", "Action", "Play all",
		"All", "Songs", "Albums", "System", "Light", "Dark",
		"on", "off", "Search songs", "Search in a toolbar",
	}
	dir := os.Getenv("MEIRO_SNAPSHOTS")
	for _, shot := range []struct {
		name string
		cfg  m3.Config
		dark bool
	}{
		{"gallery-light", m3.Config{}, false},
		{"gallery-dark-vibrant", m3.Config{Seed: ui.Hex("#1b6ef3"), Style: m3.Vibrant}, true},
		{"gallery-light-expressive", m3.Config{Seed: ui.Hex("#d81b78"), Style: m3.Expressive}, false},
	} {
		t.Run(shot.name, func(t *testing.T) {
			g := &gallery{cfg: shot.cfg, group: 1, on: true, vol: 60, hue: 250}
			tt := ui.NewTester(g.view, 980, 760)
			tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
			tt.SetDark(shot.dark)
			tt.Frame()
			for _, want := range components {
				if _, ok := tt.Find(want); !ok {
					t.Errorf("the gallery did not render %q", want)
				}
			}
			if dir == "" {
				return
			}
			f, err := os.Create(filepath.Join(dir, shot.name+".png"))
			if err != nil {
				t.Fatal(err)
			}
			if err := png.Encode(f, tt.Image()); err != nil {
				_ = f.Close()
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
