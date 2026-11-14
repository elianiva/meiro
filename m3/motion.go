package m3

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// Spring is a motion token: a damped spring, given by its stiffness and
// damping ratio, that an element settles along. Material 3 Expressive moves
// by springs instead of fixed curves: spatial springs (position, size, shape)
// overshoot a little, and effects springs (colour, opacity) never do.
type Spring struct {
	Stiffness float64
	Damping   float64
}

// The motion tokens of Material 3 Expressive.
var (
	SpatialFast    = Spring{800, 0.6}
	SpatialDefault = Spring{380, 0.8}
	SpatialSlow    = Spring{200, 0.8}
	EffectsFast    = Spring{3800, 1}
	EffectsDefault = Spring{1600, 1}
	EffectsSlow    = Spring{800, 1}
)

// animation is the token as MyGo's motion core sees it: a physical spring
// with the token's stiffness and damping, so a target that changes on the
// way keeps its velocity instead of starting over, and the desktop's
// reduced-motion preference cuts the motion short.
func (s Spring) animation() ui.Animation {
	return ui.Animation{Spring: ui.SpringOptions{
		Mass:      1,
		Stiffness: s.Stiffness,
		Damping:   2 * s.Damping * math.Sqrt(s.Stiffness),
	}}
}

// The tokens' animations, resolved once: a transition holds its animation by
// pointer, so sharing one per token keeps a frame from allocating one for
// every element that moves.
var (
	spatialFastAnimation    = SpatialFast.animation()
	spatialDefaultAnimation = SpatialDefault.animation()
	spatialSlowAnimation    = SpatialSlow.animation()
	effectsFastAnimation    = EffectsFast.animation()
	effectsDefaultAnimation = EffectsDefault.animation()
	effectsSlowAnimation    = EffectsSlow.animation()
)

// animationPointer returns the token's animation for a transition. A token
// outside the set gets one of its own.
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

// Motion is the token as a transition: changes ride the token's spring and
// keep their velocity when they are interrupted.
func (s Spring) Motion() ui.ElementTransition {
	return ui.ElementTransition{Animation: s.animationPointer()}
}

// Animate eases a value of an element to its target along a spring,
// keeping its velocity when the target changes on the way.
func Animate(e ui.Element, key any, target float32, s Spring) float32 {
	return e.AnimateTo(key, target, s.animation())
}

// Fade is the transition of an element that only changes colour.
func Fade(s Spring) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Animation: s.animationPointer()}
}

// Rise is how far below its place, in DIPs, an element is while it comes in
// or goes out: from when entering is true, which is its first frame, and
// easing to to along the token's spring; 0 for an element in its place. The
// caller moves the element by it with its layout, Top or Bottom.
func Rise(e ui.Element, key any, from, to float32, entering bool, s Spring) float32 {
	if entering {
		e.Jump(key, from)
	}
	return Animate(e, key, to, s)
}
