package m3

import (
	"sync"

	"github.com/egoist/mygo/ui"
)

type Mode int

const (
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

func Modes() []Mode { return []Mode{System, Light, Dark} }

type Config struct {
	Seed   ui.Color
	Style  Style
	Mode   Mode
	Font   string
	Colors *Colors
}

var DefaultSeed = ui.Hex("#6750a4")

type Theme struct {
	Config
	Dark     bool
	Palettes Palettes
	Scheme   Scheme
	ui       *ui.Theme
	uiBase   ui.Theme
	uiScheme Scheme
	uiSet    bool
}

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
	scheme := NewScheme(p, dark)
	if cfg.Colors != nil {
		cfg.Colors.apply(&scheme)
	}
	return &Theme{Config: cfg, Dark: dark, Palettes: p, Scheme: scheme}
}

var (
	activeMu sync.RWMutex
	active   = map[ui.Services]*Theme{}
	baseline = sync.OnceValue(func() *Theme { return New(Config{}, false) })
)

func Provide(c *ui.Context, th *Theme) {
	activeMu.Lock()
	active[c.Services()] = th
	activeMu.Unlock()
	c.SetTheme(th.UI(c.Theme()))
}

func Of(c *ui.Context) *Theme {
	activeMu.RLock()
	t := active[c.Services()]
	activeMu.RUnlock()
	if t != nil {
		return t
	}
	return baseline()
}

func (t *Theme) UI(base *ui.Theme) *ui.Theme {
	s := t.Scheme
	if t.uiSet && t.uiScheme == s && t.uiBase == *base {
		return t.ui
	}
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
	u.Scrollbar = s.Primary.Alpha(0.72)
	u.ScrollbarWidth = 4
	u.Radius = Medium
	u.Spacing = 4
	u.FontSize = 14
	u.Font = t.Font
	t.ui, t.uiScheme, t.uiBase, t.uiSet = &u, s, *base, true
	return t.ui
}

const (
	StateHover   float32 = 0.08
	StateFocus   float32 = 0.10
	StatePress   float32 = 0.10
	StateDrag    float32 = 0.16
	StateDisable float32 = 0.38
)

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

type Role struct {
	Size     float32
	Line     float32
	Weight   int
	Emphasis int
	Tracking float32
}

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

func (r Role) Style(e ui.Element, emphasized bool) ui.Element {
	w := r.Weight
	if emphasized {
		w = r.Emphasis
	}
	return e.FontSize(r.Size).FixedLineHeight(r.Line).FontWeight(w).LetterSpacing(r.Tracking)
}

func Text(c *ui.Context, r Role, s string) ui.Element {
	return r.Style(ui.Text(c, s), false).TextColor(Of(c).Scheme.OnSurface)
}

func EmphasizedText(c *ui.Context, r Role, s string) ui.Element {
	return r.Style(ui.Text(c, s), true).TextColor(Of(c).Scheme.OnSurface)
}

func RichText(c *ui.Context, r Role, spans ...ui.Span) ui.Element {
	return r.Style(ui.RichText(c, spans...), false).TextColor(Of(c).Scheme.OnSurface)
}

func EmphasizedRichText(c *ui.Context, r Role, spans ...ui.Span) ui.Element {
	return r.Style(ui.RichText(c, spans...), true).TextColor(Of(c).Scheme.OnSurface)
}
