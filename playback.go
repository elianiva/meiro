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

// playback is the track playing, the queue it plays from, and the work that
// keeps the music going: finding each track's audio and the recommendations
// that follow the queue.
type playback struct {
	current youtube.MusicItem
	queue   queue
	// total is the current track's length, once known.
	total     time.Duration
	scrub     float64
	scrubbing bool
	// seekTarget is the position the user last asked for, which seeking moves
	// the player to off the main thread.
	seekTarget  time.Duration
	seekVersion uint64
	volume      float64
	muted       float64 // the last volume above 0, to return to on unmute
	loudness    float64
	loudnessOK  bool
	loudnessMap *lru.Cache[string, youtube.Loudness]
	shuffle     bool
	repeat      repeatMode
	// playErr is the reason the current track does not play, as shown;
	// rawPlayErr is what was reported, which is also what tells one failure
	// from the next.
	playErr, rawPlayErr string
	// skipped counts the tracks in a row that were passed over because they
	// would not play; a track that plays, or one the listener chooses, ends
	// the run.
	skipped int
	// recoveries counts the times in a row the current track's stream broke
	// off and was found again; the count ends when the track plays on.
	recoveries    int
	recoveredFrom time.Duration

	// stream finds the current track's audio; it is busy from the moment a
	// track is chosen until its audio is found.
	stream task
	// notificationArtwork loads the current cover before showing its track
	// notification on Windows.
	notificationArtwork task
	// loudnessLoad and preloadLoudnessLoad ask for the loudness of the current
	// and the next track; a result lands in loudnessMap whenever it arrives.
	loudnessLoad        task
	preloadLoudnessLoad task
	streams             *audio.URLs
	// seeking moves the player to seekTarget; it is busy while the decode is
	// being restarted there.
	seeking task
	// speeding changes the player's speed to the current track's.
	speeding task
	// preload resolves the next track and has the player decode it ahead of
	// the end of the current one; preloaded is what it left with the player,
	// and preloadFailed the track it could not, which is not tried again
	// until the track changes.
	preload       task
	preloaded     preloadedAudio
	preloadFailed string
	preloadFor    string
	upNext        upNextState
	// opening is the album or playlist whose songs are being read to play
	// it, so that a second press does not read them again.
	opening    task
	openingKey string

	suspendedWhilePlaying bool

	// resumeID is the restored track that has not played yet; the first play
	// of it starts at a.scrub, where the last run left it.
	resumeID string
	// episodes are where the podcast episodes left part-way through stopped,
	// the one left last first. Playing one again starts there.
	episodes []playstate.Episode
	// episodeProgress is the authenticated YouTube watch-time session for the
	// episode playing. It is nil while signed out or between episodes.
	episodeProgress *episodeProgressSync
}

func newPlayback(streams *audio.URLs, volume float64) playback {
	return playback{queue: newQueue(), streams: streams, volume: volume, loudnessMap: lru.New[string, youtube.Loudness](256)}
}

// play starts item, with tracks as the queue.
func (a *app) play(item youtube.MusicItem, tracks []youtube.MusicItem) {
	a.playFrom(item, tracks, -1, youtube.UpNextOptions{}, "")
}

// playFrom starts an item from a queue, and remembers the context used to
// ask YouTube Music for the generated queue that follows it.
func (a *app) playFrom(item youtube.MusicItem, tracks []youtube.MusicItem, index int, options youtube.UpNextOptions, source string) {
	if item.VideoID == "" {
		return
	}
	a.clearQueueSelection()
	a.queue.replace(item, tracks, index, source)
	clear(a.scrobble.queued) // the queue they were added to is gone
	a.learnCredits(tracks...)
	a.learnCredits(item)
	options.VideoID = item.VideoID
	a.resetUpNext(options)
	if a.loading() && a.current.VideoID == item.VideoID {
		a.ensureUpNext()
		return // this track is already on its way
	}
	if a.shuffle {
		a.queue.shuffleQueue()
	}
	a.start(item)
}

// maxSkips is how many unplayable tracks in a row the queue moves past
// before it stops and shows the error, so that a network that is down does
// not use the whole queue up.
const maxSkips = 3

// start makes item the current track and finds its audio.
func (a *app) start(item youtube.MusicItem) {
	a.skipped = 0
	a.startTrack(item, false)
}

// startNext makes item, which the queue chose, the current track. Last.fm is
// told the listener did not pick it, unless they queued it.
func (a *app) startNext(item youtube.MusicItem) {
	a.skipped = 0
	a.startTrack(item, true)
}

// skipUnplayable moves on to the next track after item failed to play, unless
// maxSkips tracks were passed over already or nothing follows. Then the
// track stays where it is, with the reason it failed.
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

// startTrack is start without ending a run of skipped tracks. automatic says
// the queue chose item, rather than the listener.
func (a *app) startTrack(item youtube.MusicItem, automatic bool) {
	startAt, changed := a.enterTrack(item, automatic)
	// The old stream is stopped by the work that finds the new one: stopping
	// kills ffmpeg and waits for the audio device, which is not for the main
	// thread to do.
	a.streamAudio(item, startAt, changed)
	a.afterEnter(item, changed)
}

// enterTrack makes item the current track, and says where in it to start and
// whether it is another track than the one before.
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
	// A position in the track that is ending is not one for the next one, and
	// the decode the seek was moving is about to be replaced.
	a.seeking.stop()
	a.seekTarget = 0
	// The track's audio starts at the track's own speed.
	a.speeding.stop()
	// What was being readied was for the track after the old one.
	a.preload.stop()
	a.preloaded, a.preloadFailed = preloadedAudio{}, ""
	// Entering a track is not playing it: Last.fm hears of it once playback
	// is confirmed, which the polls see.
	a.beginListen(item, automatic)
	return startAt, changed
}

// afterEnter is what a track entered asks for besides its audio.
func (a *app) afterEnter(item youtube.MusicItem, changed bool) {
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

// notifyTrack waits for a fresh Windows cover so the native notification can
// show it instead of the app icon. Other platforms keep their normal
// notification path.
func (a *app) notifyTrack(item youtube.MusicItem) {
	if a.desktop == nil {
		return
	}
	if !notificationArtworkNeeded() || a.thumbs == nil {
		a.notificationArtwork.stop()
		a.desktop.notify(item, nil)
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
			icon = nil // The coloured app icon is the safe fallback.
		}
		a.desktop.notify(item, icon)
	})
}

// loading reports whether the track chosen is not making sound yet: its audio
// is being found, or ffmpeg is still opening it.
func (a *app) loading() bool { return a.stream.busy() || a.player.Buffering() }

// enqueue adds a track to the chosen queue without changing the current
// track. Chosen tracks stay ahead of auto-play recommendations.
func (a *app) enqueue(item youtube.MusicItem, playNext bool) {
	if item.VideoID == "" {
		return
	}
	if a.current.VideoID == "" || a.queue.len() == 0 {
		a.playFrom(item, nil, 0, youtube.UpNextOptions{}, "Queue")
		a.notice = "Playing " + item.Title
		return
	}
	a.queue.insert(item, playNext, a.settings.AutoPlay)
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
}

// moveQueue carries out a drag in the queue's list.
func (a *app) moveQueue(rows []int, to int) {
	if a.queue.moveRows(rows, to) {
		a.queueChanged()
	}
}

// removeFromQueue drops the track at a place in the queue. The track playing
// stays.
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

// selectedQueueIndices are the selected queue tracks, in descending order,
// leaving out the track playing and the recommendations divider.
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

// removeSelectedFromQueue removes every selected track except the one playing.
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

// shiftInQueue moves the track at a place in the queue one up or down.
func (a *app) shiftInQueue(index, delta int) {
	if a.queue.shift(index, delta) {
		a.queueChanged()
	}
}

// clearUpcoming empties the queue after the track playing.
func (a *app) clearUpcoming() {
	if a.queue.clearUpcoming() {
		a.notice = "Cleared the queue"
		a.queueChanged()
	}
}

// queueChanged follows an edit of the queue: what plays next may be another
// track now.
func (a *app) queueChanged() {
	a.clearQueueSelection()
	a.nowPlaying.queueFollow = "" // the list scrolls to the current track again
	a.warmNext()
	a.syncPreload()
	a.ensureUpNext()
	a.syncSystemMedia()
}

// clearQueueSelection forgets the choice before a queue change can reuse a
// row's key for a different track.
func (a *app) clearQueueSelection() {
	a.nowPlaying.queueSelection.Clear()
	a.nowPlaying.queueSelectionAnchor = -1
	a.nowPlaying.queueSelectionCursor = -1
}

// playVideoID starts a video, wherever it comes from: a link, a history
// entry or the queue. Tracks the queue or the page already holds play from
// there.
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

// pollInterval is how often the ticker looks at the player.
const pollInterval = 250 * time.Millisecond

// startTicker wakes the main thread while a track plays, or has just ended
// or failed, so poll can move playback on. The view only describes the
// state the polls leave behind.
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

// poll advances what playback depends on: the player's position, the next
// track when one ends, and the system's media controls. It runs on the main
// thread, from the ticker.
func (a *app) poll() {
	if !a.player.Active() {
		return
	}
	if tag, ok := a.player.Advanced(); ok {
		a.followed(tag)
		return
	}
	if failure := a.player.Failure(); failure != "" {
		if a.stream.busy() {
			return // a track, or this one again, is on its way
		}
		if a.player.StreamFailed() {
			if a.atTrackEnd() {
				// The stream broke off in the last moment of the track, so
				// the track is over rather than broken: finding the stream
				// again would play that moment a second time.
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
		// ffmpeg gave up on the stream, and finding it again did not help.
		// The queue keeps its place: the track after this one plays, and the
		// failure stays on the player until it does. A stream that stops
		// because the sound did is not the queue's business, so it is left
		// where it is.
		if a.player.StreamFailed() {
			a.skipUnplayable(a.current)
		}
		return
	}
	if a.player.Ended() {
		// A new track is already on its way; the old one's end is not news.
		if !a.stream.busy() {
			a.trackEnded()
		}
		return
	}
	if a.player.Playing() {
		a.syncEpisodeProgress()
		if a.recoveries > 0 && a.player.Position() > a.recoveredFrom+recoveryHealthy {
			a.recoveries = 0 // it held, so the next break gets all its tries
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

// trackEnded moves on when a track finishes: it repeats, or plays the next
// one, or stops at the end of the queue.
func (a *app) trackEnded() {
	if a.repeat == repeatTrack {
		a.startNext(a.current)
		return
	}
	a.advance()
}

// advance plays the next track of the queue, waiting for recommendations at
// its end when auto-play is on.
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
		// The queue is over and the music should not be. Wait for what
		// YouTube has to follow it with, which ensureUpNext asks for when the
		// recommendations pulled so far have been played out.
		a.upNext.waiting = true
		a.finishEpisodeProgress(false)
		a.player.Stop()
		a.syncSystemMedia()
		a.ensureUpNext()
		return
	}
	a.stopAtQueueEnd()
}

// stopAtQueueEnd stops playback, and drops a track still being found.
func (a *app) stopAtQueueEnd() {
	a.stream.stop()
	a.finishEpisodeProgress(false)
	a.player.Stop()
	a.syncSystemMedia()
}

// previous restarts the track, or plays the one before it when the track
// has only just started.
func (a *app) previous() {
	if a.queue.index == 0 || a.player.Position() > 3*time.Second {
		a.seek(0)
		return
	}
	a.start(a.queue.moveTo(a.queue.index - 1))
}

// seek moves the track to a position. seekVersion tells the system's media
// controls an explicit change of position from playback moving on.
//
// Moving the player there restarts the decode: it kills ffmpeg and waits for
// the audio device, which is not for the main thread to do. One seek is on its
// way at a time, and a position asked for while one is on its way is the one
// the next, and last, seek applies.
func (a *app) seek(position time.Duration) {
	a.scrobble.listen.Break() // the position jumps; what it jumps over was not heard
	a.seekTarget = position
	if !a.seeking.busy() {
		a.applySeek()
	}
}

// applySeek moves the player to the position asked for, then to a newer one
// that landed while this was on its way.
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

// togglePlay pauses a playing track, resumes a paused one, and starts the
// current track again after it failed or ended.
func (a *app) togglePlay() {
	if a.stream.busy() {
		return // the track is already on its way
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

// resume plays a paused track, or starts the current one again when it has
// ended or failed. It does nothing while a track plays or is on its way.
func (a *app) resume() {
	if a.stream.busy() || a.player.Playing() {
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

// pause holds the track where it is.
func (a *app) pause() {
	a.player.Pause()
	a.finishEpisodeProgress(false)
	a.syncSystemMedia()
}

// setShuffle turns shuffle on or off. Turning it on arranges the whole queue
// behind the track playing, which moves to the top, and the queue then plays
// in that order to its end, where auto-play takes over. Turning it off puts
// the queue back in the order it held, with the track playing where it was.
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

// setVolume sets the volume, from 0 to 100, and keeps it for the next run.
func (a *app) setVolume(v float64) {
	a.volume = min(max(v, 0), 100)
	if a.volume > 0 {
		a.muted = a.volume
	}
	a.player.SetVolume(playbackVolume(a.volume, a.loudness, a.loudnessOK && a.settings.NormalizeLoudness, a.settings.LogVolume))
	a.settings.Volume = a.volume
	a.syncSystemMedia()
}

// playbackSpeeds are the speeds a track can play at.
var playbackSpeeds = []float64{0.5, 0.8, 1, 1.2, 1.5, 1.8, 2, 2.5, 3}

// speedFor is how fast item plays: an episode at the speed chosen for
// podcasts, anything else at the one chosen for music.
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

// applyCrossfade sends the crossfade length to the player, zero when it is off.
func (a *app) applyCrossfade() {
	var fade time.Duration
	if a.settings.Crossfade {
		fade = time.Duration(settings.ClampCrossfade(a.settings.CrossfadeSeconds)) * time.Second
	}
	a.player.SetCrossfade(fade)
}

// setSpeed chooses how fast tracks like the current one play, podcast
// episodes or music, keeps the choice, and plays the current track at it.
func (a *app) setSpeed(speed float64) {
	if a.current.IsEpisode() {
		a.settings.PodcastSpeed = speed
	} else {
		a.settings.MusicSpeed = speed
	}
	a.saveSettings()
	a.applySpeed()
}

// applySpeed plays the current track at its speed, if it does not already.
// Changing the speed restarts the decode, which is not for the main thread to
// do. While the track's audio is on its way this is left to the work that
// finds it, which applies the speed once it plays.
func (a *app) applySpeed() {
	if a.stream.busy() {
		return
	}
	speed := a.speedFor(a.current)
	startTask(a, &a.speeding, noTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, a.player.SetSpeed(ctx, speed)
	}, func(struct{}, error) {
		a.syncSystemMedia()
	})
}

// toggleMute silences the player, or brings back the last volume above 0. With
// no such volume in this run, it unmutes to 40.
func (a *app) toggleMute() {
	switch {
	case a.volume > 0:
		a.muted = a.volume // the volume from settings at startup skips setVolume
		a.setVolume(0)
	case a.muted > 0:
		a.setVolume(a.muted)
	default:
		a.setVolume(40)
	}
}

// cycleRepeat goes from not repeating to repeating the queue to repeating the
// track and back.
func (a *app) cycleRepeat() {
	a.repeat = a.repeat.next()
	a.syncPreload()
	a.syncSystemMedia()
}

// setAudioCacheLimit changes the number of recently played audio files kept
// locally, evicting old files at once when the limit goes down.
func (a *app) setAudioCacheLimit(limit int) {
	a.settings.CacheSongs = audio.ValidLimit(limit)
	a.cacheSongsText = strconv.Itoa(a.settings.CacheSongs)
	cache, count := a.currentAudioCache(), a.settings.CacheSongs
	a.run(func() { cache.SetLimit(count) })
	a.saveSettings()
}

// parseDuration reads a "3:42" or "1:02:03" track length.
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

// clock formats a position as "3:42" or "1:02:03".
func clock(d time.Duration) string {
	total := int(max(d, 0).Seconds())
	hours, minutes, seconds := total/3600, (total%3600)/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}
