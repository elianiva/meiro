//go:build windows && (amd64 || arm64)

package systemmedia

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"sync"
	"unsafe"

	win32 "github.com/deploymenttheory/go-bindings-win32/bindings/runtime/win32"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/foundation"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/system/com"
	"github.com/deploymenttheory/go-bindings-win32/bindings/win32/ui/shell"
	wm "github.com/deploymenttheory/go-bindings-win32/bindings/win32/ui/windowsandmessaging"
	"golang.org/x/image/vector"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// Windows puts Previous, Play or Pause, and Next under the thumbnail that
// shows when the pointer rests on the taskbar button, the way Spotify does.
//
// Explorer makes the taskbar button when the window first shows, and again
// when the window shows after hiding or when Explorer restarts, and each
// time sends the window TaskbarButtonCreated. A new button has no toolbar,
// so the buttons are added again then. The clicks arrive as WM_COMMAND on
// the window, which the session subclasses to hear them. Everything runs on
// the UI thread, which owns the window and the taskbar's COM object.
//
// The Win32 and COM declarations come from go-bindings-win32, which
// generates them from Microsoft's metadata, so its GUIDs and vtable slots
// are the SDK's.

// clsidTaskbarList is CLSID_TaskbarList, the one declaration the bindings
// lack: they give the TaskbarList coclass no GUID.
var clsidTaskbarList = win32.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}

const subclassID = 0x4d45_4952 // "MEIR"

// The buttons, by their ID, in the order they show.
const (
	buttonPrevious = iota
	buttonPlayPause
	buttonNext
	buttonCount
)

type glyph int

const (
	glyphPrevious glyph = iota
	glyphPlay
	glyphPause
	glyphNext
	glyphCount
)

// glyphs are Material's previous, play, pause and next icons on their 24×24
// grid, as polygons.
var glyphs = [glyphCount][][][2]float32{
	glyphPrevious: {{{6, 6}, {8, 6}, {8, 18}, {6, 18}}, {{9.5, 12}, {18, 18}, {18, 6}}},
	glyphPlay:     {{{8, 5}, {8, 19}, {19, 12}}},
	glyphPause:    {{{6, 5}, {10, 5}, {10, 19}, {6, 19}}, {{14, 5}, {18, 5}, {18, 19}, {14, 19}}},
	glyphNext:     {{{6, 18}, {14.5, 12}, {6, 6}}, {{16, 6}, {18, 6}, {18, 18}, {16, 18}}},
}

type windowsSession struct {
	controls Controls
	hwnd     foundation.HWND
	taskbar  *shell.ITaskbarList3
	// added is set once the current taskbar button has the toolbar.
	added bool
	state State
	// light is whether the taskbar is light, which takes dark icons.
	light bool
	// icons are made once per theme and kept while the toolbar may show them.
	icons [2][glyphCount]wm.HICON
	// shown is what the toolbar last showed, to skip identical updates.
	shown [buttonCount]shell.THUMBBUTTON
}

var (
	// sessions are the subclassed windows, touched only on the UI thread.
	sessions             = map[foundation.HWND]*windowsSession{}
	subclassCallback     shell.SUBCLASSPROC
	taskbarButtonCreated uint32
	registerCallbackOnce sync.Once
	errNoWindow          = errors.New("the Windows taskbar controls need a window")
)

// New adds media buttons to the taskbar thumbnail of controls.Window. It
// must run on the window's UI thread, and so must the session's methods.
func New(controls Controls) (Session, error) {
	if controls.Window == 0 {
		return nil, errNoWindow
	}
	// The bindings panic on a missing export; ask first.
	if err := shell.Procs.SetWindowSubclass.Find(); err != nil {
		return nil, err
	}
	registerCallbackOnce.Do(func() {
		subclassCallback = shell.SUBCLASSPROC(windows.NewCallback(subclassProc))
		taskbarButtonCreated, _ = wm.RegisterWindowMessage("TaskbarButtonCreated")
	})
	var unknown *win32.IUnknown
	if err := com.CoCreateInstance(&clsidTaskbarList, nil, com.CLSCTX_INPROC_SERVER, &shell.IID_ITaskbarList3, &unknown); err != nil {
		return nil, fmt.Errorf("create the taskbar list: %w", err)
	}
	taskbar := win32.Cast[shell.ITaskbarList3](unknown)
	if err := taskbar.HrInit(); err != nil {
		taskbar.Release()
		return nil, fmt.Errorf("start the taskbar list: %w", err)
	}
	s := &windowsSession{controls: controls, hwnd: foundation.HWND(controls.Window), taskbar: taskbar, light: lightTaskbar()}
	if !shell.SetWindowSubclass(s.hwnd, subclassCallback, subclassID, 0) {
		taskbar.Release()
		return nil, errors.New("cannot subclass the window for its taskbar buttons")
	}
	sessions[s.hwnd] = s
	// An elevated Meiro would not hear Explorer, which runs unelevated.
	for _, msg := range []uint32{taskbarButtonCreated, wm.WM_COMMAND} {
		_ = wm.ChangeWindowMessageFilterEx(s.hwnd, msg, wm.MSGFLT_ALLOW, nil)
	}
	// The button may already be there, with its TaskbarButtonCreated gone
	// before the subclass could hear it.
	s.apply()
	return s, nil
}

func (s *windowsSession) Update(state State) error {
	s.state = state
	s.apply()
	return nil
}

func (s *windowsSession) Close() error {
	if s.hwnd == 0 {
		return nil
	}
	if wm.IsWindow(s.hwnd) {
		shell.RemoveWindowSubclass(s.hwnd, subclassCallback, subclassID)
	}
	s.release()
	return nil
}

// release lets go of the taskbar and the icons once the window is gone or
// the session closes.
func (s *windowsSession) release() {
	delete(sessions, s.hwnd)
	s.hwnd = 0
	if s.taskbar != nil {
		s.taskbar.Release()
		s.taskbar = nil
	}
	for theme := range s.icons {
		for i, icon := range s.icons[theme] {
			if icon != 0 {
				_ = wm.DestroyIcon(icon)
				s.icons[theme][i] = 0
			}
		}
	}
}

// subclassProc takes every argument as a full word, as Windows passes them.
func subclassProc(hwnd foundation.HWND, message uintptr, wParam foundation.WPARAM, lParam foundation.LPARAM, _, _ uintptr) foundation.LRESULT {
	msg := uint32(message)
	if s := sessions[hwnd]; s != nil {
		switch {
		case msg == taskbarButtonCreated && taskbarButtonCreated != 0:
			// A new button, without a toolbar. MyGo hears it too, for the
			// progress bar.
			s.added = false
			s.shown = [buttonCount]shell.THUMBBUTTON{}
			s.apply()
		case msg == wm.WM_COMMAND && uint32(wParam>>16&0xffff) == shell.THBN_CLICKED:
			s.click(int(wParam & 0xffff))
			return 0
		case msg == wm.WM_SETTINGCHANGE && lParam != 0 &&
			win32.UTF16ToString((*uint16)(unsafe.Pointer(lParam))) == "ImmersiveColorSet": //nolint:govet // the system's string, not Go memory
			if light := lightTaskbar(); light != s.light {
				s.light = light
				s.apply()
			}
		case msg == wm.WM_NCDESTROY:
			shell.RemoveWindowSubclass(hwnd, subclassCallback, subclassID)
			s.release()
		}
	}
	return shell.DefSubclassProc(hwnd, msg, wParam, lParam)
}

func (s *windowsSession) click(id int) {
	var fn func()
	switch id {
	case buttonPrevious:
		fn = s.controls.Previous
	case buttonPlayPause:
		fn = s.controls.Toggle
	case buttonNext:
		fn = s.controls.Next
	}
	if fn != nil {
		fn()
	}
}

// apply shows the state's buttons on the taskbar button's toolbar.
func (s *windowsSession) apply() {
	if s.taskbar == nil {
		return
	}
	playPause, playTip, canPlayPause := glyphPlay, "Play", s.state.CanPlay
	if s.state.Status == Playing {
		playPause, playTip, canPlayPause = glyphPause, "Pause", s.state.CanPause
	}
	buttons := [buttonCount]shell.THUMBBUTTON{
		s.button(buttonPrevious, glyphPrevious, "Previous", s.state.CanPrevious),
		s.button(buttonPlayPause, playPause, playTip, canPlayPause),
		s.button(buttonNext, glyphNext, "Next", s.state.CanNext),
	}
	if s.added && buttons == s.shown {
		return
	}
	if !s.added {
		// Adding fails when the button already has a toolbar, for a
		// TaskbarButtonCreated that came after the first add.
		if s.taskbar.ThumbBarAddButtons(s.hwnd, buttons[:]) != nil &&
			s.taskbar.ThumbBarUpdateButtons(s.hwnd, buttons[:]) != nil {
			// No button yet: TaskbarButtonCreated adds the toolbar later.
			return
		}
		s.added = true
	} else if s.taskbar.ThumbBarUpdateButtons(s.hwnd, buttons[:]) != nil {
		return
	}
	s.shown = buttons
}

func (s *windowsSession) button(id int, g glyph, tip string, enabled bool) shell.THUMBBUTTON {
	b := shell.THUMBBUTTON{
		DwMask:  shell.THB_ICON | shell.THB_TOOLTIP | shell.THB_FLAGS,
		IId:     uint32(id),
		HIcon:   s.icon(g),
		DwFlags: shell.THBF_DISABLED,
	}
	if enabled {
		b.DwFlags = shell.THBF_ENABLED
	}
	tip16, _ := windows.UTF16FromString(tip)
	copy(b.SzTip[:len(b.SzTip)-1], tip16)
	return b
}

// icon returns the glyph's icon in the taskbar theme's colour.
func (s *windowsSession) icon(g glyph) wm.HICON {
	theme := 0
	if s.light {
		theme = 1
	}
	if s.icons[theme][g] == 0 {
		fill := color.RGBA{0xff, 0xff, 0xff, 0xff}
		if s.light {
			fill = color.RGBA{0x1f, 0x1f, 0x1f, 0xff}
		}
		size := wm.GetSystemMetrics(wm.SM_CXSMICON)
		if size == 0 {
			size = 16
		}
		s.icons[theme][g], _ = wm.CreateIconFromResourceEx(glyphPNG(g, int(size), fill), true, 0x0003_0000, size, size, wm.LR_DEFAULTCOLOR)
	}
	return s.icons[theme][g]
}

// glyphPNG draws the glyph, size pixels square, as a PNG, which
// CreateIconFromResourceEx takes as an icon image.
func glyphPNG(g glyph, size int, fill color.RGBA) []byte {
	scale := float32(size) / 24
	var z vector.Rasterizer
	z.Reset(size, size)
	for _, polygon := range glyphs[g] {
		z.MoveTo(polygon[0][0]*scale, polygon[0][1]*scale)
		for _, p := range polygon[1:] {
			z.LineTo(p[0]*scale, p[1]*scale)
		}
		z.ClosePath()
	}
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	z.Draw(img, img.Bounds(), image.NewUniform(fill), image.Point{})
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// lightTaskbar reads whether the taskbar, and with it the thumbnail, is
// light. Windows before the setting existed has a dark taskbar.
func lightTaskbar() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	light, _, err := key.GetIntegerValue("SystemUsesLightTheme")
	return err == nil && light != 0
}
