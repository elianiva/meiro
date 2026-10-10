package main

import (
	"context"
	"sync"
	"time"
)

const (
	// streamURLTTL is how long a resolved audio URL is reused. YouTube's media
	// URLs stay valid for hours, so this is a wide safety margin.
	streamURLTTL = 30 * time.Minute
	// streamURLLimit bounds how many resolved URLs are remembered.
	streamURLLimit = 8
	// streamResolveTimeout bounds one yt-dlp run.
	streamResolveTimeout = 90 * time.Second
)

// streamResolver finds a direct audio URL and the track length for a video.
type streamResolver func(ctx context.Context, videoID, cookie string) (string, time.Duration, error)

// streamCache remembers resolved audio URLs, so a track resolved ahead of time
// plays without waiting on yt-dlp. A URL is a few hundred bytes: resolving
// ahead costs no meaningful bandwidth, unlike downloading the audio.
// Concurrent requests for one track share a single resolve.
type streamCache struct {
	resolve streamResolver
	now     func() time.Time

	mu      sync.Mutex
	entries map[string]*streamEntry
}

type streamEntry struct {
	cookie string
	at     time.Time
	done   chan struct{}
	url    string
	total  time.Duration
	err    error
}

func newStreamCache(resolve streamResolver) *streamCache {
	return &streamCache{resolve: resolve, now: time.Now, entries: make(map[string]*streamEntry)}
}

// get returns the audio URL for videoID, resolving it unless a fresh result
// or an in-flight resolve for the same session exists. A failed resolve is
// not remembered.
func (c *streamCache) get(videoID, cookie string) (string, time.Duration, error) {
	c.mu.Lock()
	if e, ok := c.entries[videoID]; ok && e.cookie == cookie && !c.staleLocked(e) {
		c.mu.Unlock()
		<-e.done
		return e.url, e.total, e.err
	}
	e := &streamEntry{cookie: cookie, at: c.now(), done: make(chan struct{})}
	c.entries[videoID] = e
	c.trimLocked()
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), streamResolveTimeout)
	e.url, e.total, e.err = c.resolve(ctx, videoID, cookie)
	cancel()
	if e.err != nil {
		c.mu.Lock()
		if c.entries[videoID] == e {
			delete(c.entries, videoID)
		}
		c.mu.Unlock()
	}
	close(e.done)
	return e.url, e.total, e.err
}

// staleLocked reports whether a finished entry is past its lifetime.
func (c *streamCache) staleLocked(e *streamEntry) bool {
	select {
	case <-e.done:
		return c.now().Sub(e.at) > streamURLTTL
	default:
		return false
	}
}

// trimLocked drops the oldest entries beyond the limit.
func (c *streamCache) trimLocked() {
	for len(c.entries) > streamURLLimit {
		oldest, oldestAt := "", time.Time{}
		for id, e := range c.entries {
			if oldest == "" || e.at.Before(oldestAt) {
				oldest, oldestAt = id, e.at
			}
		}
		delete(c.entries, oldest)
	}
}
