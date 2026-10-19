package youtube

import (
	"context"
	"crypto/sha1" //nolint:gosec // SAPISIDHASH is defined as SHA-1
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// sapisidHash is the SAPISIDHASH value for a secret, an origin and a
// timestamp, as YouTube defines it.
func sapisidHash(secret, origin string, timestamp int64) string {
	input := fmt.Sprintf("%d %s %s", timestamp, secret, origin)
	digest := sha1.Sum([]byte(input)) //nolint:gosec // SAPISIDHASH is defined as SHA-1
	return fmt.Sprintf("%d_%s", timestamp, hex.EncodeToString(digest[:]))
}

// A cookie-authenticated request must carry a SAPISIDHASH computed for the
// origin it is sent to. YouTube binds the hash to that origin and answers
// signed-out content for a request whose hash is for another one, so every
// sign-in and every library, like and playlist write failed while the hash was
// pinned to www.youtube.com and the requests went to music.youtube.com.
func TestAuthorizationHashesTheOriginTheRequestIsSentTo(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, baseURL, origin string }{
		{"the default API URL", "", "https://music.youtube.com"},
		{"an API URL of its own", "https://www.youtube.com", "https://www.youtube.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent string
			client := NewClient(Options{
				BaseURL: tc.baseURL, CookieAuth: auth,
				HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					sent = r.Header.Get("Authorization")
					if got := r.Header.Get("Origin"); got != tc.origin {
						t.Errorf("Origin header = %q, want %q", got, tc.origin)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
				})},
			})
			if _, err := client.execute(context.Background(), "browse", map[string]any{}); err != nil {
				t.Fatalf("execute: %v", err)
			}
			timestamp, err := strconv.ParseInt(strings.SplitN(strings.TrimPrefix(sent, "SAPISIDHASH "), "_", 2)[0], 10, 64)
			if err != nil {
				t.Fatalf("Authorization = %q, want SAPISIDHASH <timestamp>_<hash>", sent)
			}
			want := "SAPISIDHASH " + sapisidHash("secret", tc.origin, timestamp)
			if sent != want {
				t.Errorf("Authorization = %q, want %q: the hash must be for the origin the request is sent to", sent, want)
			}
		})
	}
}
