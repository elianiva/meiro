package m3

import (
	"github.com/egoist/mygo/ui"
)

// SearchSpec describes a search bar.
type SearchSpec struct {
	Value       *string
	Placeholder string
	// Key keeps the field's focus and caret across frames.
	Key any
	// Focus binds the field to a handle, so that Focus on it moves the
	// keyboard to the field, waiting while the bar is hidden.
	Focus *ui.Handle
	// Height is 56 by default, the Material search bar; 48 suits a toolbar.
	Height float32
	// MaxWidth keeps a wide bar from stretching across the window.
	MaxWidth float32
	// TrailingPadding reserves room at the end of the input for an overlaid
	// control, without shifting the control itself.
	TrailingPadding float32
	// Dismiss leads the bar with a back arrow that closes it, for a bar that
	// opens over the page it narrows rather than standing on a page of its
	// own.
	Dismiss bool
}

// SearchResult is what happened to a search bar this frame.
type SearchResult struct {
	// Focused is true while the bar or one of its controls owns focus.
	Focused bool
	// Submitted is Enter pressed: the one moment a search should run.
	Submitted bool
	Changed   bool
	Cleared   bool
	// Dismissed is the back arrow pressed: the search is over.
	Dismissed bool
}

// SearchBar builds a pill-shaped search field. It never searches by itself:
// typing only edits the text, and Submitted reports Enter. The element is the
// bar, for anchoring what opens under it, as the suggestions do.
func SearchBar(c *ui.Context, spec SearchSpec) (SearchResult, ui.Element) {
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
	border := ui.Color{}
	if bar.FocusWithin() {
		container = sc.SurfaceContainerHighest
		border = sc.Primary
	} else if bar.Hovered() {
		container = Layer(container, sc.OnSurface, StateHover)
	}
	bar.Border(2, border).Background(container).Transition(Fade(EffectsFast)).Cursor(ui.CursorText).Grow(1)
	if spec.MaxWidth > 0 {
		bar.MaxWidth(spec.MaxWidth)
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
		if spec.Focus != nil {
			input.Bind(spec.Focus)
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
	result.Focused = bar.FocusWithin()
	return result, bar
}
