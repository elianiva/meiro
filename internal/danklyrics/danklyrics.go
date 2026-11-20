// Package danklyrics adapts the dux-go lyrics engine to Meiro's lyrics types.
package danklyrics

import (
	"context"
	"strings"
	"time"

	dux "github.com/duxlabs-dev/dux-go/lyrics"

	"github.com/elianiva/meiro/internal/lyrics"
)

const userAgent = "Meiro (https://github.com/elianiva/meiro)"

// policy says which providers to try, in which tiers, and how long each
// network attempt may take. YouTube Music is left out: Meiro already asks it by
// video ID, which is more precise than a title search. KuGou is the slow tier.
type policy struct {
	fast, slow               []dux.Provider
	fastTimeout, slowTimeout time.Duration
}

var defaultPolicy = policy{
	fast:        []dux.Provider{dux.BetterLyrics, dux.Unison, dux.LyricsPlus, dux.LRCLIB},
	slow:        []dux.Provider{dux.KuGou},
	fastTimeout: 1200 * time.Millisecond,
	slowTimeout: 2500 * time.Millisecond,
}

// attempt is one pass over a provider set with one duration.
type attempt struct {
	duration  time.Duration
	providers []dux.Provider
	cacheOnly bool
	timeout   time.Duration
}

// Fetcher asks the engine for one attempt. It is a function so a lookup can be
// tested without the engine's cache and rate limiter.
type Fetcher func(ctx context.Context, request dux.Request) (*dux.Result, error)

// New returns a fetcher backed by the engine's disk cache and rate limiter.
func New(cacheDir string) Fetcher {
	client := dux.New(dux.Options{CacheDir: cacheDir, UserAgent: userAgent})
	return client.Lookup
}

// Source returns a lyrics source that asks the fast providers without the
// track's length, then with it, and only then the slow provider.
func Source(cacheDir string) lyrics.Source {
	fetch := New(cacheDir)
	return func(ctx context.Context, q lyrics.Query) (lyrics.Result, error) {
		return Lookup(ctx, fetch, q)
	}
}

// Lookup walks the default policy's attempt table and keeps the highest-ranked
// result: word over line over plain. A cache-only pass over both provider sets
// comes first so a repeat listen never touches the network. The engine keys
// some providers by length and some by artist and title, so a miss without the
// length can still be a hit with it. KuGou is tried last because its requests
// take about a second.
func Lookup(ctx context.Context, fetch Fetcher, q lyrics.Query) (lyrics.Result, error) {
	return lookup(ctx, fetch, q, defaultPolicy)
}

func lookup(ctx context.Context, fetch Fetcher, q lyrics.Query, p policy) (lyrics.Result, error) {
	var attempts [8]attempt
	count := 0
	attempts[count] = attempt{duration: 0, providers: p.fast, cacheOnly: true}
	count++
	if q.Duration > 0 {
		attempts[count] = attempt{duration: q.Duration, providers: p.fast, cacheOnly: true}
		count++
	}
	attempts[count] = attempt{duration: 0, providers: p.slow, cacheOnly: true}
	count++
	if q.Duration > 0 {
		attempts[count] = attempt{duration: q.Duration, providers: p.slow, cacheOnly: true}
		count++
	}
	attempts[count] = attempt{duration: 0, providers: p.fast, timeout: p.fastTimeout}
	count++
	if q.Duration > 0 {
		attempts[count] = attempt{duration: q.Duration, providers: p.fast, timeout: p.fastTimeout}
		count++
	}
	// KuGou's length-keyed cache answers much faster when it has the track, so
	// the slow tier tries the length before the broad search.
	if q.Duration > 0 {
		attempts[count] = attempt{duration: q.Duration, providers: p.slow, timeout: p.slowTimeout}
		count++
	}
	attempts[count] = attempt{duration: 0, providers: p.slow, timeout: p.slowTimeout}
	count++
	var best lyrics.Result
	bestRank := rankNone
	var lastErr error
	for _, a := range attempts[:count] {
		attemptCtx, cancel := ctx, context.CancelFunc(func() {})
		if a.timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, a.timeout)
		}
		got, err := fetch(attemptCtx, dux.Request{Title: q.Title, Artist: q.Artist, Album: q.Album, Duration: a.duration, Providers: a.providers, CacheOnly: a.cacheOnly})
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return lyrics.Result{}, ctx.Err()
			}
			lastErr = err
			continue
		}
		candidate := convert(got)
		if r := rank(candidate); r > bestRank {
			best, bestRank = candidate, r
		}
		if bestRank == rankWord {
			break
		}
	}
	if bestRank == rankNone && lastErr != nil {
		return lyrics.Result{}, lastErr
	}
	return best, nil
}

const (
	rankNone = iota
	rankPlain
	rankLine
	rankWord
)

func rank(result lyrics.Result) int {
	switch {
	case hasWords(result.Synced):
		return rankWord
	case len(result.Synced) > 0:
		return rankLine
	case strings.TrimSpace(result.Plain) != "":
		return rankPlain
	}
	return rankNone
}

func hasWords(lines []lyrics.Line) bool {
	for _, line := range lines {
		if len(line.Words) > 0 {
			return true
		}
	}
	return false
}

func convert(got *dux.Result) lyrics.Result {
	if got == nil || !got.Found {
		return lyrics.Result{}
	}
	result := lyrics.Result{Plain: strings.TrimSpace(got.Plain), Source: sourceName(got)}
	for _, line := range got.Synced {
		converted := lyrics.Line{At: line.Start.Duration(), Text: line.Text}
		for _, word := range line.Words {
			converted.Words = append(converted.Words, lyrics.Word{At: word.Start.Duration(), Text: word.Text})
		}
		result.Synced = append(result.Synced, converted)
	}
	return result
}

func sourceName(got *dux.Result) string {
	if got.Attribution.Name != "" {
		return got.Attribution.Name
	}
	return string(got.Source)
}
