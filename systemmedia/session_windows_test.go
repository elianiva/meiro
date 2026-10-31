//go:build windows && (amd64 || arm64)

package systemmedia

import (
	"testing"
)

func TestWindowsSessionHotkey(t *testing.T) {
	var toggled, next, prev, stopped bool
	s := &windowsSession{
		controls: Controls{
			Toggle:   func() { toggled = true },
			Next:     func() { next = true },
			Previous: func() { prev = true },
			Stop:     func() { stopped = true },
		},
	}

	s.hotkey(hotkeyPlayPause)
	if !toggled {
		t.Error("hotkeyPlayPause did not invoke Toggle")
	}

	s.hotkey(hotkeyNextTrack)
	if !next {
		t.Error("hotkeyNextTrack did not invoke Next")
	}

	s.hotkey(hotkeyPrevTrack)
	if !prev {
		t.Error("hotkeyPrevTrack did not invoke Previous")
	}

	s.hotkey(hotkeyStop)
	if !stopped {
		t.Error("hotkeyStop did not invoke Stop")
	}

	s.hotkey(9999)
}

func TestWindowsSessionAppCommand(t *testing.T) {
	var toggled, played, paused, next, prev, stopped bool
	s := &windowsSession{
		controls: Controls{
			Toggle:   func() { toggled = true },
			Play:     func() { played = true },
			Pause:    func() { paused = true },
			Next:     func() { next = true },
			Previous: func() { prev = true },
			Stop:     func() { stopped = true },
		},
	}

	if !s.appCommand(appCommandMediaPlayPause) || !toggled {
		t.Error("appCommandMediaPlayPause failed")
	}
	if !s.appCommand(appCommandMediaPlay) || !played {
		t.Error("appCommandMediaPlay failed")
	}
	if !s.appCommand(appCommandMediaPause) || !paused {
		t.Error("appCommandMediaPause failed")
	}
	if !s.appCommand(appCommandMediaNextTrack) || !next {
		t.Error("appCommandMediaNextTrack failed")
	}
	if !s.appCommand(appCommandMediaPrevTrack) || !prev {
		t.Error("appCommandMediaPrevTrack failed")
	}
	if !s.appCommand(appCommandMediaStop) || !stopped {
		t.Error("appCommandMediaStop failed")
	}

	if s.appCommand(9999) {
		t.Error("unknown app command should return false")
	}
}

func TestWindowsSessionAppCommandFallback(t *testing.T) {
	var toggled int
	s := &windowsSession{
		controls: Controls{
			Toggle: func() { toggled++ },
		},
	}

	if !s.appCommand(appCommandMediaPlay) || toggled != 1 {
		t.Error("appCommandMediaPlay did not fall back to Toggle")
	}
	if !s.appCommand(appCommandMediaPause) || toggled != 2 {
		t.Error("appCommandMediaPause did not fall back to Toggle")
	}
}

func TestWindowsSessionNilControlsSafe(t *testing.T) {
	s := &windowsSession{}
	s.hotkey(hotkeyPlayPause)
	s.hotkey(hotkeyNextTrack)
	s.hotkey(hotkeyPrevTrack)
	s.hotkey(hotkeyStop)

	if s.appCommand(appCommandMediaPlayPause) {
		t.Error("expected false for nil controls")
	}
	if s.appCommand(appCommandMediaPlay) {
		t.Error("expected false for nil controls")
	}
	if s.appCommand(appCommandMediaPause) {
		t.Error("expected false for nil controls")
	}
	if s.appCommand(appCommandMediaNextTrack) {
		t.Error("expected false for nil controls")
	}
	if s.appCommand(appCommandMediaPrevTrack) {
		t.Error("expected false for nil controls")
	}
	if s.appCommand(appCommandMediaStop) {
		t.Error("expected false for nil controls")
	}
}
