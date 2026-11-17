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

const DefaultEndpoint = "https://us.i.posthog.com/i/v0/e/"

const (
	disableGeoIP        = true
	createPersonProfile = false
)

const Timeout = 10 * time.Second

type Client struct {
	Key      string
	Endpoint string
	HTTP     *http.Client
}

type capture struct {
	APIKey     string         `json:"api_key"`
	Event      string         `json:"event"`
	DistinctID string         `json:"distinct_id"`
	Properties map[string]any `json:"properties"`
}

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
	props := map[string]any{
		"$lib":                    "meiro",
		"$geoip_disable":          disableGeoIP,
		"$process_person_profile": createPersonProfile,
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

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
