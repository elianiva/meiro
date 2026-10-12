package m3

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Every motion token must ride the motion core without panicking and arrive
// at its target: the spring options mapped from stiffness and damping have
// to be valid for each token.
func TestMotionTokensReachTheirTarget(t *testing.T) {
	tokens := []Spring{SpatialFast, SpatialDefault, SpatialSlow, EffectsFast, EffectsDefault, EffectsSlow}
	for _, token := range tokens {
		var value float32
		tt := ui.NewTester(func(c *ui.Context) {
			box := ui.Box(c)
			value = Animate(box, "x", 100, token)
		}, 200, 200)
		for range 600 {
			tt.Frame()
		}
		if value != 100 {
			t.Fatalf("token %+v settled at %v, want 100", token, value)
		}
	}
}

// With reduced motion the tokens cut to the target at once instead of
// travelling.
func TestMotionTokensCutAtOnceWithReducedMotion(t *testing.T) {
	var value float32 = -1
	tt := ui.NewTester(func(c *ui.Context) {
		box := ui.Box(c)
		value = Animate(box, "x", 100, SpatialDefault)
	}, 200, 200)
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	if value != 100 {
		t.Fatalf("reduced motion gave %v, want 100 at once", value)
	}
}
