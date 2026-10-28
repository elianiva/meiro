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

// lastFMAPIKey and lastFMSecret are the Last.fm application's. A release
// build has tools/dist.sh generate a file that sets them from
// MEIRO_LASTFM_API_KEY and MEIRO_LASTFM_SECRET; a development build reads
// those variables when it runs. Without them the app still runs, and the settings say
// Last.fm cannot be connected.
//
// The secret is not confidential: whatever a desktop app can sign with, its
// users can read. Last.fm's signature only ties calls to an application, and
// a user's session key is what grants access to their account, so the client
// signs directly and no backend stands between the app and Last.fm.
var lastFMAPIKey, lastFMSecret string

// lastFMClient is the client for the app's Last.fm key.
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
	// connectTimeout is how long the user has to approve Meiro on Last.fm.
	connectTimeout = 10 * time.Minute
	// approvalPoll is how often an unapproved token is exchanged again.
	approvalPoll = 3 * time.Second
	// scrobbleCallTimeout bounds one request to Last.fm.
	scrobbleCallTimeout = 30 * time.Second
	// flushAttempts is how many times in a row a delivery that fails for a
	// reason that may pass is tried, with flushBackoff doubling between tries,
	// before it waits for the next delivery to be asked for.
	flushAttempts = 4
	flushBackoff  = 15 * time.Second
	// flushInterval is how often a play that could not be delivered is tried
	// again while the app runs.
	flushInterval = 5 * time.Minute
)

// scrobbler is what the app keeps of Last.fm: the connection, the play being
// followed for it, and the plays waiting to be delivered. It belongs to the
// main thread, apart from the outbox, which locks itself.
type scrobbler struct {
	client *lastfm.Client
	// store keeps the session key, apart from the settings, and outbox the
	// plays that qualified and have not been delivered. Both are zero-value
	// safe: without a path they keep nothing.
	store  secretfile.File
	outbox *lastfm.Outbox
	// credits is who made the tracks seen so far, which fills in the tracks a
	// list left the artist out of without asking YouTube Music again.
	credits *credits.Cache
	// now is the clock the listening is measured against; tests replace it.
	now func() time.Time

	// session is the session key, empty until it is read from the store. The
	// account it belongs to is settings.LastFMUser.
	session string
	// reconnect says Last.fm refused the session, so nothing is sent until
	// the user connects again.
	reconnect bool
	// connect is the authorization in progress, and connectErr why the last
	// one did not connect.
	connect    task
	connectErr string
	// load reads the session key from the store.
	load task
	// flush delivers the outbox.
	flush task
	// lookup asks YouTube Music who made the track being followed, when the
	// list it came from did not say.
	lookup task

	// listen follows the play of the current track, nil when there is
	// nothing to scrobble.
	listen *lastfm.Listen
	// queued holds the tracks the user asked to queue, whose turn to play is
	// then the user's choice, as it is for a track picked from a list.
	queued map[string]struct{}
}

func newScrobbler() scrobbler {
	return scrobbler{client: lastFMClient(), outbox: &lastfm.Outbox{}, credits: &credits.Cache{}, now: time.Now, queued: map[string]struct{}{}}
}

// scrobbling reports whether plays are sent to Last.fm: an account is
// connected and the user has not paused it.
func (a *app) scrobbling() bool {
	return a.settings.LastFMUser != "" && !a.settings.LastFMPaused
}

// scrobbleTrack is what a track says to Last.fm. The artist is the first one
// the track credits in the links of YouTube Music's byline: a track that
// credits none has none, and is not scrobbled.
func scrobbleTrack(item youtube.MusicItem) lastfm.Track {
	return lastfm.Track{
		Artist: strings.TrimSpace(item.Artist),
		Title:  strings.TrimSpace(item.Title),
		Album:  strings.TrimSpace(item.Album),
	}
}

// beginListen starts following a track that was entered, as Last.fm will hear
// of it. It does nothing until playback is confirmed: entering a track says
// nothing of whether it plays.
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
	// A track the queue moved on to was not chosen, unless the user queued it.
	s.listen = lastfm.NewListen(track, !automatic || queued)
	if !track.Valid() {
		a.lookupCredits(item)
	}
}

// withKnownCredits is item with the artist and album it was seen with
// elsewhere, if it was.
func (a *app) withKnownCredits(item youtube.MusicItem) youtube.MusicItem {
	if known, ok := a.scrobble.credits.Get(item.VideoID); ok {
		item.Artist, item.Album = known.Artist, known.Album
	}
	return item
}

// learnCredits remembers who made the tracks of a list that name them. Every
// list that reaches the queue passes through here, so a track one list leaves
// the artist out of is filled in from another that had it, and a lookup is
// the last resort. It keeps nothing while no account is connected.
func (a *app) learnCredits(items ...youtube.MusicItem) {
	if a.settings.LastFMUser == "" {
		return
	}
	for _, item := range items {
		if item.Artist != "" {
			a.scrobble.credits.Put(item.VideoID, credits.Credits{Artist: item.Artist, Album: item.Album})
		}
	}
	// What was just learned may be about the track being followed.
	if l := a.scrobble.listen; l != nil && !l.Track.Valid() {
		if item := a.withKnownCredits(a.current); item.Artist != "" {
			l.Track = scrobbleTrack(item)
			a.current.Artist, a.current.Album = item.Artist, item.Album
		}
	}
}

// lookupCredits asks YouTube Music for the artist and album of a track whose
// list did not name them: the rows of a search for an artist leave the artist
// out, since the search says it. The track's own page names them in its
// byline, which is as structured as the lists' are. Until they arrive, or if
// they never do, the track is not submitted.
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

// followCurrent starts following the track that is already playing, when
// scrobbling begins in the middle of it: connecting, or resuming. What is
// heard from here on counts, and what came before does not.
func (a *app) followCurrent() {
	if a.current.VideoID != "" && a.player.Active() {
		a.beginListen(a.current, false)
	}
}

// rememberQueued marks a track the user asked to queue.
func (a *app) rememberQueued(id string) { a.scrobble.queued[id] = struct{}{} }

// observeListen takes a sample of the track playing. It runs on each poll
// while the player plays.
func (a *app) observeListen() {
	s := &a.scrobble
	l := s.listen
	if l == nil {
		return
	}
	// A track still being found, a stream still opening and a decode being
	// moved to another position are not listening, whatever the player
	// reports meanwhile.
	if a.stream.busy() || a.seeking.busy() || a.scrubbing || a.player.Buffering() {
		l.Break()
		return
	}
	a.observe(s.now(), a.player.Position(), a.total)
}

// observe feeds the play being followed a sample of the track playing, and
// acts on what it makes true.
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

// sendNowPlaying tells Last.fm what is playing. A failure costs nothing but
// the line on the profile, so only a refused session is acted on.
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

// scrobbleFailed acts on a failure that says the session is no good.
func (a *app) scrobbleFailed(err error) {
	if lastfm.InvalidSession(err) && !a.scrobble.reconnect && a.settings.LastFMUser != "" {
		a.scrobble.reconnect = true
		a.notice = "Last.fm needs you to connect again"
	}
}

// flushScrobbles delivers the plays waiting, off the main thread, if a
// delivery is not already on its way. Plays that cannot be delivered for a
// reason that may pass stay where they are, on disk, for the next try.
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
		a.flushScrobbles() // a play may have qualified while the last was on its way
	})
}

// deliver sends the outbox, oldest plays first, in batches. A batch Last.fm
// rejects for good is sent play by play, so one bad play is dropped rather
// than holding the rest back, and nothing is retried forever.
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

// openScrobbler keeps the session key in session and reads the plays that
// were waiting when the app last closed: a small file, like the settings.
func (a *app) openScrobbler(session secretfile.File, outboxPath string) {
	s := &a.scrobble
	s.store = session
	s.outbox = lastfm.OpenOutbox(outboxPath)
}

// openCredits reads the credits kept from earlier runs.
func (a *app) openCredits(path string) { a.scrobble.credits = credits.Open(path) }

// loadScrobbleSession reads the saved session key off the main thread, when an
// account is connected, and delivers the plays that were waiting.
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
			s.reconnect = true // the settings say connected; the key is gone
			return
		}
		s.session = key
		a.flushScrobbles()
	})
}
