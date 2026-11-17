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
	"sync"
	"time"
)

const (
	defaultAPIURL        = "https://music.youtube.com"
	defaultMusicVersion  = "1.20250219.01.00"
	defaultWebVersion    = "2.20260623.01.00"
	defaultMusicContext  = "WEB_REMIX"
	defaultMusicClientID = "67"
)

const maxResponseBytes = 64 << 20

const defaultResponseHeaderTimeout = 30 * time.Second

const browserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0.0.0 Safari/537.36"

type Options struct {
	HTTPClient              *http.Client
	BaseURL                 string
	APIKey                  string
	ClientVersion           string
	WebClientVersion        string
	VisitorData             string
	Language                string
	Country                 string
	CookieAuth              *CookieAuth
	AllowInsecureCookieAuth bool
}

type Client struct {
	httpClient              *http.Client
	baseURL                 string
	apiKey                  string
	clientVersion           string
	webClientVersion        string
	visitorDataMu           sync.RWMutex
	visitorData             string
	language                string
	country                 string
	cookieAuth              *CookieAuth
	allowInsecureCookieAuth bool
}

var defaultHTTPClient = func() *http.Client {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{Proxy: http.ProxyFromEnvironment}
	}
	transport := base.Clone()
	transport.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	return &http.Client{Transport: transport}
}()

func NewClient(options Options) *Client {
	if options.HTTPClient == nil {
		options.HTTPClient = defaultHTTPClient
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
		DeviceMake    string `json:"deviceMake,omitempty"`
		DeviceModel   string `json:"deviceModel,omitempty"`
		UserAgent     string `json:"userAgent,omitempty"`
		OSName        string `json:"osName,omitempty"`
		OSVersion     string `json:"osVersion,omitempty"`
	} `json:"client"`
	User *clientUserContext `json:"user,omitempty"`
}

type clientUserContext struct {
	OnBehalfOfUser string `json:"onBehalfOfUser,omitempty"`
}

func (c *Client) context() clientContext {
	var ctx clientContext
	ctx.Client.HL = c.language
	ctx.Client.GL = c.country
	if c.cookieAuth == nil {
		ctx.Client.VisitorData = c.currentVisitorData()
	}
	return ctx
}

func (c *Client) currentVisitorData() string {
	c.visitorDataMu.RLock()
	defer c.visitorDataMu.RUnlock()
	return c.visitorData
}

func (c *Client) rememberVisitorData(visitorData string) {
	if visitorData == "" {
		return
	}
	c.visitorDataMu.Lock()
	if c.visitorData == "" {
		c.visitorData = visitorData
	}
	c.visitorDataMu.Unlock()
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

type innerTubeClient string

const (
	musicClient        innerTubeClient = "YTMUSIC"
	webClient          innerTubeClient = "WEB"
	androidMusicClient innerTubeClient = "ANDROID_MUSIC"
)

const visionOSClient innerTubeClient = "VISIONOS"

const (
	visionOSVersion   = "1.02"
	visionOSUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15"
)

const (
	androidMusicVersion   = "7.21.50"
	androidMusicUserAgent = "com.google.android.apps.youtube.music/" + androidMusicVersion + " (Linux; U; Android 14) gzip"
)

func (c *Client) execute(ctx context.Context, endpoint string, payload map[string]any) (json.RawMessage, error) {
	return c.executeAs(ctx, musicClient, true, endpoint, payload)
}

func (c *Client) executeAs(ctx context.Context, client innerTubeClient, audioOnly bool, endpoint string, payload map[string]any) (json.RawMessage, error) {
	var clientName, clientID, clientVersion string
	switch client {
	case musicClient:
		clientName, clientID, clientVersion = defaultMusicContext, defaultMusicClientID, c.clientVersion
	case webClient:
		clientName, clientID, clientVersion = "WEB", "1", c.webClientVersion
	case androidMusicClient:
		clientName, clientID, clientVersion = "ANDROID_MUSIC", "21", androidMusicVersion
	case visionOSClient:
		clientName, clientID, clientVersion = "VISIONOS", "101", visionOSVersion
	}
	requestContext := c.context()
	requestContext.Client.ClientName = clientName
	requestContext.Client.ClientVersion = clientVersion
	if client == visionOSClient {
		device := &requestContext.Client
		device.DeviceMake, device.DeviceModel, device.UserAgent = "Apple", "RealityDevice17,1", visionOSUserAgent
		device.OSName, device.OSVersion = "visionOS", "26.5.23O471"
	}
	if c.cookieAuth != nil {
		requestContext.User = &clientUserContext{OnBehalfOfUser: c.cookieAuth.onBehalfOfUser}
	}
	payload["context"] = requestContext
	if audioOnly {
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
	if client == androidMusicClient {
		req.Header.Set("User-Agent", androidMusicUserAgent)
	}
	if client == visionOSClient {
		req.Header.Set("User-Agent", visionOSUserAgent)
	}
	origin := parsedURL.Scheme + "://" + parsedURL.Host
	req.Header.Set("Origin", origin)
	req.Header.Set("Referer", origin+"/")
	req.Header.Set("X-YouTube-Client-Name", clientID)
	req.Header.Set("X-YouTube-Client-Version", clientVersion)
	c.setIdentity(req, requestContext.Client.VisitorData)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("youtube: %s request: %w", endpoint, err)
		}
		return nil, fmt.Errorf("youtube: %s request failed: %w", endpoint, ErrUnreachable)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError(endpoint, resp)
	}
	responseBody, err := readBoundedBody(resp.Body, maxResponseBytes, endpoint+" response")
	if err != nil {
		return nil, err
	}
	var responseMetadata struct {
		ResponseContext struct {
			VisitorData string `json:"visitorData"`
		} `json:"responseContext"`
	}
	if err := json.Unmarshal(responseBody, &responseMetadata); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, fmt.Errorf("youtube: decode %s response: invalid JSON", endpoint)
		}
	} else {
		c.rememberVisitorData(responseMetadata.ResponseContext.VisitorData)
	}
	return json.RawMessage(responseBody), nil
}

func (c *Client) setIdentity(req *http.Request, visitorData string) {
	if visitorData != "" && c.cookieAuth == nil {
		req.Header.Set("X-Goog-Visitor-Id", visitorData)
	}
	if c.cookieAuth != nil {
		req.Header.Set("Cookie", c.cookieAuth.cookie)
		origin := req.URL.Scheme + "://" + req.URL.Host
		req.Header.Set("Authorization", c.cookieAuth.authorization(time.Now(), origin))
		req.Header.Set("X-Goog-Authuser", strconv.Itoa(c.cookieAuth.accountIndex))
		if c.cookieAuth.onBehalfOfUser != "" {
			req.Header.Set("X-Goog-PageId", c.cookieAuth.onBehalfOfUser)
		}
	}
}

func isYouTubeURL(endpoint *url.URL) bool {
	if endpoint.Scheme != "https" || endpoint.User != nil {
		return false
	}
	host := strings.ToLower(endpoint.Hostname())
	return host == "youtube.com" || strings.HasSuffix(host, ".youtube.com")
}

var ErrUnreachable = errors.New("could not reach YouTube Music")

var ErrUnauthorized = errors.New("youtube: the session was rejected")

const maxErrorDiagnosticBytes = 256

func responseError(operation string, resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorDiagnosticBytes+1))
	diagnostic := strings.TrimSpace(string(data))
	if int64(len(data)) > maxErrorDiagnosticBytes || strings.ContainsAny(diagnostic, "<>\r\n") || strings.Contains(diagnostic, "://") || strings.Contains(strings.ToLower(diagnostic), "cookie") || strings.Contains(strings.ToLower(diagnostic), "signature=") {
		diagnostic = "server returned an unreadable error response"
	} else {
		diagnostic = strings.Map(func(r rune) rune {
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, diagnostic)
	}
	if diagnostic == "" {
		diagnostic = http.StatusText(resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("youtube: %s returned HTTP %d: %s: %w", operation, resp.StatusCode, diagnostic, ErrUnauthorized)
	}
	return fmt.Errorf("youtube: %s returned HTTP %d: %s", operation, resp.StatusCode, diagnostic)
}
