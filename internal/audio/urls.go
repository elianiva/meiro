package audio

import (
	"context"
	"sync"
	"time"
)

const (
	urlTTL         = 30 * time.Minute
	urlLimit       = 8
	ResolveTimeout = 90 * time.Second
)

type Resolver func(ctx context.Context, videoID string) (string, time.Duration, error)

type URLs struct {
	resolve Resolver

	mu      sync.Mutex
	entries map[string]*urlEntry
}

type urlEntry struct {
	at      time.Time
	waiters int
	cancel  context.CancelFunc
	done    chan struct{}
	url     string
	total   time.Duration
	err     error
}

func NewURLs(resolve Resolver) *URLs {
	return &URLs{resolve: resolve, entries: make(map[string]*urlEntry)}
}

func (c *URLs) Get(ctx context.Context, videoID string) (string, time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return "", 0, err
	}
	c.mu.Lock()
	e, ok := c.entries[videoID]
	if !ok || e.stale() {
		runCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ResolveTimeout)
		e = &urlEntry{at: time.Now(), done: make(chan struct{}), cancel: cancel}
		c.entries[videoID] = e
		c.trimLocked()
		go c.run(runCtx, videoID, e)
	}
	e.waiters++
	c.mu.Unlock()
	select {
	case <-e.done:
		c.leave(e)
		return e.url, e.total, e.err
	case <-ctx.Done():
		c.leave(e)
		return "", 0, ctx.Err()
	}
}

func (c *URLs) leave(e *urlEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e.waiters--
	if e.waiters > 0 {
		return
	}
	select {
	case <-e.done:
	default:
		e.cancel()
		for id, other := range c.entries {
			if other == e {
				delete(c.entries, id)
			}
		}
	}
}

func (c *URLs) Forget(videoID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, videoID)
}

func (c *URLs) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	clear(c.entries)
}

func (c *URLs) run(ctx context.Context, videoID string, e *urlEntry) {
	defer e.cancel()
	e.url, e.total, e.err = c.resolve(ctx, videoID)
	if e.err != nil {
		c.mu.Lock()
		if c.entries[videoID] == e {
			delete(c.entries, videoID)
		}
		c.mu.Unlock()
	}
	close(e.done)
}

func (e *urlEntry) stale() bool {
	select {
	case <-e.done:
		return time.Since(e.at) > urlTTL
	default:
		return false
	}
}

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
