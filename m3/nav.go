package m3

import (
	"github.com/egoist/mygo/ui"
)

type NavItem struct {
	ID             string
	Label          string
	Icon, Selected *ui.SVG
	Hint           []KeyPart
}

const (
	RailCollapsed float32 = 96
	RailExpanded  float32 = 236
)

const (
	railInset     float32 = 20
	railIconInset float32 = 16
)

const (
	navRowHeight             = 64
	collapsedIndicatorHeight = 32
	expandedIndicatorHeight  = 56
)

var railSpring = Spring{260, 1}

const railFadeGain = 3

type RailSpec struct {
	Items    []NavItem
	Footer   []NavItem
	Selected string
	Expanded bool
	TopInset float32
}

type RailEvent struct {
	Item   string
	Toggle bool
}

func Rail(c *ui.Context, spec RailSpec) RailEvent {
	var event RailEvent
	sc := Of(c).Scheme
	rail := ui.Column(c.Key("m3-rail"))
	p := Animate(rail, "expand", pick(spec.Expanded, float32(1), 0), railSpring)
	p = min(max(p, 0), 1)
	railWidth := RailCollapsed + (RailExpanded-RailCollapsed)*p
	expanded := p > 0.5
	fade := min(abs32(2*p-1)*railFadeGain, 1)
	rail.Width(railWidth).Shrink(0).FillHeight().ClipX().PaddingY(12).Gap(4).
		Background(sc.SurfaceContainerLow)
	rail.AlignItems(ui.Start).Children(func() {
		if spec.TopInset > 0 {
			ui.Box(c).Height(spec.TopInset).FillWidth().Shrink(0).DragWindow()
		}
		menu := railMenu(c, p, expanded)
		reachBy := slop(32)
		menu.Margin(-reachBy, -reachBy, -reachBy, RailCollapsed/2-MinTarget/2)
		if menu.Clicked() {
			event.Toggle = true
		}
		ui.Box(c).Height(16)
		for _, item := range spec.Items {
			if navItem(c, item, item.ID == spec.Selected, expanded, fade, railWidth) {
				event.Item = item.ID
			}
		}
		ui.Spacer(c)
		for _, item := range spec.Footer {
			if navItem(c, item, item.ID == spec.Selected, expanded, fade, railWidth) {
				event.Item = item.ID
			}
		}
	})
	return event
}

func railMenu(c *ui.Context, open float32, expanded bool) ui.Element {
	sc := Of(c).Scheme
	label := pick(expanded, "Collapse navigation", "Expand navigation")
	b := ui.ButtonBase(c.Key("rail-menu"))
	b.Size(MinTarget, MinTarget).Center().Cursor(ui.CursorPointer).Label(label).Tooltip(label).Transition(Fade(EffectsFast))
	b.Children(func() {
		fill := StateFill(ui.Transparent, sc.OnSurfaceVariant, b.Hovered(), b.Pressed(), b.FocusVisible())
		shape := ui.Box(c).Size(32, 32).Radius(16).Background(fill).Center()
		focusRing(c, b, shape, 16)
		shape.Children(func() {
			icons := ui.Box(c).Size(24, 24).Center()
			icons.Children(func() {
				ui.Icon(c, IconMenu).Absolute().Top(0).Left(0).Size(24, 24).
					Rotate(180 * open).Opacity(1 - open)
				ui.Icon(c, IconMenuOpen).Absolute().Top(0).Left(0).Size(24, 24).
					Rotate(180 * (open - 1)).Opacity(open)
			})
		})
	})
	return b
}

func navItem(c *ui.Context, item NavItem, selected, expanded bool, fade, railWidth float32) bool {
	sc := Of(c).Scheme
	open := min(max((railWidth-RailCollapsed)/(RailExpanded-RailCollapsed), 0), 1)
	b := ui.ButtonBase(c.Key(item.ID))
	icon := item.Icon
	if selected && item.Selected != nil {
		icon = item.Selected
	}
	iconColor, labelColor := sc.OnSurfaceVariant, sc.OnSurfaceVariant
	if selected {
		iconColor, labelColor = sc.OnSecondaryContainer, sc.OnSecondaryContainer
	}

	b.Column().Width(railWidth).Height(navRowHeight).Justify(ui.Start).AlignItems(ui.Start).Cursor(ui.CursorPointer).Label(item.Label)
	b.Children(func() {
		pill := ui.Row(c).Radius(Full).AlignItems(ui.Center)
		target := float32(0)
		if selected {
			target = 1
		}
		sel := min(max(Animate(pill, "sel", target, SpatialDefault), 0), 1)
		height := collapsedIndicatorHeight + (expandedIndicatorHeight-collapsedIndicatorHeight)*open
		marginTop := 22 + 6*open - height/2
		fill := ui.Transparent
		if selected {
			fill = sc.SecondaryContainer.Alpha(sel)
		}
		pill.Width(railWidth - 2*railInset)
		pill.Height(height).Margin(marginTop, railInset, 0, railInset).Shrink(0).ClipX().
			Background(StateFill(fill, sc.OnSurface, b.Hovered() && !selected, b.Pressed(), b.FocusVisible()))
		focusRing(c, b, pill, Full)
		pill.PaddingX(railIconInset).Gap(8)
		pill.Children(func() {
			ui.Icon(c, icon).FontSize(24).TextColor(iconColor).Shrink(0)
		})
		expandedLabelLeft := railInset + railIconInset + 24 + 8
		if expanded {
			label := ui.Box(c.Key(item.Label)).Absolute().Top(18).Left(expandedLabelLeft).
				Width(railWidth - expandedLabelLeft).Height(LabelLarge.Line).Opacity(fade)
			label.Children(func() {
				LabelLarge.Style(ui.Text(c, item.Label), false).TextColor(labelColor).SingleLine()
			})
		} else {
			label := ui.Box(c.Key(item.Label)).Absolute().Top(42).Left(0).
				Width(RailCollapsed).Height(LabelMedium.Line).Opacity(fade)
			label.Children(func() {
				LabelMedium.Style(ui.Text(c, item.Label), selected).TextColor(labelColor).
					TextAlign(ui.Center).SingleLine()
			})
		}
		if len(item.Hint) > 0 {
			at := ui.Box(c).Absolute().PassThrough()
			if expanded {
				at.Top(18).Right(railInset + 14)
			} else {
				at.Top(20).Left(RailCollapsed/2 + 6)
			}
			at.Children(func() { KeyBadge(c, item.Hint...) })
		}
	})
	return b.Clicked()
}

func abs32(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}
