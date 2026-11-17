package m3

import (
	"math"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
)

func pick[T any](b bool, yes, no T) T {
	if b {
		return yes
	}
	return no
}

func Switch(c *ui.Context, on *bool, label string) ui.Element {
	return switchWithTrack(c, on, label, 52, 32)
}

func CompactSwitch(c *ui.Context, on *bool, label string) ui.Element {
	return switchWithTrack(c, on, label, 48, 28)
}

func switchWithTrack(c *ui.Context, on *bool, label string, trackWidth, trackHeight float32) ui.Element {
	sc := Of(c).Scheme
	s := ui.SwitchBase(c.Key("switch-"+label), on).Size(max(trackWidth, MinTarget), MinTarget).Margin(-slop(trackHeight), 0).Label(label).
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
		r = ui.Rect{X: r.X + (r.W-trackWidth)/2, Y: r.Y + (r.H-trackHeight)/2, W: trackWidth, H: trackHeight}
		p.Fill(r, track, r.H/2)
		if !v {
			p.Stroke(r, outline, r.H/2, trackHeight/16)
		}
		size := trackHeight/2 + trackHeight/4*min(max(progress, 0), 1.2)
		if pressed {
			size = trackHeight - 4
		}
		cx := r.X + trackHeight/2 + (r.W-trackHeight)*progress
		cy := r.Y + r.H/2
		if hovered || focused {
			halo := thumb
			if !v {
				halo = sc.OnSurface
			}
			haloSize := trackHeight + 8
			p.Fill(ui.Rect{X: cx - haloSize/2, Y: cy - haloSize/2, W: haloSize, H: haloSize}, halo.Alpha(StateHover+0.02), haloSize/2)
		}
		p.Fill(ui.Rect{X: cx - size/2, Y: cy - size/2, W: size, H: size}, thumb, size/2)
		if v && progress > 0.5 {
			glyphSize := trackHeight / 2
			p.Icon(IconCheck, ui.Rect{X: cx - glyphSize/2, Y: cy - glyphSize/2, W: glyphSize, H: glyphSize}, glyph)
		}
	})
	return s
}

const (
	dialogMaxWidthPercent = 92
	dialogWindowMargin    = 48
)

func Dialog(c *ui.Context, open *bool, width float32, build func()) {
	if !*open {
		return
	}
	sc := Of(c).Scheme
	_, windowHeight := c.Size()
	ui.Overlay(c, func() {
		back := ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).Center().
			Background(sc.Scrim.Alpha(0.4)).Modal()
		var panel ui.Element
		back.Children(func() {
			panel = ui.Scroll(c).Width(width).MaxWidthPercent(dialogMaxWidthPercent).MaxHeight(windowHeight-dialogWindowMargin).
				Padding(24).Gap(16).Radius(ExtraLarge).Background(sc.SurfaceContainerHigh).
				TextColor(sc.OnSurface).Role(ui.RoleDialog).Label("dialog-panel").
				ScrollbarInsets(16, 6, 16, 0)
			Elevation(c, panel, 3)
			panel.Children(build)
		})
		if scrimPressed(back, panel) || back.OverlayShortcut(0, ui.KeyEscape) {
			*open = false
		}
	})
}

func scrimPressed(scrim, panel ui.Element) bool {
	if !scrim.Clicked() {
		return false
	}
	x, y, _ := panel.PointerPosition()
	r := panel.Bounds()
	return x < 0 || y < 0 || x >= r.W || y >= r.H
}

func Snackbars(c *ui.Context, inset float32) {
	sc := Of(c).Scheme
	ui.ToastViewportBase(c, func(viewport ui.Element, toasts []ui.Toast) {
		viewport.Padding(16, 16, 16+inset, 16).AlignItems(ui.Center).Gap(8)
		for _, t := range toasts {
			toast := ui.ToastBase(c, t)
			toast.Root.Row().AlignItems(ui.Center).Gap(16).Padding(6, 8, 6, 16).MinHeight(48).MaxWidth(560).
				Radius(Medium).Background(sc.InverseSurface).TextColor(sc.InverseOnSurface)
			Elevation(c, toast.Root, 3)
			toast.Root.Transition(SpatialFast.Motion())
			toast.Root.Children(func() {
				if t.Type == "error" {
					ui.Icon(c, IconError).FontSize(20).TextColor(sc.Error)
				}
				BodyMedium.Style(ui.Text(c, t.Title), false).TextColor(sc.InverseOnSurface).Grow(1).MaxLines(2)
				if t.Action != "" {
					action := toast.ActionButton().Padding(0, 12).Height(MinTarget).Radius(Full).Center().
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

func Menu(c *ui.Context, anchor ui.Element, open *bool, width float32, build func()) {
	sc := Of(c).Scheme
	wasOpen := *open
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.Key(anchor.ID()).Width(width).Padding(8).Radius(Large).Margin(8, 0, 0, 0).Background(sc.SurfaceContainerHigh).
			TextColor(sc.OnSurface)
		Elevation(c, panel, 3)
		ui.Column(c).Gap(2).Role(ui.RoleMenu).Modal().FocusGroup(ui.Vertical).Children(build)
	})
	if wasOpen && !*open {
		c.AnimationFrame()
	}
}

func MenuItem(c *ui.Context, label string, icon *ui.SVG) ui.Element {
	sc := Of(c).Scheme
	b := ui.ButtonBase(c.Key("menu-" + label))
	b.Height(48).PaddingX(12).Gap(12).AlignItems(ui.Center).Radius(Medium).Cursor(ui.CursorPointer).Label(label).Role(ui.RoleMenuItem).
		Background(StateFill(ui.Transparent, sc.OnSurface, b.Hovered(), b.Pressed(), b.FocusVisible()))
	b.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(24).TextColor(sc.OnSurfaceVariant)
		}
		LabelLarge.Style(ui.Text(c, label), false).TextColor(sc.OnSurface).SingleLine().Grow(1).FontSize(15)
	})
	return b
}

func Art(c *ui.Context, art *ui.Bitmap, size, radius float32, overlays ...func()) ui.Element {
	sc := Of(c).Scheme
	box := ui.Box(c).Radius(radius).Background(sc.SurfaceContainerHighest).Shrink(0)
	if size > 0 {
		box.Size(size, size)
	}
	box.Children(func() {
		if art == nil {
			ui.Box(c).Fill().Center().Children(func() {
				ui.Icon(c, IconMusicNote).FontSize(max(size*0.4, 18)).TextColor(sc.OnSurfaceVariant.Alpha(0.6))
			})
		} else {
			ui.Image(c, art).Key("art").Fill().Radius(radius).ContentTransition(ui.ContentTransition{}).Fit(ui.Cover)
		}
		for _, overlay := range overlays {
			overlay()
		}
	})
	return box
}

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

func Avatar(c *ui.Context, name string, pic *ui.Bitmap, size float32) ui.Element {
	sc := Of(c).Scheme
	disc := ui.Box(c).Size(size, size).Radius(Full).Background(sc.PrimaryContainer).Shrink(0)
	disc.Children(func() {
		if pic != nil {
			ui.Image(c, pic).Fit(ui.Cover).Fill().Radius(Full)
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

type CarouselState struct {
	ui.ScrollState
	target  float32
	gliding bool
	view    float32
	asked   bool
}

func (s *CarouselState) Page(direction int) {
	from := s.X
	if s.gliding {
		from = s.target
	}
	s.target = min(max(from+float32(direction)*max(s.view*0.85, 200), 0), s.MaxX)
	s.gliding = true
}

func (s *CarouselState) CanPage(direction int) bool {
	if direction < 0 {
		return s.X > 1
	}
	return s.X < s.MaxX-1
}

func (s *CarouselState) NeedsMeasurement() bool {
	if s.view > 0 || s.asked {
		return false
	}
	s.asked = true
	return true
}

func (s *CarouselState) VisibleRange(count int, itemWidth, gap, padding float32, prefetch int) (first, last int) {
	if count <= 0 || itemWidth <= 0 || gap < 0 {
		return 0, 0
	}
	prefetch = max(prefetch, 0)
	step := itemWidth + gap
	view := s.view
	if view <= 0 {
		view = itemWidth*carouselUnmeasuredItems + gap*(carouselUnmeasuredItems-1)
	}
	first = int(math.Floor(float64((s.X-padding-itemWidth)/step))) + 1 - prefetch
	last = int(math.Ceil(float64((s.X+view-padding)/step))) + prefetch
	first = max(0, min(count, first))
	last = max(first, min(count, last))
	return first, last
}

const carouselUnmeasuredItems = 4

const carouselFoot float32 = 8

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
	row := ui.ScrollHorizontal(c.Key(key)).TrackScroll(&s.ScrollState).Gap(gap).AlignItems(ui.Start).
		Padding(0, padX, carouselFoot, padX).Scrollbars(false).Margin(0, 0, 12)
	row.Children(items)
	s.view = row.Bounds().W
	return row
}

type KeyPart struct {
	Text string
	Icon *ui.SVG
}

func KeyBadge(c *ui.Context, parts ...KeyPart) ui.Element {
	sc := Of(c).Scheme
	badge := ui.Box(c).Row().Height(22).PaddingX(7).MinWidth(22).Gap(2).Shrink(0).Center().AlignItems(ui.Center).Radius(Full).
		Background(sc.TertiaryContainer).Transition(Fade(EffectsFast))
	Elevation(c, badge, 1)
	return badge.Children(func() {
		for _, p := range parts {
			if p.Icon != nil {
				ui.Icon(c, p.Icon).FontSize(14).TextColor(sc.OnTertiaryContainer).Shrink(0)
				continue
			}
			Text(c, LabelMedium, p.Text).TextColor(sc.OnTertiaryContainer).NoWrap().Shrink(0)
		}
	})
}
