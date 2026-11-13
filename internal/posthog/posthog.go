// Package posthog sends the one message the install count needs: a ping when
// the user agrees on first run. Crash reports go to package sentry instead.
//
// Nothing here identifies a person. The message carries an ID made for it
// alone and never kept, and the collector is told neither to derive a
// location from the connection nor to make a person of that ID. A message
// that fails to arrive is the caller's to retry: this package keeps no
// state, so it cannot resend what the user agreed to only once.
package posthog

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoint is PostHog's single-event ingestion, where the project the
// key belongs to collects.
const DefaultEndpoint = "https://us.i.posthog.com/i/v0/e/"

// Timeout bounds one message, so a slow network never holds up what sends it.
const Timeout = 10 * time.Second

// Client sends the events of one project to the collector.
type Client struct {
	// Key is the project's API key. Without one, Capture sends nothing.
	Key string
	// Endpoint is where events go; empty uses DefaultEndpoint.
	Endpoint string
	// HTTP sends the requests; nil uses a client that times out.
	HTTP *http.Client
}

// capture is PostHog's single-event body.
type capture struct {
	APIKey     string         `json:"api_key"`
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
}

// Capture sends one event with properties, which may be nil. The ID it goes
// under is made here and goes nowhere else: it is never saved, so two
// messages from one copy of the app do not look like one person. The whole
// message is bounded by Timeout.
func (c *Client) Capture(event string, properties map[string]any) error {
	if c.Key == "" {
		return errors.New("posthog: no project key")
	}
	if event == "" {
		return errors.New("posthog: no event name")
	}
	id, err := randomID()
	if err != nil {
		return err
	}
	// The collector is asked to keep neither a location nor a person for the
	// message. Callers add only what the message is for.
	props := map[string]any{
		"$lib":                    "meiro",
		"$geoip_disable":          true,
		"$process_person_profile": false,
	}
	maps.Copy(props, properties)
	body, err := json.Marshal(capture{APIKey: c.Key, Event: event, DistinctID: id, Properties: props})
	if err != nil {
		return err
	}
	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
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
		return fmt.Errorf("posthog: %s answered %s: %s", event, response.Status, strings.TrimSpace(string(reply)))
	}
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}

// randomID is an ID for one message: 16 random bytes, as hex.
func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
