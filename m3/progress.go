package m3

import (
	"math"
	"time"

	"github.com/egoist/mygo/ui"
)

const animateRate = 60

func smoothstep(f float64) float64 { return f * f * (3 - 2*f) }

func animatePaint(p *ui.Painter) {
	p.AnimationFrameRate(animateRate)
}

type morph struct {
	n int
	a float64
}

var morphs = []morph{{0, 0}, {3, 0.17}, {4, 0.11}, {6, 0.11}, {8, 0.09}, {5, 0.14}}

func LoadingIndicator(c *ui.Context, size float32, contained bool) ui.Element {
	sc := Of(c).Scheme
	if contained {
		return loadingIndicator(c, size, sc.OnPrimaryContainer, sc.PrimaryContainer)
	}
	return loadingIndicator(c, size, sc.Primary, ui.Transparent)
}

func LoadingIndicatorIn(c *ui.Context, size float32, colour ui.Color) ui.Element {
	return loadingIndicator(c, size, colour, ui.Transparent)
}

func loadingIndicator(c *ui.Context, size float32, shape, disc ui.Color) ui.Element {
	contained := disc.A != 0
	e := ui.Box(c).Size(size, size).Shrink(0).Label("Loading")
	if contained {
		e.Radius(Full).Background(disc)
	}
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		animatePaint(p)
		const step = 700 * time.Millisecond
		ms := p.Now().UnixMilli()
		phase := float64(ms) / float64(step.Milliseconds())
		i := int(math.Floor(phase)) % len(morphs)
		f := phase - math.Floor(phase)
		s := smoothstep(f)
		from, to := morphs[i], morphs[(i+1)%len(morphs)]
		spin := float64(ms%4200) / 4200 * 2 * math.Pi

		cx, cy := r.X+r.W/2, r.Y+r.H/2
		reach := float64(r.W) / 2
		if contained {
			reach *= 0.62
		} else {
			reach *= 0.86
		}
		var path ui.Path
		const points = 120
		for k := range points {
			t := float64(k) / points * 2 * math.Pi
			rad := 1 + (1-s)*from.a*math.Cos(float64(from.n)*t+spin*float64(from.n)) +
				s*to.a*math.Cos(float64(to.n)*t+spin*float64(to.n))
			rad *= reach / 1.12
			x := cx + float32(rad*math.Cos(t+spin*0.35))
			y := cy + float32(rad*math.Sin(t+spin*0.35))
			if k == 0 {
				path.MoveTo(x, y)
			} else {
				path.LineTo(x, y)
			}
		}
		path.Close()
		p.FillPath(&path, shape)
	})
	return e
}

func Equalizer(c *ui.Context, size float32, playing bool, color ui.Color) ui.Element {
	e := ui.Box(c).Size(size, size).Shrink(0)
	still := c.Preferences().ReduceMotion
	e.Draw(func(p *ui.Painter, r ui.Rect) {
		if playing && !still {
			animatePaint(p)
		}
		now := float64(p.Now().UnixMilli()) / 1000
		bar := r.W / 5
		for i := range 3 {
			h := float32(0.2)
			if playing {
				h = float32(0.35 + 0.65*(0.5+0.5*math.Sin(now*(5+float64(i)*1.7)+float64(i)*1.9)))
			}
			bh := r.H * h
			x := r.X + bar*float32(2*i) + bar*0.0
			y := r.Y + r.H - bh
			if !playing {
				y = r.Y + (r.H-bh)/2
			}
			p.Fill(ui.Rect{X: x, Y: y, W: bar, H: bh}, color, bar/2)
		}
	})
	return e
}
