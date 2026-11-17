package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

type desktop struct {
	win               *mygo.Window
	awake             func()
	tray              *mygo.Tray
	trayIcon          string
	mini              mini
	notch             notchWindow
	notification      *mygo.Notification
	trayShown         trayState
	titleShown        string
	trayFailed        bool
	closeChoice       func() *bool
	answerClose       func(toTray, remember bool) bool
	quitting          bool
	restoreFullScreen bool
	leavingFullScreen int
}

func (d *desktop) update(fn func()) {
	if d == nil {
		fn()
		return
	}
	d.win.Update(func() {
		fn()
		d.invalidateMini()
		d.invalidateNotch()
	})
}

func (d *desktop) openPath(path string) error {
	if d == nil {
		return nil
	}
	return mygo.Shell.OpenPath(path)
}

func (d *desktop) invalidate() {
	if d != nil {
		d.win.Invalidate()
		d.invalidateMini()
		d.invalidateNotch()
	}
}

func (d *desktop) invalidateMini() {
	if fl := d.mini.win.Load(); fl != nil {
		fl.Invalidate()
	}
}

func (d *desktop) bringToFront() {
	if d == nil {
		return
	}
	d.win.Show()
	d.win.Restore()
	d.win.Focus()
}

func (d *desktop) keepAwake(on bool) {
	if d == nil || on == (d.awake != nil) {
		return
	}
	if d.awake != nil {
		d.awake()
		d.awake = nil
		return
	}
	d.awake = mygo.Power.KeepAwake("Playing music", false)
}

type trackNotices interface {
	appActive() bool
	notify(item youtube.MusicItem, icon []byte)
	dismissNotification()
}

func (d *desktop) appActive() bool { return d != nil && mygo.App.IsActive() }

func (d *desktop) dismissNotification() {
	if d == nil {
		return
	}
	if d.notification != nil {
		d.notification.Close()
		d.notification = nil
	}
}

func (d *desktop) notify(item youtube.MusicItem, icon []byte) {
	if d == nil || runtime.GOOS == "darwin" || !mygo.NotificationsSupported() {
		return
	}
	if d.notification != nil {
		d.notification.Close()
		d.notification = nil
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "now-playing",
		Title: item.Title,
		Body:  item.Subtitle,
		Group: "now-playing",
		Image: icon,
	})
	n.OnClick(func() {
		d.update(func() {
			d.bringToFront()
			if d.notification == n {
				d.notification = nil
			}
		})
		n.Close()
	})
	d.notification = n
	if err := n.Show(); err != nil {
		log.Printf("showing track notification: %v", err)
		d.notification = nil
	}
}

const toggleShortcut = "CmdOrCtrl+Shift+Space"

func (d *desktop) setShortcut(on bool, toggle func()) bool {
	if d == nil {
		return true
	}
	if !on {
		mygo.GlobalShortcut.Unregister(toggleShortcut)
		return true
	}
	if mygo.GlobalShortcut.IsRegistered(toggleShortcut) {
		return true
	}
	if err := mygo.GlobalShortcut.Register(toggleShortcut, toggle); err != nil {
		log.Printf("registering the global shortcut: %v", err)
		return false
	}
	return true
}

func (d *desktop) setOpenAtLogin(on bool) {
	if d == nil || mygo.App.OpenAtLogin() == on {
		return
	}
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		log.Printf("setting open at login: %v", err)
	}
}

type trayState struct {
	tip string
}

func (d *desktop) setTray(on bool, mark settings.TrayMark, state trayState, mini func(*ui.Context), shown func(bool)) {
	if d == nil {
		return
	}
	if !on {
		if d.tray != nil {
			d.closeMini()
			d.tray.Destroy()
			d.tray, d.trayIcon, d.trayShown = nil, "", trayState{}
		}
		d.trayFailed = false
		return
	}
	if d.trayFailed {
		return
	}
	if d.tray == nil {
		name := trayIconName(runtime.GOOS, mark)
		icon, err := trayIcon(name)
		if err != nil {
			log.Printf("tray icon unavailable: %v", err)
		}
		onOpenMiniPlayer := func() { d.update(func() { d.showMini(mini, shown) }) }
		options := mygo.TrayOptions{
			ID:             meiroTrayID,
			Icon:           icon,
			IconIsTemplate: trayIconTemplate(runtime.GOOS),
			ToolTip:        state.tip,
		}
		if runtime.GOOS == "linux" {
			options.Menu = linuxTrayMenu(func() { d.update(d.bringToFront) })
		}
		tray, err := mygo.NewTray(options)
		if err != nil {
			log.Printf("showing the tray icon: %v", err)
			d.trayFailed = true
			return
		}
		if runtime.GOOS != "linux" {
			tray.OnClick(onOpenMiniPlayer)
		}
		d.tray, d.trayIcon, d.trayShown = tray, name, state
		return
	}
	name := trayIconName(runtime.GOOS, mark)
	if name != d.trayIcon {
		icon, err := trayIcon(name)
		if err != nil {
			log.Printf("tray icon unavailable: %v", err)
		} else if err := d.tray.SetIcon(icon, trayIconTemplate(runtime.GOOS)); err != nil {
			log.Printf("updating the tray icon: %v", err)
		}
		d.trayIcon = name
	}
	if state == d.trayShown {
		return
	}
	d.trayShown = state
	d.tray.SetToolTip(state.tip)
}

func linuxTrayMenu(openMeiro func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Meiro", Click: func(*mygo.MenuItem, *mygo.Window) { openMeiro() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

func trayIcon(name string) ([]byte, error) {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, name))
}

func trayIconName(goos string, mark settings.TrayMark) string {
	if goos == "windows" {
		return "icon.png"
	}
	if trayMarkSupported(goos) && mark == settings.TrayMarkOMark {
		return "tray-o-mark.png"
	}
	return "tray-wordmark.png"
}

func trayMarkSupported(goos string) bool { return goos == "darwin" || goos == "linux" }

func trayIconTemplate(goos string) bool { return goos != "windows" }

func (d *desktop) closeToTray(setting func() *bool, answer func(toTray, remember bool) bool) {
	if d == nil {
		return
	}
	d.closeChoice, d.answerClose = setting, answer
	d.win.OnClose(func(e *mygo.CloseEvent) { d.onClose(e) })
	d.win.OnShow(d.restoreFullScreenOnShow)
	d.win.OnLeaveFullScreen(d.hideAfterLeavingFullScreen)
}

func (a *app) closeToBackgroundChoice() *bool {
	if a.settings.CloseToTray != nil {
		return a.settings.CloseToTray
	}
	if runtime.GOOS == "darwin" && a.notchEnabled() {
		keep := true
		return &keep
	}
	return nil
}

func (d *desktop) onClose(e *mygo.CloseEvent) {
	if d.quitting {
		return
	}
	var toTray, remember bool
	if choice := d.closeChoice(); choice != nil {
		toTray = *choice
	} else {
		message, keep := "Keep Meiro running in the tray, or quit it?", "Minimize to tray"
		if runtime.GOOS == "darwin" {
			message, keep = "Keep Meiro running in the background, or quit it? Reopen the window from the Dock, notch or menu bar.", "Keep running"
		}
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:             d.win,
			Title:              "Close Meiro?",
			Message:            message,
			Buttons:            []string{keep, "Close Meiro"},
			DestructiveButtons: []int{1},
			CancelButton:       0,
			CheckboxLabel:      "Remember my choice",
		})
		if err != nil {
			return
		}
		if res.Button < 0 {
			e.PreventDefault()
			return
		}
		toTray, remember = res.Button == 0, res.CheckboxChecked
	}
	if d.answerClose(toTray, remember) {
		e.PreventDefault()
		d.hideToTray()
	} else if runtime.GOOS == "darwin" {
		e.PreventDefault()
		mygo.App.Quit()
	}
}

func (d *desktop) hideToTray() {
	if runtime.GOOS == "darwin" && d.win.IsFullScreen() {
		d.restoreFullScreen = true
		d.leavingFullScreen++
		d.win.SetFullScreen(false)
		return
	}
	d.win.Hide()
}

func (d *desktop) hideAfterLeavingFullScreen() {
	if d.leavingFullScreen == 0 {
		return
	}
	d.leavingFullScreen--
	d.win.Hide()
}

func (d *desktop) restoreFullScreenOnShow() {
	if !d.restoreFullScreen {
		return
	}
	d.restoreFullScreen = false
	d.win.SetFullScreen(true)
}

func (d *desktop) close() {
	if d == nil {
		return
	}
	d.closeNotch()
	if d.notch.unwatch != nil {
		d.notch.unwatch()
		d.notch.unwatch = nil
	}
	d.keepAwake(false)
	mygo.GlobalShortcut.UnregisterAll()
	if d.notification != nil {
		d.notification.Close()
		d.notification = nil
	}
	if d.tray != nil {
		d.tray.Destroy()
		d.tray = nil
	}
}

const appScheme = "meiro"

const meiroTrayID = "meiro-tray"

func (a *app) setupDesktop() {
	if runtime.GOOS == "darwin" {
		mygo.ClearNotifications()
	}
	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		a.update(a.desktop.bringToFront)
	})
	mygo.App.OnOpenURL(func(raw string) {
		if target, ok := parseAppURL(raw); ok {
			a.update(func() { a.openDeepLink(target) })
		}
	})
	mygo.App.OnNotificationClick(func(string) {
		a.update(a.desktop.bringToFront)
	})
	mygo.Power.OnSuspend(func() { a.update(a.onSuspend) })
	mygo.Power.OnResume(func() { a.update(a.onResume) })
	mygo.App.OnActivate(func(visible bool) {
		if !visible || !a.desktop.win.IsVisible() {
			a.update(a.desktop.bringToFront)
		}
	})
	mygo.App.OnDidBecomeActive(func() {
		mygo.ClearNotifications()
		notification := a.desktop.notification
		a.update(func() {
			if a.desktop.notification == notification {
				a.desktop.notification = nil
			}
		})
	})
	a.desktop.closeToTray(a.closeToBackgroundChoice, a.answerClose)
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { a.desktop.quitting = true })
	a.setupNotch()
	a.syncDesktop()
	a.syncShortcut()
	a.desktop.setOpenAtLogin(a.settings.OpenAtLogin)
}

func (a *app) syncDesktop() {
	a.desktop.keepAwake(a.loading() || (a.player.Active() && !a.player.Paused() && !a.player.Ended()))
	a.desktop.setTray(a.trayEnabled(), a.settings.TrayMark, trayState{tip: a.trayTip()}, a.miniPlayer, a.setMiniShown)
	a.desktop.setTitle(a.windowTitle())
	a.syncNotch()
}

func (a *app) trayEnabled() bool {
	if runtime.GOOS == "darwin" {
		return !a.notchEnabled()
	}
	return a.settings.ShowTray
}

func (a *app) windowTitle() string {
	if a.current.VideoID == "" || !a.player.Playing() {
		return "Meiro"
	}
	artist := shownArtist(a.current)
	if artist == "" {
		return a.current.Title
	}
	return artist + " - " + a.current.Title
}

func (d *desktop) setTitle(title string) {
	if d == nil || title == d.titleShown {
		return
	}
	d.titleShown = title
	d.win.SetTitle(title)
}

func (a *app) syncShortcut() {
	if !a.desktop.setShortcut(a.settings.GlobalShortcut, func() { a.update(a.togglePlay) }) {
		a.settings.GlobalShortcut = false
		a.saveSettings()
	}
}

func (a *app) answerClose(toTray, remember bool) bool {
	if toTray {
		a.settings.ShowTray = runtime.GOOS != "darwin"
		a.syncDesktop()
		if runtime.GOOS != "darwin" && a.desktop != nil && a.desktop.trayFailed {
			return false
		}
	}
	if remember {
		on := toTray
		a.settings.CloseToTray = &on
	}
	if remember || toTray {
		a.saveSettings()
	}
	return toTray
}

func (a *app) trayTip() string {
	switch {
	case a.current.VideoID == "":
		return "Meiro"
	case a.current.Subtitle != "":
		return a.current.Title + " — " + a.current.Subtitle
	}
	return a.current.Title
}

type deepLinkAction uint8

const (
	linkPlay deepLinkAction = iota + 1
	linkSearch
)

type deepLink struct {
	action deepLinkAction
	value  string
}

func parseAppURL(raw string) (deepLink, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, appScheme) {
		return deepLink{}, false
	}
	switch strings.ToLower(u.Host) {
	case "play":
		if id := u.Query().Get("v"); id != "" {
			return deepLink{action: linkPlay, value: id}, true
		}
	case "search":
		if q := strings.TrimSpace(u.Query().Get("q")); q != "" {
			return deepLink{action: linkSearch, value: q}, true
		}
	}
	return deepLink{}, false
}

func (a *app) openDeepLink(link deepLink) {
	a.desktop.bringToFront()
	switch link.action {
	case linkPlay:
		a.playVideoID(link.value)
	case linkSearch:
		a.search.query = link.value
		a.searchField.Focus()
		a.router.Push(routeByID(routeSearch).path(""))
	}
}

func (a *app) onSuspend() {
	if a.player.Playing() {
		a.player.Pause()
		a.suspendedWhilePlaying = true
		a.syncSystemMedia()
	}
}

func (a *app) onResume() {
	if a.suspendedWhilePlaying {
		a.suspendedWhilePlaying = false
		a.player.Resume()
		a.syncSystemMedia()
	}
}

func (d *desktop) updates() bool { return d != nil && mygo.Updater.Enabled() }

func (d *desktop) window() *mygo.Window {
	if d == nil {
		return nil
	}
	return d.win
}
