package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/go-macos/objc"
)

func readNotchCutout() (cutout mygo.Rectangle) {
	displays := mygo.Screen.Displays()
	objc.AutoreleasePool(func() {
		screens := objc.ClassID("NSScreen").Send(objc.Sel("screens"))
		count := objc.Send[uint](screens, objc.Sel("count"))
		for i := range count {
			screen := screens.Send(objc.Sel("objectAtIndex:"), i)
			if !objc.Send[bool](screen, objc.Sel("respondsToSelector:"), objc.Sel("auxiliaryTopLeftArea")) {
				continue
			}
			number := screen.Send(objc.Sel("deviceDescription")).Send(objc.Sel("objectForKey:"), objc.NSString("NSScreenNumber"))
			id := objc.Send[int64](number, objc.Sel("longLongValue"))
			for _, display := range displays {
				if display.ID != id || !display.Internal {
					continue
				}
				left := objc.Send[objc.NSRect](screen, objc.Sel("auxiliaryTopLeftArea"))
				right := objc.Send[objc.NSRect](screen, objc.Sel("auxiliaryTopRightArea"))
				frame := objc.Send[objc.NSRect](screen, objc.Sel("frame"))
				width := right.Origin.X - (left.Origin.X + left.Size.Width)
				if left.Size.Height <= 0 || right.Size.Height <= 0 || width <= 0 || width >= frame.Size.Width {
					continue
				}
				cutout = mygo.Rectangle{
					X: display.Bounds.X + int(math.Round(left.Origin.X+left.Size.Width-frame.Origin.X)),
					Y: display.Bounds.Y, Width: int(math.Round(width)), Height: int(math.Round(left.Size.Height)),
				}
				return
			}
		}
	})
	return cutout
}

const (
	nsWindowStyleMaskNonactivatingPanel = uint(1 << 7)
	nsStatusWindowLevel                 = 25
	nsWindowAnimationBehaviorNone       = 2

	nsEventMaskLeftMouseDown  = uint64(1 << 1)
	nsEventMaskLeftMouseUp    = uint64(1 << 2)
	nsEventMaskRightMouseDown = uint64(1 << 3)
	nsEventMaskRightMouseUp   = uint64(1 << 4)
	nsEventMaskOtherMouseDown = uint64(1 << 25)
	nsEventMaskOtherMouseUp   = uint64(1 << 26)
	mousePressEvents          = nsEventMaskLeftMouseDown | nsEventMaskRightMouseDown | nsEventMaskOtherMouseDown
	mouseReleaseEvents        = nsEventMaskLeftMouseUp | nsEventMaskRightMouseUp | nsEventMaskOtherMouseUp
)

func prepareNotchWindow(w *mygo.Window) {
	window := objc.ID(w.NativeHandle())
	window.Send(objc.Sel("setStyleMask:"), nsWindowStyleMaskNonactivatingPanel)
	window.Send(objc.Sel("setLevel:"), nsStatusWindowLevel)
	window.Send(objc.Sel("setCanHide:"), false)
	setNotchFocusable(w, false)
	window.Send(objc.Sel("setAnimationBehavior:"), nsWindowAnimationBehaviorNone)
	watchNotchOutsideClicks(w)
}

func setNotchFocusable(w *mygo.Window, focusable bool) {
	objc.ID(w.NativeHandle()).Send(objc.Sel("setBecomesKeyOnlyIfNeeded:"), !focusable)
}

func focusNotchWindow(w *mygo.Window) {
	objc.ID(w.NativeHandle()).Send(objc.Sel("makeKeyWindow"))
}

func releaseNotchFocus(w *mygo.Window) {
	window := objc.ID(w.NativeHandle())
	if window == 0 || !objc.Send[bool](window, objc.Sel("isKeyWindow")) {
		return
	}
	visible := objc.Send[bool](window, objc.Sel("isVisible"))
	window.Send(objc.Sel("orderOut:"), objc.ID(0))
	if visible {
		window.Send(objc.Sel("orderFrontRegardless"))
	}
}

func watchNotchOutsideClicks(w *mygo.Window) {
	presses, releases := mousePressEvents, mouseReleaseEvents
	events := objc.ClassID("NSEvent")
	var held uint64
	releaseCompact := func() {
		window := objc.ID(w.NativeHandle())
		if held == 0 && window != 0 && objc.Send[bool](window, objc.Sel("becomesKeyOnlyIfNeeded")) {
			releaseNotchFocus(w)
		}
	}
	w.OnFocus(func() {
		w.Update(releaseCompact)
	})
	w.OnBlur(func() { held = 0 })
	globalBlock := objc.NewBlock(func(_ objc.Block, _ objc.ID) {
		releaseNotchFocus(w)
	})
	localBlock := objc.NewBlock(func(_ objc.Block, event objc.ID) objc.ID {
		kind := objc.Send[uint](event, objc.Sel("type"))
		button := uint64(1) << objc.Send[uint](event, objc.Sel("buttonNumber"))
		switch {
		case releases&(uint64(1)<<kind) != 0:
			held &^= button
			w.Update(releaseCompact)
		case event.Send(objc.Sel("window")) == objc.ID(w.NativeHandle()):
			held |= button
		default:
			releaseNotchFocus(w)
		}
		return event
	})
	global := events.Send(objc.Sel("addGlobalMonitorForEventsMatchingMask:handler:"), presses, globalBlock)
	local := events.Send(objc.Sel("addLocalMonitorForEventsMatchingMask:handler:"), presses|releases, localBlock)
	w.OnClosed(func() {
		if global != 0 {
			events.Send(objc.Sel("removeMonitor:"), global)
		}
		if local != 0 {
			events.Send(objc.Sel("removeMonitor:"), local)
		}
		globalBlock.Release()
		localBlock.Release()
	})
}
