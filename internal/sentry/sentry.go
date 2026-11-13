// Package sentry sends the crash report the user agreed to: the details the
// crash dialog shows, and nothing that could name a person. The event is one
// Sentry envelope, posted to the project its DSN names.
//
// Secrets and web addresses are removed here, at the boundary, so a caller
// cannot send them by accident. A report that fails to arrive is the
// caller's to retry: this package keeps no state.
package sentry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/elianiva/meiro/internal/applog"
)

// Timeout bounds one report, so a slow network never holds up what sends it.
const Timeout = 10 * time.Second

// Client sends crash reports to one project.
type Client struct {
	// DSN is the project's, as https://key@host/project. Without one,
	// Capture sends nothing.
	DSN string
	// HTTP sends the requests; nil uses the default client.
	HTTP *http.Client
}

// Crash is one panic to report.
type Crash struct {
	// Where is what the app was doing, and Message what it panicked with.
	Where, Message string
	// Details is the report the crash dialog offers to copy, with the
	// versions, the panic and its stack.
	Details string
	// Release is the app's version, empty for a development build.
	Release string
}

// wireMessage is Sentry's message field.
type wireMessage struct {
	Formatted string `json:"formatted"`
}

// event is the Sentry event an envelope carries.
type event struct {
	EventID   string                       `json:"event_id"`
	Timestamp string                       `json:"timestamp"`
	Platform  string                       `json:"platform"`
	Level     string                       `json:"level"`
	Release   string                       `json:"release,omitempty"`
	Message   wireMessage                  `json:"message"`
	Tags      map[string]string            `json:"tags"`
	Extra     map[string]string            `json:"extra"`
	Contexts  map[string]map[string]string `json:"contexts"`
	SDK       map[string]string            `json:"sdk"`
}

// Capture sends one crash report.
func (c *Client) Capture(crash Crash) error {
	if c.DSN == "" {
		return errors.New("sentry: no DSN")
	}
	endpoint, err := envelopeURL(c.DSN)
	if err != nil {
		return err
	}
	id, err := eventID()
	if err != nil {
		return err
	}
	release := ""
	if crash.Release != "" {
		release = "meiro@" + crash.Release
	}
	message := applog.Redact(Redact(crash.Message))
	details := applog.Redact(Redact(crash.Details))
	payload, err := json.Marshal(event{
		EventID:   id,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Platform:  "go",
		Level:     "fatal",
		Release:   release,
		Message:   wireMessage{Formatted: "Panic while " + crash.Where + ": " + message},
		Tags:      map[string]string{"where": crash.Where, "os": runtime.GOOS, "arch": runtime.GOARCH},
		Extra:     map[string]string{"details": details},
		Contexts: map[string]map[string]string{
			"os":      {"name": runtime.GOOS, "arch": runtime.GOARCH},
			"runtime": {"name": "go", "version": runtime.Version()},
		},
		SDK: map[string]string{"name": "meiro", "version": sdkVersion(crash.Release)},
	})
	if err != nil {
		return err
	}
	// An envelope is a header line, then one line for each item's header and
	// its body.
	header, err := json.Marshal(map[string]string{
		"event_id": id,
		"sent_at":  time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	item, err := json.Marshal(map[string]any{
		"type": "event", "content_type": "application/json", "length": len(payload),
	})
	if err != nil {
		return err
	}
	body := bytes.Join([][]byte{header, item, payload}, []byte("\n"))
	body = append(body, '\n')

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-sentry-envelope")
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		reply, _ := io.ReadAll(io.LimitReader(response.Body, 512))
		return fmt.Errorf("sentry: the report was refused with %s: %s", response.Status, strings.TrimSpace(string(reply)))
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// envelopeURL is where a DSN's project takes envelopes.
func envelopeURL(dsn string) (string, error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("sentry: the DSN is not a web address: %w", err)
	}
	key := ""
	if u.User != nil {
		key = u.User.Username()
	}
	project := strings.Trim(u.Path, "/")
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || key == "" ||
		project == "" || strings.Contains(project, "/") {
		return "", errors.New("sentry: the DSN must look like https://key@host/project")
	}
	return fmt.Sprintf("%s://%s/api/%s/envelope/?sentry_key=%s&sentry_version=7",
		u.Scheme, u.Host, project, url.QueryEscape(key)), nil
}

// addressPattern matches a web address, which can name a video or carry a
// token.
var addressPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

// sdkVersion is what the report says made it: the app's version, or that it
// came from a build without one.
func sdkVersion(release string) string {
	if release == "" {
		return "development"
	}
	return release
}

// Redact replaces web addresses with a placeholder.
func Redact(s string) string {
	return addressPattern.ReplaceAllString(s, "<url>")
}

// eventID is a Sentry event ID: 16 random bytes, as hex.
func eventID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
