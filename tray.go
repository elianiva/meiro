package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
)

// toggleShortcut is the global shortcut that plays or pauses from anywhere.
// CmdOrCtrl+Shift+Space avoids the media keys the system already owns and
// the plain shortcuts of other apps.
const toggleShortcut = "CmdOrCtrl+Shift+Space"

// syncTray shows the menu-bar icon, or removes it when the user turned it
// off. It runs once the app is ready; without a window it only drops the
// reference tests may hold.
func (a *app) syncTray() {
	if a.win == nil {
		return
	}
	if !a.settings.ShowTray {
		if a.tray != nil {
			a.tray.Destroy()
			a.tray = nil
		}
		return
	}
	if a.tray != nil {
		a.trayShown = trayShown{}
		a.syncTrayMedia()
		return
	}
	icon, err := trayIcon()
	if err != nil {
		log.Printf("tray icon unavailable: %v", err)
	}
	tray, err := mygo.NewTray(mygo.TrayOptions{
		Icon:           icon,
		IconIsTemplate: true,
		ToolTip:        a.trayTip(),
		Menu:           a.trayMenu(),
	})
	if err != nil {
		log.Printf("showing the tray icon: %v", err)
		return
	}
	tray.OnClick(func() {
		a.update(a.bringToFront)
	})
	a.tray = tray
	a.trayShown = trayShown{}
	a.syncTrayMedia()
}

// trayIcon reads the app icon for the menu bar, scaled by the system.
func trayIcon() ([]byte, error) {
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(filepath.Join(dir, "icon.png"))
}

// trayTip names the track playing, or the app when nothing plays.
func (a *app) trayTip() string {
	if a.current.VideoID != "" {
		if a.current.Subtitle != "" {
			return a.current.Title + " — " + a.current.Subtitle
		}
		return a.current.Title
	}
	return "Meiro"
}

// trayMenu is the menu of the tray icon: transport, the window, and quit.
func (a *app) trayMenu() *mygo.Menu {
	playing := a.player.Playing()
	playLabel := "Play"
	if playing || a.resolving {
		playLabel = "Pause"
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: a.trayTip(), Disabled: true},
		mygo.Separator(),
		{Label: playLabel, Click: func(*mygo.MenuItem, *mygo.Window) {
			a.update(a.togglePlay)
		}},
		{Label: "Next track", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.update(a.advance)
		}},
		mygo.Separator(),
		{Label: "Show Meiro", Click: func(*mygo.MenuItem, *mygo.Window) {
			a.update(a.bringToFront)
		}},
		{Label: "Quit", Click: func(*mygo.MenuItem, *mygo.Window) {
			mygo.App.Quit()
		}},
	})
}

// syncTrayMedia refreshes the tray's tip and menu after a track change.
func (a *app) syncTrayMedia() {
	if a.tray == nil {
		return
	}
	// The poll calls this four times a second while a track plays, and
	// replacing the native menu is not free: only what the tray shows decides.
	shown := trayShown{tip: a.trayTip(), pause: a.player.Playing() || a.resolving}
	if shown == a.trayShown {
		return
	}
	a.trayShown = shown
	a.tray.SetToolTip(shown.tip)
	a.tray.SetMenu(a.trayMenu())
}

// trayShown is what the tray last showed: the tip, and whether the menu
// offered Pause rather than Play.
type trayShown struct {
	tip   string
	pause bool
}

// syncShortcut registers the global play/pause shortcut while the user
// keeps it on, and removes it when they turn it off or it is taken.
// Without a window there is no system to register with.
func (a *app) syncShortcut() {
	if a.win == nil {
		return
	}
	if !a.settings.GlobalShortcut {
		mygo.GlobalShortcut.Unregister(toggleShortcut)
		return
	}
	if mygo.GlobalShortcut.IsRegistered(toggleShortcut) {
		return
	}
	if err := mygo.GlobalShortcut.Register(toggleShortcut, func() {
		a.update(a.togglePlay)
	}); err != nil {
		log.Printf("registering the global shortcut: %v", err)
		a.settings.GlobalShortcut = false
		a.saveSettings()
	}
}

// syncOpenAtLogin lines the login item up with the settings: it asks the
// system only when the two disagree, and never without a window.
func (a *app) syncOpenAtLogin() {
	if a.win == nil {
		return
	}
	want := a.settings.OpenAtLogin
	if mygo.App.OpenAtLogin() == want {
		return
	}
	if err := mygo.App.SetOpenAtLogin(want); err != nil {
		log.Printf("setting open at login: %v", err)
	}
}
