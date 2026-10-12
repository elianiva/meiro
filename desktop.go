package main

import (
	"log"
	"net/url"
	"strings"

	"github.com/egoist/mygo"

	"github.com/elianiva/meiro/youtube"
)

// appScheme is the deep link scheme Meiro answers to, as
// meiro://play?v=VIDEO_ID and meiro://search?q=QUERY. It is registered with
// the system by the installers mygo build makes, and at run time for
// portable builds.
const appScheme = "meiro"

// setupDesktop wires the app to the rest of the system: a single instance,
// deep links, sleep handling, notifications, the tray and the global
// shortcut. It runs once the app is ready; tests do not call it.
func (a *app) setupDesktop() {
	a.registerDesktopHandlers()
	a.syncAwake()
	a.syncTray()
	a.syncShortcut()
	a.syncOpenAtLogin()
}

// registerDesktopHandlers takes the events that must be registered before or
// when the app runs: the second-instance handoff, deep links, notification
// clicks and power changes.
func (a *app) registerDesktopHandlers() {
	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		a.update(a.bringToFront)
	})
	mygo.App.OnOpenURL(func(raw string) {
		if target, ok := parseAppURL(raw); ok {
			a.update(func() { a.openDeepLink(target) })
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		a.update(a.bringToFront)
	})
	mygo.Power.OnSuspend(func() {
		a.update(a.onSuspend)
	})
	mygo.Power.OnResume(func() {
		a.update(a.onResume)
	})
	mygo.App.OnDidBecomeActive(func() {
		mygo.ClearNotifications()
	})
}

// bringToFront restores the window and takes the focus, for a second launch,
// a deep link or a notification click.
func (a *app) bringToFront() {
	if a.win == nil {
		return
	}
	a.win.Restore()
	a.win.Focus()
}

// deepLink is a meiro:// URL the app knows how to open.
type deepLink struct {
	action string // "play" or "search"
	value  string
}

// parseAppURL reads a meiro://play?v=VIDEO_ID or meiro://search?q=QUERY
// link, and says no to anything else.
func parseAppURL(raw string) (deepLink, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, appScheme) {
		return deepLink{}, false
	}
	switch action := strings.ToLower(u.Host); action {
	case "play":
		if id := u.Query().Get("v"); id != "" {
			return deepLink{action: "play", value: id}, true
		}
	case "search":
		if q := strings.TrimSpace(u.Query().Get("q")); q != "" {
			return deepLink{action: "search", value: q}, true
		}
	}
	return deepLink{}, false
}

// openDeepLink carries out a deep link: it starts the video, or shows the
// search page with the query ready to run.
func (a *app) openDeepLink(link deepLink) {
	a.bringToFront()
	switch link.action {
	case "play":
		a.playVideoID(link.value)
	case "search":
		a.search.query = link.value
		a.focusSearch = true
		a.router.Push("/search")
	}
}

// playVideoID starts the video, wherever it comes from: a link, a history
// entry or the queue. Tracks the player already holds play at once, without
// another lookup.
func (a *app) playVideoID(id string) {
	if id == "" {
		return
	}
	for i, item := range a.queue {
		if item.VideoID == id {
			a.index = i
			a.start(a.queue[a.index])
			return
		}
	}
	for i, item := range a.playable {
		if item.VideoID == id {
			a.playWithOptions(item, a.playable, i, youtube.UpNextOptions{}, a.queueSource)
			return
		}
	}
	a.playWithOptions(youtube.MusicItem{VideoID: id}, nil, 0, youtube.UpNextOptions{}, "")
}

// onSuspend pauses playback as the machine sleeps, so it does not resume
// mid-track confusion on wake; onResume refreshes the session state.
func (a *app) onSuspend() {
	if a.player.Playing() {
		a.player.Pause()
		a.suspendedWhilePlaying = true
		a.syncSystemMedia()
		a.syncAwake()
	}
}

func (a *app) onResume() {
	if a.suspendedWhilePlaying {
		a.suspendedWhilePlaying = false
		a.player.Resume()
		a.syncSystemMedia()
		a.syncAwake()
	}
}

// updateAwake holds a sleep assertion while sound is on its way or playing,
// and releases it when the queue stops or pauses. It mirrors what the
// player reports, so every path that changes playback keeps it right by
// calling syncSystemMedia.
func (a *app) updateAwake() {
	awake := a.resolving || (a.player.Active() && !a.player.Paused() && !a.player.Ended())
	if awake == (a.awakeRelease != nil) {
		return
	}
	if a.awakeRelease != nil {
		a.awakeRelease()
		a.awakeRelease = nil
		return
	}
	a.awakeRelease = mygo.Power.KeepAwake("Playing music", false)
}

// syncAwake refreshes the sleep assertion from the player's state. It does
// nothing without a window: tests never start the app, where asking the
// system anything would wait for an event loop that does not run.
func (a *app) syncAwake() {
	if a.win == nil {
		return
	}
	a.updateAwake()
}

// releaseAwake drops the sleep assertion, for shutdown.
func (a *app) releaseAwake() {
	if a.awakeRelease != nil {
		a.awakeRelease()
		a.awakeRelease = nil
	}
}

// trackNotification announces the track playing, when the user asked for it.
// Without a window there is no app to ask, so tests skip it.
func (a *app) trackNotification(item youtube.MusicItem) {
	if a.win == nil || !a.settings.NotifyTrack {
		return
	}
	if !mygo.NotificationsSupported() {
		return
	}
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:    "now-playing",
		Title: item.Title,
		Body:  item.Subtitle,
		Group: "now-playing",
	})
	n.OnClick(func() {
		a.update(a.bringToFront)
		n.Close()
	})
	if err := n.Show(); err != nil {
		log.Printf("showing track notification: %v", err)
	}
}
