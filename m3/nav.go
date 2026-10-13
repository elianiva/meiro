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
// railIconInset then puts the icon on the collapsed icon's centre line.
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
	rail := ui.Column(c).Key("m3-rail")
	// One progress drives the width and the layout, so they stay in step:
	// the labels swap between under and beside their icons at the midpoint,
	// while they are faded out. The icons and pills never swap.
	// The spring does not overshoot: a rail that rings past its width
	// re-lays out the whole page on every frame of the wobble.
	p := Animate(rail, "expand", pick(spec.Expanded, float32(1), 0), railSpring)
	p = min(max(p, 0), 1)
	railWidth := RailCollapsed + (RailExpanded-RailCollapsed)*p
	expanded := p > 0.5
	fade := min(abs32(2*p-1)*railFadeGain, 1)
	rail.Width(railWidth).Shrink(0).FillHeight().ClipX().PaddingY(12).Gap(4).
		Background(sc.SurfaceContainerLow)
	align := ui.Center
	if expanded {
		align = ui.Start
	}
	rail.AlignItems(align).Children(func() {
		if spec.TopInset > 0 {
			ui.Box(c).Height(spec.TopInset).FillWidth().Shrink(0).DragWindow()
		}
		menu := IconButton(c, IconButtonSpec{
			Icon:  pick(expanded, IconMenuOpen, IconMenu),
			Label: pick(expanded, "Collapse navigation", "Expand navigation"),
			Key:   "rail-menu",
		})
		if expanded {
			menu.Margin(0, 0, 0, RailCollapsed/2-16)
		}
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

// navItem is one destination. Its icon and the pill behind it are the same
// element in both layouts: the pill is as wide as the rail between its
// insets, so it is the collapsed indicator at one end and the expanded row
// at the other, and the icon stays where it is. Only the label changes
// place, from under the icon to beside it, and it does that while faded out.
func navItem(c *ui.Context, item NavItem, selected, expanded bool, fade, railWidth float32) bool {
	sc := Of(c).Scheme
	b := ui.ButtonBase(c.Key("nav-" + item.ID))
	icon := item.Icon
	if selected && item.Selected != nil {
		icon = item.Selected
	}
	iconColor, labelColor := sc.OnSurfaceVariant, sc.OnSurfaceVariant
	if selected {
		iconColor, labelColor = sc.OnSecondaryContainer, sc.OnSurface
	}

	b.Column().Width(railWidth).Height(64).Justify(ui.Start).AlignItems(ui.Start).Cursor(ui.CursorPointer).Label(item.Label)
	b.Children(func() {
		pill := ui.Row(c).Radius(Full).AlignItems(ui.Center).PaddingX(railIconInset).Gap(12)
		target := float32(0)
		if selected {
			target = 1
		}
		sel := min(max(Animate(pill, "sel", target, SpatialDefault), 0), 1)
		// 32 collapsed and 40 expanded, around the icon's own centre line.
		open := min(max((railWidth-RailCollapsed)/(RailExpanded-RailCollapsed), 0), 1)
		height := 32 + 8*open
		fill := sc.SecondaryContainer.Alpha(sel)
		pill.Width(railWidth-2*railInset).Height(height).Margin(6-(height-32)/2, railInset, 0, railInset).Shrink(0).ClipX().
			Background(StateFill(fill, sc.OnSurface, b.Hovered() && !selected, b.Pressed(), b.FocusVisible())).
			Children(func() {
				ui.Icon(c, icon).FontSize(24).TextColor(iconColor).Shrink(0)
				if expanded {
					LabelLarge.Style(ui.Text(c, item.Label), selected).TextColor(labelColor).Grow(1).MinWidth(0).SingleLine().Opacity(fade)
				}
			})
		if !expanded {
			LabelMedium.Style(ui.Text(c, item.Label), selected).TextColor(labelColor).Width(RailCollapsed).TextAlign(ui.Center).SingleLine().
				Margin(4, 0, 0, 0).Opacity(fade)
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
