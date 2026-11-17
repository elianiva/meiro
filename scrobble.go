package main

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/credits"
	"github.com/elianiva/meiro/internal/lastfm"
	"github.com/elianiva/meiro/internal/secretfile"
	"github.com/elianiva/meiro/youtube"
)

var lastFMAPIKey, lastFMSecret string

func lastFMClient() *lastfm.Client {
	key, secret := lastFMAPIKey, lastFMSecret
	if key == "" {
		key = os.Getenv("MEIRO_LASTFM_API_KEY")
	}
	if secret == "" {
		secret = os.Getenv("MEIRO_LASTFM_SECRET")
	}
	return &lastfm.Client{Key: key, Secret: secret}
}

const (
	connectTimeout      = 10 * time.Minute
	approvalPoll        = 3 * time.Second
	scrobbleCallTimeout = 30 * time.Second
	flushAttempts       = 4
	flushBackoff        = 15 * time.Second
	flushInterval       = 5 * time.Minute
)

type scrobbler struct {
	client  *lastfm.Client
	store   secretfile.File
	outbox  *lastfm.Outbox
	credits *credits.Cache
	now     func() time.Time

	session    string
	reconnect  bool
	connect    task
	connectErr string
	load       task
	flush      task
	lookup     task

	listen *lastfm.Listen
	queued map[string]struct{}
}

func newScrobbler() scrobbler {
	return scrobbler{client: lastFMClient(), outbox: &lastfm.Outbox{}, credits: &credits.Cache{}, now: time.Now, queued: map[string]struct{}{}}
}

func (a *app) scrobbling() bool {
	return a.settings.LastFMUser != "" && !a.settings.LastFMPaused
}

func scrobbleTrack(item youtube.MusicItem) lastfm.Track {
	return lastfm.Track{
		Artist: strings.TrimSpace(item.Artist),
		Title:  strings.TrimSpace(item.Title),
		Album:  strings.TrimSpace(item.Album),
	}
}

func (a *app) beginListen(item youtube.MusicItem, automatic bool) {
	s := &a.scrobble
	_, queued := s.queued[item.VideoID]
	delete(s.queued, item.VideoID)
	if !a.scrobbling() {
		s.listen = nil
		return
	}
	s.lookup.stop()
	if item.Artist == "" {
		item = a.withKnownCredits(item)
	}
	track := scrobbleTrack(item)
	chosenByUser := !automatic || queued
	s.listen = lastfm.NewListen(track, chosenByUser)
	if !track.Valid() {
		a.lookupCredits(item)
	}
}

func (a *app) withKnownCredits(item youtube.MusicItem) youtube.MusicItem {
	if known, ok := a.scrobble.credits.Get(item.VideoID); ok {
		item.Artist, item.Album = known.Artist, known.Album
	}
	return item
}

func (a *app) learnCredits(items ...youtube.MusicItem) {
	if a.settings.LastFMUser == "" {
		return
	}
	for _, item := range items {
		if item.Artist != "" {
			a.scrobble.credits.Put(item.VideoID, credits.Credits{Artist: item.Artist, Album: item.Album})
		}
	}
	if l := a.scrobble.listen; l != nil && !l.Track.Valid() {
		if item := a.withKnownCredits(a.current); item.Artist != "" {
			l.Track = scrobbleTrack(item)
			a.current.Artist, a.current.Album = item.Artist, item.Album
		}
	}
}

func (a *app) lookupCredits(item youtube.MusicItem) {
	id := item.VideoID
	if id == "" || strings.TrimSpace(item.Title) == "" {
		log.Printf("last.fm: not following video_id=%s: it has no title or no video", id)
		return
	}
	client := a.client()
	startTask(a, &a.scrobble.lookup, scrobbleCallTimeout, func(ctx context.Context) (youtube.MusicItem, error) {
		result, err := client.GetUpNext(ctx, youtube.UpNextOptions{VideoID: id})
		if err != nil {
			return youtube.MusicItem{}, err
		}
		for _, found := range result.Items {
			if found.VideoID == id {
				return found, nil
			}
		}
		return youtube.MusicItem{}, errors.New("the track is not in its own queue")
	}, func(found youtube.MusicItem, err error) {
		if err != nil || strings.TrimSpace(found.Artist) == "" {
			log.Printf("last.fm: not following video_id=%s: no artist is named for it (%v)", id, err)
			return
		}
		a.scrobble.credits.Put(id, credits.Credits{Artist: found.Artist, Album: found.Album})
		l := a.scrobble.listen
		if l == nil || a.current.VideoID != id {
			return
		}
		a.current.Artist, a.current.Album = found.Artist, found.Album
		l.Track = scrobbleTrack(youtube.MusicItem{Title: item.Title, Artist: found.Artist, Album: found.Album})
	})
}

func (a *app) followCurrent() {
	if a.current.VideoID != "" && a.player.Active() {
		a.beginListen(a.current, false)
	}
}

func (a *app) rememberQueued(id string) { a.scrobble.queued[id] = struct{}{} }

func (a *app) observeListen() {
	s := &a.scrobble
	l := s.listen
	if l == nil {
		return
	}
	if a.stream.busy() || a.seeking.busy() || a.scrubbing || a.player.Buffering() {
		l.Break()
		return
	}
	a.observe(s.now(), a.player.Position(), a.total)
}

func (a *app) observe(at time.Time, position, total time.Duration) {
	s := &a.scrobble
	l := s.listen
	switch l.Observe(at, position, total) {
	case lastfm.Started:
		a.sendNowPlaying(l.Track)
	case lastfm.Qualified:
		s.outbox.Add(l.Scrobble(total))
		a.flushScrobbles()
	case lastfm.Nothing:
	}
}

func (a *app) sendNowPlaying(track lastfm.Track) {
	s := &a.scrobble
	if s.session == "" || s.reconnect || !s.client.Configured() {
		return
	}
	client, key := s.client, s.session
	spawn(a, func() error {
		ctx, cancel := context.WithTimeout(context.Background(), scrobbleCallTimeout)
		defer cancel()
		return client.UpdateNowPlaying(ctx, key, track)
	}, func(err error) {
		if err != nil {
			log.Printf("last.fm: now playing: %v", err)
			a.scrobbleFailed(err)
			return
		}
		log.Printf("last.fm: now playing sent: %q by %q", track.Title, track.Artist)
	})
}

func (a *app) scrobbleFailed(err error) {
	if lastfm.InvalidSession(err) && !a.scrobble.reconnect && a.settings.LastFMUser != "" {
		a.scrobble.reconnect = true
		a.notice = "Last.fm needs you to connect again"
	}
}

func (a *app) flushScrobbles() {
	s := &a.scrobble
	if s.session == "" || s.reconnect || !s.client.Configured() || s.flush.busy() || s.outbox.Len() == 0 {
		return
	}
	client, key, outbox, now, sleep := s.client, s.session, s.outbox, s.now, a.sleep
	startTask(a, &s.flush, noTimeout, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, deliver(ctx, client, key, outbox, now, sleep)
	}, func(_ struct{}, failure error) {
		if failure != nil {
			log.Printf("last.fm: scrobbling: %v", failure)
			a.scrobbleFailed(failure)
			return
		}
		a.flushScrobbles()
	})
}

func deliver(ctx context.Context, client *lastfm.Client, key string, outbox *lastfm.Outbox, now func() time.Time, sleep func(context.Context, time.Duration) error) error {
	limit, failures := lastfm.MaxBatch, 0
	for {
		batch := outbox.Batch(now())
		if len(batch) == 0 {
			return nil
		}
		batch = batch[:min(len(batch), limit)]
		callCtx, cancel := context.WithTimeout(ctx, scrobbleCallTimeout)
		err := client.Scrobble(callCtx, key, batch)
		cancel()
		switch {
		case err == nil:
			outbox.Done(len(batch))
			limit, failures = lastfm.MaxBatch, 0
		case ctx.Err() != nil:
			return ctx.Err()
		case lastfm.InvalidSession(err):
			return err
		case lastfm.Transient(err):
			failures++
			if failures >= flushAttempts {
				return err
			}
			if sleep(ctx, flushBackoff<<(failures-1)) != nil {
				return ctx.Err()
			}
		case len(batch) > 1:
			limit = 1
		default:
			log.Printf("last.fm: dropping %q by %q: %v", batch[0].Title, batch[0].Artist, err)
			outbox.Done(1)
		}
	}
}

func (a *app) openScrobbler(session secretfile.File, outboxPath string) {
	s := &a.scrobble
	s.store = session
	s.outbox = lastfm.OpenOutbox(outboxPath)
}

func (a *app) openCredits(path string) { a.scrobble.credits = credits.Open(path) }

func (a *app) loadScrobbleSession() {
	s := &a.scrobble
	if a.settings.LastFMUser == "" || s.session != "" || s.load.busy() || s.store.Path == "" {
		return
	}
	store := s.store
	startTask(a, &s.load, noTimeout, func(ctx context.Context) (string, error) {
		return store.Load(ctx)
	}, func(key string, err error) {
		if err != nil {
			log.Printf("last.fm: reading the saved session: %v", err)
			s.reconnect = true
			return
		}
		s.session = key
		a.flushScrobbles()
	})
}
