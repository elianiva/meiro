package m3

import (
	"github.com/egoist/mygo/ui"
)

// ButtonKind is the emphasis of a button, from the loudest to the quietest.
type ButtonKind int

const (
	// Filled is the primary action of a screen.
	Filled ButtonKind = iota
	// Tonal is a secondary action that still deserves weight.
	Tonal
	// Elevated lifts a button off a busy surface.
	Elevated
	// Outlined is a medium-emphasis action.
	Outlined
	// TextOnly is the quietest: no container at all.
	TextOnly
)

// ButtonSize is one of the five sizes of Material 3 Expressive buttons.
type ButtonSize int

const (
	ExtraSmall32  ButtonSize = iota // 32 DIPs high
	Small40                         // 40
	Medium56                        // 56
	Large96                         // 96
	ExtraLarge136                   // 136
)

type buttonDims struct {
	height, pad, icon, gap float32
	label                  Role
	square, pressed        float32 // corner radius of the square shape, and of any shape while pressed
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

// ButtonSpec describes a button. The zero value is a small filled button.
type ButtonSpec struct {
	Label string
	Icon  *ui.SVG
	Kind  ButtonKind
	Size  ButtonSize
	// Square gives the button the squarer shape; the default is a pill.
	Square bool
	// Toggle makes the button a toggle: Selected is its state, and a selected
	// toggle swaps its shape, as Expressive buttons do, so that state shows in
	// shape as well as in colour.
	Toggle, Selected bool
	Disabled         bool
	// Key keeps the button's state with its item when siblings come and go.
	Key any
	// FillWidth stretches the button across its parent.
	FillWidth bool
}

// containerColors returns the container and content colours of a kind in a
// state, and its outline.
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

// StateFill is a container with the state layer of its content colour on it.
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

func newButtonBase(c *ui.Context, key any) ui.Element {
	if key != nil {
		return ui.ButtonBase(c.Key(key))
	}
	return ui.ButtonBase(c)
}

// fitLabel gives a button's label a little room beyond its measured width.
// Linux can otherwise wrap a label whose box is exactly its intrinsic width,
// then SingleLine truncates it with an ellipsis.
func fitLabel(c *ui.Context, role Role, label string, emphasized bool, size float32) ui.Element {
	weight := role.Weight
	if emphasized {
		weight = role.Emphasis
	}
	if size <= 0 {
		size = role.Size
	}
	width, _ := c.MeasureText(0, ui.Span{
		Text: label, Size: size, Weight: weight, LetterSpacing: role.Tracking,
	})
	return role.Style(ui.Text(c, label), emphasized).FontSize(size).Width(width + 1)
}

// Button builds a button: its label and icon sit in a container whose shape,
// colour and state layers follow the active theme. A pressed button squares
// off along a spring; a selected toggle settles into the other shape.
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

	round := spec.Square == (spec.Toggle && spec.Selected) // the shape a button rests in
	rest := d.height / 2
	if !round {
		rest = d.square
	}
	if b.Pressed() {
		rest = d.pressed
	}
	radius := max(Animate(b, "radius", rest, SpatialFast), 0)

	b.Height(d.height).PaddingX(d.pad).Gap(d.gap).Radius(radius).Background(fill).
		Justify(ui.Center).AlignItems(ui.Center).Shrink(0).
		TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast))
	if spec.Label != "" {
		b.Label(spec.Label)
	}
	if spec.FillWidth {
		b.FillWidth()
	}
	if outline.A != 0 {
		b.Border(1, outline)
	}
	if spec.Kind == Elevated && !spec.Disabled {
		Elevation(c, b, 1)
	}
	b.Children(func() {
		if spec.Icon != nil {
			ui.Icon(c, spec.Icon).FontSize(d.icon).TextColor(content)
		}
		if spec.Label != "" {
			fitLabel(c, d.label, spec.Label, true, 0).TextColor(content).SingleLine()
		}
	})
	return b
}

// IconButtonSpec describes an icon button.
type IconButtonSpec struct {
	Icon *ui.SVG
	// Label names the button for assistive technology and its tooltip.
	Label string
	Kind  IconButtonKind
	Size  ButtonSize
	// Square gives the button a squarer shape; the default is a circle.
	Square bool
	// Toggle makes the button a toggle with Selected as its state, drawn with
	// SelectedIcon when it has one.
	Toggle, Selected bool
	// Morph swaps the shape of a selected button without recolouring it: the
	// play button that squares off while playing.
	Morph        bool
	SelectedIcon *ui.SVG
	Disabled     bool
	// Loading swaps the icon for the loading indicator and ignores presses,
	// while the button's action is under way.
	Loading bool
	Key     any
	// Dimension sets the button's side in DIPs where none of the five sizes
	// fits, as a play button in a player bar.
	Dimension float32
}

// IconButtonKind is the emphasis of an icon button.
type IconButtonKind int

const (
	// StandardIcon has no container until it is hovered.
	StandardIcon IconButtonKind = iota
	FilledIcon
	TonalIcon
	OutlinedIcon
)

// IconButton builds a round button holding one icon.
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
		container, content = ui.Transparent, sc.OnSurfaceVariant
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

	round := spec.Square == (spec.Selected && (spec.Toggle || spec.Morph))
	rest := d.height / 2
	if !round {
		rest = d.square
	}
	if b.Pressed() {
		rest = d.pressed
	}
	radius := max(Animate(b, "radius", rest, SpatialFast), 0)

	icon := spec.Icon
	if spec.Selected && spec.SelectedIcon != nil {
		icon = spec.SelectedIcon
	}
	b.Size(d.height, d.height).Radius(radius).Background(fill).Center().Shrink(0).
		TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast))
	if spec.Label != "" {
		b.Label(spec.Label).Tooltip(spec.Label)
	}
	if outline.A != 0 {
		b.Border(1, outline)
	}
	if spec.Loading {
		b.Disabled(true)
	}
	b.Children(func() {
		if spec.Loading {
			loadingIndicator(c, d.icon*1.4, content, ui.Transparent)
			return
		}
		ui.Icon(c, icon).FontSize(d.icon).TextColor(content)
	})
	return b
}

// FABSpec describes a floating action button.
type FABSpec struct {
	Icon  *ui.SVG
	Label string // makes it an extended FAB
	Size  FABSize
	Tone  FABTone
	Key   any
}

// FABSize is the size of a floating action button.
type FABSize int

const (
	FABMedium FABSize = iota // 56
	FABSmall                 // 40
	FABLarge                 // 96
)

// FABTone is the colour pairing of a floating action button.
type FABTone int

const (
	FABPrimaryContainer FABTone = iota
	FABSecondaryContainer
	FABTertiaryContainer
	FABPrimary
)

// FAB builds a floating action button, or an extended one when it has a
// label. It sits at elevation 3 and rises to 4 under the pointer.
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
	if b.Pressed() {
		radius = max(radius/2, Small/2)
	}
	radius = max(Animate(b, "radius", radius, SpatialFast), 0)
	b.Height(size).Radius(radius).Background(StateFill(container, content, b.Hovered(), b.Pressed(), b.FocusVisible())).
		Justify(ui.Center).AlignItems(ui.Center).Gap(8).Shrink(0).
		TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast))
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
			fitLabel(c, LabelLarge, spec.Label, true, 16).TextColor(content).SingleLine()
		}
	})
	return b
}

// Chip builds a filter chip: outlined while off, tonal with a check when on.
// It reports its click, so the caller owns the selection.
func Chip(c *ui.Context, label string, selected bool, key any) ui.Element {
	sc := Of(c).Scheme
	b := newButtonBase(c, key)
	container, content, outline := ui.Transparent, sc.OnSurfaceVariant, sc.OutlineVariant
	if selected {
		container, content, outline = sc.SecondaryContainer, sc.OnSecondaryContainer, ui.Transparent
	}
	b.Height(32).PaddingX(12).Gap(8).Radius(Small).Justify(ui.Center).AlignItems(ui.Center).Shrink(0).
		Background(StateFill(container, content, b.Hovered(), b.Pressed(), b.FocusVisible())).
		TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast)).Label(label)
	if outline.A != 0 {
		b.Border(1, outline)
	}
	b.Children(func() {
		if selected {
			ui.Icon(c, IconCheck).FontSize(18).TextColor(content)
		}
		fitLabel(c, LabelLarge, label, false, 0).TextColor(content).SingleLine()
	})
	return b
}

// ButtonGroup builds a connected button group: a row of toggle buttons that
// share one choice. The selected button rounds into a pill while its
// neighbours square their inner corners, so the choice reads in shape first.
// It reports whether the user changed the choice.
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
			seg.Height(40).PaddingX(16).Gap(8).Justify(ui.Center).AlignItems(ui.Center).
				Radius(tl, tr, tr, tl).
				Background(StateFill(container, content, seg.Hovered(), seg.Pressed(), seg.FocusVisible())).
				TextColor(content).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast)).Label(label)
			seg.Children(func() {
				if i < len(icons) && icons[i] != nil {
					ui.Icon(c, icons[i]).FontSize(18).TextColor(content)
				}
				fitLabel(c, LabelLarge, label, on, 0).TextColor(content).SingleLine()
			})
		}
	})
	return group.Track.Changed()
}
