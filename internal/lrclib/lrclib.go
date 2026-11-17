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

	"github.com/elianiva/meiro/internal/lyrics"
)

const userAgent = "Meiro (https://github.com/elianiva/meiro)"

const maxBody = 1 << 20

const (
	defaultRetryDelay = 700 * time.Millisecond
	maxAttempts       = 3
)

type Query struct {
	Title, Artist, Album string
	Duration             time.Duration
}

type Lyrics struct {
	Plain        string
	Synced       []lyrics.Line
	Instrumental bool
}

func (l *Lyrics) Empty() bool {
	return l == nil || (l.Plain == "" && len(l.Synced) == 0)
}

type Client struct {
	HTTP       *http.Client
	BaseURL    string
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
				return l, nil
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
		delay = defaultRetryDelay
	}
	for attempt := 1; ; attempt++ {
		found, retry, err := c.fetchOnce(ctx, client, base, path, values, out)
		if err == nil || !retry || attempt == maxAttempts {
			return found, err
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(time.Duration(attempt) * delay):
		}
	}
}

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

const restartJump = 10 * time.Second

func ParseLRC(text string) []lyrics.Line {
	var blocks [][]lyrics.Line
	var current []lyrics.Line
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
		if len(current) > 0 && first < previous-restartJump {
			blocks, current = append(blocks, current), nil
		}
		previous = first
		content := strings.TrimSpace(raw)
		for _, at := range stamps {
			current = append(current, lyrics.Line{At: at, Text: content})
		}
	}
	if len(current) > 0 {
		blocks = append(blocks, current)
	}
	var best []lyrics.Line
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
