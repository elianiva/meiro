// Package m3 is a Material 3 Expressive component kit for MyGo.
//
// A Theme is derived from one seed colour: its tonal palettes become a Scheme
// of colour roles, and every component here reads its colours, shapes, type
// and motion from the active theme, so changing the seed, the palette style or
// the light/dark mode restyles the whole interface at once.
//
//	th := m3.New(m3.Config{Seed: ui.Hex("#6750a4")}, c.Theme().Dark)
//	m3.Provide(c, th) // once at the top of the view, every frame
//	m3.Button(c, m3.ButtonSpec{Label: "Play", Icon: m3.IconPlay}).Clicked()
//
// Provide also sets MyGo's own theme from the scheme, so the widgets this kit
// leaves to MyGo (text inputs, scroll bars, tooltips, toasts) follow it too.
package m3

import (
	"math"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
)

// Mode says which appearance a theme takes.
type Mode int

const (
	// System follows the desktop's light or dark appearance.
	System Mode = iota
	Light
	Dark
)

func (m Mode) String() string {
	switch m {
	case Light:
		return "Light"
	case Dark:
		return "Dark"
	}
	return "System"
}

// Modes lists every mode in the order a picker shows them. Each call returns
// a fresh slice, so callers cannot change the collection.
func Modes() []Mode { return []Mode{System, Light, Dark} }

// Config is everything a theme derives from.
type Config struct {
	Seed  ui.Color
	Style Style
	Mode  Mode
	// Font is the typeface of the whole interface, applied through MyGo's
	// theme once it is registered with ui.RegisterFont; empty is the
	// system's font.
	Font string
}

// DefaultSeed is Material's baseline purple.
var DefaultSeed = ui.Hex("#6750a4")

// Theme is a resolved appearance: the scheme every component draws with.
type Theme struct {
	Config
	Dark     bool
	Palettes Palettes
	Scheme   Scheme
}

// New resolves a config. systemDark is the desktop's appearance, which only
// matters while the mode is System.
func New(cfg Config, systemDark bool) *Theme {
	if cfg.Seed == (ui.Color{}) {
		cfg.Seed = DefaultSeed
	}
	dark := systemDark
	switch cfg.Mode {
	case Light:
		dark = false
	case Dark:
		dark = true
	}
	p := NewPalettes(cfg.Seed, cfg.Style)
	return &Theme{Config: cfg, Dark: dark, Palettes: p, Scheme: NewScheme(p, dark)}
}

var (
	activeMu sync.RWMutex
	active   = map[ui.Services]*Theme{}
	baseline = sync.OnceValue(func() *Theme { return New(Config{}, false) })
)

// Provide makes th the theme components draw with in the window of c, and
// sets MyGo's theme from it. Call it at the top of the view: a window has one
// active theme, and windows do not share it.
func Provide(c *ui.Context, th *Theme) {
	activeMu.Lock()
	active[c.Services()] = th
	activeMu.Unlock()
	c.SetTheme(th.UI(c.Theme()))
}

// Of returns the theme set by Provide in the window of c, or the baseline one
// before any.
func Of(c *ui.Context) *Theme {
	activeMu.RLock()
	t := active[c.Services()]
	activeMu.RUnlock()
	if t != nil {
		return t
	}
	return baseline()
}

// UI maps the scheme onto MyGo's own theme, so its widgets match.
func (t *Theme) UI(base *ui.Theme) *ui.Theme {
	s := t.Scheme
	u := *base
	u.Dark = t.Dark
	u.Background = s.Surface
	u.Surface = s.SurfaceContainerHigh
	u.SurfaceHover = Layer(s.SurfaceContainerHigh, s.OnSurface, StateHover)
	u.SurfacePressed = Layer(s.SurfaceContainerHigh, s.OnSurface, StatePress)
	u.Border = s.OutlineVariant
	u.Text = s.OnSurface
	u.TextMuted = s.OnSurfaceVariant
	u.Accent = s.Primary
	u.AccentHover = Layer(s.Primary, s.OnPrimary, StateHover)
	u.AccentPressed = Layer(s.Primary, s.OnPrimary, StatePress)
	u.AccentText = s.OnPrimary
	u.Danger = s.Error
	u.Selection = s.Primary.Alpha(0.32)
	u.Focus = s.Secondary
	u.Inverse = s.InverseSurface
	u.InverseText = s.InverseOnSurface
	u.Scrollbar = s.OnSurfaceVariant.Alpha(0.45)
	u.ScrollbarWidth = 8
	u.Radius = Medium
	u.Spacing = 4
	u.FontSize = 14
	u.Font = t.Font
	return &u
}

// State layer opacities: how far the content colour tints a surface while it
// is hovered, focused, pressed or dragged.
const (
	StateHover   float32 = 0.08
	StateFocus   float32 = 0.10
	StatePress   float32 = 0.10
	StateDrag    float32 = 0.16
	StateDisable float32 = 0.38
)

// The shape scale, as corner radii in DIPs.
const (
	None            float32 = 0
	ExtraSmall      float32 = 4
	Small           float32 = 8
	Medium          float32 = 12
	Large           float32 = 16
	LargeIncreased  float32 = 20
	ExtraLarge      float32 = 28
	ExtraLargeInc   float32 = 32
	ExtraExtraLarge float32 = 48
	Full            float32 = 9999
)

// Elevation gives an element the shadow of a Material level, 0 to 5. One
// shadow layer is drawn per level, not Material's two: the second, wider
// layer covered a larger patch than the first, and the shadow shader works
// across the whole of it, so dropping it halves what an elevated element
// costs to draw while it still reads as raised.
func Elevation(c *ui.Context, e ui.Element, level int) ui.Element {
	t := Of(c)
	k := float32(1)
	if t.Dark {
		k = 1.6
	}
	shadow := t.Scheme.Shadow
	switch level {
	case 1:
		e.Shadow(0, 1, 2, 0, shadow.Alpha(0.30*k))
	case 2:
		e.Shadow(0, 1, 2, 0, shadow.Alpha(0.30*k))
	case 3:
		e.Shadow(0, 1, 3, 0, shadow.Alpha(0.30*k))
	case 4:
		e.Shadow(0, 2, 3, 0, shadow.Alpha(0.30*k))
	case 5:
		e.Shadow(0, 4, 4, 0, shadow.Alpha(0.30*k))
	}
	return e
}

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

func (s Spring) response(t float64) float64 {
	w0 := math.Sqrt(s.Stiffness)
	z := s.Damping
	if z >= 1 {
		return 1 - math.Exp(-w0*t)*(1+w0*t)
	}
	wd := w0 * math.Sqrt(1-z*z)
	return 1 - math.Exp(-z*w0*t)*(math.Cos(wd*t)+z*w0/wd*math.Sin(wd*t))
}

// Duration is how long the spring takes to settle within a fifth of a
// percent of its target.
func (s Spring) Duration() time.Duration {
	w0 := math.Sqrt(s.Stiffness)
	secs := math.Log(1/0.002) / (s.Damping * w0)
	return time.Duration(secs * float64(time.Second))
}

// Ease is the spring's step response over its Duration, as an easing: it may
// pass 1 and come back when the spring is underdamped.
func (s Spring) Ease() ui.Easing {
	total := float64(s.Duration()) / float64(time.Second)
	end := s.response(total)
	return func(u float32) float32 {
		x := float64(u)
		return float32(s.response(x*total) + (1-end)*x)
	}
}

// Move is the transition of an element that should move, resize and recolour
// along a spring.
func Move(s Spring) ui.ElementTransition {
	return ui.ElementTransition{Duration: s.Duration(), Ease: s.Ease()}
}

// Fade is the transition of an element that only changes colour.
func Fade(s Spring) ui.ElementTransition {
	return ui.ElementTransition{Colors: true, Duration: s.Duration(), Ease: s.Ease()}
}

// Animate eases a value of an element to its target along a spring.
func Animate(e ui.Element, key any, target float32, s Spring) float32 {
	return e.AnimateWith(key, target, s.Duration(), s.Ease())
}

// Role is an entry of the type scale.
type Role struct {
	Size     float32
	Line     float32
	Weight   int
	Emphasis int // the weight when emphasised, the way Expressive stresses headings
	Tracking float32
}

// The Material 3 type scale. Emphasised weights are what Expressive uses to
// give titles and headlines more presence.
var (
	DisplayLarge   = Role{57, 64, 400, 500, -0.25}
	DisplayMedium  = Role{45, 52, 400, 500, 0}
	DisplaySmall   = Role{36, 44, 400, 500, 0}
	HeadlineLarge  = Role{32, 40, 400, 600, 0}
	HeadlineMedium = Role{28, 36, 400, 600, 0}
	HeadlineSmall  = Role{24, 32, 400, 600, 0}
	TitleLarge     = Role{22, 28, 400, 600, 0}
	TitleMedium    = Role{16, 24, 500, 700, 0.15}
	TitleSmall     = Role{14, 20, 500, 700, 0.1}
	BodyLarge      = Role{16, 24, 400, 500, 0.5}
	BodyMedium     = Role{14, 20, 400, 500, 0.25}
	BodySmall      = Role{12, 16, 400, 500, 0.4}
	LabelLarge     = Role{14, 20, 500, 700, 0.1}
	LabelMedium    = Role{12, 16, 500, 700, 0.5}
	LabelSmall     = Role{11, 16, 500, 700, 0.5}
)

// Style applies the role to an element's text.
func (r Role) Style(e ui.Element, emphasized bool) ui.Element {
	w := r.Weight
	if emphasized {
		w = r.Emphasis
	}
	return e.FontSize(r.Size).FixedLineHeight(r.Line).FontWeight(w).LetterSpacing(r.Tracking)
}

// Text shows text in a role of the type scale, in the colour of text on a
// surface.
func Text(c *ui.Context, r Role, s string) ui.Element {
	return r.Style(ui.Text(c, s), false).TextColor(Of(c).Scheme.OnSurface)
}

// EmphasizedText is Text in the role's emphasised weight.
func EmphasizedText(c *ui.Context, r Role, s string) ui.Element {
	return r.Style(ui.Text(c, s), true).TextColor(Of(c).Scheme.OnSurface)
}
