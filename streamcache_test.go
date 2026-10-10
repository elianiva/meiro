package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamCacheResolvesOnceAndSharesInFlight(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := newStreamCache(func(_ context.Context, id, _ string) (string, time.Duration, error) {
		calls.Add(1)
		<-release
		return "url-" + id, time.Minute, nil
	})
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if url, total, err := c.get("a", "cookie"); err != nil || url != "url-a" || total != time.Minute {
				t.Errorf("get = %q, %v, %v", url, total, err)
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	_, _, _ = c.get("a", "cookie")
	if got := calls.Load(); got != 1 {
		t.Errorf("resolved %d times, want 1", got)
	}
}

func TestStreamCacheDoesNotKeepFailuresOrOtherSessions(t *testing.T) {
	calls := 0
	fail := true
	c := newStreamCache(func(context.Context, string, string) (string, time.Duration, error) {
		calls++
		if fail {
			return "", 0, errors.New("boom")
		}
		return "url", 0, nil
	})
	if _, _, err := c.get("a", ""); err == nil {
		t.Fatal("want the resolve error")
	}
	fail = false
	if _, _, err := c.get("a", ""); err != nil || calls != 2 {
		t.Fatalf("a failure was kept: err %v, calls %d", err, calls)
	}
	_, _, _ = c.get("a", "signed-in")
	if calls != 3 {
		t.Errorf("a different cookie reused the URL: calls %d", calls)
	}
}

func TestStreamCacheExpiresAndBounds(t *testing.T) {
	now := time.Now()
	calls := 0
	c := newStreamCache(func(context.Context, string, string) (string, time.Duration, error) {
		calls++
		return "url", 0, nil
	})
	c.now = func() time.Time { return now }
	_, _, _ = c.get("a", "")
	now = now.Add(streamURLTTL + time.Second)
	_, _, _ = c.get("a", "")
	if calls != 2 {
		t.Errorf("an expired URL was reused: calls %d", calls)
	}
	for i := range streamURLLimit + 5 {
		now = now.Add(time.Second)
		_, _, _ = c.get(string(rune('a'+i)), "")
	}
	if len(c.entries) > streamURLLimit {
		t.Errorf("%d entries kept, limit %d", len(c.entries), streamURLLimit)
	}
}
