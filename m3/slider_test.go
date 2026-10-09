package m3_test

import (
	"image"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/m3"
)

// The active track of a wavy slider is a wave that travels while the music
// plays. It travels from the handle back to the start, against the way the
// track fills.
func TestSliderWaveTravelsBackToTheStart(t *testing.T) {
	value := 100.0
	tt := ui.NewTester(func(c *ui.Context) {
		th := m3.New(m3.Config{}, c.Theme().Dark)
		m3.Provide(c, th)
		c.Root().Background(th.Scheme.Surface)
		ui.Column(c).Padding(20).Children(func() {
			m3.Slider(c, &value, 0, 100, m3.SliderSpec{Label: "Wave", Key: "wave", Thickness: 16, Wavy: true, Waving: true})
		})
	}, 400, 120)

	// Let the wave grow to its full amplitude first.
	for range 40 {
		tt.Frame()
		time.Sleep(5 * time.Millisecond)
	}
	started := time.Now()
	first := waveProfile(tt.Image())
	time.Sleep(240 * time.Millisecond)
	tt.Frame()
	gap := time.Since(started)
	second := waveProfile(tt.Image())

	// The wave travels one wavelength every 1800ms, so it moves about 4px
	// over this gap. A gap far from that moves it too far to read: it would
	// stand in for an earlier part of the wave.
	if gap < 150*time.Millisecond || gap > 700*time.Millisecond {
		t.Skipf("the two frames are %v apart", gap)
	}
	shift, ok := waveShift(first, second)
	if !ok {
		t.Fatal("the frames show no wave to compare")
	}
	if shift > 0 {
		t.Errorf("the wave moved %dpx to the right, want it to move to the left", shift)
	}
}

// waveShift returns the shift that best matches the first profile against the
// second, and whether the two had enough drawn columns to tell.
func waveShift(first, second []float32) (int, bool) {
	best, bestErr, bestCount := 0, float64(0), 0
	for shift := -12; shift <= 12; shift++ {
		sum, count := 0.0, 0
		for x := range first {
			y := x + shift
			if y < 0 || y >= len(second) || first[x] < 0 || second[y] < 0 {
				continue
			}
			d := float64(first[x] - second[y])
			sum += d * d
			count++
		}
		if count < 40 {
			continue
		}
		if mean := sum / float64(count); bestCount == 0 || mean < bestErr {
			best, bestErr, bestCount = shift, mean, count
		}
	}
	return best, bestCount > 0
}

// waveProfile is the mean row of what each column draws, or -1 where it draws
// nothing, which is the wave's shape in the rendered window.
func waveProfile(img *image.RGBA) []float32 {
	bounds := img.Bounds()
	background := img.RGBAAt(bounds.Min.X, bounds.Min.Y)
	out := make([]float32, bounds.Dx())
	for x := range out {
		out[x] = -1
		sum, count := 0, 0
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			if img.RGBAAt(bounds.Min.X+x, y) == background {
				continue
			}
			sum += y
			count++
		}
		if count > 0 {
			out[x] = float32(sum) / float32(count)
		}
	}
	return out
}
