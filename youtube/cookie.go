package youtube

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

type CookieOptions struct {
	AccountIndex   int
	OnBehalfOfUser string
}

type CookieAuth struct {
	cookie         string
	sapisid        string
	accountIndex   int
	onBehalfOfUser string
}

const redactedCookieAuth = "youtube.CookieAuth(<redacted>)"

func (CookieAuth) String() string { return redactedCookieAuth }

func (CookieAuth) GoString() string { return redactedCookieAuth }

func (CookieAuth) LogValue() slog.Value { return slog.StringValue(redactedCookieAuth) }

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
	digest := sha1.Sum([]byte(input))
	return fmt.Sprintf("SAPISIDHASH %d_%s", timestamp, hex.EncodeToString(digest[:]))
}
