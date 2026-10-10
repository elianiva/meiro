package m3

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// The hue slider's spectrum is precomputed; it must still be what FromHue
// gives for each band.
func TestHueStripMatchesFromHue(t *testing.T) {
	for i := range hueStrip {
		if want := FromHue(float64(i) * 360 / hueSteps); hueStrip[i] != want {
			t.Fatalf("hue band %d = %v, want %v", i, hueStrip[i], want)
		}
	}
}

func TestAnimatedPaintIntervalCapsPlaybackAnimationRate(t *testing.T) {
	if animatedPaintInterval != 33*time.Millisecond {
		t.Errorf("animated paint interval = %v, want 33 ms (~30 fps)", animatedPaintInterval)
	}
}

// Every role a component draws on a surface must stay readable, whatever the
// seed, the style and the appearance.
func TestSchemesKeepTheirContrast(t *testing.T) {
	for hue := 0.0; hue < 360; hue += 20 {
		seed := FromHue(hue)
		for _, style := range Styles() {
			for _, dark := range []bool{false, true} {
				s := NewScheme(NewPalettes(seed, style), dark)
				pairs := []struct {
					name    string
					fg, bg  ui.Color
					minimum float64
				}{
					{"onPrimary", s.OnPrimary, s.Primary, 4.5},
					{"onPrimaryContainer", s.OnPrimaryContainer, s.PrimaryContainer, 4.5},
					{"onSecondary", s.OnSecondary, s.Secondary, 4.5},
					{"onSecondaryContainer", s.OnSecondaryContainer, s.SecondaryContainer, 4.5},
					{"onTertiary", s.OnTertiary, s.Tertiary, 4.5},
					{"onTertiaryContainer", s.OnTertiaryContainer, s.TertiaryContainer, 4.5},
					{"onError", s.OnError, s.Error, 4.5},
					{"onSurface", s.OnSurface, s.Surface, 7},
					{"onSurface/high", s.OnSurface, s.SurfaceContainerHigh, 7},
					{"onSurfaceVariant", s.OnSurfaceVariant, s.SurfaceContainerHighest, 4.5},
					{"inverse", s.InverseOnSurface, s.InverseSurface, 7},
					{"primary on surface", s.Primary, s.Surface, 3},
				}
				for _, p := range pairs {
					if got := Contrast(p.fg, p.bg); got < p.minimum {
						t.Errorf("hue %.0f %v dark=%v: %s contrast %.2f < %.1f", hue, style, dark, p.name, got, p.minimum)
					}
				}
			}
		}
	}
}

func TestTonesRunFromBlackToWhite(t *testing.T) {
	p := Palette{Hue: 260, Chroma: 0.12}
	if c := p.Tone(0); c != ui.RGB(0, 0, 0) {
		t.Errorf("tone 0 = %v", c)
	}
	if c := p.Tone(100); c != ui.RGB(255, 255, 255) {
		t.Errorf("tone 100 = %v", c)
	}
	last := -1.0
	for tone := 0.0; tone <= 100; tone += 5 {
		l := luminance(p.Tone(tone))
		if l < last {
			t.Fatalf("tone %.0f is darker than the one before it", tone)
		}
		last = l
	}
	// A tone's luminance is what L* says it is.
	if l := luminance(p.Tone(50)); math.Abs(l-0.184) > 0.02 {
		t.Errorf("tone 50 has luminance %.3f, want about 0.184", l)
	}
}

func TestHuesSurviveTheGamutMapping(t *testing.T) {
	for hue := 0.0; hue < 360; hue += 30 {
		got, chroma := HueOf(Palette{Hue: hue, Chroma: 0.1}.Tone(55))
		diff := math.Abs(got - hue)
		if diff > 180 {
			diff = 360 - diff
		}
		if diff > 4 || chroma < 0.04 {
			t.Errorf("hue %.0f came back as %.1f at chroma %.3f", hue, got, chroma)
		}
	}
}

func TestMonochromeHasNoColour(t *testing.T) {
	s := NewScheme(NewPalettes(ui.Hex("#d81b78"), Monochrome), false)
	for _, c := range []ui.Color{s.Primary, s.Secondary, s.Tertiary, s.Surface, s.SurfaceContainer} {
		if math.Max(math.Max(float64(c.R), float64(c.G)), float64(c.B))-math.Min(math.Min(float64(c.R), float64(c.G)), float64(c.B)) > 3 {
			t.Errorf("monochrome has the colour %v", c)
		}
	}
}

func TestSpringsSettleAndOvershootAsTheyShould(t *testing.T) {
	for name, spring := range map[string]Spring{"spatial": SpatialDefault, "fast": SpatialFast, "effects": EffectsDefault} {
		ease := spring.Ease()
		if ease(0) != 0 || math.Abs(float64(ease(1))-1) > 1e-6 {
			t.Errorf("%s: ease(0) = %v, ease(1) = %v", name, ease(0), ease(1))
		}
		peak := float32(0)
		for i := 0; i <= 200; i++ {
			peak = max(peak, ease(float32(i)/200))
		}
		overshoots := peak > 1.001
		if want := spring.Damping < 1; overshoots != want {
			t.Errorf("%s: peak %.3f, overshoot %v, want %v", name, peak, overshoots, want)
		}
		if d := spring.Duration().Milliseconds(); d < 50 || d > 1500 {
			t.Errorf("%s: duration %dms", name, d)
		}
	}
}

func TestMixingSchemesBlendsEveryRole(t *testing.T) {
	light := NewScheme(NewPalettes(DefaultSeed, TonalSpot), false)
	dark := NewScheme(NewPalettes(DefaultSeed, TonalSpot), true)
	if got := light.Mix(dark, 0); got != light {
		t.Errorf("mixing at 0 changed the scheme")
	}
	if got := light.Mix(dark, 1); got != dark {
		t.Errorf("mixing at 1 did not reach the other scheme")
	}
	mid := light.Mix(dark, 0.5)
	if mid.Surface == light.Surface || mid.Surface == dark.Surface || mid.OnSurface == light.OnSurface {
		t.Errorf("mixing halfway left roles at their ends")
	}
	// Every colour role takes part, so Mix cannot silently leave one behind.
	colour := reflect.TypeOf(ui.Color{})
	lv, dv, mv := reflect.ValueOf(light), reflect.ValueOf(dark), reflect.ValueOf(mid)
	for i := 0; i < lv.NumField(); i++ {
		if lv.Field(i).Type() != colour {
			continue
		}
		from, to := lv.Field(i).Interface(), dv.Field(i).Interface()
		if from == to {
			continue // scrim and shadow are black in both appearances
		}
		if got := mv.Field(i).Interface(); got == from || got == to {
			t.Errorf("%s did not blend: %v, from %v to %v", lv.Type().Field(i).Name, got, from, to)
		}
	}
}

func TestThemeModes(t *testing.T) {
	if New(Config{Mode: Light}, true).Dark {
		t.Errorf("Light mode followed a dark desktop")
	}
	if !New(Config{Mode: Dark}, false).Dark {
		t.Errorf("Dark mode followed a light desktop")
	}
	if !New(Config{}, true).Dark || New(Config{}, false).Dark {
		t.Errorf("System mode did not follow the desktop")
	}
}

func TestCarouselNeedsMeasurementOnce(t *testing.T) {
	s := &CarouselState{}
	if !s.NeedsMeasurement() {
		t.Error("an unmeasured carousel did not ask for a frame")
	}
	if s.NeedsMeasurement() {
		t.Error("an unmeasured carousel asked for another frame")
	}
	measured := &CarouselState{view: 100}
	if measured.NeedsMeasurement() {
		t.Error("a measured carousel asked for a frame")
	}
}

func TestCarouselVisibleRangeIncludesViewportAndPrefetch(t *testing.T) {
	state := &CarouselState{}
	if first, last := state.VisibleRange(20, 100, 10, 10, 1); first != 0 || last != 5 {
		t.Errorf("initial range = [%d, %d), want [0, 5)", first, last)
	}

	state.view = 430
	state.X = 220
	if first, last := state.VisibleRange(20, 100, 10, 10, 1); first != 1 || last != 7 {
		t.Errorf("scrolled range = [%d, %d), want [1, 7)", first, last)
	}

	state.X = 10000
	if first, last := state.VisibleRange(20, 100, 10, 10, 1); first != 20 || last != 20 {
		t.Errorf("end range = [%d, %d), want [20, 20)", first, last)
	}
}

func TestFitLabelLeavesRoomPastItsIntrinsicWidth(t *testing.T) {
	const label = "Shuffle"
	var measured float32
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		measured, _ = c.MeasureText(0, ui.Span{
			Text: label, Size: LabelLarge.Size, Weight: LabelLarge.Emphasis,
			LetterSpacing: LabelLarge.Tracking,
		})
		fitLabel(c, LabelLarge, label, true, 0).SingleLine().Label("fit-label")
	}, 200, 50)
	if bounds, ok := tt.Find("fit-label"); !ok || bounds.W <= measured {
		t.Fatalf("fit label width = %v (found %v), measured text width = %v", bounds.W, ok, measured)
	}
}

func TestExpandedRailItemsFillTheirRows(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		Rail(c, RailSpec{
			Items: []NavItem{{ID: "home", Label: "Home"}}, Expanded: true,
		})
	}, 400, 300)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true, TextScale: 1})
	tt.Frame()
	if bounds, ok := tt.Find("Home"); !ok || bounds.W < RailExpanded-24 {
		t.Fatalf("expanded Home row width = %v (found %v), want at least %v", bounds.W, ok, RailExpanded-24)
	}
}
