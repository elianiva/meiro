package lastfm

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	APIRoot     = "https://ws.audioscrobbler.com/2.0/"
	authRoot    = "https://www.last.fm/api/auth/"
	MaxBatch    = 50
	maxResponse = 1 << 20
)

type Client struct {
	Key, Secret string
	Root        string
	HTTP        *http.Client
}

func (c *Client) Configured() bool { return c != nil && c.Key != "" && c.Secret != "" }

type Track struct {
	Artist, Title, Album string
	Duration             time.Duration
}

func (t Track) Valid() bool {
	return strings.TrimSpace(t.Artist) != "" && strings.TrimSpace(t.Title) != ""
}

type Scrobble struct {
	Track
	Started time.Time
	Chosen  bool
}

type Session struct {
	Name, Key string
}

type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("last.fm error %d: %s", e.Code, e.Message) }

const (
	codeAuthFailed       = 4
	codeOperationFailed  = 8
	codeInvalidSession   = 9
	codeServiceOffline   = 11
	codeTemporary        = 16
	codeRateLimited      = 29
	codeTokenNotApproved = 14
)

func InvalidSession(err error) bool {
	var e *Error
	return errors.As(err, &e) && (e.Code == codeInvalidSession || e.Code == codeAuthFailed)
}

func NotApproved(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == codeTokenNotApproved
}

func Transient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case codeOperationFailed, codeServiceOffline, codeTemporary, codeRateLimited:
			return true
		}
		return false
	}
	var status *statusError
	if errors.As(err, &status) {
		return status.code >= 500 || status.code == http.StatusTooManyRequests
	}
	return true
}

type statusError struct{ code int }

func (e *statusError) Error() string { return "last.fm answered HTTP " + strconv.Itoa(e.code) }

func Sign(params url.Values, secret string) string {
	names := make([]string, 0, len(params))
	for name := range params {
		if name != "format" && name != "callback" && name != "api_sig" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, name := range names {
		b.WriteString(name)
		b.WriteString(params.Get(name))
	}
	b.WriteString(secret)
	sum := md5.Sum([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

func (c *Client) AuthURL(token string) string {
	return authRoot + "?api_key=" + url.QueryEscape(c.Key) + "&token=" + url.QueryEscape(token)
}

func (c *Client) Token(ctx context.Context) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	if err := c.call(ctx, "auth.getToken", url.Values{}, "", &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("last.fm sent no token")
	}
	return out.Token, nil
}

func (c *Client) Session(ctx context.Context, token string) (Session, error) {
	var out struct {
		Session struct {
			Name string `json:"name"`
			Key  string `json:"key"`
		} `json:"session"`
	}
	if err := c.call(ctx, "auth.getSession", url.Values{"token": {token}}, "", &out); err != nil {
		return Session{}, err
	}
	if out.Session.Key == "" {
		return Session{}, errors.New("last.fm sent no session")
	}
	return Session{Name: out.Session.Name, Key: out.Session.Key}, nil
}

func (c *Client) UpdateNowPlaying(ctx context.Context, sessionKey string, t Track) error {
	params := url.Values{}
	setTrack(params, "", t)
	return c.call(ctx, "track.updateNowPlaying", params, sessionKey, nil)
}

func (c *Client) Scrobble(ctx context.Context, sessionKey string, plays []Scrobble) error {
	if len(plays) == 0 {
		return nil
	}
	if len(plays) > MaxBatch {
		return fmt.Errorf("last.fm takes %d scrobbles at a time, not %d", MaxBatch, len(plays))
	}
	params := url.Values{}
	for i, play := range plays {
		suffix := "[" + strconv.Itoa(i) + "]"
		setTrack(params, suffix, play.Track)
		params.Set("timestamp"+suffix, strconv.FormatInt(play.Started.Unix(), 10))
		chosen := "0"
		if play.Chosen {
			chosen = "1"
		}
		params.Set("chosenByUser"+suffix, chosen)
	}
	return c.call(ctx, "track.scrobble", params, sessionKey, nil)
}

func setTrack(params url.Values, suffix string, t Track) {
	params.Set("artist"+suffix, t.Artist)
	params.Set("track"+suffix, t.Title)
	if t.Album != "" {
		params.Set("album"+suffix, t.Album)
	}
	if seconds := int(t.Duration.Round(time.Second) / time.Second); seconds > 0 {
		params.Set("duration"+suffix, strconv.Itoa(seconds))
	}
}

func (c *Client) call(ctx context.Context, method string, params url.Values, sessionKey string, out any) error {
	if !c.Configured() {
		return errors.New("last.fm is not set up in this build")
	}
	form := url.Values{}
	for name, values := range params {
		form[name] = values
	}
	form.Set("method", method)
	form.Set("api_key", c.Key)
	if sessionKey != "" {
		form.Set("sk", sessionKey)
	}
	form.Set("api_sig", Sign(form, c.Secret))
	form.Set("format", "json")

	root := c.Root
	if root == "" {
		root = APIRoot
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, root, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
	if err != nil {
		return err
	}
	var failure struct {
		Code    int    `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &failure) == nil && failure.Code != 0 {
		return &Error{Code: failure.Code, Message: failure.Message}
	}
	if response.StatusCode != http.StatusOK {
		return &statusError{response.StatusCode}
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("reading last.fm's answer: %w", err)
		}
	}
	return nil
}
