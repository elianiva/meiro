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
}

// Rail widths, in DIPs.
const (
	RailCollapsed float32 = 96
	RailExpanded  float32 = 236
)

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
// indicator behind the current destination grows out of its icon.
func Rail(c *ui.Context, spec RailSpec) RailEvent {
	var event RailEvent
	rail := ui.Column(c).Key("m3-rail")
	width := Animate(rail, "width", pick(spec.Expanded, RailExpanded, RailCollapsed), SpatialDefault)
	railWidth := max(width, RailCollapsed-8)
	rail.Width(railWidth).Shrink(0).FillHeight().ClipX().PaddingY(12).Gap(4)
	align := ui.Center
	if spec.Expanded {
		align = ui.Start
	}
	rail.AlignItems(align).Children(func() {
		if spec.TopInset > 0 {
			ui.Box(c).Height(spec.TopInset).FillWidth().Shrink(0).DragWindow()
		}
		menu := IconButton(c, IconButtonSpec{
			Icon:  pick(spec.Expanded, IconMenuOpen, IconMenu),
			Label: pick(spec.Expanded, "Collapse navigation", "Expand navigation"),
			Key:   "rail-menu",
		})
		if spec.Expanded {
			menu.Margin(0, 0, 0, 16)
		}
		if menu.Clicked() {
			event.Toggle = true
		}
		ui.Box(c).Height(16)
		for _, item := range spec.Items {
			if navItem(c, item, item.ID == spec.Selected, spec.Expanded, railWidth) {
				event.Item = item.ID
			}
		}
		ui.Spacer(c)
		for _, item := range spec.Footer {
			if navItem(c, item, item.ID == spec.Selected, spec.Expanded, railWidth) {
				event.Item = item.ID
			}
		}
	})
	return event
}

func navItem(c *ui.Context, item NavItem, selected, expanded bool, railWidth float32) bool {
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

	if expanded {
		fill := ui.Transparent
		if selected {
			fill = sc.SecondaryContainer
		}
		b.Width(railWidth-24).Height(56).PaddingX(16).Margin(0, 12).Gap(12).AlignItems(ui.Center).Radius(Full).
			Background(StateFill(fill, sc.OnSurface, b.Hovered() && !selected, b.Pressed(), b.FocusVisible())).
			TextColor(labelColor).Cursor(ui.CursorPointer).Transition(Fade(EffectsFast)).Label(item.Label)
		b.Children(func() {
			ui.Icon(c, icon).FontSize(24).TextColor(iconColor)
			LabelLarge.Style(ui.Text(c, item.Label), selected).TextColor(labelColor).Grow(1).MinWidth(0).SingleLine()
		})
		return b.Clicked()
	}

	b.Column().Gap(4).AlignItems(ui.Center).PaddingY(6).Width(RailCollapsed).Cursor(ui.CursorPointer).Label(item.Label)
	b.Children(func() {
		hit := ui.Row(c).Size(56, 32).Radius(Full).Center()
		hover := ui.Transparent
		if b.Hovered() && !selected {
			hover = sc.OnSurface.Alpha(StateHover)
		} else if b.Pressed() {
			hover = sc.OnSurface.Alpha(StatePress)
		}
		hit.Background(hover)
		hit.Children(func() {
			indicator := ui.Row(c).Height(32).Radius(Full).Center()
			target := float32(0)
			if selected {
				target = 1
			}
			p := Animate(indicator, "sel", target, SpatialDefault)
			indicator.Width(32 + 24*max(p, 0)).Background(sc.SecondaryContainer.Alpha(min(max(p, 0), 1)))
			indicator.Children(func() {
				ui.Icon(c, icon).FontSize(24).TextColor(iconColor)
			})
		})
		LabelMedium.Style(ui.Text(c, item.Label), selected).TextColor(labelColor).FillWidth().TextAlign(ui.Center).SingleLine()
	})
	return b.Clicked()
}
