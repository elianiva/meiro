package m3

import (
	"math"

	"github.com/egoist/mygo/ui"
)

type Spring struct {
	Stiffness float64
	Damping   float64
}

var (
	SpatialFast    = Spring{800, 0.6}
	SpatialDefault = Spring{380, 0.8}
	SpatialSlow    = Spring{200, 0.8}
	EffectsFast    = Spring{3800, 1}
	EffectsDefault = Spring{1600, 1}
	EffectsSlow    = Spring{800, 1}
)

func (s Spring) animation() ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{
		Mass:      1,
		Stiffness: s.Stiffness,
		Damping:   2 * s.Damping * math.Sqrt(s.Stiffness),
	}}
}

var (
	spatialFastAnimation    = SpatialFast.animation()
	spatialDefaultAnimation = SpatialDefault.animation()
	spatialSlowAnimation    = SpatialSlow.animation()
	effectsFastAnimation    = EffectsFast.animation()
	effectsDefaultAnimation = EffectsDefault.animation()
	effectsSlowAnimation    = EffectsSlow.animation()
)

func (s Spring) animationPointer() *ui.Animation {
	switch s {
	case SpatialFast:
		return &spatialFastAnimation
	case SpatialDefault:
		return &spatialDefaultAnimation
	case SpatialSlow:
		return &spatialSlowAnimation
	case EffectsFast:
		return &effectsFastAnimation
	case EffectsDefault:
		return &effectsDefaultAnimation
	case EffectsSlow:
		return &effectsSlowAnimation
	}
	a := s.animation()
	return &a
}

func (s Spring) Motion() ui.ElementTransition {
	return ui.ElementTransition{Animation: s.animationPointer()}
}

func Animate(e ui.Element, key any, target float32, s Spring) float32 {
	return e.AnimateTo(key, target, s.animation())
}

func Fade(s Spring) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Animation: s.animationPointer()}
}

func Rise(e ui.Element, key any, from, to float32, entering bool, s Spring) float32 {
	if entering {
		e.Jump(key, from)
	}
	return Animate(e, key, to, s)
}
