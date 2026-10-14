// Meiro is a small desktop client for YouTube Music, drawn by MyGo itself in
// Material 3 Expressive.
//
// It browses the public catalogue with the youtube package, and the
// account's library once the user signs in with their YouTube Music session.
// Playback resolves a stream URL and decodes it in a child ffmpeg; see
// package player. The interface is built from the components of package m3,
// which take their colours, shapes and motion from one theme.
//
// The main package is the app itself: its state, the pages and the views
// that draw them. Everything that can stand on its own lives under internal.
// See AGENTS.md for the conventions the package keeps.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/updater/native"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/applog"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/diskcache"
	"github.com/elianiva/meiro/internal/memprof"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/systemmedia"
)

func main() {
	// The heap is mostly artwork, which lives as long as the user browses and
	// then goes at once. The default lets the heap grow to twice what is live
	// before a collection; half as much costs a little time and saves tens of
	// megabytes.
	debug.SetGCPercent(50)
	stopMemprof := memprof.Start(15 * time.Second)
	proc.SweepTemp()
	// A second launch hands its work to the running app instead of starting
	// another player with its own audio.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	logsDir, closeLog := openLog()
	// Signed auto-updates with the updater's native window: in builds made
	// without update signing it stays idle and says so when asked.
	mygo.Use(native.Plugin)
	a := newApp()
	a.logsDir = logsDir
	mygo.App.WhenReady(func() {
		a.setup()
		a.desktop = &desktop{win: mygo.NewWindow(mygo.WindowOptions{
			Title:         "Meiro",
			Width:         1180,
			Height:        760,
			MinWidth:      860,
			MinHeight:     560,
			StateKey:      "meiro",
			TitleBarStyle: mygo.TitleBarHidden,
			Content:       ui.View(a.frame),
		})}
		session, err := systemmedia.New(a.systemMediaControls())
		if err != nil {
			log.Printf("system media controls unavailable: %v", err)
		} else {
			a.media = session
		}
		a.setupDesktop()
		a.syncSystemMedia()
		// Background work reaches the window through the desktop, so it
		// starts only once the window is there.
		a.restoreAccount()
		a.loadHistory()
		a.startTicker()
		a.every(persistInterval, func() { a.update(a.persistPlayback) })
		// Browsing decodes and drops pictures in bursts, and the runtime
		// keeps the freed memory for a while before it gives it back.
		a.every(30*time.Second, debug.FreeOSMemory)
		a.watchSession()
		a.desktop.win.OnClosed(a.stopTicking)
	})
	err := mygo.App.Run()
	a.stopTicking()
	a.shutdown()
	stopMemprof()
	if err != nil {
		log.Print(err)
		closeLog()
		os.Exit(1)
	}
	closeLog()
}

// openLog sends the standard logger, and slog, to the log file as well as to
// standard error, and returns the folder the log is in and what closes the
// file. A log that cannot be
// opened is not worth stopping for: the app runs without one.
func openLog() (dir string, closeLog func()) {
	dir, err := mygo.App.Path(mygo.PathLogs)
	if err != nil {
		log.Printf("finding the logs folder: %v", err)
		return "", func() {}
	}
	tools := applog.NewLazy("ffmpeg, yt-dlp: looking up")
	file, err := applog.Open(dir, logHeader(tools))
	if err != nil {
		log.Printf("opening the log file: %v", err)
		return "", func() {}
	}
	applog.Install(file)
	// The tools are asked after the log is open, so launch does not wait on
	// them; the answer goes into the log, and into the header of the next file.
	tools.Start(toolVersions, func(v string) { log.Print("tools: ", v) })
	return dir, func() { _ = file.Close() }
}

// toolVersions asks the tools the app ships for their versions.
func toolVersions() string {
	info := report.Collect(context.Background(), mygo.App.Version())
	return fmt.Sprintf("ffmpeg %s, yt-dlp %s", info.FFmpeg, info.YtDlp)
}

// logHeader names the versions in use, for the top of each log file.
func logHeader(tools *applog.Lazy) func() string {
	return func() string {
		version := mygo.App.Version()
		if version == "" {
			version = "development build"
		}
		return fmt.Sprintf("Meiro %s on %s/%s, %s\n%s",
			version, runtime.GOOS, runtime.GOARCH, runtime.Version(), tools.Get())
	}
}

// playerOptions decode with the ffmpeg the app ships, when there is one.
func playerOptions() []player.Option {
	if ffmpeg, err := proc.Tool("ffmpeg"); err == nil {
		return []player.Option{player.WithFFmpeg(ffmpeg)}
	}
	return nil
}

// setup reads the settings, opens the audio cache and the credential store,
// and makes the public client.
func (a *app) setup() {
	if directory, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.saver.Path = filepath.Join(directory, "settings.json")
		a.settings = settings.Load(a.saver.Path)
		a.account.creds.store = &cookies.FileStore{Path: filepath.Join(directory, "cookie.txt")}
		a.historyPath = filepath.Join(directory, "history.json")
		a.restorePlayback(filepath.Join(directory, "playback.json"))
	} else {
		a.account.signIn.err = err.Error()
	}
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	a.volume = a.settings.Volume
	a.player.SetVolume(a.volume / 100)
	a.openAudioCache()
	a.openArtworkCache()
}

// artworkCacheLimit is how much downloaded artwork is kept on disk.
const artworkCacheLimit = 256 << 20

// openArtworkCache keeps downloaded artwork in the cache folder, so covers
// that leave memory are read back instead of downloaded again. Opening reads
// the details of every file in the folder, thousands of them once it is full,
// so it goes off the main thread: covers asked for before it is done are
// downloaded, as they are without a cache.
func (a *app) openArtworkCache() {
	root, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		return
	}
	dir := filepath.Join(root, "Meiro Artwork Cache")
	a.run(func() {
		store, err := diskcache.Open(dir, artworkCacheLimit)
		if err != nil {
			log.Printf("setting up artwork cache: %v", err)
			return
		}
		a.thumbs.UseStore(store)
	})
}

// openAudioCache opens the audio cache where the settings say, or at the
// default location when that folder is gone.
func (a *app) openAudioCache() {
	defaultRoot, err := mygo.App.Path(mygo.PathCache)
	if err != nil {
		log.Printf("finding default audio cache directory: %v", err)
		return
	}
	root := a.settings.CacheDirectory
	if root == "" {
		root = defaultRoot
	}
	a.cacheDirectoryText = root
	cache, canonical, err := audio.Open(root, a.settings.CacheSongs)
	if err != nil && root != defaultRoot {
		log.Printf("setting up audio cache at %q: %v; using the default location", root, err)
		a.cacheDirError = "The saved folder is unavailable. Using the default cache location."
		cache, canonical, err = audio.Open(defaultRoot, a.settings.CacheSongs)
	}
	if err != nil {
		log.Printf("setting up audio cache: %v", err)
		return
	}
	a.cacheDirectoryText = canonical
	a.replaceAudioCache(cache)
}

// shutdown flushes the settings, then closes what belongs to the app.
func (a *app) shutdown() {
	a.savePlayback(0)
	a.saver.Wait()
	a.historyWriter.Wait()
	a.playSave.writer.Wait()
	a.desktop.close()
	if a.media != nil {
		if err := a.media.Close(); err != nil {
			log.Printf("close system media controls: %v", err)
		}
	}
	a.player.Stop()
	a.closeAudioCaches()
}

// stopTicking ends the periodic work. The window closing and the app exiting
// both call it.
func (a *app) stopTicking() { a.stopOnce.Do(func() { close(a.stop) }) }

// every runs fn once per interval off the main thread, until the app stops.
func (a *app) every(interval time.Duration, fn func()) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-a.stop:
				return
			case <-ticker.C:
				a.guarded("periodic work", fn)
			}
		}
	}()
}

// themeState is the theme being shown. A change of colours glides from one
// scheme to the next rather than cutting.
type themeState struct {
	shown *m3.Theme
	to    *m3.Theme
	from  m3.Scheme
	at    time.Time
	// cached is the last theme resolved, reused while the configuration and
	// appearance that make it are unchanged.
	cached     *m3.Theme
	cachedCfg  m3.Config
	cachedDark bool
	// dynamic is the seed taken from the artwork playing, and dynamicFor the
	// artwork it was taken from.
	dynamic    ui.Color
	dynamicOK  bool
	dynamicFor string
	// previews caches the palette-style preview schemes the settings show,
	// until the seed or appearance they are drawn from changes.
	previews    map[m3.Style]m3.Scheme
	previewSeed ui.Color
	previewDark bool
}

// themeGlide is how long a change of colours takes.
const themeGlide = 420 * time.Millisecond

// resolveTheme returns the theme to draw this frame with: the one the
// settings describe, glided to from the one before when it changed.
func (a *app) resolveTheme(c *ui.Context) *m3.Theme {
	t := &a.theme
	cfg := a.settings.Theme(uiFont)
	if a.settings.Dynamic && t.dynamicOK {
		cfg.Seed = t.dynamic
	}
	dark := c.Theme().Dark
	if t.cached == nil || t.cachedCfg != cfg || t.cachedDark != dark {
		t.cached, t.cachedCfg, t.cachedDark = m3.New(cfg, dark), cfg, dark
	}
	target := t.cached
	if t.to == nil {
		t.to, t.shown, t.from = target, target, target.Scheme
		return target
	}
	if target.Config != t.to.Config || target.Dark != t.to.Dark {
		t.from = t.shown.Scheme
		t.to, t.at = target, c.Now()
		if c.Preferences().ReduceMotion {
			t.at = time.Time{} // no glide: cut to the new colours
		}
	}
	k := float32(c.Now().Sub(t.at)) / float32(themeGlide)
	if k >= 1 || t.at.IsZero() {
		t.shown = t.to
		return t.shown
	}
	shown := *t.to
	shown.Scheme = t.from.Mix(t.to.Scheme, 1-(1-k)*(1-k)*(1-k))
	t.shown = &shown
	c.AnimationFrame()
	return t.shown
}

// followArtwork takes the theme's seed from the artwork of the track playing,
// when the user asked for it.
func (a *app) followArtwork() {
	t := &a.theme
	art := a.current.Thumbnail
	if !a.settings.Dynamic || art == "" || t.dynamicFor == art {
		return
	}
	colour, has, ready := a.thumbs.Colour(art, playerArt)
	if !ready {
		return // the picture or its colour is still on the way; the cache wakes the view
	}
	t.dynamicFor = art
	if has {
		t.dynamic, t.dynamicOK = colour, true
	}
}

// systemMediaControls maps operating-system commands to the app's playback
// actions. Platform callbacks arrive on other goroutines, so each goes
// through update before it touches the app.
func (a *app) systemMediaControls() systemmedia.Controls {
	on := func(fn func()) func() { return func() { a.update(fn) } }
	return systemmedia.Controls{
		Play:     on(a.resume),
		Pause:    on(a.pause),
		Toggle:   on(a.togglePlay),
		Stop:     on(a.stopAtQueueEnd),
		Next:     on(a.advance),
		Previous: on(a.previous),
		Seek: func(position time.Duration) {
			a.update(func() { a.seek(position) })
		},
		SetVolume: func(volume float64) {
			a.update(func() {
				a.setVolume(volume * 100)
				a.saveSettings()
			})
		},
		SetShuffle: func(shuffle bool) {
			a.update(func() { a.setShuffle(shuffle) })
		},
		SetLoop: func(loop systemmedia.Loop) {
			a.update(func() {
				a.repeat = repeatFromLoop(loop)
				a.syncSystemMedia()
			})
		},
	}
}

// repeatLoops pairs each repeat mode with the loop status the system shows.
var repeatLoops = [...]systemmedia.Loop{
	repeatOff:   systemmedia.LoopNone,
	repeatQueue: systemmedia.LoopPlaylist,
	repeatTrack: systemmedia.LoopTrack,
}

func repeatFromLoop(loop systemmedia.Loop) repeatMode {
	for mode, l := range repeatLoops {
		if l == loop {
			return repeatMode(mode)
		}
	}
	return repeatOff
}

// syncSystemMedia publishes a snapshot of playback to the system's media
// controls, and lines the sleep assertion and the tray up with it. Every
// path that changes playback calls it.
func (a *app) syncSystemMedia() {
	a.syncDesktop()
	if a.media == nil {
		return
	}
	status := systemmedia.Stopped
	switch {
	case a.player.Playing():
		status = systemmedia.Playing
	case a.player.Paused():
		status = systemmedia.Paused
	}
	position := time.Duration(0)
	if a.player.Active() {
		position = a.player.Position()
	}
	hasTrack := a.current.VideoID != ""
	canNext := false
	if hasTrack {
		_, canNext = a.queue.next(a.settings.AutoPlay, a.repeat)
		if !canNext && a.settings.AutoPlay {
			canNext = a.upNext.load.busy() || !a.upNext.fetched || a.upNext.token != ""
		}
	}
	if err := a.media.Update(systemmedia.State{
		VideoID:     a.current.VideoID,
		Title:       a.current.Title,
		Artist:      a.current.Subtitle,
		ArtworkURL:  a.current.Thumbnail,
		Duration:    a.total,
		Position:    position,
		SeekVersion: a.seekVersion,
		Status:      status,
		Loop:        repeatLoops[a.repeat],
		Volume:      a.volume / 100,
		CanPlay:     hasTrack,
		CanPause:    a.player.Playing(),
		CanNext:     canNext,
		CanPrevious: hasTrack,
		CanSeek:     a.total > 0 && a.player.Active(),
		Shuffle:     a.shuffle,
	}); err != nil {
		log.Printf("system media controls unavailable: %v", err)
		if closeErr := a.media.Close(); closeErr != nil {
			log.Printf("close system media controls: %v", closeErr)
		}
		a.media = nil
	}
}
