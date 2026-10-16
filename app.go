package main

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/lru"
	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/internal/ytdlp"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/systemmedia"
	"github.com/elianiva/meiro/youtube"
)

// app is the state the window shows. It is owned by the main thread: the
// view reads and changes it there, and background work hands its results
// back through update, or through a task.
//
// The state is grouped by what owns it. A group with rules of its own, as
// the queue, the account session and the page shown, is a type of its own
// whose methods keep those rules.
type app struct {
	// desktop is the window and the system around it; nil in tests.
	desktop *desktop
	router  *ui.Router
	player  *player.Player
	media   systemmedia.Session
	thumbs  *artwork.Cache

	// run runs work off the main thread. Tests replace it with a runner
	// that runs the work inline, so a frame sees its result at once.
	run func(work func())
	// sleep waits for a delay, or until ctx ends. Tests do not wait.
	sleep func(ctx context.Context, delay time.Duration) error
	// openURL opens a link in the default browser. Tests record it.
	openURL func(url string) error

	// What the user chose, and the writer that keeps it.
	settings settings.Settings
	saver    settings.Writer
	// playSave keeps the queue and position for the next run.
	playSave playbackSave

	account accountState
	// scrobble is the Last.fm connection and the plays waiting for it.
	scrobble scrobbler
	// keyboard is the shortcuts, and the recording of a new one.
	keyboard keyboardState
	// openPath shows a file or folder in the system; tests replace it.
	openPath func(path string) error
	// logsDir is the folder the log file is in; empty when there is none.
	logsDir string
	// notice is a message for the next frame to show as a toast.
	notice string

	theme themeState

	// page is the page shown. It moves between here and pages, the cache of
	// recent ones, so exactly one place holds each.
	page      *pageData
	location  string
	routed    routed
	pageLoad  task // the load of the page shown
	pageMore  task // the next part of the page shown
	pages     *lru.Cache[string, *pageData]
	details   *lru.Cache[string, detail]
	carousels *lru.Cache[string, *m3.CarouselState]
	list      *ui.ListState
	listWidth float32 // width the page's list had in the last frame
	search    searchState
	// searchField and detailSearchField move the keyboard to the top bar's
	// search and to the playlist page's, where Focus waits while one is hidden.
	searchField, detailSearchField ui.Handle
	// writing holds the writes to a like, the library or a subscription that
	// are out, by what they change, so that one is not asked for twice.
	writing  map[string]bool
	menuOpen bool // the account menu
	// detailFilter narrows a playlist page to the songs that match. It is
	// part of the page's rows key, so a change asks for the rows again.
	// detailSearchOpen is whether the bar that types it is open.
	detailFilter         string
	detailSearchOpen     bool
	detailSearchHadFocus bool
	detailSort           playlistSort
	detailSortOpen       bool
	menu                 itemMenu

	// Dialogs.
	crash         *crashState
	newPlaylist   *newPlaylistState
	addToPlaylist *addToPlaylistState
	// knownPlaylists are the playlists the library last showed, offered at
	// once by the playlist picker.
	knownPlaylists knownPlaylists

	playback
	nowPlaying nowPlayingState

	// The local listening history and the stats page built from it.
	historyPath    string
	history        []history.Entry
	historyLoaded  bool
	historyLoading bool
	// historyDiscard drops the plays the read in flight brings: the user
	// cleared the history while it was being read.
	historyDiscard bool
	// confirmClearHistory is set once the user has asked to clear the history,
	// until they confirm or keep it.
	confirmClearHistory bool
	historyWriter       history.Writer
	statsRange          int // days the stats page covers: 7, 14 or 30
	stats               statsMemo

	// audioCache is the cache playback uses now; the settings page swaps it
	// from the main thread while background work holds on to the one it
	// started with.
	audioCacheMu       sync.RWMutex
	audioCache         *audio.Cache
	retiredAudioCaches []*audio.Cache
	cacheSongsText     string
	cacheLimitError    string
	cacheDirectoryText string
	cacheDirError      string
	// report is the lookup of tool versions for a problem report, and
	// reportError what went wrong opening it.
	report      task
	reportError string
	// tools are the versions of the bundled programs, shown in Settings once
	// toolsLookup has read them.
	tools       report.Info
	toolsLoaded bool
	toolsLookup task

	// eqApplied is the band table the player last received, so a slider
	// release applies only what changed.
	eqApplied [player.EQBandCount]float64
	// eqDebounce waits out a quiet moment after the sliders moved before the
	// changed bands restart the decode.
	eqDebounce task

	// lyricsSource finds a track's lyrics on LRCLIB; tests replace it.
	lyricsSource lyricsSource

	// stop ends the periodic work; stopOnce closes it once.
	stop     chan struct{}
	stopOnce sync.Once
}

// newApp builds the app with no window. setup gives it one.
func newApp() *app {
	a := &app{
		router:     ui.NewRouter(routeByID(routeHome).path("")),
		player:     player.New(playerOptions()...),
		settings:   settings.Defaults(),
		openURL:    mygo.Shell.OpenExternal,
		page:       &pageData{},
		list:       &ui.ListState{},
		pages:      lru.New[string, *pageData](pageCacheLimit),
		details:    lru.New[string, detail](pageCacheLimit),
		carousels:  lru.New[string, *m3.CarouselState](pageCacheLimit * 4),
		stop:       make(chan struct{}),
		statsRange: 14,
		keyboard:   keyboardState{hintDelay: hintDelay},
	}
	a.scrobble = newScrobbler()
	a.playback = newPlayback(audio.NewURLs(ytdlp.Resolve), a.settings.Volume)
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	a.run = func(work func()) {
		go func() {
			defer a.recoverBackground("background work")
			work()
		}()
	}
	a.sleep = sleep
	a.openPath = func(path string) error { return a.desktop.openPath(path) }
	a.account = newAccountState(func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{CookieAuth: auth})
	}, cookies.Sources)
	a.thumbs = artwork.New(&http.Client{Timeout: 30 * time.Second}, a.refresh)
	a.lyricsSource = new(lrclib.Client).Get
	return a
}

// update runs fn where the view can see its result, on the main thread, and
// draws a frame. Without a desktop, as in tests, it runs fn at once.
func (a *app) update(fn func()) {
	if a.desktop != nil {
		inner := fn
		fn = func() {
			defer a.recoverMain("updating the window")
			inner()
		}
	}
	a.desktop.update(fn)
}

// refresh draws a frame with what background work produced.
func (a *app) refresh() { a.desktop.invalidate() }

// saveSettings keeps the settings, off the main thread.
func (a *app) saveSettings() { a.saver.Save(&a.settings) }

// client returns the client to use for a request: the account's when the
// user is signed in, the public one otherwise.
func (a *app) client() *youtube.Client {
	if s := a.account.session; s != nil {
		return s.client
	}
	return a.account.public
}

// signedIn reports whether an account is in use.
func (a *app) signedIn() bool { return a.account.session != nil }

// currentAudioCache is the audio cache playback uses now. Background work
// keeps the one it was handed, which stays usable until shutdown.
func (a *app) currentAudioCache() *audio.Cache {
	a.audioCacheMu.RLock()
	defer a.audioCacheMu.RUnlock()
	return a.audioCache
}

// replaceAudioCache makes cache the one playback uses, and returns the one it
// replaces, which is closed at shutdown.
func (a *app) replaceAudioCache(cache *audio.Cache) *audio.Cache {
	a.audioCacheMu.Lock()
	defer a.audioCacheMu.Unlock()
	previous := a.audioCache
	if previous != nil && previous != cache {
		a.retiredAudioCaches = append(a.retiredAudioCaches, previous)
	}
	a.audioCache = cache
	return previous
}

// closeAudioCaches stops every cache's downloads and waits for them.
func (a *app) closeAudioCaches() {
	a.audioCacheMu.Lock()
	caches := a.retiredAudioCaches
	caches = append(caches, a.audioCache)
	a.retiredAudioCaches, a.audioCache = nil, nil
	a.audioCacheMu.Unlock()
	for _, cache := range caches {
		cache.Close()
	}
}

// sleep waits for delay, or until ctx ends, and reports why it stopped.
func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
