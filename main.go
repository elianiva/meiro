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

const (
	gcPercentForArtworkHeavyHeap = 50
	freeOSMemoryInterval         = 30 * time.Second
)

func main() {
	debug.SetGCPercent(gcPercentForArtworkHeavyHeap)
	stopMemprof := memprof.Start(15 * time.Second)
	proc.ExtendPath()
	proc.SweepTemp()
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	logsDir, closeLog := openLog()
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
		a.desktop.win.OnHide(a.releaseArtwork)
		a.desktop.win.OnMinimize(a.releaseArtwork)
		a.syncSystemMedia()
		a.restoreAccount()
		a.loadScrobbleSession()
		a.loadHistory()
		a.startPrivacy()
		a.startTicker()
		a.every(persistInterval, func() { a.update(a.persistPlayback) })
		a.every(freeOSMemoryInterval, debug.FreeOSMemory)
		a.watchSettings()
		a.watchSession()
		a.every(flushInterval, func() { a.update(a.flushScrobbles) })
		a.desktop.win.OnClosed(a.stopTicking)
		mygo.App.OnQuit(a.shutdown)
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

func (a *app) releaseArtwork() {
	a.thumbs.Purge()
	a.run(debug.FreeOSMemory)
}

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
	tools.Start(toolVersions, func(v string) { log.Print("tools: ", v) })
	return dir, func() { _ = file.Close() }
}

func toolVersions() string {
	return "ffmpeg " + report.FFmpeg(context.Background())
}

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

func playerOptions() []player.Option {
	if ffmpeg, err := proc.Tool("ffmpeg"); err == nil {
		return []player.Option{player.WithFFmpeg(ffmpeg)}
	}
	return nil
}

const credentialsKey = "credentials-key"

func (a *app) setup() {
	if directory, err := mygo.App.Path(mygo.PathUserData); err == nil {
		a.saver.Path = filepath.Join(directory, "settings.json")
		a.settings = settings.Load(a.saver.Path)
		a.colorsPath = filepath.Join(directory, "colors.json")
		a.colors = a.readColors()
		a.account.creds.store = &secretfile.File{Path: filepath.Join(directory, "cookie.txt"), Keychain: mygo.Secrets, Name: credentialsKey}
		a.historyPath = filepath.Join(directory, "history.json")
		a.restorePlayback(filepath.Join(directory, "playback.json"))
		a.openScrobbler(secretfile.File{Path: filepath.Join(directory, "lastfm-session.txt"), Keychain: mygo.Secrets, Name: credentialsKey}, filepath.Join(directory, "lastfm-pending.json"))
		a.openCredits(filepath.Join(directory, "credits.json"))
		a.openDownloads(filepath.Join(directory, "Downloads"))
	} else {
		a.account.signIn.err = err.Error()
	}
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	a.volume = a.settings.Volume
	a.player.SetVolume(playbackVolume(a.volume, 0, false, a.settings.LogVolume))
	a.loadEQ()
	a.applyCrossfade()
	a.openAudioCache()
	a.openArtworkCache()
}

const artworkCacheLimit = 256 << 20

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

func (a *app) shutdown() {
	a.shutdownOnce.Do(func() {
		a.aiCheck.stop()
		a.notificationArtwork.stop()
		a.finishEpisodeProgress(true)
		a.savePlayback(0)
		a.saver.Wait()
		a.historyWriter.Wait()
		a.downloads.writer.Wait()
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
	})
}

func (a *app) stopTicking() { a.stopOnce.Do(func() { close(a.stop) }) }

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
