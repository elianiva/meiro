package m3

import (
	"github.com/egoist/mygo/ui"
)

// SearchSpec describes a search bar.
type SearchSpec struct {
	Value       *string
	Placeholder string
	// Key keeps the field's focus and caret across frames.
	Key       any
	AutoFocus bool
	// Height is 56 by default, the Material search bar; 48 suits a toolbar.
	Height float32
	// MaxWidth keeps a wide bar from stretching across the window.
	MaxWidth float32
}

// SearchResult is what happened to a search bar this frame.
type SearchResult struct {
	// Submitted is Enter pressed: the one moment a search should run.
	Submitted bool
	Changed   bool
	Cleared   bool
}

// SearchBar builds a pill-shaped search field. It never searches by itself:
// typing only edits the text, and Submitted reports Enter.
func SearchBar(c *ui.Context, spec SearchSpec) SearchResult {
	sc := Of(c).Scheme
	height := spec.Height
	if height == 0 {
		height = 56
	}
	var result SearchResult
	if spec.Key == nil {
		spec.Key = "search-bar"
	}
	bar := ui.Row(c.Key(spec.Key)).Height(height).PaddingX(16).Gap(12).AlignItems(ui.Center).Radius(Full)
	container := sc.SurfaceContainerHigh
	if bar.FocusWithin() {
		container = sc.SurfaceContainerHighest
		bar.Border(2, sc.Primary)
	} else if bar.Hovered() {
		container = Layer(container, sc.OnSurface, StateHover)
	}
	bar.Background(container).Transition(Fade(EffectsFast)).Cursor(ui.CursorText).Grow(1)
	if spec.MaxWidth > 0 {
		bar.MaxWidth(spec.MaxWidth)
	}
	bar.Children(func() {
		ui.Icon(c, IconSearch).FontSize(24).TextColor(sc.OnSurfaceVariant)
		input := ui.TextInputBase(c.Key("input"), spec.Value).Placeholder(spec.Placeholder).Label(spec.Placeholder).
			Grow(1).MinWidth(0).FontSize(16).TextColor(sc.OnSurface)
		if spec.AutoFocus {
			input.AutoFocus()
		}
		if input.Submitted() {
			result.Submitted = true
		}
		if input.Changed() {
			result.Changed = true
		}
		if *spec.Value != "" {
			if IconButton(c, IconButtonSpec{Icon: IconClose, Label: "Clear search", Size: ExtraSmall32, Key: "clear"}).Clicked() {
				*spec.Value = ""
				result.Cleared = true
			}
		}
	})
	return result
}
