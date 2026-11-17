package youtube

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Loudness struct {
	LoudnessDB float64
}

func parseLoudness(raw json.RawMessage) (Loudness, error) {
	var response struct {
		PlayabilityStatus struct {
			Status string `json:"status"`
		} `json:"playabilityStatus"`
		PlayerConfig struct {
			AudioConfig struct {
				PerceptualLoudnessDb float64 `json:"perceptualLoudnessDb"`
				LoudnessDb           float64 `json:"loudnessDb"`
			} `json:"audioConfig"`
		} `json:"playerConfig"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return Loudness{}, err
	}
	if response.PlayabilityStatus.Status == "UNPLAYABLE" {
		return Loudness{}, errors.New("youtube: player response is unplayable")
	}
	audio := response.PlayerConfig.AudioConfig
	if audio.LoudnessDb != 0 {
		return Loudness{LoudnessDB: audio.LoudnessDb}, nil
	}
	if audio.PerceptualLoudnessDb != 0 {
		return Loudness{LoudnessDB: audio.PerceptualLoudnessDb}, nil
	}
	return Loudness{}, errors.New("youtube: player response has no loudness metadata")
}

func (c *Client) Loudness(ctx context.Context, videoID string) (Loudness, error) {
	if c.cookieAuth == nil {
		return Loudness{}, errors.New("youtube: loudness requires a signed-in session")
	}
	if strings.TrimSpace(videoID) == "" {
		return Loudness{}, errors.New("youtube: loudness requires a video ID")
	}
	raw, err := c.executeAs(ctx, webClient, false, "player", map[string]any{
		"videoId":         videoID,
		"playbackContext": map[string]any{"contentPlaybackContext": map[string]any{}},
	})
	if err != nil {
		return Loudness{}, err
	}
	return parseLoudness(raw)
}

func (c *Client) AudioStream(ctx context.Context, videoID string) (string, time.Duration, error) {
	if c.cookieAuth != nil {
		return "", 0, errors.New("youtube: audio streams are resolved anonymously")
	}
	if strings.TrimSpace(videoID) == "" {
		return "", 0, errors.New("youtube: audio stream requires a video ID")
	}
	if c.currentVisitorData() == "" {
		if _, err := c.executeAs(ctx, musicClient, false, "visitor_id", map[string]any{}); err != nil {
			return "", 0, err
		}
	}
	raw, err := c.executeAs(ctx, visionOSClient, false, "player", map[string]any{
		"videoId": videoID, "contentCheckOk": true, "racyCheckOk": true,
	})
	if err != nil {
		return "", 0, err
	}
	stream, length, err := parseAudioStream(raw)
	if err != nil {
		return "", 0, err
	}
	if err := c.probe(ctx, stream); err != nil {
		return "", 0, err
	}
	return stream, length, nil
}

const firstByteRange = "bytes=0-0"

func (c *Client) probe(ctx context.Context, media string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, media, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Range", firstByteRange)
	req.Header.Set("User-Agent", browserUserAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("youtube: probe audio stream: %w", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("youtube: audio stream answers HTTP %d", resp.StatusCode)
	}
	return nil
}

func parseAudioStream(raw json.RawMessage) (string, time.Duration, error) {
	var response struct {
		PlayabilityStatus struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		} `json:"playabilityStatus"`
		StreamingData struct {
			AdaptiveFormats []struct {
				MimeType         string `json:"mimeType"`
				Bitrate          int    `json:"bitrate"`
				URL              string `json:"url"`
				ApproxDurationMs string `json:"approxDurationMs"`
				IsDrc            bool   `json:"isDrc"`
				AudioTrack       *struct {
					AudioIsDefault bool `json:"audioIsDefault"`
				} `json:"audioTrack"`
			} `json:"adaptiveFormats"`
		} `json:"streamingData"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", 0, err
	}
	if status := response.PlayabilityStatus; status.Status != "OK" {
		return "", 0, fmt.Errorf("youtube: player says %s: %s", status.Status, status.Reason)
	}
	best, bitrate, length := "", -1, time.Duration(0)
	for _, format := range response.StreamingData.AdaptiveFormats {
		isAudioOnly := strings.HasPrefix(format.MimeType, "audio/")
		isOtherLanguage := format.AudioTrack != nil && !format.AudioTrack.AudioIsDefault
		if !isAudioOnly || format.URL == "" || format.Bitrate <= bitrate || format.IsDrc || isOtherLanguage {
			continue
		}
		best, bitrate = format.URL, format.Bitrate
		ms, _ := strconv.ParseInt(format.ApproxDurationMs, 10, 64)
		length = time.Duration(ms) * time.Millisecond
	}
	if best == "" {
		return "", 0, errors.New("youtube: player lists no audio with a direct URL")
	}
	return best, length, nil
}

func (c *Client) ReportPlayback(ctx context.Context, videoID string) error {
	_, err := c.reportPlayback(ctx, videoID, 0, 0, false)
	return err
}

type PlaybackSession struct {
	client       *Client
	watchtimeURL *url.URL
	cpn          string
	startedAt    time.Time
}

func (c *Client) BeginEpisodePlayback(ctx context.Context, videoID string, position, duration time.Duration) (*PlaybackSession, error) {
	return c.reportPlayback(ctx, videoID, position, duration, true)
}

func (c *Client) reportPlayback(ctx context.Context, videoID string, position, duration time.Duration, withWatchtime bool) (*PlaybackSession, error) {
	if c.cookieAuth == nil || strings.TrimSpace(videoID) == "" {
		return nil, nil
	}
	raw, err := c.execute(ctx, "player", map[string]any{
		"videoId":         videoID,
		"playbackContext": map[string]any{"contentPlaybackContext": map[string]any{}},
	})
	if err != nil {
		return nil, err
	}
	root := decodeResponse(raw)
	playbackURL, err := trackingURL(root, "videostatsPlaybackUrl")
	if err != nil {
		return nil, err
	}
	var watchtimeURL *url.URL
	if withWatchtime {
		watchtimeURL, err = trackingURL(root, "videostatsWatchtimeUrl")
		if err != nil {
			return nil, err
		}
	}
	cpn := clientPlaybackNonce()
	params := url.Values{
		"ver": {"2"},
		"c":   {defaultMusicContext},
		"cpn": {cpn},
	}
	if withWatchtime {
		params.Set("state", "playing")
		params.Set("cmt", seconds(position))
		params.Set("len", seconds(duration))
		params.Set("rt", "0")
	}
	if err := c.pingTracking(ctx, playbackURL, params, "playback"); err != nil {
		return nil, err
	}
	if !withWatchtime {
		return nil, nil
	}
	return &PlaybackSession{client: c, watchtimeURL: watchtimeURL, cpn: cpn, startedAt: time.Now()}, nil
}

func (s *PlaybackSession) ReportWatchtime(ctx context.Context, from, to, duration time.Duration, state string, final bool) error {
	if s == nil || s.client == nil || s.watchtimeURL == nil {
		return nil
	}
	params := url.Values{
		"ver":   {"2"},
		"c":     {defaultMusicContext},
		"cpn":   {s.cpn},
		"st":    {seconds(from)},
		"et":    {seconds(to)},
		"cmt":   {seconds(to)},
		"len":   {seconds(duration)},
		"state": {state},
		"rt":    {seconds(time.Since(s.startedAt))},
	}
	if final {
		params.Set("final", "1")
	}
	return s.client.pingTracking(ctx, s.watchtimeURL, params, "watchtime")
}

func trackingURL(root any, key string) (*url.URL, error) {
	tracking := findRenderer(root, key)
	base, _ := tracking.get("baseUrl").(string)
	if base == "" {
		return nil, errors.New("youtube: player response has no " + key)
	}
	target, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (c *Client) pingTracking(ctx context.Context, target *url.URL, params url.Values, operation string) error {
	if !c.allowInsecureCookieAuth && !isYouTubeURL(target) {
		return errors.New("youtube: refusing to send cookie authentication to a non-YouTube or non-HTTPS URL")
	}
	query := target.Query()
	for key, values := range params {
		query.Del(key)
		for _, value := range values {
			query.Add(key, value)
		}
	}
	requestURL := *target
	requestURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Referer", "https://music.youtube.com/")
	c.setIdentity(req, c.currentVisitorData())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return responseError(operation, resp)
	}
	return nil
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

const playbackNonceLength = 16

func clientPlaybackNonce() string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	b := make([]byte, playbackNonceLength)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[b[i]%byte(len(alphabet))]
	}
	return string(b)
}
