package m3

import (
	"github.com/egoist/mygo/ui"
)

// NavItem is a destination of a navigation rail.
type NavItem struct {
	ID    string
	Label string
	// Icon is drawn while the destination is idle, Selected while it is
	// chosen: Material fills the icon of the current destination.
	Icon, Selected *ui.SVG
	// Hint, when set, is shown over the destination's icon as the key that
	// opens it.
	Hint []KeyPart
}

// Rail widths, in DIPs.
const (
	RailCollapsed float32 = 96
	RailExpanded  float32 = 236
)

// railInset is the space either side of an expanded destination: the
// collapsed indicator's left edge, which the expanded pill grows from.
// railIconInset puts the icon on the collapsed icon's centre line.
const (
	railInset     float32 = 20
	railIconInset float32 = 16
)

// railSpring moves the rail's width: critically damped, and slower than the
// default spatial spring so the labels have frames to fade in and out.
var railSpring = Spring{260, 1}

// railFadeGain sets how much of the travel the labels are hidden for: they
// are opaque once the rail is 1/railFadeGain of the way from the midpoint.
const railFadeGain = 3

// RailSpec describes a navigation rail.
type RailSpec struct {
	Items    []NavItem
	Footer   []NavItem // destinations pinned to the bottom
	Selected string
	Expanded bool
	// TopInset keeps the rail clear of the window controls of a window with
	// a hidden title bar, and lets the band they sit in drag the window.
	TopInset float32
}

// RailEvent is what the user did to a rail this frame.
type RailEvent struct {
	Item   string // the destination chosen, or ""
	Toggle bool   // the menu button was pressed
}

// Rail builds a navigation rail, collapsed to icons over labels or expanded to
// icons beside labels. Its width follows a spring when it changes, and the
// pill behind each icon grows with it.
func Rail(c *ui.Context, spec RailSpec) RailEvent {
	var event RailEvent
	sc := Of(c).Scheme
	rail := ui.Column(c.Key("m3-rail"))
	// One progress drives the width and the layout, so they stay in step:
	// labels fade between two fixed layouts while the toggle glyph rotates
	// through its change and the selected pill grows with the rail.
	// The spring does not overshoot: a rail that rings past its width
	// re-lays out the whole page on every frame of the wobble.
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
		// The button sits on the collapsed icon's centre line at every width:
		// centring it in the rail would slide it while the rail grows.
		// Its pointer target is MinTarget wide and its shape is
		// narrower, so it reaches past the shape without moving it.
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

// railMenu rotates the old and new glyphs through the transition, without
// changing the button's hit target or moving it off the collapsed icon's
// centre line.
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

// navItem is one destination. Its icon and pill keep their identities as the
// rail changes width: the pill is the collapsed indicator at one end and the
// expanded row at the other. The label switches between its stacked and
// inline positions while faded out, rather than travelling between them.
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

	// Keep each destination's row anchored while the icon shifts down inside
	// the taller expanded pill; shrinking the rows would pull later icons up.
	b.Column().Width(railWidth).Height(64).Justify(ui.Start).AlignItems(ui.Start).Cursor(ui.CursorPointer).Label(item.Label)
	b.Children(func() {
		pill := ui.Row(c).Radius(Full).AlignItems(ui.Center)
		target := float32(0)
		if selected {
			target = 1
		}
		sel := min(max(Animate(pill, "sel", target, SpatialDefault), 0), 1)
		// In the expanded rail, the selected indicator grows down from its
		// anchor to give the icon and label more vertical padding.
		height := 32 + 24*open
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
