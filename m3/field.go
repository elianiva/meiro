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
	// Container overrides the neutral resting fill, for a bar that should
	// take the colour of the page behind it.
	Container ui.Color
	// TrailingPadding reserves room at the end of the input for an overlaid
	// control, without shifting the control itself.
	TrailingPadding float32
	// Dismiss leads the bar with a back arrow that closes it, for a bar that
	// opens over the page it narrows rather than standing on a page of its
	// own.
	Dismiss bool
	// Underline draws the bar as a line under the field, with no container
	// around it: for a search that narrows the list in place, where a
	// second pill beside the page's controls would be too much.
	Underline bool
	// NoClear leaves out the button that empties the field, for a filter
	// whose text is cleared by closing the search instead.
	NoClear bool
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

// SearchBar builds a search field. It never searches by itself: typing only
// edits the text, and Submitted reports Enter. The element is the bar, for
// anchoring what opens under it, as the suggestions do. It is a filled pill by
// default, or a single line under the field with Underline.
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
	bar := ui.Row(c.Key(spec.Key)).Height(height).Gap(12).AlignItems(ui.Center).Grow(1)
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
	return result, bar
}
