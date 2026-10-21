package main

import (
	"context"
	"time"

	"github.com/elianiva/meiro/youtube"
)

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

// resetUpNext forgets the recommendations of the queue being replaced, and
// drops any request for them still on its way.
func (a *app) resetUpNext(options youtube.UpNextOptions) {
	u := &a.upNext
	u.load.stop()
	u.options, u.fetched, u.token, u.err, u.waiting = options, false, "", "", false
	u.seen = map[string]struct{}{}
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
		a.learnLikes(result.Items)
		// The first page holds the track playing, with its full byline.
		a.learnCredits(result.Items...)
		u.token = result.ContinuationToken
		if u.token == continuation {
			u.token = ""
		}
		a.clearQueueSelection()
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
	a.syncPreload()
	a.syncSystemMedia()
}
