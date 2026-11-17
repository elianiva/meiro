package main

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/betterlyrics"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/discordrpc"
	"github.com/elianiva/meiro/internal/history"
	"github.com/elianiva/meiro/internal/lrclib"
	"github.com/elianiva/meiro/internal/lru"
	"github.com/elianiva/meiro/internal/lyrics"
	"github.com/elianiva/meiro/internal/posthog"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/internal/sentry"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/internal/ytdlp"
	"github.com/elianiva/meiro/m3"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/systemmedia"
	"github.com/elianiva/meiro/youtube"
)

type app struct {
	desktop      *desktop
	router       *ui.Router
	player       *player.Player
	media        systemmedia.Session
	discord      *discordrpc.Session
	thumbs       *artwork.Cache
	displayScale float32

	run           func(work func())
	sleep         func(ctx context.Context, delay time.Duration) error
	openURL       func(url string) error
	keepRunning   func(reason string) func()
	capture       func(event string, properties map[string]any) error
	sendCrash     func(crash sentry.Crash) error
	bundled       func(name string) bool
	notifications func() bool

	settings     settings.Settings
	saver        settings.Writer
	settingsSeen []byte
	colors       *m3.Colors
	colorsPath   string
	colorsSeen   []byte
	playSave     playbackSave

	account       accountState
	scrobble      scrobbler
	keyboard      keyboardState
	openPath      func(path string) error
	hasPremium    func(ctx context.Context, cookie string) (bool, error)
	logsDir       string
	notice        string
	noticeHelp    bool
	noticePremium bool

	theme themeState

	page                           *pageData
	location                       string
	routed                         routed
	pageLoad                       task
	pageMore                       task
	pageSimilar                    task
	pages                          *lru.Cache[string, *pageData]
	details                        *lru.Cache[string, detail]
	carousels                      *lru.Cache[string, *m3.CarouselState]
	list                           *ui.ListState
	listWidthLastFrame             float32
	detailCompact                  bool
	pull                           pullState
	swipe                          swipeState
	swipeMods                      ui.Modifiers
	railItems, railFooter          []m3.NavItem
	search                         searchState
	searchField, detailSearchField ui.Handle
	settingsSearch                 settingsSearchState
	writing                        map[string]bool
	accountMenuOpen                bool
	detailFilter                   string
	detailSearchOpen               bool
	detailSearchHadFocus           bool
	detailSort                     playlistSort
	detailSortOpen                 bool
	menu                           itemMenu
	cover                          cardCover
	playerCover                    coverState
	miniCover                      coverState
	bylineFor                      bylineKey
	bylineParts                    []youtube.BylinePart
	artMemo                        map[artKey]*ui.Bitmap

	crash          *crashState
	toolHelp       *toolHelpState
	newPlaylist    *newPlaylistState
	deletePlaylist *deletePlaylistState
	addToPlaylist  *addToPlaylistState
	privacy        privacyState

	knownPlaylists knownPlaylists

	playback
	aiFilter   aiFilterState
	nowPlaying nowPlayingState
	notch      notchState

	historyPath         string
	history             []history.Entry
	historyLoaded       bool
	historyLoading      bool
	historyDiscard      bool
	confirmClearHistory bool
	historyWriter       history.Writer
	statsRangeDays      int
	stats               statsMemo

	downloads downloads

	audioCacheMu       sync.RWMutex
	audioCache         *audio.Cache
	audioAuth          audioAuth
	traces             resolveTraces
	nerd               *nerdState
	playing            playingAudio
	retiredAudioCaches []*audio.Cache
	cacheSongsText     string
	cacheLimitError    string
	cacheDirectoryText string
	cacheDirError      string
	report             task
	reportError        string
	tools              report.Info
	toolsLoaded        bool
	toolsLookup        task

	eqApplied  [player.EQBandCount]float64
	eqDebounce task

	betterLyrics func(ctx context.Context, q betterlyrics.Query) ([]lyrics.Line, error)
	lyricsSource lyricsSource
	collectInfo  func(ctx context.Context, app string) report.Info

	stop         chan struct{}
	stopOnce     sync.Once
	shutdownOnce sync.Once
}

func newApp() *app {
	a := &app{
		router:         ui.NewRouter(routeByID(routeHome).path("")),
		player:         player.New(playerOptions()...),
		settings:       settings.Defaults(),
		openURL:        mygo.Shell.OpenExternal,
		keepRunning:    mygo.Power.KeepRunning,
		bundled:        proc.Bundled,
		nowPlaying:     nowPlayingState{queueSelectionAnchor: -1, queueSelectionCursor: -1},
		page:           &pageData{scroll: &ui.ScrollState{}},
		list:           &ui.ListState{},
		pages:          lru.New[string, *pageData](pageCacheLimit),
		details:        lru.New[string, detail](pageCacheLimit),
		carousels:      lru.New[string, *m3.CarouselState](pageCacheLimit * 4),
		writing:        make(map[string]bool),
		stop:           make(chan struct{}),
		statsRangeDays: 14,
		swipeMods:      ui.Shift,
		keyboard:       keyboardState{hintDelay: hintDelay},
	}
	a.notifications = mygo.NotificationsSupported
	a.scrobble = newScrobbler()
	a.playback = newPlayback(audio.NewURLs(resolveAudio(youtube.NewClient(youtube.Options{}), &a.audioAuth, &a.traces)), a.settings.Volume)
	a.aiFilter = newAIFilter()
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	a.run = func(work func()) {
		go func() {
			defer a.recoverBackground("background work")
			work()
		}()
	}
	a.sleep = sleep
	if key := posthogKey(); key != "" {
		client := &posthog.Client{Key: key}
		a.capture = client.Capture
	}
	if dsn := sentryDSN(); dsn != "" {
		client, err := sentry.New(dsn, mygo.App.Version())
		if err != nil {
			log.Printf("setting up crash reports: %v", err)
		} else {
			a.sendCrash = client.Capture
		}
	}
	a.hasPremium = ytdlp.HasPremium
	a.openPath = func(path string) error { return a.desktop.openPath(path) }
	a.account = newAccountState(func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{CookieAuth: auth})
	}, cookies.Sources)
	a.thumbs = artwork.New(&http.Client{Timeout: 30 * time.Second}, a.refresh)
	a.betterLyrics = new(betterlyrics.Client).Get
	a.lyricsSource = new(lrclib.Client).Get
	a.collectInfo = report.Collect
	return a
}

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

func (a *app) refresh() { a.desktop.invalidate() }

func (a *app) saveSettings() { a.saver.Save(&a.settings) }

func (a *app) client() *youtube.Client {
	if s := a.account.session; s != nil {
		return s.client
	}
	return a.account.public
}

func (a *app) signedIn() bool { return a.account.session != nil }

func (a *app) currentAudioCache() *audio.Cache {
	a.audioCacheMu.RLock()
	defer a.audioCacheMu.RUnlock()
	return a.audioCache
}

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
