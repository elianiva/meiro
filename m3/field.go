package m3

import (
	"github.com/egoist/mygo/ui"
)

type SearchSpec struct {
	Value           *string
	Placeholder     string
	Key             any
	Focus           *ui.Handle
	Height          float32
	MaxWidth        float32
	Container       ui.Color
	TrailingPadding float32
	Dismiss         bool
	Underline       bool
	NoClear         bool
}

type SearchResult struct {
	Focused   bool
	Submitted bool
	Changed   bool
	Cleared   bool
	Dismissed bool
}

func SearchBar(c *ui.Context, spec SearchSpec) (result SearchResult, bar, field ui.Element) {
	sc := Of(c).Scheme
	height := spec.Height
	if height == 0 {
		height = 56
	}
	if spec.Key == nil {
		spec.Key = "search-bar"
	}
	bar = ui.Row(c.Key(spec.Key)).Height(height).Gap(12).AlignItems(ui.Center).Grow(1)
	if spec.MaxWidth > 0 {
		bar.MaxWidth(spec.MaxWidth)
	}
	lineColor, lineHeight := sc.Outline, float32(1)
	switch {
	case spec.Underline && bar.FocusWithin():
		lineColor, lineHeight = sc.Primary, 2
	case spec.Underline:
		if bar.Hovered() {
			lineColor = sc.OnSurfaceVariant
		}
		bar.PaddingX(8).Cursor(ui.CursorText)
	default:
		container := sc.SurfaceContainerHigh
		if spec.Container.A != 0 {
			container = spec.Container
		}
		border := ui.Color{}
		if bar.FocusWithin() {
			if spec.Container.A != 0 {
				container = Layer(container, sc.OnSurface, 0.08)
			} else {
				container = sc.SurfaceContainerHighest
			}
			border = sc.Primary
		} else if bar.Hovered() {
			container = Layer(container, sc.OnSurface, StateHover)
		}
		bar.PaddingX(16).Radius(Full).Border(2, border).Background(container).Transition(Fade(EffectsFast)).Cursor(ui.CursorText)
	}
	bar.Children(func() {
		if spec.Dismiss {
			if IconButton(c, IconButtonSpec{Icon: IconArrowBack, Label: "Close the search", Size: ExtraSmall32, Key: "dismiss"}).Clicked() {
				result.Dismissed = true
			}
		} else {
			ui.Icon(c, IconSearch).FontSize(24).TextColor(sc.OnSurfaceVariant)
		}
		input := ui.TextInputBase(c.Key("input"), spec.Value).Placeholder(spec.Placeholder).Label(spec.Placeholder).
			Grow(1).MinWidth(0).FontSize(16).TextColor(sc.OnSurface).Padding(0, spec.TrailingPadding, 0, 0).FocusRing(false)
		field = input
		if spec.Focus != nil {
			input.Bind(spec.Focus)
		}
		if input.Submitted() {
			result.Submitted = true
		}
		if input.Changed() {
			result.Changed = true
		}
		if *spec.Value != "" && !spec.NoClear {
			if IconButton(c, IconButtonSpec{Icon: IconClose, Label: "Clear search", Size: ExtraSmall32, Key: "clear"}).Clicked() {
				*spec.Value = ""
				result.Cleared = true
			}
		}
		if spec.Underline {
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(lineHeight).Background(lineColor).PassThrough()
		}
	})
	result.Focused = bar.FocusWithin()
	return result, bar, field
}
