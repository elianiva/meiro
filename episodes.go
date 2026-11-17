package main

import (
	"context"
	"log"
	"slices"
	"time"

	"github.com/elianiva/meiro/internal/playstate"
	"github.com/elianiva/meiro/youtube"
)

type episodeProgressSync struct {
	item         youtube.MusicItem
	client       *youtube.Client
	session      *youtube.PlaybackSession
	duration     time.Duration
	lastReported time.Duration
	lastPosition time.Duration
	lastSample   time.Time
	pendingTo    time.Duration
	seekVersion  uint64
	pending      bool
	finished     bool
	finalSent    bool
	finishAt     time.Duration
}

const episodeProgressInterval = 20 * time.Second

const episodeSeekTolerance = 2 * time.Second

const episodeProgressTimeout = 15 * time.Second

func (a *app) reportPlayback(item youtube.MusicItem) {
	if !a.signedIn() {
		return
	}
	client := a.client()
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), upNextTimeout)
		defer cancel()
		if err := client.ReportPlayback(ctx, item.VideoID); err != nil {
			log.Printf("playback: could not report video_id=%s: %v", item.VideoID, err)
		}
	})
}

func episodeProgressPosition(item youtube.MusicItem, duration time.Duration) time.Duration {
	if !item.IsEpisode() || item.ProgressPercent <= 0 || item.ProgressPercent >= 100 || duration <= 0 {
		return 0
	}
	position := time.Duration(int64(duration) * int64(item.ProgressPercent) / 100)
	if position < episodeStartGuard || duration-position < episodeEndGuard {
		return 0
	}
	return position
}

func (a *app) beginEpisodeProgress(item youtube.MusicItem, position, duration time.Duration) {
	if !a.signedIn() || !item.IsEpisode() {
		return
	}
	if a.episodeProgress != nil {
		a.finishEpisodeProgress(false)
	}
	state := &episodeProgressSync{
		item: item, client: a.client(), duration: duration, lastReported: position,
		lastPosition: position, lastSample: time.Now(), seekVersion: a.seekVersion,
		pending: true,
	}
	a.episodeProgress = state
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), episodeProgressTimeout)
		defer cancel()
		session, err := state.client.BeginEpisodePlayback(ctx, item.VideoID, position, duration)
		a.update(func() {
			state.pending = false
			if err != nil {
				log.Printf("playback: could not start episode progress video_id=%s: %v", item.VideoID, err)
				if a.episodeProgress == state {
					a.episodeProgress = nil
				}
				return
			}
			state.session = session
			if state.finished {
				a.sendEpisodeProgressFinal(state)
				return
			}
			if a.episodeProgress != state {
				return
			}
		})
	})
}

func (a *app) syncEpisodeProgress() {
	state := a.episodeProgress
	if state == nil || state.finished || state.session == nil || state.pending || !a.player.Playing() {
		return
	}
	now, position := time.Now(), a.player.Position()
	if state.seekVersion != a.seekVersion {
		state.seekVersion = a.seekVersion
		state.lastReported, state.lastPosition, state.lastSample = position, position, now
		return
	}
	elapsed := now.Sub(state.lastSample)
	advanced := position - state.lastPosition
	state.lastPosition, state.lastSample = position, now
	if advanced < 0 || advanced > elapsed+episodeSeekTolerance {
		state.lastReported = position
		return
	}
	if position-state.lastReported < episodeProgressInterval {
		return
	}
	from, to := state.lastReported, position
	state.pending = true
	state.pendingTo = to
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), episodeProgressTimeout)
		defer cancel()
		err := state.session.ReportWatchtime(ctx, from, to, state.duration, "playing", false)
		a.update(func() {
			state.pending = false
			state.pendingTo = 0
			if err != nil {
				log.Printf("playback: could not report episode progress video_id=%s: %v", state.item.VideoID, err)
			} else {
				state.lastReported = to
			}
			if state.finished {
				a.sendEpisodeProgressFinal(state)
			}
		})
	})
}

func (a *app) finishEpisodeProgress(wait bool) {
	state := a.episodeProgress
	if state == nil {
		return
	}
	state.finished = true
	state.finishAt = max(a.playbackPosition(), state.lastReported)
	a.episodeProgress = nil
	if state.session == nil || state.pending {
		if !wait {
			return
		}
	}
	if state.session == nil || state.finalSent {
		return
	}
	from, to := state.lastReported, state.finishAt
	if state.pending && state.session != nil {
		from = max(from, state.pendingTo)
	}
	if to < from {
		to = from
	}
	if wait {
		state.finalSent = true
		result := make(chan error, 1)
		a.run(func() {
			ctx, cancel := context.WithTimeout(context.Background(), episodeProgressTimeout)
			defer cancel()
			result <- state.session.ReportWatchtime(ctx, from, to, state.duration, "paused", true)
		})
		if err := <-result; err != nil {
			log.Printf("playback: could not finish episode progress video_id=%s: %v", state.item.VideoID, err)
		}
		return
	}
	a.sendEpisodeProgressFinal(state)
}

func (a *app) sendEpisodeProgressFinal(state *episodeProgressSync) {
	if state.session == nil || state.finalSent {
		return
	}
	from, to := state.lastReported, max(state.finishAt, state.lastReported)
	state.finalSent = true
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), episodeProgressTimeout)
		defer cancel()
		if err := state.session.ReportWatchtime(ctx, from, to, state.duration, "paused", true); err != nil {
			log.Printf("playback: could not finish episode progress video_id=%s: %v", state.item.VideoID, err)
		}
	})
}

func (a *app) leaveEpisode() {
	item := a.current
	if !item.IsEpisode() {
		return
	}
	position := a.playbackPosition()
	a.takeEpisode(item.VideoID)
	if position < episodeStartGuard || (a.total > 0 && position > a.total-episodeEndGuard) {
		return
	}
	a.episodes = slices.Insert(a.episodes, 0, playstate.Episode{VideoID: item.VideoID, Position: position})
	a.episodes = a.episodes[:min(len(a.episodes), playstate.MaxEpisodes)]
}

func (a *app) takeEpisode(videoID string) time.Duration {
	i := slices.IndexFunc(a.episodes, func(e playstate.Episode) bool { return e.VideoID == videoID })
	if i < 0 {
		return 0
	}
	position := a.episodes[i].Position
	a.episodes = slices.Delete(a.episodes, i, i+1)
	return position
}
