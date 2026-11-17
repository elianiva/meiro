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

const settingsDebounce = 100 * time.Millisecond

const settingsCheckEvery = time.Second

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
			check()
			err = watcher.run(a.stop)
		}
		if err != nil {
			log.Printf("watching settings: %v; checking once a second instead", err)
			a.every(settingsCheckEvery, check)
		}
	})
}

type settingsWatcher struct {
	events   *fsnotify.Watcher
	names    []string
	debounce time.Duration
	changed  func()
}

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

func (a *app) checkSettings() {
	if a.saver.Pending() {
		return
	}
	data, err := os.ReadFile(a.saver.Path)
	if err != nil || bytes.Equal(data, a.settingsSeen) {
		return
	}
	a.settingsSeen = data
	a.update(func() { a.reloadSettings(data) })
}

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

func (a *app) checkColors() {
	data, err := os.ReadFile(a.colorsPath)
	if errors.Is(err, fs.ErrNotExist) {
		data = nil
	} else if err != nil {
		return
	}
	if bytes.Equal(data, a.colorsSeen) {
		return
	}
	var colors *m3.Colors
	if data != nil {
		if colors, err = settings.ParseColors(data); err != nil {
			return
		}
	}
	a.colorsSeen = data
	a.update(func() {
		a.colors = colors
		a.refresh()
	})
}

func (a *app) reloadSettings(data []byte) {
	if a.saver.Pending() {
		return
	}
	next, err := settings.Parse(data)
	if err != nil {
		return
	}
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
	if s.ShowTray != previous.ShowTray || s.TrayMark != previous.TrayMark || s.ShowNotch != previous.ShowNotch ||
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
	if s.ExcludeAIGenerated != previous.ExcludeAIGenerated {
		a.applyAIFilterChange()
	}
}
