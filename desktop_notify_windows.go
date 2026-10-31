//go:build windows && (amd64 || arm64)

package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"
)

var (
	notificationUser32 = windows.NewLazySystemDLL("user32.dll")
	notificationShell  = windows.NewLazySystemDLL("shell32.dll")

	procCreateNotificationIcon  = notificationUser32.NewProc("CreateIconFromResourceEx")
	procDestroyNotificationIcon = notificationUser32.NewProc("DestroyIcon")
	procFindNotificationWindow  = notificationUser32.NewProc("FindWindowExW")
	procShellNotifyIcon         = notificationShell.NewProc("Shell_NotifyIconW")
)

const (
	nimModifyNotification = 1
	nifInfoNotification   = 0x10
	niifUserNotification  = 0x4
	// niifLargeIconNotification lets the balloon icon be larger than a
	// small icon; without it Windows rejects the 48 px cover.
	niifLargeIconNotification = 0x20
	notificationIconSide      = 48
)

type notificationIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     uintptr
}

func notificationArtworkNeeded() bool { return true }

// prepareNotificationArtwork gives MyGo's notification backend the artwork
// tray icon to use when the user has hidden Meiro's persistent tray.
func prepareNotificationArtwork(d *desktop, icon []byte) error {
	if d.tray != nil {
		d.clearNotificationTray()
		return nil
	}
	if len(icon) == 0 {
		d.clearNotificationTray()
		return nil
	}
	if d.notificationTray != nil {
		if err := d.notificationTray.SetIcon(icon, false); err != nil {
			d.clearNotificationTray()
			return err
		}
		return nil
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           icon,
		IconIsTemplate: false,
	})
	if err != nil {
		return err
	}
	d.notificationTray = tray
	return nil
}

// expireNotificationTray removes the temporary tray icon after Windows has
// had time to display its notification.
func (d *desktop) expireNotificationTray() {
	if d.notificationTimer != nil {
		d.notificationTimer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(15*time.Second, func() {
		d.update(func() {
			if d.notificationTimer == timer {
				d.clearNotificationTray()
			}
		})
	})
	d.notificationTimer = timer
}

// applyNotificationArtwork attaches the cover as the notification's own
// HICON. The persistent app tray keeps its coloured brand icon.
func applyNotificationArtwork(d *desktop, title, body string, icon []byte) error {
	if d.tray == nil {
		if d.notificationTray != nil {
			d.expireNotificationTray()
		}
		return nil
	}
	if len(icon) == 0 {
		d.clearNotificationArtwork()
		return nil
	}
	return setWindowsBalloonIcon(d, title, body, icon)
}

func setWindowsBalloonIcon(d *desktop, title, body string, png []byte) error {
	icon, _, _ := procCreateNotificationIcon.Call(
		uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, 0x0003_0000,
		notificationIconSide, notificationIconSide, 0,
	)
	if icon == 0 {
		return errors.New("Windows could not create an icon from the cover")
	}
	owner := notificationTrayWindow()
	if owner == 0 {
		procDestroyNotificationIcon.Call(icon)
		return errors.New("MyGo's tray window was not found")
	}
	data := notificationIconData{
		Wnd:         owner,
		ID:          notificationTrayNumber(meiroTrayID),
		Flags:       nifInfoNotification,
		InfoFlags:   niifUserNotification | niifLargeIconNotification,
		BalloonIcon: icon,
	}
	data.Size = uint32(unsafe.Sizeof(data))
	copyNotificationText(data.InfoTitle[:], title)
	copyNotificationText(data.Info[:], body)
	r, _, callErr := procShellNotifyIcon.Call(nimModifyNotification, uintptr(unsafe.Pointer(&data)))
	if r == 0 {
		procDestroyNotificationIcon.Call(icon)
		if callErr != windows.ERROR_SUCCESS {
			return fmt.Errorf("Windows rejected the notification cover: %w", callErr)
		}
		return errors.New("Windows rejected the notification cover")
	}
	d.clearNotificationArtwork()
	d.notificationIcon = icon
	return nil
}

func copyNotificationText(dst []uint16, value string) {
	encoded, _ := windows.UTF16FromString(strings.ReplaceAll(value, "\x00", ""))
	n := copy(dst[:len(dst)-1], encoded)
	dst[n] = 0
}

// notificationTrayWindow finds this process's hidden MyGo application
// window. MyGo adds tray icons for it rather than for the main window, and
// Windows finds an icon by that window and the icon's number.
func notificationTrayWindow() uintptr {
	class, _ := windows.UTF16PtrFromString("MyGoApp")
	pid := windows.GetCurrentProcessId()
	var hwnd uintptr
	for {
		hwnd, _, _ = procFindNotificationWindow.Call(0, hwnd, uintptr(unsafe.Pointer(class)), 0)
		if hwnd == 0 {
			return 0
		}
		var owner uint32
		if _, err := windows.GetWindowThreadProcessId(windows.HWND(hwnd), &owner); err == nil && owner == pid {
			return hwnd
		}
	}
}

// notificationTrayNumber mirrors MyGo's stable tray ID so the cover updates
// the notification without replacing the icon the user sees in the tray.
func notificationTrayNumber(id string) uint32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	return hash.Sum32() | 1<<31
}

func destroyNotificationIcon(icon uintptr) { procDestroyNotificationIcon.Call(icon) }
