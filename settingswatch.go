package main

import (
	"bytes"
	"errors"
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
)

// settingsDebounce gives one save's burst of file events time to settle
// before the file is read.
const settingsDebounce = 100 * time.Millisecond

// settingsCheckEvery is the fallback interval when the OS watcher cannot
// start, so a settings file that changes is still noticed.
const settingsCheckEvery = time.Second

// watchSettings applies changes another program makes to settings.json while
// the app runs. The OS reports changes to the file's directory, so an idle
// file costs nothing; if the watcher cannot start, the app checks once a
// second instead. The app writes the same file; its own write parses to the
// settings it already holds, so it changes nothing.
func (a *app) watchSettings() {
	if a.saver.Path == "" {
		return
	}
	a.settingsSeen, _ = os.ReadFile(a.saver.Path)
	a.colorsSeen, _ = os.ReadFile(a.colorsPath)
	path := a.saver.Path
	check := func() {
		a.checkSettings()
		a.checkColors()
	}
	a.run(func() {
		watcher, err := newSettingsWatcher(filepath.Dir(path), filepath.Base(path), settingsDebounce, check, filepath.Base(a.colorsPath))
		if err == nil {
			// Catch a change between the first read and the watch starting.
			check()
			err = watcher.run(a.stop)
		}
		if err != nil {
			log.Printf("watching settings: %v; checking once a second instead", err)
			a.every(settingsCheckEvery, check)
		}
	})
}

// settingsWatcher watches the directory settings.json and colors.json are in.
// A tool that writes a temporary file and renames it over settings.json breaks
// a watch on the file itself; watching the directory sees the new name. It
// also sees colors.json go, which gives the theme back to the seed.
type settingsWatcher struct {
	events   *fsnotify.Watcher
	names    []string
	debounce time.Duration
	changed  func()
}

// newSettingsWatcher starts watching dir for changes to name and to the files
// named in more.
func newSettingsWatcher(dir, name string, debounce time.Duration, changed func(), more ...string) (*settingsWatcher, error) {
	events, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := events.Add(dir); err != nil {
		_ = events.Close()
		return nil, err
	}
	return &settingsWatcher{events: events, names: append([]string{name}, more...), debounce: debounce, changed: changed}, nil
}

// run waits for the OS to report a change to the settings file, debounces the
// burst of events one save makes, and calls changed. It returns when stop
// closes, or when the watcher stops unexpectedly.
func (s *settingsWatcher) run(stop <-chan struct{}) error {
	defer s.events.Close()
	var wait <-chan time.Time
	for {
		select {
		case <-stop:
			return nil
		case event, ok := <-s.events.Events:
			if !ok {
				return errors.New("the file watcher stopped")
			}
			if !slices.Contains(s.names, filepath.Base(event.Name)) {
				continue
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
				continue
			}
			wait = time.After(s.debounce)
		case <-wait:
			wait = nil
			s.changed()
		case err, ok := <-s.events.Errors:
			if !ok {
				return errors.New("the file watcher stopped")
			}
			log.Printf("watching settings: %v", err)
		}
	}
}

// checkSettings reads settings.json and hands a change to the main thread. It
// runs off the main thread.
func (a *app) checkSettings() {
	if a.saver.Pending() {
		return // the app's own write is on its way; it is the newer one
	}
	data, err := os.ReadFile(a.saver.Path)
	if err != nil || bytes.Equal(data, a.settingsSeen) {
		return
	}
	a.settingsSeen = data
	a.update(func() { a.reloadSettings(data) })
}

// readColors reads colors.json, or returns nil when there is none or it is
// not a colours file. It runs off the main thread, or before any exists.
func (a *app) readColors() *m3.Colors {
	data, err := os.ReadFile(a.colorsPath)
	if err != nil {
		return nil
	}
	colors, err := settings.ParseColors(data)
	if err != nil {
		log.Printf("reading %s: %v", a.colorsPath, err)
		return nil
	}
	return colors
}

// checkColors reads colors.json and hands a change to the main thread. A file
// that is half written or wrong keeps the colours the app has; a file that is
// gone gives the theme back to the seed. It runs off the main thread.
func (a *app) checkColors() {
	data, err := os.ReadFile(a.colorsPath)
	if errors.Is(err, fs.ErrNotExist) {
		data = nil
	} else if err != nil {
		return // unreadable for now; the next change reads it again
	}
	if bytes.Equal(data, a.colorsSeen) {
		return
	}
	var colors *m3.Colors
	if data != nil {
		if colors, err = settings.ParseColors(data); err != nil {
			return // still being written, or wrong; the next change reads it again
		}
	}
	a.colorsSeen = data
	a.update(func() {
		a.colors = colors
		a.refresh()
	})
}

// reloadSettings applies settings another program wrote. It runs on the main
// thread.
func (a *app) reloadSettings(data []byte) {
	if a.saver.Pending() {
		return
	}
	next, err := settings.Parse(data)
	if err != nil {
		return // a file still being written; the next check reads it whole
	}
	// The account and Last.fm sessions live outside the file, and the audio
	// cache is opened at startup. Keep the connections and folder the app
	// has rather than making settings.json claim others.
	next.Channel = a.settings.Channel
	next.SignInSource = a.settings.SignInSource
	next.LastFMUser = a.settings.LastFMUser
	next.LastFMPaused = a.settings.LastFMPaused
	next.CacheDirectory = a.settings.CacheDirectory
	if reflect.DeepEqual(next, a.settings) {
		return
	}
	previous := a.settings
	a.settings = next
	a.applyReloadedSettings(previous)
	a.refresh()
}

// applyReloadedSettings lines the running app up with settings that changed on
// disk. The theme, zoom, rail and the switches read while drawing need no
// work: the next frame reads them from a.settings.
func (a *app) applyReloadedSettings(previous settings.Settings) {
	s := &a.settings
	if s.Volume != previous.Volume || s.LogVolume != previous.LogVolume {
		a.volume = s.Volume
		if a.volume > 0 {
			a.muted = a.volume
		}
		a.player.SetVolume(playbackVolume(a.volume, a.loudness, a.loudnessOK && s.NormalizeLoudness, s.LogVolume))
	}
	if s.NormalizeLoudness != previous.NormalizeLoudness {
		a.applyLoudness()
		if s.NormalizeLoudness && a.current.VideoID != "" {
			a.fetchLoudness(a.current, &a.loudnessLoad)
		}
	}
	if s.EQOn != previous.EQOn || !slices.Equal(s.EQBands, previous.EQBands) {
		a.loadEQ()
	}
	if s.Crossfade != previous.Crossfade || s.CrossfadeSeconds != previous.CrossfadeSeconds {
		a.applyCrossfade()
	}
	if (s.PodcastSpeed != previous.PodcastSpeed || s.MusicSpeed != previous.MusicSpeed) && a.current.VideoID != "" {
		a.applySpeed()
	}
	if !maps.Equal(s.Shortcuts, previous.Shortcuts) {
		a.keyboard.keys = nil
		a.keyboard.problem = ""
	}
	if s.ShowTray != previous.ShowTray || s.TrayMark != previous.TrayMark ||
		s.DiscordPresence != previous.DiscordPresence ||
		s.Volume != previous.Volume || s.NormalizeLoudness != previous.NormalizeLoudness ||
		s.LogVolume != previous.LogVolume {
		a.syncSystemMedia()
	}
	if s.GlobalShortcut != previous.GlobalShortcut {
		a.syncShortcut()
	}
	if s.OpenAtLogin != previous.OpenAtLogin {
		a.desktop.setOpenAtLogin(s.OpenAtLogin)
	}
	if s.CacheSongs != previous.CacheSongs {
		a.cacheSongsText = strconv.Itoa(s.CacheSongs)
		if cache := a.currentAudioCache(); cache != nil {
			a.run(func() { cache.SetLimit(s.CacheSongs) })
		}
	}
}
