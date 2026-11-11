package audio

import (
	"context"
	"sync"
	"time"
)

const (
	// urlTTL is how long a resolved audio URL is reused. YouTube's media URLs
	// stay valid for hours, so this is a wide safety margin.
	urlTTL = 30 * time.Minute
	// urlLimit bounds how many resolved URLs are remembered.
	urlLimit = 8
	// ResolveTimeout bounds one resolve, a yt-dlp run.
	ResolveTimeout = 90 * time.Second
)

// Resolver finds a direct audio URL and the length of a track.
type Resolver func(ctx context.Context, videoID string) (string, time.Duration, error)

// URLs remembers resolved audio URLs, so a track resolved ahead of time plays
// without waiting on yt-dlp. A URL is a few hundred bytes: resolving ahead
// costs no meaningful bandwidth, unlike downloading the audio. Concurrent
// requests for one track share a single resolve. It is safe for concurrent
// use.
type URLs struct {
	resolve Resolver

	mu      sync.Mutex
	entries map[string]*urlEntry
}

type urlEntry struct {
	at    time.Time
	done  chan struct{}
	url   string
	total time.Duration
	err   error
}

// NewURLs returns an empty cache that resolves with resolve.
func NewURLs(resolve Resolver) *URLs {
	return &URLs{resolve: resolve, entries: make(map[string]*urlEntry)}
}

// Get returns the audio URL and length of a track, resolving it unless a
// fresh result or a resolve in flight exists. The resolve goes on when ctx
// ends, so a later Get can still use it; a failed one is not remembered.
func (c *URLs) Get(ctx context.Context, videoID string) (string, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	c.mu.Lock()
	e, ok := c.entries[videoID]
	if !ok || e.stale() {
		e = &urlEntry{at: time.Now(), done: make(chan struct{})}
		c.entries[videoID] = e
		c.trimLocked()
		// The resolve outlives this request, so a later one can share it;
		// it keeps the request's values but not its cancellation.
		go c.run(context.WithoutCancel(ctx), videoID, e)
	}
	c.mu.Unlock()
	select {
	case <-e.done:
		return e.url, e.total, e.err
	case <-ctx.Done():
		return "", 0, ctx.Err()
	}
}

// Forget drops what is remembered of a track, so that the next Get resolves
// it again. It is for a URL that stopped working before its time, as one that
// expired while the machine slept.
func (c *URLs) Forget(videoID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, videoID)
}

// Reset drops everything remembered, for a change in how tracks are resolved.
func (c *URLs) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

func (c *URLs) run(ctx context.Context, videoID string, e *urlEntry) {
	ctx, cancel := context.WithTimeout(ctx, ResolveTimeout)
	defer cancel()
	e.url, e.total, e.err = c.resolve(ctx, videoID)
	if e.err != nil {
		c.mu.Lock()
		if c.entries[videoID] == e {
			delete(c.entries, videoID)
		}
		c.mu.Unlock()
	}
	// Remove failed results before waking waiters. Otherwise a caller that
	// retries immediately can still find and reuse this failed entry.
	close(e.done)
}

// stale reports whether a finished entry is past its lifetime.
func (e *urlEntry) stale() bool {
	select {
	case <-e.done:
		return time.Since(e.at) > urlTTL
	default:
		return false
	}
}

// trimLocked drops the oldest entries beyond the limit. The caller holds
// c.mu.
func (c *URLs) trimLocked() {
	for len(c.entries) > urlLimit {
		oldest, oldestAt := "", time.Time{}
		for id, e := range c.entries {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = id, e.at
			}
		}
		delete(c.entries, oldest)
	}
}
