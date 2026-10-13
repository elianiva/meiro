package main

import (
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/audio"
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
	muted       float64 // the volume to return to, while muted
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
	stream  task
	streams *audio.URLs
	// seeking moves the player to seekTarget; it is busy while the decode is
	// being restarted there.
	seeking task
	upNext  upNextState
	// opening is the album or playlist whose songs are being read to play
	// it, so that a second press does not read them again.
	opening    task
	openingKey string

	suspendedWhilePlaying bool

	// resumeID is the restored track that has not played yet; the first play
	// of it starts at a.scrub, where the last run left it.
	resumeID string
}

func newPlayback(streams *audio.URLs, volume float64) playback {
	return playback{queue: newQueue(), streams: streams, volume: volume}
}

// upNextState is the queue YouTube Music generates after the chosen tracks.
type upNextState struct {
	load    task
	options youtube.UpNextOptions
	// fetched says the first page was asked for; token continues it, and
	// seen holds the tokens already used, so a loop of pages ends.
	fetched bool
	token   string
	seen    map[string]struct{}
	err     string
	// waiting says playback reached the end of the queue and waits for what
	// YouTube Music has to follow it.
	waiting bool
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
	a.queue.replace(item, tracks, index, source)
	options.VideoID = item.VideoID
	a.resetUpNext(options)
	if a.loading() && a.current.VideoID == item.VideoID {
		a.ensureUpNext()
		return // this track is already on its way
	}
	if a.shuffle {
		a.queue.shuffleUpcoming()
	}
	a.start(item)
}

// resetUpNext forgets the recommendations of the queue being replaced, and
// drops any request for them still on its way.
func (a *app) resetUpNext(options youtube.UpNextOptions) {
	u := &a.upNext
	u.load.stop()
	u.options, u.fetched, u.token, u.err, u.waiting = options, false, "", "", false
	u.seen = map[string]struct{}{}
}

// playAll plays everything on the page shown.
func (a *app) playAll() {
	if len(a.page.playable) == 0 {
		return
	}
	options, source := a.queueContext(0)
	a.playFrom(a.page.playable[0], a.page.playable, 0, options, source)
}

// shufflePage turns shuffle on and plays the page from a track drawn at
// random, with the whole page arranged behind it.
func (a *app) shufflePage() {
	if len(a.page.playable) == 0 {
		return
	}
	a.shuffle = true
	// The position the playlist is read from goes with the draw, so the queue
	// YouTube is asked for follows the same track.
	start, tracks := drawFirst(a.page.playable)
	options, source := a.queueContext(start)
	a.playFrom(tracks[0], tracks, 0, options, source)
}

// drawFirst draws a track at random to open a queue on, with the rest
// shuffled behind it, and says where the drawn track was.
func drawFirst(tracks []youtube.MusicItem) (int, []youtube.MusicItem) {
	start := rand.IntN(len(tracks))
	out := make([]youtube.MusicItem, 0, len(tracks))
	out = append(out, tracks[start])
	out = append(out, tracks[:start]...)
	out = append(out, tracks[start+1:]...)
	rest := out[1:]
	rand.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return start, out
}

// queueContext is what playing from the page shown tells YouTube Music about
// the queue, and the name of the page, for the queue's heading. A playlist
// page adds where in the playlist the queue starts.
func (a *app) queueContext(index int) (youtube.UpNextOptions, string) {
	options := youtube.UpNextOptions{}
	r, id := a.route()
	if r.target == youtube.TargetPlaylist {
		playlistIndex := index
		options.PlaylistID, options.PlaylistIndex = id, &playlistIndex
	}
	source := a.page.detail.title
	if source == "" {
		source = r.source
	}
	return options, source
}

// maxSkips is how many unplayable tracks in a row the queue moves past
// before it stops and shows the error, so that a network that is down does
// not use the whole queue up.
const maxSkips = 3

// start makes item the current track and finds its audio.
func (a *app) start(item youtube.MusicItem) {
	a.skipped = 0
	a.startTrack(item)
}

// skipUnplayable moves on to the next track after item failed to play, unless
// maxSkips tracks were passed over already or nothing follows. Then the
// track stays where it is, with the reason it failed.
func (a *app) skipUnplayable(item youtube.MusicItem) {
	if a.current.VideoID != item.VideoID || a.skipped >= maxSkips {
		return
	}
	next, ok := a.queue.next(a.settings.AutoPlay, a.repeat)
	if !ok {
		return
	}
	a.skipped++
	a.notice = "Skipped " + item.Title + ": " + strings.TrimSuffix(a.playErr, ".")
	a.startTrack(a.queue.moveTo(next))
}

// startTrack is start without ending a run of skipped tracks.
func (a *app) startTrack(item youtube.MusicItem) {
	a.recoveries = 0
	changed := a.current.VideoID != item.VideoID
	a.current = item
	a.total = parseDuration(item.Duration)
	startAt := time.Duration(0)
	if a.resumeID == item.VideoID {
		startAt = time.Duration(a.scrub * float64(time.Second))
	}
	a.resumeID = ""
	a.scrub, a.playErr, a.rawPlayErr = startAt.Seconds(), "", ""
	// A position in the track that is ending is not one for the next one, and
	// the decode the seek was moving is about to be replaced.
	a.seeking.stop()
	a.seekTarget = 0
	// The old stream is stopped by the work that finds the new one: stopping
	// kills ffmpeg and waits for the audio device, which is not for the main
	// thread to do.
	a.streamAudio(item, startAt)
	a.ensureUpNext()
	a.loadPanel()
	if changed {
		a.recordHistory(item)
		if a.settings.NotifyTrack {
			a.desktop.notify(item)
		}
	}
	a.syncSystemMedia()
}

// loading reports whether the track chosen is not making sound yet: its audio
// is being found, or ffmpeg is still opening it.
func (a *app) loading() bool { return a.stream.busy() || a.player.Buffering() }

// resolvedAudio is where a track's audio is, and how long it plays.
type resolvedAudio struct {
	url       string
	total     time.Duration
	fromCache bool
	// playErr is why the player could not open the audio, once found.
	playErr error
}

// streamAudio finds a track's audio off the main thread, in the cache or
// with yt-dlp, and plays it.
func (a *app) streamAudio(item youtube.MusicItem, startAt time.Duration) {
	cookie, cache := a.account.cookie(), a.currentAudioCache()
	log.Printf("playback: resolving video_id=%s signed_in=%t", item.VideoID, cookie != "")
	startTask(a, &a.stream, audio.ResolveTimeout, func(ctx context.Context) (resolvedAudio, error) {
		a.player.Stop()
		found, err := a.findAudio(ctx, item, cookie, cache)
		if err != nil || ctx.Err() != nil {
			return found, err
		}
		// Opening the audio device and starting ffmpeg take long enough to
		// be felt, so they happen here and not on the main thread.
		found.playErr = a.player.PlayAt(found.url, startAt, false)
		return found, nil
	}, func(found resolvedAudio, err error) {
		if err != nil {
			a.failPlayback(item, "audio resolution failed", err.Error())
			a.skipUnplayable(item)
			return
		}
		if found.total > 0 {
			a.total = found.total
		}
		if found.playErr != nil {
			a.failPlayback(item, "player could not open", found.playErr.Error())
			a.skipUnplayable(item)
			return
		}
		log.Printf("playback: audio started video_id=%s from_cache=%t duration=%s", item.VideoID, found.fromCache, found.total)
		a.skipped = 0
		if !found.fromCache {
			// The direct URL is already in hand, so the cache downloads it as
			// it is instead of resolving the same track again.
			cache.Add(item.VideoID, found.url)
		}
		a.warmNext()
		a.syncSystemMedia()
	})
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
)

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
	cookie, cache := a.account.cookie(), a.currentAudioCache()
	log.Printf("playback: stream broke off video_id=%s at=%s attempt=%d/%d: %s", item.VideoID, at.Round(time.Second), attempt, maxRecoveries, failure)
	startTask(a, &a.stream, audio.ResolveTimeout+time.Duration(attempt)*recoveryBackoff, func(ctx context.Context) (resolvedAudio, error) {
		if err := a.sleep(ctx, time.Duration(attempt)*recoveryBackoff); err != nil {
			return resolvedAudio{}, err
		}
		a.streams.Forget(item.VideoID)
		found, err := a.findAudio(ctx, item, cookie, cache)
		if err != nil || ctx.Err() != nil {
			return found, err
		}
		found.playErr = a.player.PlayAt(found.url, at, paused)
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
		if paused && bySleep && !a.suspendedWhilePlaying {
			a.player.Resume() // the wake came while the stream was on its way
		}
		a.syncSystemMedia()
	})
	return true
}

// findAudio is where a track's audio is: in the cache, or from yt-dlp.
func (a *app) findAudio(ctx context.Context, item youtube.MusicItem, cookie string, cache *audio.Cache) (resolvedAudio, error) {
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
	url, total, err := a.streams.Get(ctx, item.VideoID, cookie)
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
	cache, cookie := a.currentAudioCache(), a.account.cookie()
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), audio.ResolveTimeout)
		defer cancel()
		url, _, err := a.streams.Get(ctx, next.VideoID, cookie)
		if err != nil {
			log.Printf("playback: could not resolve next video_id=%s: %v", next.VideoID, err)
			return
		}
		if cache.Warmable() {
			cache.Add(next.VideoID, url)
		}
	})
}

// upNextTimeout bounds one request for recommendations.
const upNextTimeout = 45 * time.Second

// ensureUpNext asks for the generated queue at once, then for another page
// only when the generated part is almost played out.
func (a *app) ensureUpNext() {
	u := &a.upNext
	if !a.settings.AutoPlay || a.current.VideoID == "" || u.load.busy() || a.queue.len() == 0 {
		return
	}
	client := a.client()
	continuation := ""
	if u.fetched {
		switch {
		case u.token == "":
			// Everything the recommendations held has been played out, so
			// there is nothing to continue. At the end of the queue that is
			// where the music stopped: ask again from the track that is
			// playing, which is what keeps the queue going.
			if a.queue.remaining() > 0 {
				return
			}
			u.seen = map[string]struct{}{}
			u.options.VideoID = a.current.VideoID
		case a.queue.remaining() > 1:
			return
		default:
			continuation = u.token
			if u.seen == nil {
				u.seen = map[string]struct{}{}
			}
			if _, seen := u.seen[continuation]; seen {
				u.token = ""
				return
			}
			u.seen[continuation] = struct{}{}
		}
	}
	u.fetched = true
	options := u.options
	startTask(a, &u.load, upNextTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		if continuation == "" {
			return client.GetUpNext(ctx, options)
		}
		return client.ContinueUpNext(ctx, options, continuation)
	}, func(result *youtube.BrowseResult, err error) {
		if err != nil {
			u.err, u.token = err.Error(), ""
			if u.waiting {
				u.waiting = false
				a.stopAtQueueEnd()
			}
			return
		}
		u.err = ""
		if result.QueuePlaylistID != "" {
			if u.options.PlaylistID != "" && u.options.PlaylistID != result.QueuePlaylistID {
				u.options.PlaylistIndex = nil
			}
			u.options.PlaylistID = result.QueuePlaylistID
		}
		u.token = result.ContinuationToken
		if u.token == continuation {
			u.token = ""
		}
		a.queue.appendGenerated(result.Items)
		// A recommended track can be the one that plays next, so warm it too.
		a.warmNext()
		if !u.waiting {
			return
		}
		switch {
		case a.queue.remaining() > 0:
			u.waiting = false
			a.advance()
		case continuation != "":
			a.ensureUpNext() // the list had more to pull
		default:
			// A fresh request found nothing to follow the queue with.
			u.waiting = false
			a.stopAtQueueEnd()
		}
	})
}

// setAutoPlay changes whether generated tracks can follow the chosen queue.
func (a *app) setAutoPlay(enabled bool) {
	if a.settings.AutoPlay == enabled {
		return
	}
	a.settings.AutoPlay = enabled
	a.saveSettings()
	a.upNext.load.stop()
	a.upNext.err, a.upNext.waiting = "", false
	if enabled {
		a.upNext.fetched, a.upNext.token = false, ""
		a.upNext.seen = map[string]struct{}{}
		a.upNext.options.VideoID = a.current.VideoID
		a.queue.regenerate()
		a.ensureUpNext()
	}
	a.syncSystemMedia()
}

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
	if playNext {
		a.notice = item.Title + " will play next"
	} else {
		a.notice = "Added " + item.Title + " to the queue"
	}
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
	a.nowPlaying.queueFollow = "" // the list scrolls to the current track again
	a.warmNext()
	a.ensureUpNext()
	a.syncSystemMedia()
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
	if failure := a.player.Failure(); failure != "" {
		if a.stream.busy() {
			return // a track, or this one again, is on its way
		}
		if a.player.StreamFailed() && a.recoverStream(failure) {
			return
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
		if a.recoveries > 0 && a.player.Position() > a.recoveredFrom+recoveryHealthy {
			a.recoveries = 0 // it held, so the next break gets all its tries
		}
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
		a.start(a.current)
		return
	}
	a.advance()
}

// advance plays the next track of the queue, waiting for recommendations at
// its end when auto-play is on.
func (a *app) advance() {
	if next, ok := a.queue.next(a.settings.AutoPlay, a.repeat); ok {
		a.start(a.queue.moveTo(next))
		return
	}
	if a.settings.AutoPlay {
		// The queue is over and the music should not be. Wait for what
		// YouTube has to follow it with, which ensureUpNext asks for when the
		// recommendations pulled so far have been played out.
		a.upNext.waiting = true
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
	a.seekTarget = position
	if !a.seeking.busy() {
		a.applySeek()
	}
}

// applySeek moves the player to the position asked for, then to a newer one
// that landed while this was on its way.
func (a *app) applySeek() {
	at := a.seekTarget
	startTask(a, &a.seeking, noTimeout, func(context.Context) (time.Duration, error) {
		a.player.Seek(at)
		return at, nil
	}, func(applied time.Duration, _ error) {
		a.seekVersion++
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
	a.player.Toggle()
	a.syncSystemMedia()
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
	}
	a.syncSystemMedia()
}

// pause holds the track where it is.
func (a *app) pause() {
	a.player.Pause()
	a.syncSystemMedia()
}

// setShuffle turns shuffle on or off. Turning it on arranges what is still to
// play, and the queue then plays in that order to its end, where auto-play
// takes over. Turning it off leaves the queue in the order it reached.
func (a *app) setShuffle(on bool) {
	a.shuffle = on
	if on {
		a.queue.shuffleUpcoming()
	}
	a.syncSystemMedia()
}

// setVolume sets the volume, from 0 to 100, and keeps it for the next run.
func (a *app) setVolume(v float64) {
	a.volume = min(max(v, 0), 100)
	a.player.SetVolume(a.volume / 100)
	a.settings.Volume = a.volume
	a.syncSystemMedia()
}

// toggleMute silences the player, or brings the volume back, never quieter
// than 40.
func (a *app) toggleMute() {
	if a.volume > 0 {
		a.muted = a.volume
		a.setVolume(0)
		return
	}
	a.setVolume(max(a.muted, 40))
}

// cycleRepeat goes from not repeating to repeating the queue to repeating the
// track and back.
func (a *app) cycleRepeat() {
	a.repeat = a.repeat.next()
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

// openTimeout bounds reading an album or a playlist to play it.
const openTimeout = time.Minute

// playCollection plays an album or a playlist without opening its page, as
// the play button over its card does. With shuffle, a song is drawn to open
// on and the rest are arranged behind it. key names the card pressed.
func (a *app) playCollection(item youtube.MusicItem, key string, shuffle bool) {
	if shuffle {
		a.shuffle = true
	}
	target := item.Target()
	if target.Kind == youtube.TargetTrack {
		a.play(item, []youtube.MusicItem{item})
		return
	}
	if target.Kind != youtube.TargetAlbum && target.Kind != youtube.TargetPlaylist && target.Kind != youtube.TargetArtist {
		return
	}
	if a.isOpening(key) {
		return
	}
	a.openingKey = key
	client := a.client()
	startTask(a, &a.opening, openTimeout, func(ctx context.Context) (*youtube.BrowseResult, error) {
		switch target.Kind {
		case youtube.TargetAlbum:
			return client.GetAlbum(ctx, target.ID)
		case youtube.TargetArtist:
			return client.GetArtist(ctx, target.ID)
		case youtube.TargetPlaylist, youtube.TargetTrack, youtube.TargetNone:
			// The guard above leaves the playlist of the kinds that are not
			// its own.
		}
		// The first page of a playlist holds the playlist itself. Draining
		// it reads on into the shelf of suggestions beside it, which never
		// ends, and long outlasts the read: the page reads it the same way.
		return client.GetPlaylist(ctx, target.ID)
	}, func(result *youtube.BrowseResult, err error) {
		a.openingKey = ""
		if err != nil {
			a.notice = "Could not open “" + item.Title + "”: " + firstLine(err.Error())
			return
		}
		songs := tracksOf(result, item.Thumbnail)
		if len(songs) == 0 {
			return
		}
		// What the playlist is read from follows the song drawn to open it,
		// so the queue YouTube is asked for keeps the same place.
		start := 0
		if shuffle && len(songs) > 1 {
			start, songs = drawFirst(songs)
		}
		options := youtube.UpNextOptions{}
		if target.Kind == youtube.TargetPlaylist {
			playlistIndex := start
			options.PlaylistID, options.PlaylistIndex = target.ID, &playlistIndex
		}
		a.playFrom(songs[0], songs, 0, options, item.Title)
	})
}

// isOpening reports whether the card under key is being read to play.
func (a *app) isOpening(key string) bool { return a.opening.busy() && a.openingKey == key }

// tracksOf picks the tracks out of a page, in order, giving those without
// artwork of their own the page's.
func tracksOf(result *youtube.BrowseResult, art string) []youtube.MusicItem {
	all := result.Items
	if len(result.Sections) > 0 {
		all = nil
		for _, section := range result.Sections {
			all = append(all, section.Items...)
		}
	}
	var songs []youtube.MusicItem
	for _, song := range all {
		if !song.IsTrack() {
			continue
		}
		if song.Thumbnail == "" {
			song.Thumbnail = art
		}
		songs = append(songs, song)
	}
	return songs
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
		if err != nil {
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
