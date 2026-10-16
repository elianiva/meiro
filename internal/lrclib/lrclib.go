// Package lrclib fetches song lyrics from https://lrclib.net, and reads the
// time-synced ones, which come in the LRC format, into lines with the moment
// each is sung.
package lrclib

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// userAgent names the app, as LRCLIB asks of its clients.
const userAgent = "Meiro (https://github.com/elianiva/meiro)"

// maxBody bounds the answer read; lyrics are a few kilobytes.
const maxBody = 1 << 20

// Query names the track to look for. Duration is optional, and lets LRCLIB
// tell apart versions of a song that are of different length.
type Query struct {
	Title, Artist, Album string
	Duration             time.Duration
}

// Line is one sung line and when it starts.
type Line struct {
	At   time.Duration
	Text string
}

// Lyrics is what LRCLIB knows of a track. Plain or Synced can be empty; a
// track that is only music has neither, and Instrumental set.
type Lyrics struct {
	Plain        string
	Synced       []Line
	Instrumental bool
}

// Empty reports whether there is nothing to show.
func (l *Lyrics) Empty() bool {
	return l == nil || (l.Plain == "" && len(l.Synced) == 0)
}

// Client asks LRCLIB. The zero value uses the real service.
type Client struct {
	HTTP *http.Client
	// BaseURL replaces https://lrclib.net, for tests.
	BaseURL string
	// RetryDelay is the wait before the first retry, and twice that before
	// the second: 700 ms when zero.
	RetryDelay time.Duration
}

type response struct {
	Instrumental bool   `json:"instrumental"`
	PlainLyrics  string `json:"plainLyrics"`
	SyncedLyrics string `json:"syncedLyrics"`
}

func (r response) lyrics() *Lyrics {
	return &Lyrics{
		Plain:        strings.TrimSpace(r.PlainLyrics),
		Synced:       ParseLRC(r.SyncedLyrics),
		Instrumental: r.Instrumental,
	}
}

// Get finds the lyrics of a track. It returns nil and no error when LRCLIB
// has none: the exact match is tried first, then a search by title and artist,
// then by the title cut down to the song's own name. A failing request does not
// end the lookup, the next step is tried, and the error is only returned when
// nothing was found at all.
func (c *Client) Get(ctx context.Context, q Query) (*Lyrics, error) {
	if q.Title == "" {
		return nil, nil
	}
	values := url.Values{"track_name": {q.Title}, "artist_name": {q.Artist}}
	if q.Album != "" {
		values.Set("album_name", q.Album)
	}
	if q.Duration > 0 {
		values.Set("duration", strconv.Itoa(int(q.Duration.Round(time.Second).Seconds())))
	}
	var firstErr error
	var one response
	found, err := c.fetch(ctx, "/api/get", values, &one)
	firstErr = err
	if found {
		if l := one.lyrics(); !l.Empty() || l.Instrumental {
			return l, nil
		}
	}
	titles := []string{q.Title}
	if short := ShortTitle(q.Title); short != q.Title {
		titles = append(titles, short)
	}
	for _, title := range titles {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var many []response
		if _, err := c.fetch(ctx, "/api/search", url.Values{"track_name": {title}, "artist_name": {q.Artist}}, &many); err != nil {
			firstErr = cmp.Or(firstErr, err)
			continue
		}
		var plain *Lyrics
		for _, r := range many {
			l := r.lyrics()
			switch {
			case len(l.Synced) > 0:
				return l, nil // the best there is: it has both forms
			case plain == nil && !l.Empty():
				plain = l
			}
		}
		if plain != nil {
			return plain, nil
		}
	}
	return nil, firstErr
}

// ShortTitle is a track title without what YouTube Music adds to it: the
// translation or version after a " - ", and a "(feat. …)" credit.
func ShortTitle(title string) string {
	if i := strings.Index(title, " - "); i > 0 {
		title = title[:i]
	}
	for {
		open := strings.LastIndexAny(title, "(（")
		closing := strings.LastIndexAny(title, ")）")
		if open < 0 || closing < open {
			break
		}
		inner := strings.ToLower(title[open+1 : closing])
		if !strings.HasPrefix(inner, "feat") && !strings.HasPrefix(inner, "ft.") && !strings.HasPrefix(inner, "with ") {
			break
		}
		title = strings.TrimSpace(title[:open] + title[closing+1:])
	}
	return strings.TrimSpace(title)
}

// fetch reads a JSON answer into out, and reports false for a 404.
func (c *Client) fetch(ctx context.Context, path string, values url.Values, out any) (bool, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://lrclib.net"
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	delay := c.RetryDelay
	if delay == 0 {
		delay = 700 * time.Millisecond
	}
	// LRCLIB answers 5xx now and then, and a request that fails at the
	// network is as likely to work the next time, so each is tried three times.
	for attempt := 1; ; attempt++ {
		found, retry, err := c.fetchOnce(ctx, client, base, path, values, out)
		if err == nil || !retry || attempt == 3 {
			return found, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt) * delay):
		}
	}
}

// fetchOnce makes one request, and reports whether a failure is worth another.
func (c *Client) fetchOnce(ctx context.Context, client *http.Client, base, path string, values url.Values, out any) (found, retry bool, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path+"?"+values.Encode(), nil)
	if err != nil {
		return false, false, err
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		log.Printf("lyrics: lrclib GET %s failed: %v", request.URL, err)
		return false, ctx.Err() == nil, err
	}
	defer response.Body.Close()
	log.Printf("lyrics: lrclib GET %s -> %s", request.URL, response.Status)
	switch {
	case response.StatusCode == http.StatusNotFound:
		return false, false, nil
	case response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests:
		return false, true, fmt.Errorf("lrclib: %s", response.Status)
	case response.StatusCode != http.StatusOK:
		return false, false, fmt.Errorf("lrclib: %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBody))
	if err != nil {
		return false, true, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return false, false, errors.New("lrclib: unreadable answer")
	}
	return true, false, nil
}

// restart is how far back the first stamp of a line must jump, from the one
// before it, to start another copy of the song.
const restart = 10 * time.Second

// ParseLRC reads synced lyrics: lines such as "[01:23.45] text", which may
// carry several stamps for a repeated line. Lines without a stamp, and
// metadata tags such as "[ar: name]", are skipped. The result is in time
// order.
//
// Some files hold the song more than once, as a romanised or translated copy
// after the original, and the second copy starts again from the top. Such a
// file is split where its time jumps back, and the copy with the most
// non-ASCII text, which is the original for a song in Japanese or Korean, is
// kept; with no difference between them, the first.
func ParseLRC(text string) []Line {
	var blocks [][]Line
	var current []Line
	var previous time.Duration
	for raw := range strings.SplitSeq(text, "\n") {
		raw = strings.TrimSpace(raw)
		var stamps []time.Duration
		for strings.HasPrefix(raw, "[") {
			end := strings.IndexByte(raw, ']')
			if end < 0 {
				break
			}
			at, ok := parseStamp(raw[1:end])
			if !ok {
				break
			}
			stamps = append(stamps, at)
			raw = raw[end+1:]
		}
		if len(stamps) == 0 {
			continue
		}
		first := slices.Min(stamps)
		if len(current) > 0 && first < previous-restart {
			blocks, current = append(blocks, current), nil
		}
		previous = first
		content := strings.TrimSpace(raw)
		for _, at := range stamps {
			current = append(current, Line{At: at, Text: content})
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	var best []Line
	bestScore := -1
	for _, block := range blocks {
		score := 0
		for _, line := range block {
			for _, r := range line.Text {
				if r > unicode.MaxASCII && unicode.IsLetter(r) {
					score++
				}
			}
		}
		if score > bestScore {
			best, bestScore = block, score
		}
	}
	sort.SliceStable(best, func(i, j int) bool { return best[i].At < best[j].At })
	return best
}

// parseStamp reads "mm:ss" or "mm:ss.xx".
func parseStamp(s string) (time.Duration, bool) {
	minutes, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	m, err := strconv.Atoi(minutes)
	if err != nil || m < 0 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(rest, 64)
	if err != nil || secs < 0 || secs >= 60 {
		return 0, false
	}
	return time.Duration(m)*time.Minute + time.Duration(secs*float64(time.Second)), true
}

// IndexAt is the line being sung at position: the last one that has started.
// It is -1 before the first line.
func IndexAt(lines []Line, position time.Duration) int {
	return sort.Search(len(lines), func(i int) bool { return lines[i].At > position }) - 1
}
