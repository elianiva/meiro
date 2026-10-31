package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

// desktop is the app's window and what it holds of the system around it: the
// sleep assertion, the tray icon, notifications, the global shortcut and the
// login item.
//
// Every method does nothing on a nil desktop, and update runs its function at
// once. Tests run the app without one, since asking the system anything
// without an event loop would wait forever; the app's own code never needs
// to ask whether it has a desktop.
type desktop struct {
	win *mygo.Window
	// awake releases the sleep assertion while one is held.
	awake func()
	tray  *mygo.Tray
	// trayIcon is the resource name last applied to the tray.
	trayIcon string
	// mini is the tray's mini player while it is open.
	mini  mini
	notch notchWindow
	// notificationTray is the temporary full-colour icon used to show a
	// notification's cover when the user has hidden the persistent tray.
	notificationTray  *mygo.Tray
	notificationTimer *time.Timer
	notification      *mygo.Notification
	// notificationIcon is the native icon currently attached to a track
	// notification; zero on platforms without custom notification artwork.
	notificationIcon uintptr
	// trayShown is what the tray last showed, to skip identical updates.
	trayShown trayState
	// titleShown is the window title last set, to skip identical updates.
	titleShown string
	// trayFailed is set when the system refused the tray, so it is not
	// asked again until the user turns the tray off and on.
	trayFailed bool
	// closeChoice reads the CloseToTray setting; answerClose takes an
	// answer to the close dialog and reports whether the window can hide.
	closeChoice func() *bool
	answerClose func(toTray, remember bool) bool
	// quitting is set while App.Quit closes the windows, so OnClose hides
	// nothing and the quit goes through.
	quitting bool
	// restoreFullScreen is set when hiding the window had to leave full
	// screen first. macOS leaves a black Space behind a window hidden
	// while it is full screen, so the window leaves first and goes back
	// when it is shown again.
	restoreFullScreen bool
	// leavingFullScreen counts a hide that asked the window to leave full
	// screen and has not heard that it did. A leave the user started
	// themselves is not one of these, so it must not hide the window.
	leavingFullScreen int
}

// update runs fn on the main thread and draws a frame with what it changed.
// Without a desktop it runs fn at once.
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

// openPath opens a file or folder with the system's default application. It
// waits for the main thread, so it is called off it.
func (d *desktop) openPath(path string) error {
	if d == nil {
		return nil
	}
	return mygo.Shell.OpenPath(path)
}

// invalidate draws a frame with what background work produced.
func (d *desktop) invalidate() {
	if d != nil {
		d.win.Invalidate()
		d.invalidateMini()
		d.invalidateNotch()
	}
}

// invalidateMini draws the mini player, when it is open, with what the
// state now shows. It is safe from any goroutine.
func (d *desktop) invalidateMini() {
	if fl := d.mini.win.Load(); fl != nil {
		fl.Invalidate()
	}
}

// bringToFront shows the window when closing it hid it, restores it when it
// is minimized, and takes the focus, for a second launch, a deep link, a
// notification click or the mini/notch player's Open Meiro button.
func (d *desktop) bringToFront() {
	if d == nil {
		return
	}
	d.win.Show()
	d.win.Restore()
	d.win.Focus()
}

// keepAwake holds a sleep assertion while on is set, and releases it once it
// is not.
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

// notify announces a track with its cover when the platform supports it, and
// brings the window back when the user clicks the notification.
func (d *desktop) notify(item youtube.MusicItem, icon []byte) {
	if d == nil || runtime.GOOS == "darwin" || !mygo.NotificationsSupported() {
		return
	}
	if d.notification != nil {
		d.notification.Close()
		d.notification = nil
	}
	if err := prepareNotificationArtwork(d, icon); err != nil {
		log.Printf("preparing notification artwork: %v", err)
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "now-playing",
		Title: item.Title,
		Body:  item.Subtitle,
		Group: "now-playing",
	})
	n.OnClick(func() {
		d.update(func() {
			d.bringToFront()
			if d.notification == n {
				d.clearNotificationTray()
				d.notification = nil
				d.clearNotificationArtwork()
			}
		})
		n.Close()
	})
	d.notification = n
	if err := n.Show(); err != nil {
		log.Printf("showing track notification: %v", err)
		d.notification = nil
		d.clearNotificationTray()
		d.clearNotificationArtwork()
		return
	}
	if err := applyNotificationArtwork(d, item.Title, item.Subtitle, icon); err != nil {
		log.Printf("setting notification artwork: %v", err)
	}
}

// clearNotificationTray removes the temporary tray icon used only when a
// notification has a cover and the persistent tray is hidden.
func (d *desktop) clearNotificationTray() {
	if d.notificationTimer != nil {
		d.notificationTimer.Stop()
		d.notificationTimer = nil
	}
	if d.notificationTray != nil {
		d.notificationTray.Destroy()
		d.notificationTray = nil
	}
}

// clearNotificationArtwork releases the native icon attached to a track
// notification, when this platform creates one.
func (d *desktop) clearNotificationArtwork() {
	if d == nil || d.notificationIcon == 0 {
		return
	}
	destroyNotificationIcon(d.notificationIcon)
	d.notificationIcon = 0
}

// toggleShortcut is the global shortcut that plays or pauses from anywhere.
// CmdOrCtrl+Shift+Space avoids the media keys the system already owns and
// the plain shortcuts of other apps.
const toggleShortcut = "CmdOrCtrl+Shift+Space"

// setShortcut registers the global play/pause shortcut while on is set, and
// removes it otherwise. It reports false when the system refused it.
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

// setOpenAtLogin lines the login item up with on, asking the system only
// when the two disagree.
func (d *desktop) setOpenAtLogin(on bool) {
	if d == nil || mygo.App.OpenAtLogin() == on {
		return
	}
	if err := mygo.App.SetOpenAtLogin(on); err != nil {
		log.Printf("setting open at login: %v", err)
	}
}

// trayState is what the tray shows: the tooltip naming the track playing.
type trayState struct {
	tip string
}

// setTray shows the tray icon with state while on is set, and removes it
// otherwise. macOS and Windows open the mini player. Linux uses a
// minimal menu to open the main window or quit: AppIndicator does not report
// icon clicks or position, so MyGo centers tray flyouts in the primary
// display's work area instead of anchoring them to the icon. It changes the
// tooltip only when it changed: playback calls it four times a second.
func (d *desktop) setTray(on bool, mark settings.TrayMark, state trayState, mini func(*ui.Context), shown func(bool)) {
	if d == nil {
		return
	}
	if !on {
		if d.tray != nil {
			d.closeMini()
			d.tray.Destroy()
			d.clearNotificationArtwork()
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
		// Avoid repeating a failed resource update on every playback tick. A
		// different choice still gets a fresh attempt.
		d.trayIcon = name
	}
	if state == d.trayShown {
		return
	}
	d.trayShown = state
	d.tray.SetToolTip(state.tip)
}

// linuxTrayMenu offers only window restore and quit. Linux tray protocols do
// not deliver primary icon clicks or icon position; MyGo centers tray flyouts
// in the primary display's work area.
func linuxTrayMenu(openMeiro func()) *mygo.Menu {
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Meiro", Click: func(*mygo.MenuItem, *mygo.Window) { openMeiro() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

// trayIcon reads a tray image from the app's resources.
func trayIcon(name string) ([]byte, error) {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, name))
}

// trayIconName and trayIconTemplate select marks that stay legible at the
// platform's tray size. Windows keeps the coloured app icon as a safe fallback
// for track notifications.
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

// closeToTray wires what closing the window does to the CloseToTray setting:
// hide the window in the tray, quit, or ask once and keep the answer.
// setting reads the setting; answer takes an answer to the dialog, shows the
// tray for a yes, keeps the answer when asked to, and reports whether the
// window can hide. It runs once the window is there.
func (d *desktop) closeToTray(setting func() *bool, answer func(toTray, remember bool) bool) {
	if d == nil {
		return
	}
	d.closeChoice, d.answerClose = setting, answer
	d.win.OnClose(func(e *mygo.CloseEvent) { d.onClose(e) })
	d.win.OnShow(d.restoreFullScreenOnShow)
	// Registered with the close handler, not from it: the close dialog is
	// modal, so AppKit asks windowShouldClose from inside it, and a
	// listener added there races the list MyGo is calling.
	d.win.OnLeaveFullScreen(d.hideAfterLeavingFullScreen)
}

// closeToBackgroundChoice preserves an explicit preference. With no choice
// yet, enabling the player on a notched Mac also keeps its main window alive
// when closed, ready for Open Meiro. Other desktops still ask on first close.
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

// onClose decides between hiding the window and quitting.
// An unresolved choice asks; the dialog's checkbox keeps the answer, and
// dismissing the dialog keeps the window and asks again next time. It runs
// on the main thread.
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
			Parent:        d.win,
			Title:         "Close Meiro?",
			Message:       message,
			Buttons:       []string{keep, "Close Meiro"},
			CancelButton:  -1,
			CheckboxLabel: "Remember my choice",
		})
		if err != nil {
			return // the dialog failed; the close goes on
		}
		if res.Button < 0 {
			e.PreventDefault() // dismissed with Escape or the close button
			return
		}
		toTray, remember = res.Button == 0, res.CheckboxChecked
	}
	if d.answerClose(toTray, remember) {
		e.PreventDefault()
		d.hideToTray()
	} else if runtime.GOOS == "darwin" {
		// A remaining notch would keep the event loop alive after the main
		// window was destroyed. An explicit quit must close every window;
		// OnBeforeQuit lets the close handlers pass through on that second pass.
		e.PreventDefault()
		mygo.App.Quit()
	}
}

// hideToTray hides the window. On macOS a full-screen window leaves full
// screen first: orderOut on one that is still full screen keeps its Space
// and paints it black, which is what closing to the tray used to do. The
// leave is animated and finishes later, so the hide waits for it. The
// window goes back to full screen when it is shown again.
func (d *desktop) hideToTray() {
	if runtime.GOOS == "darwin" && d.win.IsFullScreen() {
		d.restoreFullScreen = true
		d.leavingFullScreen++
		d.win.SetFullScreen(false)
		return
	}
	d.win.Hide()
}

// hideAfterLeavingFullScreen hides the window once a hide to the tray has
// finished leaving full screen. A leave the user started is ignored. It
// runs on the main thread, from OnLeaveFullScreen.
func (d *desktop) hideAfterLeavingFullScreen() {
	if d.leavingFullScreen == 0 {
		return
	}
	d.leavingFullScreen--
	d.win.Hide()
}

// restoreFullScreenOnShow puts the window back in full screen when hiding
// it to the tray had to leave first. It runs on the main thread, from
// OnShow.
func (d *desktop) restoreFullScreenOnShow() {
	if !d.restoreFullScreen {
		return
	}
	d.restoreFullScreen = false
	d.win.SetFullScreen(true)
}

// close releases what the desktop holds, for shutdown.
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
	d.clearNotificationTray()
	d.clearNotificationArtwork()
}

// appScheme is the deep link scheme Meiro answers to, as
// meiro://play?v=VIDEO_ID and meiro://search?q=QUERY. It is registered with
// the system by the installers mygo build makes, and at run time for
// portable builds.
const appScheme = "meiro"

const meiroTrayID = "meiro-tray"

// setupDesktop wires the app to the rest of the system: the second-instance
// handoff, deep links, notification clicks, power changes, the tray, the
// global shortcut and the login item. It runs once the window is there.
func (a *app) setupDesktop() {
	if runtime.GOOS == "darwin" {
		// Clear track banners delivered by an earlier build, including a launch
		// that does not activate the main window.
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
		// A visible notch panel must not prevent a Dock click from reopening
		// the hidden main window.
		if !visible || !a.desktop.win.IsVisible() {
			a.update(a.desktop.bringToFront)
		}
	})
	mygo.App.OnDidBecomeActive(func() {
		mygo.ClearNotifications()
		notification := a.desktop.notification
		a.update(func() {
			if a.desktop.notification == notification {
				a.desktop.clearNotificationTray()
				a.desktop.notification = nil
				a.desktop.clearNotificationArtwork()
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

// syncDesktop lines the sleep assertion and the tray up with playback. Every
// path that changes playback reaches it through syncSystemMedia.
func (a *app) syncDesktop() {
	a.desktop.keepAwake(a.loading() || (a.player.Active() && !a.player.Paused() && !a.player.Ended()))
	a.desktop.setTray(a.trayEnabled(), a.settings.TrayMark, trayState{tip: a.trayTip()}, a.miniPlayer, a.setMiniShown)
	a.desktop.setTitle(a.windowTitle())
	a.syncNotch()
}

// trayEnabled gives macOS one playback surface: the notch when supported and
// chosen, otherwise the menu bar. Other platforms keep their tray preference.
func (a *app) trayEnabled() bool {
	if runtime.GOOS == "darwin" {
		return !a.notchEnabled()
	}
	return a.settings.ShowTray
}

// windowTitle names the track while it plays, as "Artist - Title" the way
// the taskbar and the window switcher show it, and the app otherwise.
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

// setTitle shows title on the window, skipping a title it already shows.
func (d *desktop) setTitle(title string) {
	if d == nil || title == d.titleShown {
		return
	}
	d.titleShown = title
	d.win.SetTitle(title)
}

// syncShortcut registers the global shortcut as the settings say, and turns
// the setting off when the system refuses it.
func (a *app) syncShortcut() {
	if !a.desktop.setShortcut(a.settings.GlobalShortcut, func() { a.update(a.togglePlay) }) {
		a.settings.GlobalShortcut = false
		a.saveSettings()
	}
}

// answerClose takes the user's answer to the close dialog, and reports
// whether the window can hide. macOS reopens from the Dock; other platforms
// need a tray, and cannot hide when the system refuses it. With remember it
// keeps the answer as the CloseToTray setting.
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

// trayTip names the track playing, or the app when nothing plays.
func (a *app) trayTip() string {
	switch {
	case a.current.VideoID == "":
		return "Meiro"
	case a.current.Subtitle != "":
		return a.current.Title + " — " + a.current.Subtitle
	}
	return a.current.Title
}

// deepLinkAction is what a deep link asks for.
type deepLinkAction uint8

const (
	linkPlay deepLinkAction = iota + 1
	linkSearch
)

// deepLink is a meiro:// URL the app knows how to open.
type deepLink struct {
	action deepLinkAction
	value  string
}

// parseAppURL reads a meiro://play?v=VIDEO_ID or meiro://search?q=QUERY
// link, and says no to anything else.
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

// openDeepLink carries out a deep link: it starts the video, or shows the
// search page with the query ready to run.
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

// onSuspend pauses playback as the machine sleeps; onResume plays on if the
// sleep was what paused it.
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

// updates reports whether this copy updates itself: a signed release, run
// as an app. The updater plugin only runs with the app, so tests and
// development builds say the copy updates another way.
func (d *desktop) updates() bool { return d != nil && mygo.Updater.Enabled() }

// window is the app's window, to parent the system's dialogs; nil without a
// desktop.
func (d *desktop) window() *mygo.Window {
	if d == nil {
		return nil
	}
	return d.win
}
