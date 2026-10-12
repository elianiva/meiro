package main

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

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
	// trayShown is what the tray last showed, to skip identical updates.
	trayShown trayState
	// trayFailed is set when the system refused the tray, so it is not
	// asked again until the user turns the tray off and on.
	trayFailed bool
}

// update runs fn on the main thread and draws a frame with what it changed.
// Without a desktop it runs fn at once.
func (d *desktop) update(fn func()) {
	if d == nil {
		fn()
		return
	}
	d.win.Update(fn)
}

// invalidate draws a frame with what background work produced.
func (d *desktop) invalidate() {
	if d != nil {
		d.win.Invalidate()
	}
}

// bringToFront restores the window and takes the focus, for a second launch,
// a deep link or a notification click.
func (d *desktop) bringToFront() {
	if d == nil {
		return
	}
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

// notify announces a track, and brings the window back when it is clicked.
func (d *desktop) notify(item youtube.MusicItem) {
	if d == nil || !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "now-playing",
		Title: item.Title,
		Body:  item.Subtitle,
		Group: "now-playing",
	})
	n.OnClick(func() {
		d.update(d.bringToFront)
		n.Close()
	})
	if err := n.Show(); err != nil {
		log.Printf("showing track notification: %v", err)
	}
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

// trayState is what the tray shows: the track playing, and whether its menu
// offers Pause rather than Play.
type trayState struct {
	tip   string
	pause bool
}

// trayActions are what the tray's menu does.
type trayActions struct {
	toggle, next, show func()
}

// setTray shows the tray icon with state while on is set, and removes it
// otherwise. It replaces the native menu only when what it shows changed:
// playback calls it four times a second.
func (d *desktop) setTray(on bool, state trayState, actions trayActions) {
	if d == nil {
		return
	}
	if !on {
		if d.tray != nil {
			d.tray.Destroy()
			d.tray, d.trayShown = nil, trayState{}
		}
		d.trayFailed = false
		return
	}
	if d.trayFailed {
		return
	}
	if d.tray == nil {
		icon, err := trayIcon()
		if err != nil {
			log.Printf("tray icon unavailable: %v", err)
		}
		tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:           icon,
			IconIsTemplate: true,
			ToolTip:        state.tip,
			Menu:           d.trayMenu(state, actions),
		})
		if err != nil {
			log.Printf("showing the tray icon: %v", err)
			d.trayFailed = true
			return
		}
		tray.OnClick(func() { d.update(actions.show) })
		d.tray, d.trayShown = tray, state
		return
	}
	if state == d.trayShown {
		return
	}
	d.trayShown = state
	d.tray.SetToolTip(state.tip)
	d.tray.SetMenu(d.trayMenu(state, actions))
}

// trayIcon reads the app icon for the menu bar, scaled by the system.
func trayIcon() ([]byte, error) {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, "icon.png"))
}

// trayMenu is the menu of the tray icon: transport, the window, and quit.
// Its items are clicked on the system's thread; actions run on the main
// thread.
func (d *desktop) trayMenu(state trayState, actions trayActions) *mygo.Menu {
	playLabel := "Play"
	if state.pause {
		playLabel = "Pause"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: state.tip, Disabled: true},
		mygo.Separator(),
		{Label: playLabel, Click: func(*mygo.MenuItem, *mygo.Window) { d.update(actions.toggle) }},
		{Label: "Next track", Click: func(*mygo.MenuItem, *mygo.Window) { d.update(actions.next) }},
		mygo.Separator(),
		{Label: "Show Meiro", Click: func(*mygo.MenuItem, *mygo.Window) { d.update(actions.show) }},
		{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
}

// close releases what the desktop holds, for shutdown.
func (d *desktop) close() {
	if d == nil {
		return
	}
	d.keepAwake(false)
	mygo.GlobalShortcut.UnregisterAll()
	if d.tray != nil {
		d.tray.Destroy()
		d.tray = nil
	}
}

// appScheme is the deep link scheme Meiro answers to, as
// meiro://play?v=VIDEO_ID and meiro://search?q=QUERY. It is registered with
// the system by the installers mygo build makes, and at run time for
// portable builds.
const appScheme = "meiro"

// setupDesktop wires the app to the rest of the system: the second-instance
// handoff, deep links, notification clicks, power changes, the tray, the
// global shortcut and the login item. It runs once the window is there.
func (a *app) setupDesktop() {
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
	mygo.App.OnDidBecomeActive(func() { mygo.ClearNotifications() })
	a.syncDesktop()
	a.syncShortcut()
	a.desktop.setOpenAtLogin(a.settings.OpenAtLogin)
}

// syncDesktop lines the sleep assertion and the tray up with playback. Every
// path that changes playback reaches it through syncSystemMedia.
func (a *app) syncDesktop() {
	a.desktop.keepAwake(a.loading() || (a.player.Active() && !a.player.Paused() && !a.player.Ended()))
	a.desktop.setTray(a.settings.ShowTray, trayState{tip: a.trayTip(), pause: a.player.Playing() || a.loading()}, trayActions{
		toggle: a.togglePlay,
		next:   a.advance,
		show:   a.desktop.bringToFront,
	})
}

// syncShortcut registers the global shortcut as the settings say, and turns
// the setting off when the system refuses it.
func (a *app) syncShortcut() {
	if !a.desktop.setShortcut(a.settings.GlobalShortcut, func() { a.update(a.togglePlay) }) {
		a.settings.GlobalShortcut = false
		a.saveSettings()
	}
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
		a.focusSearch = true
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
