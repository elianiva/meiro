package m3

import (
	"github.com/egoist/mygo/ui"
)

type ButtonKind int

const (
	Filled ButtonKind = iota
	Tonal
	Elevated
	Outlined
	TextOnly
)

type ButtonSize int

const (
	ExtraSmall32 ButtonSize = iota
	Small40
	Medium56
	Large96
	ExtraLarge136
)

type buttonDims struct {
	height, pad, icon, gap float32
	label                  Role
	square, pressed        float32
}

func (s ButtonSize) dims() buttonDims {
	switch s {
	case ExtraSmall32:
		return buttonDims{32, 12, 20, 4, LabelLarge, Medium, Small}
	case Medium56:
		return buttonDims{56, 24, 24, 8, TitleMedium, Large, Medium}
	case Large96:
		return buttonDims{96, 48, 32, 12, HeadlineSmall, ExtraLarge, Large}
	case ExtraLarge136:
		return buttonDims{136, 64, 40, 16, HeadlineLarge, ExtraLarge, Large}
	}
	return buttonDims{40, 16, 20, 8, LabelLarge, Medium, Small}
}

type ButtonSpec struct {
	Label            string
	Icon             *ui.SVG
	IconRotation     float32
	Kind             ButtonKind
	Size             ButtonSize
	Square           bool
	Padding          float32
	Toggle, Selected bool
	Disabled         bool
	Key              any
}

func containerColors(s Scheme, kind ButtonKind, toggle, selected bool) (container, content, outline ui.Color) {
	switch kind {
	case Tonal:
		if toggle && selected {
			return s.Secondary, s.OnSecondary, ui.Transparent
		}
		return s.SecondaryContainer, s.OnSecondaryContainer, ui.Transparent
	case Elevated:
		if toggle && selected {
			return s.Primary, s.OnPrimary, ui.Transparent
		}
		return s.SurfaceContainerLow, s.Primary, ui.Transparent
	case Outlined:
		if toggle && selected {
			return s.InverseSurface, s.InverseOnSurface, ui.Transparent
		}
		return ui.Transparent, s.OnSurfaceVariant, s.OutlineVariant
	case TextOnly:
		return ui.Transparent, s.Primary, ui.Transparent
	}
	if toggle && !selected {
		return s.SurfaceContainer, s.OnSurfaceVariant, ui.Transparent
	}
	return s.Primary, s.OnPrimary, ui.Transparent
}

func StateFill(container, content ui.Color, hovered, pressed, focused bool) ui.Color {
	var op float32
	switch {
	case pressed:
		op = StatePress
	case focused:
		op = StateFocus
	case hovered:
		op = StateHover
	}
	if op == 0 {
		return container
	}
	if container.A == 0 {
		return content.Alpha(op)
	}
	return Layer(container, content, op)
}

const MinTarget = 48

func slop(side float32) float32 {
	return max((MinTarget-side)/2, 0)
}

func newButtonBase(c *ui.Context, key any) ui.Element {
	if key != nil {
		return ui.ButtonBase(c.Key(key))
	}
	return ui.ButtonBase(c)
}

func focusRing(c *ui.Context, owner, shape ui.Element, radius float32) {
	owner.FocusRing(false)
	if owner.FocusVisible() {
		color := Of(c).Scheme.Primary
		shape.DrawOver(func(p *ui.Painter, r ui.Rect) {
			p.Stroke(ui.Rect{X: r.X - 3, Y: r.Y - 3, W: r.W + 6, H: r.H + 6}, color, radius+3, 2)
		})
	}
}

const labelMeasureSlack = 4

func buttonLabel(c *ui.Context, role Role, label string, emphasized bool, size float32) ui.Element {
	if size <= 0 {
		size = role.Size
	}
	weight := role.Weight
	if emphasized {
		weight = role.Emphasis
	}
	width, _ := c.MeasureText(0, ui.Span{Text: label, Size: size, Weight: weight, LetterSpacing: role.Tracking})
	return role.Style(ui.Text(c, label), emphasized).FontSize(size).MinWidth(width + labelMeasureSlack)
}

func Button(c *ui.Context, spec ButtonSpec) ui.Element {
	sc := Of(c).Scheme
	d := spec.Size.dims()
	b := newButtonBase(c, spec.Key)
	container, content, outline := containerColors(sc, spec.Kind, spec.Toggle, spec.Selected)

	if spec.Disabled {
		b.Disabled(true)
		content = sc.OnSurface.Alpha(StateDisable)
		if container.A != 0 {
			container = sc.OnSurface.Alpha(0.12)
		}
		if outline.A != 0 {
			outline = sc.OnSurface.Alpha(0.12)
		}
	}
	fill := StateFill(container, content, b.Hovered() && !spec.Disabled, b.Pressed(), b.FocusVisible())

	round := spec.Square == (spec.Toggle && spec.Selected)
	rest := d.height / 2
	if !round {
		rest = d.square
	}
	if b.Pressed() {
		rest = d.pressed
	}
	radius := max(Animate(b, "radius", rest, SpatialFast), 0)

	b.Shrink(0).Justify(ui.Center).AlignItems(ui.Center).TextColor(content).Cursor(ui.CursorPointer)
	if spec.Label != "" {
		b.Label(spec.Label)
	}
	reachBy := slop(d.height)
	b.Height(d.height+2*reachBy).Margin(-reachBy, 0)
	iconRotation := Animate(b, "icon-rotation", spec.IconRotation, SpatialFast)
	b.Children(func() {
		pad := d.pad
		if spec.Padding > 0 {
			pad = spec.Padding
		}
		shape := ui.Row(c).Height(d.height).PaddingX(pad).Gap(d.gap).Radius(radius).Background(fill).
			Justify(ui.Center).AlignItems(ui.Center).TextColor(content).Transition(Fade(EffectsFast))
		focusRing(c, b, shape, radius)
		if outline.A != 0 {
			shape.Border(1, outline)
		}
		if spec.Kind == Elevated && !spec.Disabled {
			Elevation(c, shape, 1)
		}
		shape.Children(func() {
			if spec.Icon != nil {
				ui.Icon(c, spec.Icon).FontSize(d.icon).TextColor(content).Rotate(iconRotation)
			}
			if spec.Label != "" {
				buttonLabel(c, d.label, spec.Label, true, 0).TextColor(content).SingleLine()
			}
		})
	})
	return b
}

type IconButtonSpec struct {
	Icon             *ui.SVG
	Label            string
	Kind             IconButtonKind
	Size             ButtonSize
	Toggle, Selected bool
	Morph            bool
	Disabled         bool
	Loading          bool
	Key              any
	Container        ui.Color
	Dimension        float32
}

type IconButtonKind int

const (
	StandardIcon IconButtonKind = iota
	FilledIcon
	TonalIcon
	OutlinedIcon
)

func IconButton(c *ui.Context, spec IconButtonSpec) ui.Element {
	sc := Of(c).Scheme
	d := spec.Size.dims()
	if spec.Dimension > 0 {
		d = buttonDims{height: spec.Dimension, icon: spec.Dimension * 0.5, square: spec.Dimension * 0.34, pressed: spec.Dimension * 0.26}
	}
	b := newButtonBase(c, spec.Key)

	var container, content, outline ui.Color
	switch spec.Kind {
	case FilledIcon:
		container, content, outline = containerColors(sc, Filled, spec.Toggle, spec.Selected)
	case TonalIcon:
		container, content, outline = containerColors(sc, Tonal, spec.Toggle, spec.Selected)
	case OutlinedIcon:
		container, content, outline = containerColors(sc, Outlined, spec.Toggle, spec.Selected)
	default:
		container, content = spec.Container, sc.OnSurfaceVariant
		if spec.Container.A != 0 {
			content = sc.OnSurface
		}
		if spec.Toggle && spec.Selected {
			content = sc.Primary
		}
	}
	if spec.Disabled {
		b.Disabled(true)
		content = sc.OnSurface.Alpha(StateDisable)
		if container.A != 0 {
			container = sc.OnSurface.Alpha(0.12)
		}
	}
	fill := StateFill(container, content, b.Hovered() && !spec.Disabled, b.Pressed(), b.FocusVisible())

	rest := d.height / 2
	if spec.Selected && (spec.Toggle || spec.Morph) {
		rest = d.square
	}
	if b.Pressed() {
		rest = d.pressed
	}
	radius := max(Animate(b, "radius", rest, SpatialFast), 0)

	side, reachBy := d.height, slop(d.height)
	b.Center().Shrink(0).TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast))
	if reachBy > 0 {
		b.Size(MinTarget, MinTarget).Margin(-reachBy)
	} else {
		b.Size(side, side)
	}
	if spec.Label != "" {
		b.Label(spec.Label).Tooltip(spec.Label)
	}
	if spec.Loading {
		b.Disabled(true)
	}
	b.Children(func() {
		shape := ui.Box(c).Size(side, side).Radius(radius).Background(fill).Center()
		focusRing(c, b, shape, radius)
		if outline.A != 0 {
			shape.Border(1, outline)
		}
		shape.Children(func() {
			if spec.Loading {
				loadingIndicator(c, d.icon*1.4, content, ui.Transparent)
				return
			}
			ui.Icon(c, spec.Icon).FontSize(d.icon).TextColor(content)
		})
	})
	return b
}

type FABSpec struct {
	Icon   *ui.SVG
	Label  string
	Size   FABSize
	Tone   FABTone
	Key    any
	Radius float32
}

type FABSize int

const (
	FABMedium FABSize = iota
	FABSmall
	FABLarge
)

type FABTone int

const (
	FABPrimaryContainer FABTone = iota
	FABSecondaryContainer
	FABTertiaryContainer
	FABPrimary
)

func FAB(c *ui.Context, spec FABSpec) ui.Element {
	sc := Of(c).Scheme
	b := newButtonBase(c, spec.Key)
	var container, content ui.Color
	switch spec.Tone {
	case FABSecondaryContainer:
		container, content = sc.SecondaryContainer, sc.OnSecondaryContainer
	case FABTertiaryContainer:
		container, content = sc.TertiaryContainer, sc.OnTertiaryContainer
	case FABPrimary:
		container, content = sc.Primary, sc.OnPrimary
	default:
		container, content = sc.PrimaryContainer, sc.OnPrimaryContainer
	}
	size, radius, icon := float32(56), Large, float32(24)
	switch spec.Size {
	case FABSmall:
		size, radius, icon = 40, Medium, 24
	case FABLarge:
		size, radius, icon = 96, ExtraLarge, 36
	}
	if spec.Radius > 0 {
		radius = spec.Radius
	}
	if b.Pressed() {
		radius = max(radius/2, Small/2)
	}
	radius = max(Animate(b, "radius", radius, SpatialFast), 0)
	b.Height(size).Radius(radius).Background(StateFill(container, content, b.Hovered(), b.Pressed(), b.FocusVisible())).
		Justify(ui.Center).AlignItems(ui.Center).Gap(8).Shrink(0).
		TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast))
	focusRing(c, b, b, radius)
	if spec.Label == "" {
		b.Width(size)
		if spec.Icon != nil {
			b.Label("Action")
		}
	} else {
		b.PaddingX(16).Label(spec.Label)
	}
	if b.Hovered() {
		Elevation(c, b, 4)
	} else {
		Elevation(c, b, 3)
	}
	b.Children(func() {
		ui.Icon(c, spec.Icon).FontSize(icon).TextColor(content)
		if spec.Label != "" {
			buttonLabel(c, LabelLarge, spec.Label, true, 16).TextColor(content).SingleLine()
		}
	})
	return b
}

func Chip(c *ui.Context, label string, selected bool, key any) ui.Element {
	sc := Of(c).Scheme
	b := newButtonBase(c, key)
	container, content, outline := ui.Transparent, sc.OnSurfaceVariant, sc.OutlineVariant
	if selected {
		container, content, outline = sc.SecondaryContainer, sc.OnSecondaryContainer, ui.Transparent
	}
	b.Height(MinTarget).Margin(-slop(32), 0).Shrink(0).Justify(ui.Center).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Label(label)
	b.Children(func() {
		shape := ui.Row(c).Height(32).PaddingX(12).Gap(8).Radius(Small).Justify(ui.Center).AlignItems(ui.Center).
			Background(StateFill(container, content, b.Hovered(), b.Pressed(), b.FocusVisible())).
			TextColor(content).Transition(Fade(EffectsFast))
		focusRing(c, b, shape, Small)
		if outline.A != 0 {
			shape.Border(1, outline)
		}
		shape.Children(func() {
			if selected {
				ui.Icon(c, IconCheck).FontSize(18).TextColor(content)
			}
			buttonLabel(c, LabelLarge, label, false, 0).TextColor(content).SingleLine()
		})
	})
	return b
}

func IconChip(c *ui.Context, icon *ui.SVG, label string, key any) ui.Element {
	sc := Of(c).Scheme
	b := newButtonBase(c, key)
	b.Size(MinTarget, MinTarget).Margin(-slop(32)).Center().Shrink(0).Cursor(ui.CursorPointer).Label(label)
	b.Children(func() {
		shape := ui.Box(c).Size(32, 32).Radius(Small).Center().TextColor(sc.OnSurfaceVariant).Transition(Fade(EffectsFast)).
			Background(StateFill(ui.Transparent, sc.OnSurfaceVariant, b.Hovered(), b.Pressed(), b.FocusVisible())).
			Border(1, sc.OutlineVariant)
		focusRing(c, b, shape, Small)
		shape.Children(func() {
			ui.Icon(c, icon).FontSize(18).TextColor(sc.OnSurfaceVariant)
		})
	})
	return b
}

func ButtonGroup(c *ui.Context, key any, selected *int, labels []string, icons []*ui.SVG) bool {
	sc := Of(c).Scheme
	group := ui.SegmentedBase(c.Key(key), selected, len(labels))
	group.Track.Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		for i, label := range labels {
			on := i == *selected
			container, content := sc.SecondaryContainer, sc.OnSecondaryContainer
			if on {
				container, content = sc.Secondary, sc.OnSecondary
			}
			seg := group.Segment(i)
			outer, inner := float32(20), Small
			left, right := inner, inner
			if i == 0 {
				left = outer
			}
			if i == len(labels)-1 {
				right = outer
			}
			if on {
				left, right = outer, outer
			}
			tl := max(Animate(seg, "l", left, SpatialFast), 0)
			tr := max(Animate(seg, "r", right, SpatialFast), 0)
			seg.Height(MinTarget).Margin(-slop(40), 0).PaddingX(0).Justify(ui.Center).AlignItems(ui.Center).
				Cursor(ui.CursorPointer).Label(label)
			seg.Children(func() {
				ui.Row(c).Height(40).PaddingX(16).Gap(8).Radius(tl, tr, tr, tl).Justify(ui.Center).AlignItems(ui.Center).
					Background(StateFill(container, content, seg.Hovered(), seg.Pressed(), seg.FocusVisible())).
					TextColor(content).Transition(Fade(EffectsFast)).Children(func() {
					if i < len(icons) && icons[i] != nil {
						ui.Icon(c, icons[i]).FontSize(18).TextColor(content)
					}
					buttonLabel(c, LabelLarge, label, on, 0).TextColor(content).SingleLine()
				})
			})
		}
	})
	return group.Track.Changed()
}
