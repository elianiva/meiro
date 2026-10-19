package youtube

import (
	"crypto/sha1" //nolint:gosec // SAPISIDHASH is defined as SHA-1
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// CookieOptions selects an account or channel when the cookie authenticates
// more than one profile.
type CookieOptions struct {
	AccountIndex   int
	OnBehalfOfUser string
}

// CookieAuth contains browser cookies used to authenticate InnerTube calls.
// Treat the cookie string as a password. Cookie auth is how the client reaches
// an account: it is what the YouTube Music web app signs in with, and it is
// the only way to the personal home page, the library and every channel.
type CookieAuth struct {
	cookie         string
	sapisid        string
	accountIndex   int
	onBehalfOfUser string
}

const redactedCookieAuth = "youtube.CookieAuth(<redacted>)"

// String prevents ordinary formatting from exposing browser credentials.
func (CookieAuth) String() string { return redactedCookieAuth }

// GoString prevents Go-syntax formatting from exposing browser credentials.
func (CookieAuth) GoString() string { return redactedCookieAuth }

// LogValue prevents structured logs from exposing browser credentials.
func (CookieAuth) LogValue() slog.Value { return slog.StringValue(redactedCookieAuth) }

// NewCookieAuth validates and stores a Cookie request-header value. The
// exported API does not log or serialize this credential.
func NewCookieAuth(cookie string, options CookieOptions) (*CookieAuth, error) {
	cookie = strings.TrimSpace(cookie)
	if cookie == "" || strings.ContainsAny(cookie, "\r\n") {
		return nil, errors.New("youtube: cookie header is empty or invalid")
	}
	sapisid := cookieValue(cookie, "SAPISID")
	if sapisid == "" {
		return nil, errors.New("youtube: cookie header must contain SAPISID")
	}
	return &CookieAuth{
		cookie: cookie, sapisid: sapisid, accountIndex: options.AccountIndex,
		onBehalfOfUser: options.OnBehalfOfUser,
	}, nil
}

func cookieValue(cookie, name string) string {
	for part := range strings.SplitSeq(cookie, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && key == name {
			return strings.Trim(strings.TrimSpace(value), `"`)
		}
	}
	return ""
}

func (auth *CookieAuth) authorization(at time.Time, origin string) string {
	timestamp := at.Unix()
	input := fmt.Sprintf("%d %s %s", timestamp, auth.sapisid, origin)
	digest := sha1.Sum([]byte(input)) //nolint:gosec // SAPISIDHASH is defined as SHA-1
	return fmt.Sprintf("SAPISIDHASH %d_%s", timestamp, hex.EncodeToString(digest[:]))
}
