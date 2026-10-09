// Package youtube provides the read-only YouTube Music client Meiro browses
// and plays with. It uses YouTube's InnerTube API and parses the responses
// into the small convenience types the app needs.
package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIURL        = "https://www.youtube.com"
	defaultMusicVersion  = "1.20250219.01.00"
	defaultWebVersion    = "2.20260623.01.00"
	defaultMusicContext  = "WEB_REMIX"
	defaultMusicClientID = "67"
)

// maxResponseBytes is the most of an API response the client will read. The
// biggest pages, a whole library, are a few megabytes.
const maxResponseBytes = 64 << 20

// defaultResponseHeaderTimeout bounds how long the default client waits for a
// response's headers. The body is left to the request context, so a slow
// stream is not cut off by a client-wide deadline.
const defaultResponseHeaderTimeout = 30 * time.Second

// browserUserAgent is sent with every request. YouTube serves the InnerTube API
// to public clients, and a browser User-Agent keeps it from treating the
// request as a script and returning a stub.
const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

// Options configures a Music client. BaseURL is the YouTube InnerTube API host.
// APIKey is optional: YouTube answers the supported endpoints without one, and
// when it is set it is sent as the request's key.
type Options struct {
	HTTPClient       *http.Client
	BaseURL          string
	APIKey           string
	ClientVersion    string
	WebClientVersion string
	VisitorData      string
	Language         string
	Country          string
	CookieAuth       *CookieAuth
	// AllowInsecureCookieAuth permits sending CookieAuth to a non-YouTube or
	// non-HTTPS BaseURL. This is intended for trusted local test servers only.
	AllowInsecureCookieAuth bool
}

// Client issues read-only YouTube Music requests.
type Client struct {
	httpClient              *http.Client
	baseURL                 string
	apiKey                  string
	clientVersion           string
	webClientVersion        string
	visitorData             string
	language                string
	country                 string
	cookieAuth              *CookieAuth
	allowInsecureCookieAuth bool
}

// newHTTPClient builds the default HTTP client. It bounds only the wait for
// response headers: the request context governs how long the body may take.
func newHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	return &http.Client{Transport: transport}
}

// NewClient constructs a client. No network request is made until a method is
// called.
func NewClient(options Options) *Client {
	if options.HTTPClient == nil {
		options.HTTPClient = newHTTPClient(defaultResponseHeaderTimeout)
	}
	if options.BaseURL == "" {
		options.BaseURL = defaultAPIURL
	}
	if options.ClientVersion == "" {
		options.ClientVersion = defaultMusicVersion
	}
	if options.WebClientVersion == "" {
		options.WebClientVersion = defaultWebVersion
	}
	if options.Language == "" {
		options.Language = "en"
	}
	if options.Country == "" {
		options.Country = "US"
	}
	return &Client{
		httpClient:              options.HTTPClient,
		baseURL:                 strings.TrimRight(options.BaseURL, "/"),
		apiKey:                  options.APIKey,
		clientVersion:           options.ClientVersion,
		webClientVersion:        options.WebClientVersion,
		visitorData:             options.VisitorData,
		language:                options.Language,
		country:                 options.Country,
		cookieAuth:              options.CookieAuth,
		allowInsecureCookieAuth: options.AllowInsecureCookieAuth,
	}
}

type clientContext struct {
	Client struct {
		HL            string `json:"hl"`
		GL            string `json:"gl"`
		ClientName    string `json:"clientName"`
		ClientVersion string `json:"clientVersion"`
		VisitorData   string `json:"visitorData,omitempty"`
	} `json:"client"`
	User *clientUserContext `json:"user,omitempty"`
}

type clientUserContext struct {
	OnBehalfOfUser string `json:"onBehalfOfUser,omitempty"`
}

// context builds the request context shared by every InnerTube client. The
// client-specific name and version are set by executeForClient, which knows
// which client the request speaks as.
func (c *Client) context() clientContext {
	var ctx clientContext
	ctx.Client.HL = c.language
	ctx.Client.GL = c.country
	if c.cookieAuth == nil {
		ctx.Client.VisitorData = c.visitorData
	}
	return ctx
}

func readBoundedBody(body io.Reader, limit int64, label string) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("youtube: read %s: %w", label, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("youtube: %s exceeds %d bytes", label, limit)
	}
	return data, nil
}

// innerTubeClient is which of YouTube's clients a request speaks as.
type innerTubeClient string

const (
	musicClient innerTubeClient = "YTMUSIC"
	webClient   innerTubeClient = "WEB"
)

func (c *Client) execute(ctx context.Context, endpoint string, payload map[string]any) (json.RawMessage, error) {
	return c.executeForClient(ctx, endpoint, payload, musicClient)
}

func (c *Client) executeForClient(ctx context.Context, endpoint string, payload map[string]any, client innerTubeClient) (json.RawMessage, error) {
	var clientName, clientID, clientVersion string
	switch client {
	case musicClient:
		clientName, clientID, clientVersion = defaultMusicContext, defaultMusicClientID, c.clientVersion
	case webClient:
		clientName, clientID, clientVersion = "WEB", "1", c.webClientVersion
	}
	requestContext := c.context()
	requestContext.Client.ClientName = clientName
	requestContext.Client.ClientVersion = clientVersion
	if c.cookieAuth != nil {
		requestContext.User = &clientUserContext{OnBehalfOfUser: c.cookieAuth.onBehalfOfUser}
	}
	payload["context"] = requestContext
	if client == musicClient {
		payload["isAudioOnly"] = true
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("youtube: encode request: %w", err)
	}
	endpointURL := c.baseURL + "/youtubei/v1/" + strings.TrimLeft(endpoint, "/")
	parsedURL, err := url.Parse(endpointURL)
	if err != nil {
		return nil, fmt.Errorf("youtube: invalid API URL: %w", err)
	}
	if c.cookieAuth != nil && !c.allowInsecureCookieAuth && !isYouTubeURL(parsedURL) {
		return nil, errors.New("youtube: refusing to send cookie authentication to a non-YouTube or non-HTTPS URL")
	}
	query := parsedURL.Query()
	if c.apiKey != "" {
		query.Set("key", c.apiKey)
	}
	parsedURL.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsedURL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUserAgent)
	origin := parsedURL.Scheme + "://" + parsedURL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("X-YouTube-Client-Name", clientID)
	req.Header.Set("X-YouTube-Client-Version", clientVersion)
	// The visitor ID is an anonymous visit's; a signed-in session is its own.
	if c.visitorData != "" && c.cookieAuth == nil {
		req.Header.Set("X-Goog-Visitor-Id", c.visitorData)
	}
	if c.cookieAuth != nil {
		req.Header.Set("Cookie", c.cookieAuth.cookie)
		req.Header.Set("Authorization", c.cookieAuth.authorization(time.Now()))
		req.Header.Set("X-Goog-Authuser", strconv.Itoa(c.cookieAuth.accountIndex))
		if c.cookieAuth.onBehalfOfUser != "" {
			req.Header.Set("X-Goog-PageId", c.cookieAuth.onBehalfOfUser)
		}
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("youtube: %s request: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(endpoint, resp)
	}
	responseBody, err := readBoundedBody(resp.Body, maxResponseBytes, endpoint+" response")
	if err != nil {
		return nil, err
	}
	var result json.RawMessage
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return nil, fmt.Errorf("youtube: decode %s response: %w", endpoint, err)
	}
	return result, nil
}

func isYouTubeURL(endpoint *url.URL) bool {
	if endpoint.Scheme != "https" || endpoint.User != nil {
		return false
	}
	host := strings.ToLower(endpoint.Hostname())
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

func responseError(operation string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("youtube: %s returned HTTP %d: %s", operation, resp.StatusCode, strings.TrimSpace(string(data)))
}

func (c *Client) browse(ctx context.Context, browseID string) (*BrowseResult, error) {
	raw, err := c.execute(ctx, "browse", map[string]any{"browseId": browseID})
	if err != nil {
		return nil, err
	}
	return c.newBrowseResult(raw), nil
}
