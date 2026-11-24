package main

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/lru"
	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/internal/settings"
	"github.com/elianiva/meiro/youtube"
)

type playback struct {
	current             youtube.MusicItem
	queue               queue
	total               time.Duration
	scrub               float64
	scrubbing           bool
	seekTarget          time.Duration
	seekVersion         uint64
	volume              float64
	muted               float64
	loudness            float64
	loudnessOK          bool
	loudnessMap         *lru.Cache[string, youtube.Loudness]
	shuffle             bool
	repeat              repeatMode
	playErr, rawPlayErr string
	skipped             int
	aiRefills           int
	recoveries          int
	recoveredFrom       time.Duration

	stream              task
	aiCheck             task
	notificationArtwork task
	notices             trackNotices
	loudnessLoad        task
	preloadLoudnessLoad task
	streams             *audio.URLs
	seeking             task
	speeding            task
	preload             task
	preloaded           preloadedAudio
	preloadFailed       string
	preloadFor          string
	upNext              upNextState
	queueRest           task
	opening             task
	openingKey          string

	suspendedWhilePlaying bool

	resumeID        string
	episodes        []playstate.Episode
	episodeProgress *episodeProgressSync
}

func (a *app) notificationArtworkNeeded() bool {
	return runtime.GOOS != "darwin" && a.notifications()
}

func newPlayback(streams *audio.URLs, volume float64) playback {
	return playback{queue: newQueue(), streams: streams, volume: volume, loudnessMap: lru.New[string, youtube.Loudness](256)}
}

func (a *app) play(item youtube.MusicItem, tracks []youtube.MusicItem) {
	a.playFrom(item, tracks, -1, youtube.UpNextOptions{}, "")
}

func (a *app) playFrom(item youtube.MusicItem, tracks []youtube.MusicItem, index int, options youtube.UpNextOptions, source string) {
	token := ""
	if r, id := a.route(); r.id == routePlaylist && id == options.PlaylistID && len(tracks) == len(a.page.playable) {
		token = a.page.playlistMore
	}
	a.playFromWithContinuation(item, tracks, index, options, source, token)
}

func (a *app) playFromWithContinuation(item youtube.MusicItem, tracks []youtube.MusicItem, index int, options youtube.UpNextOptions, source, token string) {
	if item.VideoID == "" {
		return
	}
	a.queueRest.stop()
	a.clearQueueSelection()
	a.queue.replace(item, tracks, index, source)
	if a.settings.ExcludeAIGenerated {
		a.queue.dropMarkedAI(a.excludesItem)
	}
	clear(a.scrobble.queued)
	a.learnCredits(tracks...)
	a.learnCredits(item)
	options.VideoID = item.VideoID
	a.resetUpNext(options)
	a.queue.playlistMore = token
	if a.loading() && a.current.VideoID == item.VideoID {
		a.loadPlayingPlaylistRest()
		a.ensureUpNext()
		return
	}
	if a.shuffle {
		a.queue.shuffleQueue()
	}
	a.start(item)
}

const maxSkips = 3

func (a *app) start(item youtube.MusicItem) {
	a.skipped = 0
	a.aiRefills = 0
	a.startTrack(item, false)
}

func (a *app) startNext(item youtube.MusicItem) {
	a.skipped = 0
	a.startTrack(item, true)
}

func (a *app) skipUnplayable(item youtube.MusicItem) {
	if a.current.VideoID != item.VideoID || a.skipped >= maxSkips {
		return
	}
	next, ok := a.queue.nextEligible(a.settings.AutoPlay, a.repeat, a.settings.AutoSkipDisliked)
	if !ok {
		return
	}
	a.skipped++
	a.notice = "Skipped " + item.Title + ": " + strings.TrimSuffix(a.playErr, ".")
	a.noticeHelp = isFormatUnavailable(a.rawPlayErr)
	a.startTrack(a.queue.moveTo(next), true)
}

func (a *app) startTrack(item youtube.MusicItem, automatic bool) {
	a.aiCheck.stop()
	a.stream.stop()
	startAt, changed := a.enterTrack(item, automatic)
	if a.checkAIBeforePlay(item, startAt, changed) {
		return
	}
	a.startTrackNow(item, startAt, changed)
}

func (a *app) startTrackNow(item youtube.MusicItem, startAt time.Duration, changed bool) {
	a.streamAudio(item, startAt, changed, true)
	a.afterEnter(item, changed)
}

func (a *app) enterTrack(item youtube.MusicItem, automatic bool) (time.Duration, bool) {
	a.recoveries = 0
	changed := a.current.VideoID != item.VideoID
	if changed {
		a.finishEpisodeProgress(false)
		a.leaveEpisode()
	}
	a.current = item
	a.total = parseDuration(item.Duration)
	a.loudness, a.loudnessOK = 0, false
	startAt := time.Duration(0)
	switch {
	case a.resumeID == item.VideoID:
		startAt = time.Duration(a.scrub * float64(time.Second))
	case item.IsEpisode():
		startAt = a.takeEpisode(item.VideoID)
	}
	a.resumeID = ""
	a.scrub, a.playErr, a.rawPlayErr = startAt.Seconds(), "", ""
	a.seeking.stop()
	a.seekTarget = 0
	a.speeding.stop()
	a.preload.stop()
	a.preloaded, a.preloadFailed = preloadedAudio{}, ""
	a.beginListen(item, automatic)
	a.prefetchNextLyrics()
	return startAt, changed
}

func (a *app) afterEnter(item youtube.MusicItem, changed bool) {
	a.loadPlayingPlaylistRest()
	if changed {
		a.notch.resetLyrics()
	}
	a.ensureUpNext()
	a.loadPanel()
	if changed {
		if runtime.GOOS != "darwin" && a.settings.NotifyTrack {
			a.notifyTrack(item)
		} else {
			a.notificationArtwork.stop()
		}
	}
	a.syncSystemMedia()
}

func (a *app) notifyTrack(item youtube.MusicItem) {
	notices := a.trackNotices()
	if notices == nil {
		return
	}
	show := func(icon []byte) {
		if notices.appActive() {
			notices.dismissNotification()
			return
		}
		notices.notify(item, icon)
	}
	if !a.notificationArtworkNeeded() || a.thumbs == nil {
		a.notificationArtwork.stop()
		show(nil)
		return
	}
	startTask(a, &a.notificationArtwork, 8*time.Second, func(ctx context.Context) ([]byte, error) {
		return a.thumbs.NotificationIcon(ctx, item.Thumbnail)
	}, func(icon []byte, err error) {
		if a.current.VideoID != item.VideoID {
			return
		}
		if err != nil {
			log.Printf("fetching track notification artwork: %v", err)
			icon = nil
		}
		show(icon)
	})
}

func (a *app) trackNotices() trackNotices {
	if a.notices != nil {
		return a.notices
	}
	if a.desktop == nil {
		return nil
	}
	return a.desktop
}

func (a *app) starting() bool { return a.aiCheck.busy() || a.stream.busy() }

func (a *app) loading() bool { return a.starting() || a.player.Buffering() }

func (a *app) enqueue(item youtube.MusicItem, playNext bool) int {
	if item.VideoID == "" {
		return -1
	}
	if a.excludesItem(item) {
		a.notice = "Skipped " + item.Title + ": YouTube marks it as made with AI"
		return -1
	}
	if a.current.VideoID == "" || a.queue.len() == 0 {
		a.playFrom(item, nil, 0, youtube.UpNextOptions{}, "Queue")
		a.notice = "Playing " + item.Title
		return a.queue.index
	}
	index := a.queue.insert(item, playNext, a.settings.AutoPlay)
	a.clearQueueSelection()
	a.rememberQueued(item.VideoID)
	a.learnCredits(item)
	if playNext {
		a.notice = item.Title + " will play next"
	} else {
		a.notice = "Added " + item.Title + " to the queue"
	}
	a.syncPreload()
	a.syncSystemMedia()
	return index
}

func (a *app) moveQueue(rows []int, to int) {
	if a.queue.moveRows(rows, to) {
		a.queueChanged()
	}
}

func (a *app) removeFromQueue(index int) {
	if index < 0 || index >= a.queue.len() {
		return
	}
	title := a.queue.at(index).Title
	if !a.queue.remove(index) {
		return
	}
	a.notice = "Removed " + title + " from the queue"
	a.queueChanged()
}

func (a *app) selectedQueueIndices() []int {
	var indices []int
	for row := a.queue.rows() - 1; row >= 0; row-- {
		index, ok := a.queue.trackAt(row)
		if !ok || index == a.queue.index {
			continue
		}
		if a.nowPlaying.queueSelection.Has(queueRowKey(&a.queue, row)) {
			indices = append(indices, index)
		}
	}
	return indices
}

func (a *app) removeSelectedFromQueue() {
	indices := a.selectedQueueIndices()
	if len(indices) == 0 {
		return
	}
	for _, index := range indices {
		a.queue.remove(index)
	}
	a.notice = fmt.Sprintf("Removed %d tracks from the queue", len(indices))
	a.queueChanged()
}

func (a *app) shiftInQueue(index, delta int) {
	if a.queue.shift(index, delta) {
		a.queueChanged()
	}
}

func (a *app) playNextInQueue(index int) {
	if index < 0 || index >= a.queue.len() {
		return
	}
	a.enqueue(a.queue.at(index), true)
}

func (a *app) clearUpcoming() {
	a.queueRest.stop()
	if a.queue.clearUpcoming() {
		a.notice = "Cleared the queue"
		a.queueChanged()
	}
}

func (a *app) queueChanged() {
	a.clearQueueSelection()
	a.nowPlaying.queueFollow = ""
	a.warmNext()
	a.syncPreload()
	a.ensureUpNext()
	a.syncSystemMedia()
}

func (a *app) clearQueueSelection() {
	a.nowPlaying.queueSelection.Clear()
	a.nowPlaying.queueSelectionAnchor = -1
	a.nowPlaying.queueSelectionCursor = -1
}

func (a *app) playVideoID(id string) {
	if id == "" {
		return
	}
	if i := a.queue.find(id); i >= 0 {
		a.start(a.queue.moveTo(i))
		return
	}
	for i, item := range a.page.playable {
		if item.VideoID == id {
			a.playFrom(item, a.page.playable, i, youtube.UpNextOptions{}, a.queue.source)
			return
		}
	}
	a.play(youtube.MusicItem{VideoID: id}, nil)
}

const pollInterval = 250 * time.Millisecond

func (a *app) startTicker() {
	failure := ""
	a.every(pollInterval, func() {
		now := a.player.Failure()
		if a.player.Playing() || a.player.Ended() || now != failure {
			failure = now
			a.update(a.poll)
		}
	})
}

func (a *app) poll() {
	if a.aiCheck.busy() || !a.player.Active() {
		return
	}
	if tag, ok := a.player.Advanced(); ok {
		a.followed(tag)
		return
	}
	if failure := a.player.Failure(); failure != "" {
		if a.stream.busy() {
			return
		}
		if a.player.StreamFailed() {
			if a.atTrackEnd() {
				log.Printf("playback: stream broke off at the end of the track video_id=%s at=%s: %s", a.current.VideoID, a.player.Position().Round(time.Second), failure)
				a.trackEnded()
				return
			}
			if a.recoverStream(failure) {
				return
			}
		}
		if a.rawPlayErr != failure {
			a.failPlayback(a.current, "player stopped", failure)
		}
		if a.player.StreamFailed() {
			a.skipUnplayable(a.current)
		}
		return
	}
	if a.player.Ended() {
		if !a.stream.busy() {
			a.trackEnded()
		}
		return
	}
	if a.player.Playing() {
		a.syncEpisodeProgress()
		if a.recoveries > 0 && a.player.Position() > a.recoveredFrom+recoveryHealthy {
			a.recoveries = 0
		}
		a.observeListen()
		a.syncPreload()
		a.ensureUpNext()
		if !a.scrubbing {
			a.scrub = a.player.Position().Seconds()
		}
		a.syncSystemMedia()
	}
}

func (a *app) trackEnded() {
	if a.repeat == repeatTrack {
		a.startNext(a.current)
		return
	}
	a.advance()
}

func (a *app) advance() {
	if next, ok := a.queue.nextEligible(a.settings.AutoPlay, a.repeat, a.settings.AutoSkipDisliked); ok {
		a.startNext(a.queue.moveTo(next))
		return
	}
	if a.settings.AutoSkipDisliked && a.queue.remaining() > 0 &&
		(!a.settings.AutoPlay || a.upNext.fetched && a.upNext.token == "" && !a.upNext.load.busy()) {
		a.upNext.waiting = false
		limit := a.queue.limit(a.settings.AutoPlay)
		if a.queue.index < limit && (a.queue.index+1 < limit || a.repeat == repeatQueue) {
			a.notice = "Playback stopped because every remaining track is disliked"
		}
		a.stopAtQueueEnd()
		return
	}
	if a.settings.AutoPlay {
		a.upNext.waiting = true
		a.finishEpisodeProgress(false)
		a.player.Stop()
		a.syncSystemMedia()
		a.ensureUpNext()
		return
	}
	a.stopAtQueueEnd()
}

func (a *app) stopAtQueueEnd() {
	a.aiCheck.stop()
	a.stream.stop()
	a.finishEpisodeProgress(false)
	a.player.Stop()
	a.syncSystemMedia()
}

func (a *app) previous() {
	if a.queue.index == 0 || a.player.Position() > 3*time.Second {
		a.seek(0)
		return
	}
	a.start(a.queue.moveTo(a.queue.index - 1))
}

func (a *app) seek(position time.Duration) {
	a.scrobble.listen.Break()
	a.seekTarget = position
	if !a.seeking.busy() {
		a.applySeek()
	}
}

func (a *app) applySeek() {
	at := a.seekTarget
	startTask(a, &a.seeking, noTimeout, func(ctx context.Context) (time.Duration, error) {
		a.player.SeekContext(ctx, at)
		return at, nil
	}, func(applied time.Duration, _ error) {
		a.seekVersion++
		if state := a.episodeProgress; state != nil {
			state.lastReported, state.lastPosition, state.lastSample = applied, applied, time.Now()
			state.seekVersion = a.seekVersion
		}
		a.syncSystemMedia()
		if a.seekTarget != applied {
			a.applySeek()
		}
	})
}

func (a *app) togglePlay() {
	if a.starting() {
		return
	}
	if a.playErr != "" || !a.player.Active() {
		if a.current.VideoID != "" {
			a.start(a.current)
		}
		return
	}
	if a.player.Playing() {
		a.pause()
	} else {
		a.resume()
	}
}

func (a *app) resume() {
	if a.starting() || a.player.Playing() {
		return
	}
	if a.playErr != "" || !a.player.Active() || a.player.Ended() {
		if a.current.VideoID != "" {
			a.start(a.current)
		}
	} else {
		a.player.Resume()
		if a.current.IsEpisode() {
			a.beginEpisodeProgress(a.current, a.player.Position(), a.total)
		}
	}
	a.syncSystemMedia()
}

func (a *app) pause() {
	a.player.Pause()
	a.finishEpisodeProgress(false)
	a.syncSystemMedia()
}

func (a *app) setShuffle(on bool) {
	a.clearQueueSelection()
	a.shuffle = on
	if on {
		a.queue.shuffleQueue()
	} else {
		a.queue.restoreQueue()
	}
	a.syncPreload()
	a.syncSystemMedia()
}

func (a *app) setVolume(v float64) {
	a.volume = min(max(v, 0), 100)
	if a.volume > 0 {
		a.muted = a.volume
	}
	a.player.SetVolume(playbackVolume(a.volume, a.loudness, a.loudnessOK && a.settings.NormalizeLoudness, a.settings.LogVolume))
	a.settings.Volume = a.volume
	a.syncSystemMedia()
}

var playbackSpeeds = []float64{0.5, 0.8, 1, 1.2, 1.5, 1.8, 2, 2.5, 3}

func (a *app) speedFor(item youtube.MusicItem) float64 {
	speed := a.settings.MusicSpeed
	if item.IsEpisode() {
		speed = a.settings.PodcastSpeed
	}
	if slices.Contains(playbackSpeeds, speed) {
		return speed
	}
	return 1
}

func (a *app) applyCrossfade() {
	var fade time.Duration
	if a.settings.Crossfade {
		fade = time.Duration(settings.ClampCrossfade(a.settings.CrossfadeSeconds)) * time.Second
	}
	a.player.SetCrossfade(fade)
}

func (a *app) setSpeed(speed float64) {
	if a.current.IsEpisode() {
		a.settings.PodcastSpeed = speed
	} else {
		a.settings.MusicSpeed = speed
	}
	a.saveSettings()
	a.applySpeed()
}

func (a *app) applySpeed() {
	if a.starting() {
		return
	}
	speed := a.speedFor(a.current)
	startTask(a, &a.speeding, noTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, a.player.SetSpeed(ctx, speed)
	}, func(struct{}, error) {
		a.syncSystemMedia()
	})
}

const defaultUnmuteVolume = 40

func (a *app) toggleMute() {
	switch {
	case a.volume > 0:
		a.muted = a.volume
		a.setVolume(0)
	case a.muted > 0:
		a.setVolume(a.muted)
	default:
		a.setVolume(defaultUnmuteVolume)
	}
}

func (a *app) cycleRepeat() {
	a.repeat = a.repeat.next()
	a.syncPreload()
	a.syncSystemMedia()
}

func (a *app) setAudioCacheLimit(limit int) {
	a.settings.CacheSongs = audio.ValidLimit(limit)
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	cache, count := a.currentAudioCache(), a.settings.CacheSongs
	a.run(func() { cache.SetLimit(count) })
	a.saveSettings()
}

func parseDuration(text string) time.Duration {
	parts := strings.Split(strings.TrimSpace(text), ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0
	}
	total := time.Duration(0)
	for _, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0
		}
		total = total*60 + time.Duration(value)*time.Second
	}
	return total
}

func clock(d time.Duration) string {
	total := int(max(d, 0).Seconds())
	hours, minutes, seconds := total/3600, (total%3600)/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
