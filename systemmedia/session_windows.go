//go:build windows && (amd64 || arm64)

package systemmedia

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"runtime"
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
// the window, which the session subclasses to hear them on the UI thread.
// The taskbar's COM object lives on a thread of its own (see run).

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procRegisterWindowMessageW      = user32.NewProc("RegisterWindowMessageW")
	procCreateIconFromResourceEx    = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon                 = user32.NewProc("DestroyIcon")
	procGetSystemMetrics            = user32.NewProc("GetSystemMetrics")
	procChangeWindowMessageFilterEx = user32.NewProc("ChangeWindowMessageFilterEx")
	procCoCreateInstance            = ole32.NewProc("CoCreateInstance")
	procCoGetApartmentType          = ole32.NewProc("CoGetApartmentType")
	procSetWindowSubclass           = comctl32.NewProc("SetWindowSubclass")
	procRemoveWindowSubclass        = comctl32.NewProc("RemoveWindowSubclass")
	procDefSubclassProc             = comctl32.NewProc("DefSubclassProc")

	clsidTaskbarList = windows.GUID{Data1: 0x56fdf344, Data2: 0xfd6d, Data3: 0x11d0, Data4: [8]byte{0x95, 0x8a, 0x00, 0x60, 0x97, 0xc9, 0xa0, 0x90}}
	iidITaskbarList3 = windows.GUID{Data1: 0xea1afb91, Data2: 0x9e28, Data3: 0x4b86, Data4: [8]byte{0x90, 0xe9, 0x9e, 0x9f, 0x8a, 0x5e, 0xee, 0x84}}
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

	// ITaskbarList3 vtable indices (ShObjIdl_core.h).
	tbRelease               = 2
	tbHrInit                = 3
	tbThumbBarAddButtons    = 15
	tbThumbBarUpdateButtons = 16

	thbIcon    = 0x2
	thbTooltip = 0x4
	thbFlags   = 0x8

	thbfEnabled  = 0x0
	thbfDisabled = 0x1
)

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

	// What the UI thread hands the taskbar thread, under mu.
	mu      sync.Mutex
	state   State
	light   bool // the taskbar is light, which takes dark icons
	rebuilt bool // Explorer made a new taskbar button, without a toolbar
	wake    chan struct{}
	quit    chan struct{}
	stop    sync.Once

	// The taskbar thread's own.
	taskbar uintptr // ITaskbarList3
	// added is set once the current taskbar button has the toolbar.
	added bool
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
	logApartment()
	s := &windowsSession{
		controls: controls, hwnd: controls.Window, light: lightTaskbar(),
		wake: make(chan struct{}, 1), quit: make(chan struct{}),
	}
	started := make(chan error)
	go s.run(started)
	if err := <-started; err != nil {
		return nil, err
	}
	if ok, _, _ := procSetWindowSubclass.Call(s.hwnd, subclassCallback, subclassID, 0); ok == 0 {
		s.end()
		return nil, errors.New("cannot subclass the window for its taskbar buttons")
	}
	sessions[s.hwnd] = s
	// An elevated Meiro would not hear Explorer, which runs unelevated.
	for _, msg := range []uint32{taskbarButtonCreated, wmCommand} {
		_, _, _ = procChangeWindowMessageFilterEx.Call(s.hwnd, uintptr(msg), msgfltAllow, 0)
	}
	// The button may already be there, with its TaskbarButtonCreated gone
	// before the subclass could hear it.
	s.signal()
	return s, nil
}

// logApartment notes the UI thread's COM apartment when it is not the
// single-threaded one MyGo asks for, which is why the taskbar object lives
// on its own thread: an apartment-threaded object made on a multithreaded
// thread comes as a proxy, and ITaskbarList3 has none (E_NOINTERFACE).
func logApartment() {
	var kind, qualifier int32
	hr, _, _ := procCoGetApartmentType.Call(uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&qualifier)))
	const aptSTA, aptMainSTA = 0, 3
	if failed(hr) {
		log.Printf("windows taskbar: the UI thread has no COM apartment (HRESULT %#08x)", uint32(hr))
	} else if kind != aptSTA && kind != aptMainSTA {
		log.Printf("windows taskbar: the UI thread's COM apartment is type %d, not single-threaded", kind)
	}
}

// run owns the taskbar object, on a thread of its own in a single-threaded
// apartment whatever the UI thread's apartment is. It reports on started
// whether the object could be made, then applies what the UI thread hands
// it until the session ends.
func (s *windowsSession) run(started chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// S_FALSE: the thread already had this apartment, which still needs
	// the CoUninitialize.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		started <- fmt.Errorf("start COM for the taskbar: %w", err)
		return
	}
	defer windows.CoUninitialize()
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidTaskbarList)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(&iidITaskbarList3)), uintptr(unsafe.Pointer(&s.taskbar)))
	if failed(hr) || s.taskbar == 0 {
		started <- fmt.Errorf("create the taskbar list: HRESULT %#08x", uint32(hr))
		return
	}
	defer s.release()
	if hr := comCall(s.taskbar, tbHrInit); failed(hr) {
		started <- fmt.Errorf("start the taskbar list: HRESULT %#08x", uint32(hr))
		return
	}
	close(started)
	for {
		select {
		case <-s.wake:
			s.apply()
		case <-s.quit:
			return
		}
	}
}

// signal has the taskbar thread show what changed.
func (s *windowsSession) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// end stops the taskbar thread, which lets go of what it holds. It does
// not wait: the thread may be inside a call to Explorer.
func (s *windowsSession) end() { s.stop.Do(func() { close(s.quit) }) }

func (s *windowsSession) Update(state State) error {
	s.mu.Lock()
	s.state = state
	s.mu.Unlock()
	s.signal()
	return nil
}

func (s *windowsSession) Close() error {
	if sessions[s.hwnd] == s {
		_, _, _ = procRemoveWindowSubclass.Call(s.hwnd, subclassCallback, subclassID)
		delete(sessions, s.hwnd)
	}
	s.end()
	return nil
}

// release lets go of the taskbar object and the icons, on the taskbar
// thread as it ends.
func (s *windowsSession) release() {
	comCall(s.taskbar, tbRelease)
	s.taskbar = 0
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
			s.mu.Lock()
			s.rebuilt = true
			s.mu.Unlock()
			s.signal()
		case msg == wmCommand && wParam>>16&0xffff == thbnClicked:
			s.click(int(wParam & 0xffff))
			return 0
		case msg == wmSettingChange && lParam != 0 &&
			windows.UTF16PtrToString((*uint16)(unsafe.Pointer(lParam))) == "ImmersiveColorSet": //nolint:govet // the system's string, not Go memory
			light := lightTaskbar()
			s.mu.Lock()
			s.light = light
			s.mu.Unlock()
			s.signal()
		case msg == wmNCDestroy:
			_ = s.Close()
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

// apply shows the state's buttons on the taskbar button's toolbar. It runs
// on the taskbar thread.
func (s *windowsSession) apply() {
	s.mu.Lock()
	state, light, rebuilt := s.state, s.light, s.rebuilt
	s.rebuilt = false
	s.mu.Unlock()
	if rebuilt {
		s.added = false
	}
	playPause, playTip, canPlayPause := glyphPlay, "Play", state.CanPlay
	if state.Status == Playing {
		playPause, playTip, canPlayPause = glyphPause, "Pause", state.CanPause
	}
	buttons := [buttonCount]thumbButton{
		s.button(buttonPrevious, glyphPrevious, "Previous", state.CanPrevious, light),
		s.button(buttonPlayPause, playPause, playTip, canPlayPause, light),
		s.button(buttonNext, glyphNext, "Next", state.CanNext, light),
	}
	if s.added && buttons == s.shown {
		return
	}
	ptr := uintptr(unsafe.Pointer(&buttons[0]))
	if !s.added {
		// Adding fails when the button already has a toolbar, for a
		// TaskbarButtonCreated that came after the first add.
		if hr := comCall(s.taskbar, tbThumbBarAddButtons, s.hwnd, buttonCount, ptr); failed(hr) &&
			failed(comCall(s.taskbar, tbThumbBarUpdateButtons, s.hwnd, buttonCount, ptr)) {
			// No button yet: TaskbarButtonCreated adds the toolbar later.
			return
		}
		s.added = true
	} else if failed(comCall(s.taskbar, tbThumbBarUpdateButtons, s.hwnd, buttonCount, ptr)) {
		return
	}
	s.shown = buttons
}

func (s *windowsSession) button(id int, g glyph, tip string, enabled, light bool) thumbButton {
	b := thumbButton{Mask: thbIcon | thbTooltip | thbFlags, ID: uint32(id), Icon: s.icon(g, light), Flags: thbfDisabled}
	if enabled {
		b.Flags = thbfEnabled
	}
	tip16, _ := windows.UTF16FromString(tip)
	copy(b.Tip[:len(b.Tip)-1], tip16)
	return b
}

// icon returns the glyph's icon in the taskbar theme's colour.
func (s *windowsSession) icon(g glyph, light bool) uintptr {
	theme := 0
	if light {
		theme = 1
	}
	if s.icons[theme][g] == 0 {
		fill := color.RGBA{0xff, 0xff, 0xff, 0xff}
		if light {
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

func comCall(obj uintptr, method int, args ...uintptr) uintptr {
	// The object and its vtable are COM's memory, not Go's.
	vtbl := *(*uintptr)(unsafe.Pointer(obj))                                            //nolint:govet // see above
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(method)*unsafe.Sizeof(uintptr(0)))) //nolint:govet // see above
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func failed(hr uintptr) bool { return int32(uint32(hr)) < 0 }
