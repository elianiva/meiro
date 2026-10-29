package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/go-macos/objc"
)

// readNotchCutout runs on the UI thread. AppKit describes the two usable
// rectangles beside the camera in points, so Retina scaling needs no guess.
// MyGo's display bounds supply its top-left coordinate convention.
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

func prepareNotchWindow(w *mygo.Window) {
	window := objc.ID(w.NativeHandle())
	// MyGo's Frameless hides the title but retains a titled window style.
	// Remove that title-bar geometry for the screen edge while preserving
	// NSWindowStyleMaskNonactivatingPanel from the panel's creation.
	window.Send(objc.Sel("setStyleMask:"), uint(1<<7))
	// NSStatusWindowLevel is above the menu bar; a floating panel's default
	// level is below it. Keep the footprint limited to the notch and its wings.
	window.Send(objc.Sel("setLevel:"), 25)
	window.Send(objc.Sel("setCanHide:"), false)
	// Compact playback needs clicks, not keyboard ownership. Expanded controls
	// opt in explicitly so Tab, seeking and Escape still work.
	setNotchFocusable(w, false)
	window.Send(objc.Sel("setAnimationBehavior:"), 2) // NSWindowAnimationBehaviorNone; the UI animates.
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
	// resignKeyWindow is an AppKit notification, not a request to release the
	// nonactivating panel's keyboard ownership. Order out and back in during
	// the same event turn, without becoming key or activating the application.
	// This leaves the UI's collapse animation and native bounds intact.
	visible := objc.Send[bool](window, objc.Sel("isVisible"))
	window.Send(objc.Sel("orderOut:"), objc.ID(0))
	if visible {
		window.Send(objc.Sel("orderFrontRegardless"))
	}
}

func watchNotchOutsideClicks(w *mygo.Window) {
	// Nonactivating panels need not receive a blur when another application's
	// menu bar is clicked. Observe pointer events; never consume or replay them.
	// Both AppKit monitor handlers run on the main thread.
	const presses = uint64(1<<1 | 1<<3 | 1<<25) // left, right, other mouse down
	const releases = uint64(1<<2 | 1<<4 | 1<<26)
	events := objc.ClassID("NSEvent")
	// Track delivered presses, including accessibility-generated events. The
	// hardware button snapshot need not reflect events queued by those tools.
	var held uint64
	releaseCompact := func() {
		window := objc.ID(w.NativeHandle())
		if held == 0 && window != 0 && objc.Send[bool](window, objc.Sel("becomesKeyOnlyIfNeeded")) {
			releaseNotchFocus(w)
		}
	}
	w.OnFocus(func() {
		// Activation can make even the compact panel key. Update can run
		// between down and up; ordering out then would cancel MyGo's click.
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
			// Let MyGo finish the press before releasing focus. If its view
			// has already expanded, leave the keyboard with the controls.
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
