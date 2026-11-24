package main

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/ytdlp"
	"github.com/elianiva/meiro/youtube"
)

func resolveAudio(client *youtube.Client, auth *audioAuth, traces *resolveTraces) audio.Resolver {
	return func(ctx context.Context, videoID string) (string, time.Duration, error) {
		trace := resolveTrace{VideoID: videoID, Signed: false}
		started := time.Now()
		defer func() { trace.Elapsed = time.Since(started); traces.put(trace) }()
		if cookie := auth.get(); cookie != "" {
			trace.Source, trace.Signed = "yt-dlp", true
			url, total, run, err := ytdlp.Resolve(ctx, videoID, cookie)
			trace.Run, trace.URL = run, url
			return url, total, err
		}
		url, total, err := client.AudioStream(ctx, videoID)
		if err == nil {
			trace.Source, trace.URL = "YouTube player request", url
			return url, total, nil
		}
		trace.DirectError = err.Error()
		if ctx.Err() != nil {
			return "", 0, err
		}
		log.Printf("playback: direct resolve failed video_id=%s, trying yt-dlp: %v", videoID, err)
		trace.Source = "yt-dlp"
		var run ytdlp.Run
		url, total, run, err = ytdlp.Resolve(ctx, videoID, "")
		trace.Run, trace.URL = run, url
		return url, total, err
	}
}

type audioAuth struct {
	mu     sync.Mutex
	cookie string
}

func (s *audioAuth) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cookie
}

func (s *audioAuth) set(cookie string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := s.cookie != cookie
	s.cookie = cookie
	return changed
}

func (a *app) syncAudioAuth() {
	cookie := ""
	if a.settings.AccountAudio && a.account.session != nil {
		cookie = a.account.session.cookie
	}
	if a.audioAuth.set(cookie) {
		a.streams.Reset()
	}
}

type resolvedAudio struct {
	url        string
	total      time.Duration
	fromCache  bool
	playErr    error
	loudness   youtube.Loudness
	loudnessOK bool
}

func (a *app) streamAudio(item youtube.MusicItem, startAt time.Duration, changed, newPlay bool) {
	cache := a.currentAudioCache()
	accountProgress := a.signedIn()
	loudnessClient := a.loudnessClient(item.VideoID)
	known, knownOK := a.loudnessMap.Get(item.VideoID)
	volume, normalize, logarithmic := a.volume, a.settings.NormalizeLoudness, a.settings.LogVolume
	speed := a.speedFor(item)
	streams := a.streams
	local := a.downloaded(item.VideoID)
	log.Printf("playback: resolving video_id=%s", item.VideoID)
	startTask(a, &a.stream, audio.ResolveTimeout, func(ctx context.Context) (resolvedAudio, error) {
		a.player.Stop()
		if err := a.player.SetSpeed(ctx, speed); err != nil {
			return resolvedAudio{}, err
		}
		var fresh youtube.Loudness
		var freshOK bool
		if loudnessClient != nil {
			wait, cancel := context.WithTimeout(ctx, loudnessWait)
			value, err := loudnessClient.Loudness(wait, item.VideoID)
			cancel()
			if err != nil {
				log.Printf("playback: loudness unavailable video_id=%s: %v", item.VideoID, err)
			} else {
				known, knownOK = value, true
				fresh, freshOK = value, true
			}
		}
		a.player.SetVolume(playbackVolume(volume, known.LoudnessDB, knownOK && normalize, logarithmic))
		found, err := findAudio(ctx, streams, item, local, cache)
		found.loudness, found.loudnessOK = fresh, freshOK
		if err != nil || ctx.Err() != nil {
			return found, err
		}
		if accountProgress && item.IsEpisode() {
			startAt = max(startAt, episodeProgressPosition(item, found.total))
		}
		found.playErr = a.player.PlayAtContext(ctx, found.url, startAt, false)
		return found, nil
	}, func(found resolvedAudio, err error) {
		if found.loudnessOK {
			a.loudnessMap.Put(item.VideoID, found.loudness)
		}
		if err != nil {
			a.failPlayback(item, "audio resolution failed", err.Error())
			a.skipUnplayable(item)
			return
		}
		a.applyLoudness()
		if found.total > 0 {
			a.total = found.total
			a.refreshLyrics()
		}
		if found.playErr != nil {
			a.failPlayback(item, "player could not open", found.playErr.Error())
			a.skipUnplayable(item)
			return
		}
		log.Printf("playback: audio started video_id=%s from_cache=%t duration=%s", item.VideoID, found.fromCache, found.total)
		a.applySpeed()
		a.audioStarted(item, found, a.player.Position(), changed, newPlay, cache)
		a.syncSystemMedia()
	})
}

func (a *app) audioStarted(item youtube.MusicItem, found resolvedAudio, position time.Duration, changed, newPlay bool, cache *audio.Cache) {
	a.skipped = 0
	a.aiRefills = 0
	if newPlay {
		a.recordHistory(item)
	}
	if item.IsEpisode() {
		a.beginEpisodeProgress(item, position, found.total)
	} else if changed {
		a.reportPlayback(item)
	}
	a.playing = playingAudio{videoID: item.VideoID, url: found.url, file: found.fromCache}
	if !found.fromCache {
		cache.Add(item.VideoID, found.url)
	}
	a.warmNext()
}

const preloadLead = 20 * time.Second

const probeTimeout = 15 * time.Second

type preloadedAudio struct {
	id string
	resolvedAudio
}

func (a *app) following() (int, youtube.MusicItem, bool) {
	if a.repeat == repeatTrack {
		return -1, a.current, a.current.VideoID != ""
	}
	i, ok := a.queue.nextEligible(a.settings.AutoPlay, a.repeat, a.settings.AutoSkipDisliked)
	if !ok {
		return 0, youtube.MusicItem{}, false
	}
	return i, a.queue.at(i), true
}

func (a *app) syncPreload() {
	_, next, ok := a.following()
	if ok && a.settings.ExcludeAIGenerated && isMusicTrack(next) && a.aiDisclosure(next) != youtube.AINone {
		ok = false
	}
	if held := a.player.Preloaded(); held != "" && (!ok || held != next.VideoID) {
		a.player.CancelPreload()
		a.preloaded = preloadedAudio{}
	}
	if !ok || a.preload.busy() && a.preloadFor != next.VideoID {
		a.preload.stop()
	}
	speedChangesAtNext := ok && a.speedFor(next) != a.speedFor(a.current)
	if !ok || a.starting() || a.preload.busy() || a.preloadFailed == next.VideoID || speedChangesAtNext ||
		a.player.Preloaded() == next.VideoID || a.total <= 0 || !a.player.Playing() ||
		a.total-a.player.Position() > preloadLead {
		return
	}
	cache := a.currentAudioCache()
	local := a.downloaded(next.VideoID)
	a.preloadFor = next.VideoID
	a.fetchLoudness(next, &a.preloadLoudnessLoad)
	startTask(a, &a.preload, audio.ResolveTimeout, func(ctx context.Context) (preloadedAudio, error) {
		found, err := findAudio(ctx, a.streams, next, local, cache)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return preloadedAudio{}, err
		}
		return preloadedAudio{next.VideoID, found}, a.player.PreloadContext(ctx, next.VideoID, found.url)
	}, func(found preloadedAudio, err error) {
		if err != nil {
			log.Printf("playback: could not preload video_id=%s: %v", next.VideoID, err)
			a.preloadFailed = next.VideoID
			return
		}
		log.Printf("playback: preloaded video_id=%s from_cache=%t", next.VideoID, found.fromCache)
		a.preloaded = found
	})
}

func (a *app) followed(tag string) {
	i, next, ok := a.following()
	if !ok || next.VideoID != tag || a.preloaded.id != tag {
		a.trackEnded()
		return
	}
	found := a.preloaded
	item := next
	if i >= 0 {
		item = a.queue.moveTo(i)
	}
	startAt, changed := a.enterTrack(item, true)
	a.applyLoudness()
	if found.total > 0 {
		a.total = found.total
	}
	if item.IsEpisode() {
		if a.signedIn() {
			startAt = max(startAt, episodeProgressPosition(item, found.total))
		}
		if startAt > 0 {
			a.seek(startAt)
		}
	}
	log.Printf("playback: audio followed on video_id=%s from_cache=%t duration=%s", item.VideoID, found.fromCache, found.total)
	a.audioStarted(item, found.resolvedAudio, startAt, changed, true, a.currentAudioCache())
	a.afterEnter(item, changed)
	a.syncSystemMedia()
}

func (a *app) failPlayback(item youtube.MusicItem, what, raw string) {
	log.Printf("playback: %s video_id=%s: %s", what, item.VideoID, raw)
	a.rawPlayErr, a.playErr = raw, playErrorMessage(raw)
	a.syncSystemMedia()
}

const (
	maxRecoveries   = 3
	recoveryHealthy = 10 * time.Second
	recoveryBackoff = 500 * time.Millisecond
	endOfTrackLead  = 3 * time.Second
)

func (a *app) atTrackEnd() bool {
	if a.total <= 0 || a.player.Paused() {
		return false
	}
	return a.total-a.player.Position() <= endOfTrackLead
}

func (a *app) recoverStream(failure string) bool {
	item := a.current
	if item.VideoID == "" || a.recoveries >= maxRecoveries {
		return false
	}
	a.recoveries++
	attempt := a.recoveries
	at := a.player.Position()
	if attempt == 1 {
		a.recoveredFrom = at
	}
	paused, bySleep := a.player.Paused(), a.suspendedWhilePlaying
	cache := a.currentAudioCache()
	local := a.downloaded(item.VideoID)
	log.Printf("playback: stream broke off video_id=%s at=%s attempt=%d/%d: %s", item.VideoID, at.Round(time.Second), attempt, maxRecoveries, failure)
	a.fetchLoudness(item, &a.loudnessLoad)
	startTask(a, &a.stream, audio.ResolveTimeout+time.Duration(attempt)*recoveryBackoff, func(ctx context.Context) (resolvedAudio, error) {
		if err := a.sleep(ctx, time.Duration(attempt)*recoveryBackoff); err != nil {
			return resolvedAudio{}, err
		}
		a.streams.Forget(item.VideoID)
		found, err := findAudio(ctx, a.streams, item, local, cache)
		if err != nil || ctx.Err() != nil {
			return found, err
		}
		found.playErr = a.player.PlayAtContext(ctx, found.url, at, paused)
		return found, nil
	}, func(found resolvedAudio, err error) {
		if err == nil {
			err = found.playErr
		}
		if err != nil {
			log.Printf("playback: recovery failed video_id=%s attempt=%d/%d: %v", item.VideoID, attempt, maxRecoveries, err)
			if !a.recoverStream(err.Error()) {
				a.failPlayback(item, "recovery failed", err.Error())
				a.skipUnplayable(item)
			}
			return
		}
		a.applyLoudness()
		a.applySpeed()
		if paused && bySleep && !a.suspendedWhilePlaying {
			a.player.Resume()
		}
		a.syncSystemMedia()
	})
	return true
}

func findAudio(ctx context.Context, streams *audio.URLs, item youtube.MusicItem, local string, cache *audio.Cache) (resolvedAudio, error) {
	found := resolvedAudio{total: parseDuration(item.Duration)}
	if local != "" {
		if _, err := os.Stat(local); err != nil {
			log.Printf("playback: downloaded audio unavailable video_id=%s: %v", item.VideoID, err)
			local = ""
		}
	}
	path, ok := local, local != ""
	if ok {
		log.Printf("playback: playing downloaded audio video_id=%s", item.VideoID)
	} else if path, ok = cache.Get(item.VideoID); ok {
		log.Printf("playback: audio cache hit video_id=%s", item.VideoID)
	}
	if ok {
		found.url, found.fromCache = path, true
		if found.total <= 0 {
			probe, cancel := context.WithTimeout(ctx, probeTimeout)
			defer cancel()
			found.total = audio.Probe(probe, path)
		}
		return found, nil
	}
	url, total, err := streams.Get(ctx, item.VideoID)
	found.url = url
	if total > 0 {
		found.total = total
	}
	return found, err
}

func (a *app) warmNext() {
	_, next, ok := a.following()
	if !ok {
		return
	}
	if a.settings.ExcludeAIGenerated && isMusicTrack(next) && a.aiDisclosure(next) != youtube.AINone {
		return
	}
	if next.VideoID == "" || next.VideoID == a.current.VideoID || a.isDownloaded(next.VideoID) {
		return
	}
	cache := a.currentAudioCache()
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), audio.ResolveTimeout)
		defer cancel()
		url, _, err := a.streams.Get(ctx, next.VideoID)
		if err != nil {
			log.Printf("playback: could not resolve next video_id=%s: %v", next.VideoID, err)
			return
		}
		if cache.Warmable() {
			cache.Add(next.VideoID, url)
		}
	})
}

func (a *app) offerPremium() {
	s := a.account.session
	if s == nil || a.settings.AccountAudio || a.settings.PremiumOffered {
		return
	}
	hasPremium := a.hasPremium
	spawn(a, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		premium, err := hasPremium(ctx, s.cookie)
		if err != nil {
			log.Printf("account: premium check failed: %v", err)
		}
		return premium
	}, func(premium bool) {
		if !premium || a.account.session != s || a.settings.AccountAudio || a.settings.PremiumOffered {
			return
		}
		a.settings.PremiumOffered = true
		a.saveSettings()
		a.notice, a.noticePremium = "YouTube Premium found. Play audio as your account for higher bitrate?", true
	})
}
