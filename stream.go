package main

import (
	"context"
	"log"
	"time"

	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/youtube"
)

// resolvedAudio is where a track's audio is, and how long it plays.
type resolvedAudio struct {
	url       string
	total     time.Duration
	fromCache bool
	// playErr is why the player could not open the audio, once found.
	playErr error
	// loudness is the track's loudness when it was looked up on the way.
	loudness   youtube.Loudness
	loudnessOK bool
}

// streamAudio finds a track's audio off the main thread, in the cache or
// with yt-dlp, and plays it.
func (a *app) streamAudio(item youtube.MusicItem, startAt time.Duration, changed bool) {
	cache := a.currentAudioCache()
	accountProgress := a.signedIn()
	// The work below runs off the main thread, so it takes what it needs of
	// the app's state now.
	loudnessClient := a.loudnessClient(item.VideoID)
	known, knownOK := a.loudnessMap.Get(item.VideoID)
	volume, normalize := a.volume, a.settings.NormalizeLoudness
	speed := a.speedFor(item)
	streams := a.streams
	log.Printf("playback: resolving video_id=%s", item.VideoID)
	startTask(a, &a.stream, audio.ResolveTimeout, func(ctx context.Context) (resolvedAudio, error) {
		a.player.Stop()
		if err := a.player.SetSpeed(ctx, speed); err != nil {
			return resolvedAudio{}, err
		}
		// The gain is in place before the device starts, so a track that is
		// louder or quieter than the last one does not begin at the wrong
		// volume for a moment. A lookup that fails, or that runs past
		// loudnessWait, plays the track unnormalized rather than not at all.
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
		a.player.SetVolume(playbackVolume(volume, known.LoudnessDB, knownOK && normalize))
		found, err := findAudio(ctx, streams, item, cache)
		found.loudness, found.loudnessOK = fresh, freshOK
		if err != nil || ctx.Err() != nil {
			return found, err
		}
		if accountProgress && item.IsEpisode() {
			startAt = max(startAt, episodeProgressPosition(item, found.total))
		}
		// Opening the audio device and starting ffmpeg take long enough to
		// be felt, so they happen here and not on the main thread.
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
		}
		if found.playErr != nil {
			a.failPlayback(item, "player could not open", found.playErr.Error())
			a.skipUnplayable(item)
			return
		}
		log.Printf("playback: audio started video_id=%s from_cache=%t duration=%s", item.VideoID, found.fromCache, found.total)
		a.applySpeed() // a speed chosen while the audio was on its way
		a.audioStarted(item, found, a.player.Position(), changed, cache)
		a.syncSystemMedia()
	})
}

// audioStarted takes note of a track whose audio has begun to play from
// position: the run of skips ends, the play is recorded and reported, its
// audio is cached for the next time, and the track after it is readied.
// cache is the audio cache the track was looked for in.
func (a *app) audioStarted(item youtube.MusicItem, found resolvedAudio, position time.Duration, changed bool, cache *audio.Cache) {
	a.skipped = 0
	if changed {
		a.recordHistory(item)
	}
	if item.IsEpisode() {
		a.beginEpisodeProgress(item, position, found.total)
	} else if changed {
		a.reportPlayback(item)
	}
	if !found.fromCache {
		// The direct URL is already in hand, so the cache downloads it as it
		// is instead of resolving the same track again.
		cache.Add(item.VideoID, found.url)
	}
	a.warmNext()
}

// preloadLead is how long before the end of a track the next one is opened:
// long enough for ffmpeg to have it decoding when the sound runs out, short
// enough not to hold a connection to the server open through the whole song.
const preloadLead = 20 * time.Second

// preloadedAudio is the audio of the track the player holds ready.
type preloadedAudio struct {
	id string
	resolvedAudio
}

// following is the place in the queue of the track that plays after this
// one, and the track. A repeated track follows itself, at place -1.
func (a *app) following() (int, youtube.MusicItem, bool) {
	if a.repeat == repeatTrack {
		return -1, a.current, a.current.VideoID != ""
	}
	i, ok := a.queue.next(a.settings.AutoPlay, a.repeat)
	if !ok {
		return 0, youtube.MusicItem{}, false
	}
	return i, a.queue.at(i), true
}

// syncPreload keeps the player's preloaded stream the one for the track that
// follows, and starts it once the current track is near its end. It runs on
// every poll and after anything that can change what follows.
func (a *app) syncPreload() {
	_, next, ok := a.following()
	if held := a.player.Preloaded(); held != "" && (!ok || held != next.VideoID) {
		a.player.CancelPreload()
		a.preloaded = preloadedAudio{}
	}
	if !ok || a.preload.busy() && a.preloadFor != next.VideoID {
		a.preload.stop()
	}
	// A track that plays at another speed starts on its own: the player
	// follows on at the speed of the track before.
	if !ok || a.stream.busy() || a.preload.busy() || a.preloadFailed == next.VideoID || a.speedFor(next) != a.speedFor(a.current) ||
		a.player.Preloaded() == next.VideoID || a.total <= 0 || !a.player.Playing() ||
		a.total-a.player.Position() > preloadLead {
		return
	}
	cache := a.currentAudioCache()
	a.preloadFor = next.VideoID
	a.fetchLoudness(next, &a.preloadLoudnessLoad)
	startTask(a, &a.preload, audio.ResolveTimeout, func(ctx context.Context) (preloadedAudio, error) {
		found, err := findAudio(ctx, a.streams, next, cache)
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

// followed moves on to the track the player took over with, as the track
// before it ended. The queue may have changed since the player took it:
// then what it plays is not what follows, and the right track is started.
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
	a.audioStarted(item, found.resolvedAudio, startAt, changed, a.currentAudioCache())
	a.afterEnter(item, changed)
	a.syncSystemMedia()
}

// failPlayback records why item did not play: the raw error goes to the log
// and the listener sees a short reason.
func (a *app) failPlayback(item youtube.MusicItem, what, raw string) {
	log.Printf("playback: %s video_id=%s: %s", what, item.VideoID, raw)
	a.rawPlayErr, a.playErr = raw, playErrorMessage(raw)
	a.syncSystemMedia()
}

const (
	// maxRecoveries is how many times in a row a stream that broke off is
	// found again before the track is given up on.
	maxRecoveries = 3
	// recoveryHealthy is how far a recovered stream plays before it counts as
	// working again.
	recoveryHealthy = 10 * time.Second
	// recoveryBackoff is the wait before the first retry; each one waits
	// longer, which gives a network that just came back a moment to settle.
	recoveryBackoff = 500 * time.Millisecond
	// endOfTrackLead is how much of a track may be left when its stream
	// breaks off and the track still counts as over. YouTube's server stops
	// at the end of a track's data, which breaks the stream off in the last
	// moment of it: what is left is shorter than the wait before the stream
	// is found again, and playing that moment again is what the listener
	// hears as the ending twice.
	endOfTrackLead = 3 * time.Second
)

// atTrackEnd reports whether the track playing has reached the last moment of
// its length, where a stream that breaks off is over rather than broken. A
// paused stream is not at its end: it holds where the listener left it.
func (a *app) atTrackEnd() bool {
	if a.total <= 0 || a.player.Paused() {
		return false
	}
	return a.total-a.player.Position() <= endOfTrackLead
}

// recoverStream picks the current track up where its stream broke off, as it
// does when the network drops mid-song, or the machine wakes with an audio
// URL that has expired: it resolves the track again, which the URL cache
// must not answer from memory, and plays from the position reached. It
// reports false when the tries are used up, and a failure that ends one try
// starts the next from the apply.
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
	log.Printf("playback: stream broke off video_id=%s at=%s attempt=%d/%d: %s", item.VideoID, at.Round(time.Second), attempt, maxRecoveries, failure)
	a.fetchLoudness(item, &a.loudnessLoad)
	startTask(a, &a.stream, audio.ResolveTimeout+time.Duration(attempt)*recoveryBackoff, func(ctx context.Context) (resolvedAudio, error) {
		if err := a.sleep(ctx, time.Duration(attempt)*recoveryBackoff); err != nil {
			return resolvedAudio{}, err
		}
		a.streams.Forget(item.VideoID)
		found, err := findAudio(ctx, a.streams, item, cache)
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
			a.player.Resume() // the wake came while the stream was on its way
		}
		a.syncSystemMedia()
	})
	return true
}

// findAudio is where a track's audio is: in the cache, or from yt-dlp.
func findAudio(ctx context.Context, streams *audio.URLs, item youtube.MusicItem, cache *audio.Cache) (resolvedAudio, error) {
	found := resolvedAudio{total: parseDuration(item.Duration)}
	if path, ok := cache.Get(item.VideoID); ok {
		log.Printf("playback: audio cache hit video_id=%s", item.VideoID)
		found.url, found.fromCache = path, true
		if found.total <= 0 {
			// A track cached ahead of time was never resolved, so its
			// length is written nowhere but the file itself.
			probe, cancel := context.WithTimeout(ctx, 15*time.Second)
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

// warmNext resolves the next track's audio URL while the current one plays,
// so a play that follows skips yt-dlp. That costs a few kilobytes. When the
// cache is big enough for a warmed track to survive until it plays, the
// track is also downloaded from that URL.
func (a *app) warmNext() {
	if a.queue.remaining() < 1 {
		return
	}
	next := a.queue.at(a.queue.index + 1)
	if next.VideoID == "" || next.VideoID == a.current.VideoID {
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
