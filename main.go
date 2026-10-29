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
	"github.com/elianiva/meiro/internal/diskcache"
	"github.com/elianiva/meiro/internal/memprof"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/internal/secretfile"
	"github.com/elianiva/meiro/internal/settings"
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
	proc.ExtendPath()
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
		a.displayScale = displayScale()
		mygo.Screen.OnDisplaysChanged(func() { a.update(func() { a.displayScale = displayScale() }) })
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
		controls := a.systemMediaControls()
		controls.Window = a.desktop.win.NativeHandle()
		session, err := systemmedia.New(controls)
		if err != nil {
			log.Printf("system media controls unavailable: %v", err)
		} else {
			a.media = session
		}
		a.setupDesktop()
		// A hidden window shows no artwork: what the artwork held goes back
		// to the system while the music plays on.
		a.desktop.win.OnHide(a.releaseArtwork)
		a.desktop.win.OnMinimize(a.releaseArtwork)
		a.syncSystemMedia()
		// Background work reaches the window through the desktop, so it
		// starts only once the window is there.
		a.restoreAccount()
		a.loadScrobbleSession()
		a.loadHistory()
		a.startTicker()
		a.every(persistInterval, func() { a.update(a.persistPlayback) })
		// Browsing decodes and drops pictures in bursts, and the runtime
		// keeps the freed memory for a while before it gives it back.
		a.every(30*time.Second, debug.FreeOSMemory)
		a.watchSession()
		a.every(flushInterval, func() { a.update(a.flushScrobbles) })
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

// releaseArtwork drops the decoded artwork and returns the memory to the
// system.
func (a *app) releaseArtwork() {
	a.thumbs.Purge()
	a.run(debug.FreeOSMemory)
}

// displayScale is the largest scale of the displays, 2 when none says.
func displayScale() float32 {
	scale := 0.0
	for _, d := range mygo.Screen.Displays() {
		scale = max(scale, d.ScaleFactor)
	}
	if scale <= 0 {
		return 2
	}
	return float32(scale)
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
	tools := applog.NewLazy("ffmpeg: looking up")
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

// toolVersions asks ffmpeg for its version. yt-dlp's is left to a report:
// asking it starts Python, some 50 MB for a moment, on every launch.
func toolVersions() string {
	return "ffmpeg " + report.FFmpeg(context.Background())
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

// credentialsKey is the one key the saved sessions are encrypted with, so
// that macOS asks for it once after an update, not once per session.
const credentialsKey = "credentials-key"

// setup reads the settings, opens the audio cache and the credential store,
// and makes the public client.
func (a *app) setup() {
	if directory, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.saver.Path = filepath.Join(directory, "settings.json")
		a.settings = settings.Load(a.saver.Path)
		a.account.creds.store = &secretfile.File{Path: filepath.Join(directory, "cookie.txt"), Keychain: mygo.Secrets, Name: credentialsKey}
		a.historyPath = filepath.Join(directory, "history.json")
		a.restorePlayback(filepath.Join(directory, "playback.json"))
		a.openScrobbler(secretfile.File{Path: filepath.Join(directory, "lastfm-session.txt"), Keychain: mygo.Secrets, Name: credentialsKey}, filepath.Join(directory, "lastfm-pending.json"))
		a.openCredits(filepath.Join(directory, "credits.json"))
	} else {
		a.account.signIn.err = err.Error()
	}
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	a.volume = a.settings.Volume
	a.player.SetVolume(a.volume / 100)
	a.loadEQ()
	a.applyCrossfade()
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
	a.notificationArtwork.stop()
	a.finishEpisodeProgress(true)
	a.savePlayback(0)
	a.saver.Wait()
	a.historyWriter.Wait()
	a.playSave.writer.Wait()
	a.scrobble.outbox.Wait()
	a.scrobble.credits.Wait()
	a.desktop.close()
	if a.discord != nil {
		a.discord.Close()
		a.discord = nil
	}
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
