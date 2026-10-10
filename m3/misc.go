package m3

import (
	"math"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

// pick returns yes when b is true, else no: a choice without a map lookup.
func pick[T any](b bool, yes, no T) T {
	if b {
		return yes
	}
	return no
}

// Switch builds a Material 3 switch: a pill track and a thumb that grows when
// it is on, shrinks when it is off and swells while pressed, with a check
// mark in the thumb when on. It toggles *on by itself.
func Switch(c *ui.Context, on *bool, label string) ui.Element {
	sc := Of(c).Scheme
	s := ui.SwitchBase(c.Key("switch-"+label), on).Size(52, 32).Radius(Full).Label(label).
		Cursor(ui.CursorPointer).Shrink(0)
	v := *on
	progress := Animate(s, "on", pick(v, float32(1), 0), SpatialFast)
	pressed := s.Pressed()
	hovered := s.Hovered()
	focused := s.FocusVisible()
	track, outline, thumb, glyph := sc.SurfaceContainerHighest, sc.Outline, sc.Outline, sc.SurfaceContainerHighest
	if v {
		track, outline, thumb, glyph = sc.Primary, sc.Primary, sc.OnPrimary, sc.OnPrimaryContainer
	}
	s.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, track, r.H/2)
		if !v {
			p.Stroke(r, outline, r.H/2, 2)
		}
		size := float32(16) + 8*min(max(progress, 0), 1.2)
		if pressed {
			size = 28
		}
		cx := r.X + 16 + (r.W-32)*progress
		cy := r.Y + r.H/2
		if hovered || focused {
			halo := thumb
			if !v {
				halo = sc.OnSurface
			}
			p.Fill(ui.Rect{X: cx - 20, Y: cy - 20, W: 40, H: 40}, halo.Alpha(StateHover+0.02), 20)
		}
		p.Fill(ui.Rect{X: cx - size/2, Y: cy - size/2, W: size, H: size}, thumb, size/2)
		if v && progress > 0.5 {
			p.Icon(IconCheck, ui.Rect{X: cx - 8, Y: cy - 8, W: 16, H: 16}, glyph)
		}
	})
	return s
}

// Dialog shows a Material dialog over a scrim while *open is true. build
// fills the panel; a press on the scrim or Escape closes it.
func Dialog(c *ui.Context, open *bool, width float32, build func()) {
	sc := Of(c).Scheme
	ui.DialogBase(c, open, func(backdrop, panel ui.Element) {
		backdrop.Background(sc.Scrim.Alpha(0.4))
		panel.Width(width).MaxWidthPercent(92).Padding(24).Gap(16).Radius(ExtraLarge).
			Background(sc.SurfaceContainerHigh).TextColor(sc.OnSurface)
		Elevation(c, panel, 3)
		build()
	})
}

// Snackbars shows the window's toasts as Material snackbars along the bottom
// of the window, lifted by inset DIPs to clear a player.
func Snackbars(c *ui.Context, inset float32) {
	sc := Of(c).Scheme
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16, 16, 16+inset, 16).AlignItems(ui.Center).Gap(8)
		for _, t := range toasts {
			toast := ui.ToastBase(c, t)
			toast.Root.Row().AlignItems(ui.Center).Gap(16).Padding(6, 8, 6, 16).MinHeight(48).MaxWidth(560).
				Radius(Medium).Background(sc.InverseSurface).TextColor(sc.InverseOnSurface)
			Elevation(c, toast.Root, 3)
			toast.Root.Transition(Move(SpatialFast))
			toast.Root.Children(func() {
				if t.Type == "error" {
					ui.Icon(c, IconError).FontSize(20).TextColor(sc.Error)
				}
				BodyMedium.Style(ui.Text(c, t.Title), false).TextColor(sc.InverseOnSurface).Grow(1).MaxLines(2)
				if t.Action != "" {
					action := toast.ActionButton().Padding(0, 12).Height(40).Radius(Full).Center().
						Cursor(ui.CursorPointer).TextColor(sc.InversePrimary)
					action.Background(StateFill(ui.Transparent, sc.InversePrimary, action.Hovered(), action.Pressed(), false))
					action.Children(func() {
						LabelLarge.Style(ui.Text(c, t.Action), true).TextColor(sc.InversePrimary)
					})
				}
				toast.CloseButton().Label("Dismiss").Size(40, 40).Radius(Full).Center().Cursor(ui.CursorPointer).
					TextColor(sc.InverseOnSurface).Children(func() {
					ui.Icon(c, IconClose).FontSize(20)
				})
			})
		}
	})
}

// Menu opens a Material menu below anchor while *open is true.
func Menu(c *ui.Context, anchor ui.Element, open *bool, width float32, build func()) {
	sc := Of(c).Scheme
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.Width(width).Padding(8).Radius(Large).Margin(8, 0, 0, 0).Background(sc.SurfaceContainerHigh).
			Gap(2).TextColor(sc.OnSurface)
		Elevation(c, panel, 3)
		build()
	})
}

// MenuItem is one entry of a menu.
func MenuItem(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	sc := Of(c).Scheme
	b := ui.ButtonBase(c.Key("menu-" + label))
	b.Height(48).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(Medium).Cursor(ui.CursorPointer).Label(label).
		Background(StateFill(ui.Transparent, sc.OnSurface, b.Hovered(), b.Pressed(), b.FocusVisible()))
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(24).TextColor(sc.OnSurfaceVariant)
		}
		LabelLarge.Style(ui.Text(c, label), false).TextColor(sc.OnSurface).SingleLine().Grow(1).FontSize(15)
	})
	return b
}

// Art shows artwork in a rounded frame, or a tonal placeholder with a note
// while the picture loads or has failed. A zero size leaves it to the layout.
//
// Overlays are built on top of the picture, to put a control over it.
func Art(c *ui.Context, art *ui.Bitmap, size, radius float32, overlays ...func()) ui.Element {
	sc := Of(c).Scheme
	box := ui.Box(c).Radius(radius).Clip().Background(sc.SurfaceContainerHighest).Shrink(0)
	if size > 0 {
		box.Size(size, size)
	}
	box.Children(func() {
		if art == nil {
			ui.Box(c).Fill().Center().Children(func() {
				ui.Icon(c, IconMusicNote).FontSize(max(size*0.4, 18)).TextColor(sc.OnSurfaceVariant.Alpha(0.6))
			})
		} else {
			ui.Image(c, art).Fit(ui.Cover).Fill()
		}
		for _, overlay := range overlays {
			overlay()
		}
	})
	return box
}

// VideoBadge marks artwork for a video that is being played as audio.
func VideoBadge(c *ui.Context, artSize float32) {
	sc := Of(c).Scheme
	inset := min(max(artSize*0.08, 6), 24)
	available := artSize - inset*2
	fontSize := min(max(available/5.8, 6), 13)
	height := fontSize * 1.8
	width := min(fontSize*5.8, available)
	badge := ui.Box(c).Size(width, height).Attach(ui.AnchorTopLeft, ui.AnchorTopLeft).
		Top(inset).Left(inset).Radius(Full).Background(sc.Scrim.Alpha(0.55)).Center().Label("Video")
	badge.Children(func() {
		LabelSmall.Style(ui.Text(c, "VIDEO"), true).Width(width).FontSize(fontSize).FixedLineHeight(height).
			TextColor(ui.RGB(255, 255, 255)).TextAlign(ui.Center).SingleLine().Shrink(0)
	})
}

// Avatar shows a person's picture, or the first letter of their name on a
// tonal disc.
func Avatar(c *ui.Context, name string, pic *ui.Bitmap, size float32) ui.Element {
	sc := Of(c).Scheme
	disc := ui.Box(c).Size(size, size).Radius(Full).Clip().Background(sc.PrimaryContainer).Shrink(0)
	disc.Children(func() {
		if pic != nil {
			ui.Image(c, pic).Fit(ui.Cover).Fill()
			return
		}
		initial := "?"
		for _, r := range strings.TrimSpace(name) {
			initial = string(unicode.ToUpper(r))
			break
		}
		ui.Box(c).Fill().Center().Children(func() {
			ui.Text(c, initial).FontSize(size * 0.42).FontWeight(600).TextColor(sc.OnPrimaryContainer)
		})
	})
	return disc
}

// CarouselState is the scroll of a carousel, which can page by a spring-less
// ease so that the arrows glide instead of jumping.
type CarouselState struct {
	ui.ScrollState
	target  float32
	gliding bool
	view    float32
	// asked records that a caller was told the carousel needs measuring, so
	// it asks only once.
	asked bool
}

// Page glides a carousel one page toward the end (1) or the start (-1).
func (s *CarouselState) Page(direction int) {
	from := s.X
	if s.gliding {
		from = s.target
	}
	s.target = min(max(from+float32(direction)*max(s.view*0.85, 200), 0), s.MaxX)
	s.gliding = true
}

// CanPage reports whether there is more to scroll toward direction.
func (s *CarouselState) CanPage(direction int) bool {
	if direction < 0 {
		return s.X > 1
	}
	return s.X < s.MaxX-1
}

// NeedsMeasurement reports whether the carousel has not been laid out yet, so
// a caller that planned its items from the viewport asks for the frame that
// measures it. It answers true once, so a carousel that is never laid out
// does not ask again every frame.
func (s *CarouselState) NeedsMeasurement() bool {
	if s.view > 0 || s.asked {
		return false
	}
	s.asked = true
	return true
}

// VisibleRange returns the item indices to show or prefetch for a carousel.
// The first index is inclusive and the last is exclusive. It uses the
// viewport measured in the previous frame, with room for four items before
// the first layout has measured it.
func (s *CarouselState) VisibleRange(count int, itemWidth, gap, padding float32, prefetch int) (first, last int) {
	if count <= 0 || itemWidth <= 0 || gap < 0 {
		return 0, 0
	}
	prefetch = max(prefetch, 0)
	step := itemWidth + gap
	view := s.view
	if view <= 0 {
		view = itemWidth*4 + gap*3
	}
	first = int(math.Floor(float64((s.X-padding-itemWidth)/step))) + 1 - prefetch
	last = int(math.Ceil(float64((s.X+view-padding)/step))) + prefetch
	first = max(0, min(count, first))
	last = max(first, min(count, last))
	return first, last
}

// Carousel lays items out in one row that scrolls sideways, as a shelf of
// cards does.
func Carousel(c *ui.Context, s *CarouselState, key any, gap, padX float32, items func()) ui.Element {
	if s.gliding {
		delta := s.target - s.X
		if delta > -0.5 && delta < 0.5 {
			s.X, s.gliding = s.target, false
		} else {
			s.X += delta * 0.2
			c.Invalidate()
		}
	}
	row := ui.ScrollHorizontal(c.Key(key)).TrackScroll(&s.ScrollState).Gap(gap).PaddingX(padX).AlignItems(ui.Start)
	row.Children(items)
	s.view = row.Bounds().W
	return row
}
