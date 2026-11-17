package m3

import (
	"math"

	"github.com/egoist/mygo/ui"
)

type Palette struct {
	Hue    float64
	Chroma float64
}

func (p Palette) Tone(t float64) ui.Color {
	t = math.Max(0, math.Min(100, t))
	return oklchToColor(toneToLightness(t), p.Chroma, p.Hue)
}

func toneToLightness(tone float64) float64 {
	var y float64
	if tone > 8 {
		v := (tone + 16) / 116
		y = v * v * v
	} else {
		y = tone / 903.2963
	}
	return math.Cbrt(y)
}

func HueOf(c ui.Color) (hue, chroma float64) {
	_, a, b := colorToOklab(c)
	chroma = math.Hypot(a, b)
	hue = math.Atan2(b, a) * 180 / math.Pi
	if hue < 0 {
		hue += 360
	}
	return hue, chroma
}

func FromHue(hue float64) ui.Color {
	return Palette{Hue: hue, Chroma: 0.14}.Tone(55)
}

func srgbToLinear(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func linearToSRGB(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func colorToOklab(c ui.Color) (l, a, b float64) {
	r := srgbToLinear(float64(c.R) / 255)
	g := srgbToLinear(float64(c.G) / 255)
	bl := srgbToLinear(float64(c.B) / 255)
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*bl)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*bl)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*bl)
	l = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a = 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	b = 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	return
}

func oklabToLinear(l, a, b float64) (r, g, bl float64) {
	lc := l + 0.3963377774*a + 0.2158037573*b
	mc := l - 0.1055613458*a - 0.0638541728*b
	sc := l - 0.0894841775*a - 1.2914855480*b
	lc, mc, sc = lc*lc*lc, mc*mc*mc, sc*sc*sc
	r = 4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc
	g = -1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc
	bl = -0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc
	return
}

func inGamut(r, g, b float64) bool {
	const eps = 1e-4
	return r >= -eps && r <= 1+eps && g >= -eps && g <= 1+eps && b >= -eps && b <= 1+eps
}

func oklchToColor(l, c, h float64) ui.Color {
	if l <= 0 {
		return ui.RGB(0, 0, 0)
	}
	if l >= 1 {
		return ui.RGB(255, 255, 255)
	}
	rad := h * math.Pi / 180
	cos, sin := math.Cos(rad), math.Sin(rad)
	r, g, b := oklabToLinear(l, c*cos, c*sin)
	if !inGamut(r, g, b) {
		lo, hi := 0.0, c
		for range 22 {
			mid := (lo + hi) / 2
			if mr, mg, mb := oklabToLinear(l, mid*cos, mid*sin); inGamut(mr, mg, mb) {
				lo = mid
			} else {
				hi = mid
			}
		}
		r, g, b = oklabToLinear(l, lo*cos, lo*sin)
	}
	to8 := func(v float64) uint8 {
		v = math.Max(0, math.Min(1, linearToSRGB(math.Max(0, math.Min(1, v)))))
		return uint8(math.Round(v * 255))
	}
	return ui.RGB(to8(r), to8(g), to8(b))
}

type Style int

const (
	TonalSpot Style = iota
	Vibrant
	Expressive
	Neutral
	Monochrome
)

func Styles() []Style { return []Style{TonalSpot, Vibrant, Expressive, Neutral, Monochrome} }

func (s Style) String() string {
	switch s {
	case Vibrant:
		return "Vibrant"
	case Expressive:
		return "Expressive"
	case Neutral:
		return "Neutral"
	case Monochrome:
		return "Monochrome"
	}
	return "Tonal spot"
}

func (s Style) Blurb() string {
	switch s {
	case Vibrant:
		return "Bold and tinted"
	case Expressive:
		return "Accents drift away"
	case Neutral:
		return "A trace of colour"
	case Monochrome:
		return "No colour at all"
	}
	return "Calm and balanced"
}

type recipe struct {
	primary                  float64
	secondaryTurn, secondary float64
	tertiaryTurn, tertiary   float64
	neutral, neutralVariant  float64
}

func (s Style) recipe() recipe {
	switch s {
	case Vibrant:
		return recipe{primary: 0.19, secondaryTurn: 18, secondary: 0.085, tertiaryTurn: 60, tertiary: 0.12, neutral: 0.016, neutralVariant: 0.03}
	case Expressive:
		return recipe{primary: 0.15, secondaryTurn: 40, secondary: 0.08, tertiaryTurn: 120, tertiary: 0.13, neutral: 0.014, neutralVariant: 0.028}
	case Neutral:
		return recipe{primary: 0.028, secondaryTurn: 0, secondary: 0.018, tertiaryTurn: 40, tertiary: 0.03, neutral: 0.006, neutralVariant: 0.012}
	case Monochrome:
		return recipe{}
	}
	return recipe{primary: 0.125, secondaryTurn: 0, secondary: 0.042, tertiaryTurn: 60, tertiary: 0.07, neutral: 0.008, neutralVariant: 0.017}
}

type Palettes struct {
	Primary, Secondary, Tertiary, Neutral, NeutralVariant, Error Palette
}

func NewPalettes(seed ui.Color, style Style) Palettes {
	hue, _ := HueOf(seed)
	r := style.recipe()
	return Palettes{
		Primary:        Palette{hue, r.primary},
		Secondary:      Palette{math.Mod(hue+r.secondaryTurn, 360), r.secondary},
		Tertiary:       Palette{math.Mod(hue+r.tertiaryTurn, 360), r.tertiary},
		Neutral:        Palette{hue, r.neutral},
		NeutralVariant: Palette{hue, r.neutralVariant},
		Error:          Palette{29, 0.19},
	}
}

type Scheme struct {
	Dark bool

	Primary, OnPrimary, PrimaryContainer, OnPrimaryContainer         ui.Color
	Secondary, OnSecondary, SecondaryContainer, OnSecondaryContainer ui.Color
	Tertiary, OnTertiary, TertiaryContainer, OnTertiaryContainer     ui.Color
	Error, OnError, ErrorContainer, OnErrorContainer                 ui.Color

	Surface, OnSurface, OnSurfaceVariant                          ui.Color
	SurfaceDim, SurfaceBright                                     ui.Color
	SurfaceContainerLowest, SurfaceContainerLow, SurfaceContainer ui.Color
	SurfaceContainerHigh, SurfaceContainerHighest                 ui.Color
	Outline, OutlineVariant                                       ui.Color
	InverseSurface, InverseOnSurface, InversePrimary              ui.Color
	Scrim, Shadow                                                 ui.Color
}

func NewScheme(p Palettes, dark bool) Scheme {
	pick := func(light, darkTone float64) float64 {
		if dark {
			return darkTone
		}
		return light
	}
	s := Scheme{Dark: dark}

	s.Primary = p.Primary.Tone(pick(40, 80))
	s.OnPrimary = p.Primary.Tone(pick(100, 20))
	s.PrimaryContainer = p.Primary.Tone(pick(90, 30))
	s.OnPrimaryContainer = p.Primary.Tone(pick(10, 90))

	s.Secondary = p.Secondary.Tone(pick(40, 80))
	s.OnSecondary = p.Secondary.Tone(pick(100, 20))
	s.SecondaryContainer = p.Secondary.Tone(pick(90, 30))
	s.OnSecondaryContainer = p.Secondary.Tone(pick(10, 90))

	s.Tertiary = p.Tertiary.Tone(pick(40, 80))
	s.OnTertiary = p.Tertiary.Tone(pick(100, 20))
	s.TertiaryContainer = p.Tertiary.Tone(pick(90, 30))
	s.OnTertiaryContainer = p.Tertiary.Tone(pick(10, 90))

	s.Error = p.Error.Tone(pick(40, 80))
	s.OnError = p.Error.Tone(pick(100, 20))
	s.ErrorContainer = p.Error.Tone(pick(90, 30))
	s.OnErrorContainer = p.Error.Tone(pick(10, 90))

	s.Surface = p.Neutral.Tone(pick(98, 6))
	s.OnSurface = p.Neutral.Tone(pick(10, 90))
	s.OnSurfaceVariant = p.NeutralVariant.Tone(pick(30, 80))
	s.SurfaceDim = p.Neutral.Tone(pick(87, 6))
	s.SurfaceBright = p.Neutral.Tone(pick(98, 24))
	s.SurfaceContainerLowest = p.Neutral.Tone(pick(100, 4))
	s.SurfaceContainerLow = p.Neutral.Tone(pick(96, 10))
	s.SurfaceContainer = p.Neutral.Tone(pick(94, 12))
	s.SurfaceContainerHigh = p.Neutral.Tone(pick(92, 17))
	s.SurfaceContainerHighest = p.Neutral.Tone(pick(90, 22))

	s.Outline = p.NeutralVariant.Tone(pick(50, 60))
	s.OutlineVariant = p.NeutralVariant.Tone(pick(80, 30))
	s.InverseSurface = p.Neutral.Tone(pick(20, 90))
	s.InverseOnSurface = p.Neutral.Tone(pick(95, 20))
	s.InversePrimary = p.Primary.Tone(pick(80, 40))
	s.Scrim = ui.RGB(0, 0, 0)
	s.Shadow = ui.RGB(0, 0, 0)
	return s
}

func Layer(base, over ui.Color, opacity float32) ui.Color {
	return over.Alpha(opacity).Over(base)
}

func Contrast(a, b ui.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(c ui.Color) float64 {
	return 0.2126*srgbToLinear(float64(c.R)/255) + 0.7152*srgbToLinear(float64(c.G)/255) + 0.0722*srgbToLinear(float64(c.B)/255)
}

func (s Scheme) Mix(o Scheme, t float32) Scheme {
	out := s
	out.Primary = s.Primary.Mix(o.Primary, t)
	out.OnPrimary = s.OnPrimary.Mix(o.OnPrimary, t)
	out.PrimaryContainer = s.PrimaryContainer.Mix(o.PrimaryContainer, t)
	out.OnPrimaryContainer = s.OnPrimaryContainer.Mix(o.OnPrimaryContainer, t)

	out.Secondary = s.Secondary.Mix(o.Secondary, t)
	out.OnSecondary = s.OnSecondary.Mix(o.OnSecondary, t)
	out.SecondaryContainer = s.SecondaryContainer.Mix(o.SecondaryContainer, t)
	out.OnSecondaryContainer = s.OnSecondaryContainer.Mix(o.OnSecondaryContainer, t)

	out.Tertiary = s.Tertiary.Mix(o.Tertiary, t)
	out.OnTertiary = s.OnTertiary.Mix(o.OnTertiary, t)
	out.TertiaryContainer = s.TertiaryContainer.Mix(o.TertiaryContainer, t)
	out.OnTertiaryContainer = s.OnTertiaryContainer.Mix(o.OnTertiaryContainer, t)

	out.Error = s.Error.Mix(o.Error, t)
	out.OnError = s.OnError.Mix(o.OnError, t)
	out.ErrorContainer = s.ErrorContainer.Mix(o.ErrorContainer, t)
	out.OnErrorContainer = s.OnErrorContainer.Mix(o.OnErrorContainer, t)

	out.Surface = s.Surface.Mix(o.Surface, t)
	out.OnSurface = s.OnSurface.Mix(o.OnSurface, t)
	out.OnSurfaceVariant = s.OnSurfaceVariant.Mix(o.OnSurfaceVariant, t)
	out.SurfaceDim = s.SurfaceDim.Mix(o.SurfaceDim, t)
	out.SurfaceBright = s.SurfaceBright.Mix(o.SurfaceBright, t)
	out.SurfaceContainerLowest = s.SurfaceContainerLowest.Mix(o.SurfaceContainerLowest, t)
	out.SurfaceContainerLow = s.SurfaceContainerLow.Mix(o.SurfaceContainerLow, t)
	out.SurfaceContainer = s.SurfaceContainer.Mix(o.SurfaceContainer, t)
	out.SurfaceContainerHigh = s.SurfaceContainerHigh.Mix(o.SurfaceContainerHigh, t)
	out.SurfaceContainerHighest = s.SurfaceContainerHighest.Mix(o.SurfaceContainerHighest, t)

	out.Outline = s.Outline.Mix(o.Outline, t)
	out.OutlineVariant = s.OutlineVariant.Mix(o.OutlineVariant, t)
	out.InverseSurface = s.InverseSurface.Mix(o.InverseSurface, t)
	out.InverseOnSurface = s.InverseOnSurface.Mix(o.InverseOnSurface, t)
	out.InversePrimary = s.InversePrimary.Mix(o.InversePrimary, t)
	out.Scrim = s.Scrim.Mix(o.Scrim, t)
	out.Shadow = s.Shadow.Mix(o.Shadow, t)

	if t >= 0.5 {
		out.Dark = o.Dark
	}
	return out
}

type Colors struct {
	Light, Dark map[string]ui.Color
}

func (s *Scheme) Role(name string) *ui.Color {
	switch name {
	case "primary":
		return &s.Primary
	case "on_primary":
		return &s.OnPrimary
	case "primary_container":
		return &s.PrimaryContainer
	case "on_primary_container":
		return &s.OnPrimaryContainer
	case "secondary":
		return &s.Secondary
	case "on_secondary":
		return &s.OnSecondary
	case "secondary_container":
		return &s.SecondaryContainer
	case "on_secondary_container":
		return &s.OnSecondaryContainer
	case "tertiary":
		return &s.Tertiary
	case "on_tertiary":
		return &s.OnTertiary
	case "tertiary_container":
		return &s.TertiaryContainer
	case "on_tertiary_container":
		return &s.OnTertiaryContainer
	case "error":
		return &s.Error
	case "on_error":
		return &s.OnError
	case "error_container":
		return &s.ErrorContainer
	case "on_error_container":
		return &s.OnErrorContainer
	case "surface":
		return &s.Surface
	case "on_surface":
		return &s.OnSurface
	case "on_surface_variant":
		return &s.OnSurfaceVariant
	case "surface_dim":
		return &s.SurfaceDim
	case "surface_bright":
		return &s.SurfaceBright
	case "surface_container_lowest":
		return &s.SurfaceContainerLowest
	case "surface_container_low":
		return &s.SurfaceContainerLow
	case "surface_container":
		return &s.SurfaceContainer
	case "surface_container_high":
		return &s.SurfaceContainerHigh
	case "surface_container_highest":
		return &s.SurfaceContainerHighest
	case "outline":
		return &s.Outline
	case "outline_variant":
		return &s.OutlineVariant
	case "inverse_surface":
		return &s.InverseSurface
	case "inverse_on_surface":
		return &s.InverseOnSurface
	case "inverse_primary":
		return &s.InversePrimary
	case "scrim":
		return &s.Scrim
	case "shadow":
		return &s.Shadow
	}
	return nil
}

func (c *Colors) apply(s *Scheme) {
	roles := c.Light
	if s.Dark {
		roles = c.Dark
	}
	for name, colour := range roles {
		if role := s.Role(name); role != nil {
			*role = colour
		}
	}
}
