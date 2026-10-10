package m3

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// hueSteps is how many bands paint the hue slider's spectrum; hueStrip holds
// their colours, derived once rather than on every paint.
const hueSteps = 144

var hueStrip = func() [hueSteps]ui.Color {
	var strip [hueSteps]ui.Color
	for i := range strip {
		strip[i] = FromHue(float64(i) * 360 / hueSteps)
	}
	return strip
}()

// SliderSpec describes a slider.
type SliderSpec struct {
	Label string
	Key   any
	// Thickness is the track's height; 4 by default, 16 and up for the
	// Expressive sizes.
	Thickness float32
	// Wavy draws the active track as a wave. Waving makes it travel while true
	// and settle flat while false, so a paused track goes still.
	Wavy, Waving bool
	// Hue paints the track as the spectrum, for choosing a hue.
	Hue bool
	// Disabled greys the slider out.
	Disabled bool
}

// Slider builds an Expressive slider: the active track and the inactive one
// part around a slim handle, which narrows as it is held. It reports changes
// and presses through the returned element.
func Slider(c *ui.Context, value *float64, lo, hi float64, spec SliderSpec) ui.Element {
	sc := Of(c).Scheme
	thick := spec.Thickness
	if thick == 0 {
		thick = 4
	}
	key := spec.Key
	if key == nil {
		key = "slider-" + spec.Label
	}
	const pad float32 = 10
	handleH := max(thick+16, 24)
	s := ui.SliderBase(c.Key(key), value, lo, hi).Height(max(handleH, 32)).PaddingX(pad).
		Cursor(ui.CursorPointer).Grow(1).MinWidth(40)
	if spec.Label != "" {
		s.Label(spec.Label)
	}
	if spec.Disabled {
		s.Disabled(true)
	}

	frac := float32(0)
	if hi > lo {
		frac = float32((*value - lo) / (hi - lo))
	}
	frac = min(max(frac, 0), 1)
	pressed := s.Pressed()
	handleW := Animate(s, "handle", pick(pressed, float32(2), 4), SpatialFast)
	amplitude := float32(0)
	if spec.Wavy {
		amplitude = Animate(s, "wave", pick(spec.Waving, float32(2.4), 0), SpatialDefault)
	}
	active, inactive, handle := sc.Primary, sc.SecondaryContainer, sc.Primary
	if spec.Hue {
		inactive = sc.SurfaceContainerHighest
	}
	if spec.Disabled {
		active, inactive, handle = sc.OnSurface.Alpha(0.38), sc.OnSurface.Alpha(0.12), sc.OnSurface.Alpha(0.38)
	}
	focused := s.FocusVisible()

	s.Draw(func(p *ui.Painter, r ui.Rect) {
		x0, x1 := r.X+pad, r.X+r.W-pad
		cy := r.Y + r.H/2
		hx := x0 + (x1-x0)*frac
		gap := float32(6)
		if pressed {
			gap = 4
		}
		radius := thick / 2

		if spec.Hue {
			track := ui.Rect{X: x0, Y: cy - thick/2, W: x1 - x0, H: thick}
			p.Clip(track, radius, func() {
				w := track.W / hueSteps
				for i := 0; i < hueSteps; i++ {
					p.Fill(ui.Rect{X: track.X + w*float32(i), Y: track.Y, W: w + 0.75, H: track.H}, hueStrip[i], 0)
				}
			})
			p.Fill(ui.Rect{X: hx - 3, Y: cy - handleH/2, W: 6, H: handleH}, sc.Surface, 3)
			p.Fill(ui.Rect{X: hx - 1.5, Y: cy - handleH/2 + 2, W: 3, H: handleH - 4}, sc.OnSurface, 1.5)
			return
		}

		// The inactive track, from the handle to the end.
		if left := hx + gap + handleW/2; x1-left > 1 {
			p.Fill(ui.Rect{X: left, Y: cy - thick/2, W: x1 - left, H: thick}, inactive, radius)
			if x1-left > 14 && !spec.Wavy {
				p.Fill(ui.Rect{X: x1 - 6, Y: cy - 2, W: 4, H: 4}, active, 2)
			}
		}
		// The active track, from the start to the handle: flat, or a wave.
		if end := hx - gap - handleW/2; end-x0 > 1 {
			if amplitude < 0.05 {
				p.Fill(ui.Rect{X: x0, Y: cy - thick/2, W: end - x0, H: thick}, active, radius)
			} else {
				const wavelength = 30
				phase := float32(0)
				if spec.Waving {
					animatePaint(p)
					// The wave travels from the handle back to the start.
					phase = -float32(p.Now().UnixMilli()%1800) / 1800 * wavelength
				}
				var path ui.Path
				first := true
				for x := x0; ; x += 1.5 {
					if x > end {
						x = end
					}
					ramp := min((x-x0)/10, 1) // the wave grows out of the start
					y := cy + amplitude*ramp*float32(math.Sin(2*math.Pi*float64((x-x0-phase)/wavelength)))
					if first {
						path.MoveTo(x, y)
						first = false
					} else {
						path.LineTo(x, y)
					}
					if x >= end {
						p.Fill(ui.Rect{X: x - radius, Y: y - radius, W: thick, H: thick}, active, radius)
						break
					}
				}
				p.StrokePath(&path, thick, active)
				p.Fill(ui.Rect{X: x0 - radius, Y: cy - radius, W: thick, H: thick}, active, radius)
			}
		}
		// The handle.
		p.Fill(ui.Rect{X: hx - handleW/2, Y: cy - handleH/2, W: handleW, H: handleH}, handle, handleW/2)
		if focused {
			p.Stroke(ui.Rect{X: hx - 6, Y: cy - handleH/2 - 3, W: 12, H: handleH + 6}, sc.Secondary, 6, 2)
		}
	})
	return s
}
