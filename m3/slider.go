package m3

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// hueSteps is how many gradients paint the hue slider's spectrum, one per
// span between two stops of hueStrip, which holds the colours of the stops,
// derived once rather than on every paint. A band per colour would be a
// hundred and more quads in every frame of the page.
const hueSteps = 24

var hueStrip = func() [hueSteps + 1]ui.Color {
	var strip [hueSteps + 1]ui.Color
	for i := range strip {
		strip[i] = FromHue(float64(i%hueSteps) * 360 / hueSteps)
	}
	return strip
}()

// A wavy slider's active track is a wave that travels from the handle back to
// the start while the music plays: waveWavelength is the distance between two
// crests, and the wave covers one wavelength every wavePeriodMS.
const (
	waveWavelength = 30
	wavePeriodMS   = 1800
)

// wavePhase is where the travelling wave sits at a wall-clock time, in
// milliseconds since the epoch. It moves toward the start of the track as time
// passes and repeats once per period, so its value is always negative except
// at the start of a period.
func wavePhase(nowMillis int64) float32 {
	return -float32(nowMillis%wavePeriodMS) / wavePeriodMS * waveWavelength
}

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
	// Vertical stands the slider up: lo at the bottom, hi at the top. Draw
	// expects a tall, narrow box, as an equaliser's band slider has.
	Vertical bool
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
	s := ui.SliderBase(c.Key(key), value, lo, hi).Height(max(handleH, MinTarget)).PaddingX(pad).
		Cursor(ui.CursorPointer).Grow(1).MinWidth(40)
	if !spec.Vertical {
		// The pointer finds the slider MinTarget high; the layout keeps its
		// old height, as a button's does.
		s.Margin(-slop(max(handleH, 32)), 0)
	}
	if spec.Label != "" {
		s.Label(spec.Label)
	}
	if spec.Vertical {
		s.Vertical().Padding(pad)
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

		if spec.Vertical {
			cx := r.X + r.W/2
			y0, y1 := r.Y+pad, r.Y+r.H-pad
			hy := y1 - (y1-y0)*frac
			p.Fill(ui.Rect{X: cx - thick/2, Y: y0, W: thick, H: y1 - y0}, inactive, radius)
			// The active track fills from the middle of the range to the
			// thumb: above the middle for a boost, below for a cut.
			mid := y1
			if lo < 0 && hi > 0 {
				mid = y1 - (y1-y0)*float32(-lo/(hi-lo))
				// The 0 dB mark, a short line beside the track at the
				// level the thumb rests on.
				p.Fill(ui.Rect{X: cx - thick/2 - 9, Y: mid - 1, W: 6, H: 2}, handle, 1)
			}
			if span := max(mid, hy) - min(mid, hy); span > 1 {
				p.Fill(ui.Rect{X: cx - thick/2, Y: min(mid, hy), W: thick, H: span}, active, radius)
			}
			// The thumb, a circle on the track, a shade wider while held.
			d := max(thick+10, 18)
			if pressed {
				d += 2
			}
			p.Fill(ui.Rect{X: cx - d/2, Y: hy - d/2, W: d, H: d}, handle, d/2)
			if focused {
				p.Stroke(ui.Rect{X: cx - d/2 - 3, Y: hy - d/2 - 3, W: d + 6, H: d + 6}, sc.Secondary, 6, 2)
			}
			return
		}

		if spec.Hue {
			track := ui.Rect{X: x0, Y: cy - thick/2, W: x1 - x0, H: thick}
			p.Clip(track, radius, func() {
				w := track.W / hueSteps
				for i := range hueSteps {
					p.FillGradient(ui.Rect{X: track.X + w*float32(i), Y: track.Y, W: w + 0.75, H: track.H},
						ui.LinearGradient{From: hueStrip[i], To: hueStrip[i+1], Angle: 90, Oklab: true}, 0)
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
				phase := float32(0)
				if spec.Waving {
					animatePaint(p)
					// The wave travels from the handle back to the start.
					phase = wavePhase(p.Now().UnixMilli())
				}
				var path ui.Path
				first := true
				for x := x0; ; x += 1.5 {
					if x > end {
						x = end
					}
					ramp := min((x-x0)/10, 1) // the wave grows out of the start
					y := cy + amplitude*ramp*float32(math.Sin(2*math.Pi*float64((x-x0-phase)/waveWavelength)))
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
