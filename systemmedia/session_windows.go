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
	"syscall"
	"unsafe"

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

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procRegisterWindowMessageW      = user32.NewProc("RegisterWindowMessageW")
	procCreateIconFromResourceEx    = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon                 = user32.NewProc("DestroyIcon")
	procGetSystemMetrics            = user32.NewProc("GetSystemMetrics")
	procIsWindow                    = user32.NewProc("IsWindow")
	procChangeWindowMessageFilterEx = user32.NewProc("ChangeWindowMessageFilterEx")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procSetWindowSubclass           = comctl32.NewProc("SetWindowSubclass")
	procRemoveWindowSubclass        = comctl32.NewProc("RemoveWindowSubclass")
	procDefSubclassProc             = comctl32.NewProc("DefSubclassProc")

	clsidTaskbarList = windows.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}
	// ITaskbarList4 rather than ITaskbarList3, which it extends with the
	// thumbnail toolbar's methods in the same places: Windows 10 22H2 has
	// been seen to answer E_NOINTERFACE to the ITaskbarList3 ID while it
	// answers to this one.
	iidITaskbarList4 = windows.GUID{Data1: 0xc43dc798, Data2: 0x95d1, Data3: 0x4bea, Data4: [8]byte{0x90, 0x30, 0xbb, 0x99, 0xe2, 0x98, 0x3a, 0x1a}}
)

const (
	wmCommand       = 0x0111
	wmSettingChange = 0x001A
	wmNCDestroy     = 0x0082
	thbnClicked     = 0x1800
	clsctxInproc    = 0x1
	smCxSmIcon      = 49
	subclassID      = 0x4d45_4952 // "MEIR"
	msgfltAllow     = 1

	thbIcon    = 0x2
	thbTooltip = 0x4
	thbFlags   = 0x8

	thbfEnabled  = 0x0
	thbfDisabled = 0x1
)

// taskbarList is an ITaskbarList4: what COM hands back is a pointer to a
// pointer to its table of methods.
type taskbarList struct{ vtbl *taskbarListVtbl }

// taskbarListVtbl is the start of ITaskbarList4's method table, in the
// order ShObjIdl_core.h declares the interfaces it extends: IUnknown,
// ITaskbarList, ITaskbarList2, then ITaskbarList3, which ends with the
// thumbnail toolbar's methods. ITaskbarList4 only adds one after them.
type taskbarListVtbl struct {
	QueryInterface, AddRef, Release                       uintptr
	HrInit, AddTab, DeleteTab, ActivateTab, SetActiveAlt  uintptr
	MarkFullscreenWindow                                  uintptr
	SetProgressValue, SetProgressState                    uintptr
	RegisterTab, UnregisterTab, SetTabOrder, SetTabActive uintptr
	ThumbBarAddButtons, ThumbBarUpdateButtons             uintptr
}

// call calls one of t's methods, which take t itself first.
func (t *taskbarList) call(method uintptr, args ...uintptr) error {
	hr, _, _ := syscall.SyscallN(method, append([]uintptr{uintptr(unsafe.Pointer(t))}, args...)...)
	if int32(uint32(hr)) < 0 {
		return fmt.Errorf("HRESULT %#08x", uint32(hr))
	}
	return nil
}

func (t *taskbarList) HrInit() error { return t.call(t.vtbl.HrInit) }
func (t *taskbarList) Release()      { _ = t.call(t.vtbl.Release) }

func (t *taskbarList) ThumbBarAddButtons(hwnd uintptr, buttons []thumbButton) error {
	return t.call(t.vtbl.ThumbBarAddButtons, hwnd, uintptr(len(buttons)), uintptr(unsafe.Pointer(&buttons[0])))
}

func (t *taskbarList) ThumbBarUpdateButtons(hwnd uintptr, buttons []thumbButton) error {
	return t.call(t.vtbl.ThumbBarUpdateButtons, hwnd, uintptr(len(buttons)), uintptr(unsafe.Pointer(&buttons[0])))
}

// thumbButton is THUMBBUTTON, laid out as on 64-bit Windows.
type thumbButton struct {
	Mask   uint32
	ID     uint32
	Bitmap uint32
	Icon   uintptr
	Tip    [260]uint16
	Flags  uint32
}

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
	hwnd     uintptr
	taskbar  *taskbarList
	// added is set once the current taskbar button has the toolbar.
	added bool
	state State
	// light is whether the taskbar is light, which takes dark icons.
	light bool
	// icons are made once per theme and kept while the toolbar may show them.
	icons [2][glyphCount]uintptr
	// shown is what the toolbar last showed, to skip identical updates.
	shown [buttonCount]thumbButton
}

var (
	// sessions are the subclassed windows, touched only on the UI thread.
	sessions             = map[uintptr]*windowsSession{}
	subclassCallback     uintptr
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
	if err := comctl32.Load(); err != nil {
		return nil, err
	}
	registerCallbackOnce.Do(func() {
		subclassCallback = windows.NewCallback(subclassProc)
		name, _ := windows.UTF16PtrFromString("TaskbarButtonCreated")
		r, _, _ := procRegisterWindowMessageW.Call(uintptr(unsafe.Pointer(name)))
		taskbarButtonCreated = uint32(r)
	})
	var taskbar *taskbarList
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(&iidITaskbarList4)), uintptr(unsafe.Pointer(&taskbar)))
	if int32(uint32(hr)) < 0 || taskbar == nil {
		return nil, fmt.Errorf("create the taskbar list: HRESULT %#08x", uint32(hr))
	}
	if err := taskbar.HrInit(); err != nil {
		taskbar.Release()
		return nil, fmt.Errorf("start the taskbar list: %w", err)
	}
	s := &windowsSession{controls: controls, hwnd: controls.Window, taskbar: taskbar, light: lightTaskbar()}
	if ok, _, _ := procSetWindowSubclass.Call(s.hwnd, subclassCallback, subclassID, 0); ok == 0 {
		taskbar.Release()
		return nil, errors.New("cannot subclass the window for its taskbar buttons")
	}
	sessions[s.hwnd] = s
	// An elevated Meiro would not hear Explorer, which runs unelevated.
	for _, msg := range []uint32{taskbarButtonCreated, wmCommand} {
		_, _, _ = procChangeWindowMessageFilterEx.Call(s.hwnd, uintptr(msg), msgfltAllow, 0)
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
	if ok, _, _ := procIsWindow.Call(s.hwnd); ok != 0 {
		_, _, _ = procRemoveWindowSubclass.Call(s.hwnd, subclassCallback, subclassID)
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
				_, _, _ = procDestroyIcon.Call(icon)
				s.icons[theme][i] = 0
			}
		}
	}
}

func subclassProc(hwnd, msg, wParam, lParam, _, _ uintptr) uintptr {
	if s := sessions[hwnd]; s != nil {
		switch {
		case uint32(msg) == taskbarButtonCreated && taskbarButtonCreated != 0:
			// A new button, without a toolbar. MyGo hears it too, for the
			// progress bar.
			s.added = false
			s.shown = [buttonCount]thumbButton{}
			s.apply()
		case msg == wmCommand && wParam>>16&0xffff == thbnClicked:
			s.click(int(wParam & 0xffff))
			return 0
		case msg == wmSettingChange && lParam != 0 &&
			windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lParam))) == "ImmersiveColorSet": //nolint:govet // the system's string, not Go memory
			if light := lightTaskbar(); light != s.light {
				s.light = light
				s.apply()
			}
		case msg == wmNCDestroy:
			_, _, _ = procRemoveWindowSubclass.Call(hwnd, subclassCallback, subclassID)
			s.release()
		}
	}
	r, _, _ := procDefSubclassProc.Call(hwnd, msg, wParam, lParam)
	return r
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
	buttons := [buttonCount]thumbButton{
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

func (s *windowsSession) button(id int, g glyph, tip string, enabled bool) thumbButton {
	b := thumbButton{Mask: thbIcon | thbTooltip | thbFlags, ID: uint32(id), Icon: s.icon(g), Flags: thbfDisabled}
	if enabled {
		b.Flags = thbfEnabled
	}
	tip16, _ := windows.UTF16FromString(tip)
	copy(b.Tip[:len(b.Tip)-1], tip16)
	return b
}

// icon returns the glyph's icon in the taskbar theme's colour.
func (s *windowsSession) icon(g glyph) uintptr {
	theme := 0
	if s.light {
		theme = 1
	}
	if s.icons[theme][g] == 0 {
		fill := color.RGBA{0xff, 0xff, 0xff, 0xff}
		if s.light {
			fill = color.RGBA{0x1f, 0x1f, 0x1f, 0xff}
		}
		size, _, _ := procGetSystemMetrics.Call(smCxSmIcon)
		if size == 0 {
			size = 16
		}
		data := glyphPNG(g, int(size), fill)
		s.icons[theme][g], _, _ = procCreateIconFromResourceEx.Call(uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
			1, 0x0003_0000, size, size, 0)
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
