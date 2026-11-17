package main

import (
	"context"
	"time"

	"github.com/elianiva/meiro/youtube"
)

type upNextState struct {
	load    task
	options youtube.UpNextOptions
	fetched bool
	token   string
	seen    map[string]struct{}
	err     string
	waiting bool
}

func (a *app) resetUpNext(options youtube.UpNextOptions) {
	u := &a.upNext
	u.load.stop()
	u.options, u.fetched, u.token, u.err, u.waiting = options, false, "", "", false
	u.seen = map[string]struct{}{}
}

const upNextTimeout = 45 * time.Second

func (a *app) ensureUpNext() {
	u := &a.upNext
	if a.queue.playlistMore != "" || a.queueRest.busy() || !a.settings.AutoPlay || a.current.VideoID == "" || u.load.busy() || a.queue.len() == 0 {
		return
	}
	client := a.client()
	continuation := ""
	if u.fetched {
		switch {
		case u.token == "":
			if a.queue.remaining() > 0 {
				return
			}
			u.seen = map[string]struct{}{}
			u.options = youtube.UpNextOptions{VideoID: a.current.VideoID}
		case a.queue.remaining() > 1 && !u.waiting:
			return
		default:
			continuation = u.token
			if u.seen == nil {
				u.seen = map[string]struct{}{}
			}
			if _, seen := u.seen[continuation]; seen {
				u.token = ""
				if u.waiting {
					u.waiting = false
					a.stopAtQueueEnd()
				}
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
		a.learnLikes(result.Items)
		a.learnCredits(result.Items...)
		u.token = result.ContinuationToken
		if u.token == continuation {
			u.token = ""
		}
		a.clearQueueSelection()
		a.queue.appendGenerated(a.visibleItems(result.Items))
		a.warmNext()
		if !u.waiting {
			return
		}
		_, eligible := a.queue.nextEligible(a.settings.AutoPlay, a.repeat, a.settings.AutoSkipDisliked)
		switch {
		case a.queue.remaining() > 0 && eligible:
			u.waiting = false
			a.advance()
		case a.settings.AutoSkipDisliked && u.token != "":
			a.ensureUpNext()
		case !a.settings.AutoSkipDisliked && continuation != "":
			a.ensureUpNext()
		case a.settings.AutoSkipDisliked && a.queue.remaining() > 0:
			u.waiting = false
			a.advance()
		default:
			u.waiting = false
			a.stopAtQueueEnd()
		}
	})
}

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
	a.syncPreload()
	a.syncSystemMedia()
}
