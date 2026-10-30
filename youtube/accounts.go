package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
)

// AccountDetails contains the active account response.
type AccountDetails struct {
	Name      string `json:"name,omitempty"`
	Email     string `json:"email,omitempty"`
	ChannelID string `json:"channelId,omitempty"`
	Thumbnail string `json:"thumbnail,omitempty"`
}

// AccountChannel describes a channel in the account switcher.
type AccountChannel struct {
	// AccountIndex is the Google authuser index, or -1 for a malformed signin
	// token (which also disables the item). Missing tokens use the client index.
	AccountIndex int    `json:"accountIndex"`
	Name         string `json:"name,omitempty"`
	Email        string `json:"email,omitempty"`
	Byline       string `json:"byline,omitempty"`
	Handle       string `json:"handle,omitempty"`
	ChannelID    string `json:"channelId,omitempty"`
	Thumbnail    string `json:"thumbnail,omitempty"`
	Selected     bool   `json:"selected,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
	HasChannel   bool   `json:"hasChannel,omitempty"`
}

// AccountList contains channels available to a cookie-authenticated account.
type AccountList struct {
	Items []AccountChannel `json:"items"`
}

// GetAccountDetails returns the active signed-in account. It requires cookie
// authentication.
func (c *Client) GetAccountDetails(ctx context.Context) (*AccountDetails, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccountDetails requires cookie authentication")
	}
	raw, err := c.execute(ctx, "account/account_menu", map[string]any{})
	if err != nil {
		return nil, err
	}
	details := &AccountDetails{}
	root := decodeResponse(raw)
	// The account menu names the active account, its email, and its photo in
	// one header renderer.
	if header := findRenderer(root, "activeAccountHeaderRenderer"); header != nil {
		details.Name = rendererText(header.get("accountName"))
		details.Email = rendererText(header.get("email"))
		details.Thumbnail = rendererThumbnail(header.get("accountPhoto"))
	}
	// The header carries no channel ID; the menu's account list does.
	for _, channel := range extractAccountChannels(root, c.cookieAuth.accountIndex) {
		if !channel.Selected || channel.AccountIndex != c.cookieAuth.accountIndex {
			continue
		}
		details.ChannelID = channel.ChannelID
		if details.Thumbnail == "" {
			details.Thumbnail = channel.Thumbnail
		}
		break
	}
	// Music can omit the email in an otherwise valid account menu. Resolve
	// it from the web switcher's Google-account section, never from a name
	// or a row's position (multiple accounts can have the same name).
	if details.Email == "" && (details.Name != "" || details.ChannelID != "") {
		accounts, err := c.GetSignInAccounts(ctx)
		if err != nil {
			return nil, err
		}
		for _, account := range accounts.Items {
			if !account.Disabled && account.AccountIndex == c.cookieAuth.accountIndex && account.Email != "" {
				details.Email = account.Email
				break
			}
		}
	}
	return details, nil
}

// GetAccounts lists all channels available to a cookie-authenticated account.
func (c *Client) GetAccounts(ctx context.Context) (*AccountList, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetAccounts requires cookie authentication")
	}
	raw, err := c.executeAs(ctx, webClient, false, "account/accounts_list", map[string]any{
		"requestType":      "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER",
		"callCircumstance": "SWITCHING_USERS_FULL",
	})
	if err != nil {
		return nil, err
	}
	return &AccountList{Items: extractAccountChannels(decodeResponse(raw), c.cookieAuth.accountIndex)}, nil
}

// GetSignInAccounts reads the web account switcher, which includes other Google
// accounts sharing these cookies, not just the current account's channels.
func (c *Client) GetSignInAccounts(ctx context.Context) (*AccountList, error) {
	if c.cookieAuth == nil {
		return nil, errors.New("youtube: GetSignInAccounts requires cookie authentication")
	}
	base := c.baseURL
	if base == defaultAPIURL {
		base = "https://www.youtube.com"
	}
	endpoint, err := url.Parse(base + "/getAccountSwitcherEndpoint")
	if err != nil {
		return nil, errors.New("youtube: invalid account switcher URL")
	}
	if !c.allowInsecureCookieAuth && !isYouTubeURL(endpoint) {
		return nil, errors.New("youtube: refusing to send cookie authentication to a non-YouTube or non-HTTPS URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", browserUserAgent)
	req.Header.Set("Accept", "application/json")
	c.setIdentity(req, "")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseError("account switcher", resp)
	}
	body, err := readBoundedBody(resp.Body, maxResponseBytes, "account switcher response")
	if err != nil {
		return nil, err
	}
	// This web endpoint prefixes its JSON with an XSSI guard.
	body = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(body), []byte(")]}'")))
	if !json.Valid(body) {
		return nil, errors.New("youtube: account switcher returned invalid JSON")
	}
	return &AccountList{Items: extractAccountChannels(decodeResponse(body), c.cookieAuth.accountIndex)}, nil
}

func extractAccountChannels(root any, defaultIndex int) []AccountChannel {
	var channels []AccountChannel
	type accountIdentity struct {
		index                   int
		channelID, name, handle string
	}
	seen := make(map[accountIdentity]struct{})
	appendChannel := func(channel AccountChannel, email string) {
		channel.Email = email
		if channel.ChannelID == "" && channel.Name == "" && channel.Handle == "" {
			return
		}
		identity := accountIdentity{channel.AccountIndex, channel.ChannelID, channel.Name, channel.Handle}
		if _, exists := seen[identity]; exists {
			return
		}
		seen[identity] = struct{}{}
		channels = append(channels, channel)
	}
	var walk func(any, string)
	walk = func(value any, email string) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				walk(child, email)
			}
		case *jsonObject:
			if header, ok := nestedObject(node.get("header"), "googleAccountHeaderRenderer"); ok {
				email = rendererText(header.get("email"))
			}
			if hasAccountChannelFields(node) {
				appendChannel(parseAccountChannel(node, defaultIndex), email)
				return
			}
			for _, member := range node.members {
				child, ok := member.value.(*jsonObject)
				if !ok {
					walk(member.value, email)
					continue
				}
				if member.key == "accountItemRenderer" || member.key == "accountItem" {
					appendChannel(parseAccountChannel(child, defaultIndex), email)
					continue
				}
				if hasAccountChannelFields(child) {
					appendChannel(parseAccountChannel(child, defaultIndex), email)
					continue
				}
				walk(child, email)
			}
		}
	}
	walk(root, "")
	return channels
}

func parseAccountChannel(renderer *jsonObject, defaultIndex int) AccountChannel {
	channel := AccountChannel{
		Name:       rendererText(renderer.get("accountName")),
		Byline:     rendererText(renderer.get("accountByline")),
		Handle:     rendererText(renderer.get("channelHandle")),
		Thumbnail:  rendererThumbnail(renderer.get("accountPhoto")),
		Selected:   rendererBool(renderer.get("isSelected")),
		Disabled:   rendererBool(renderer.get("isDisabled")),
		HasChannel: rendererBool(renderer.get("hasChannel")),
		ChannelID:  navigationID(renderer.get("endpoint")),
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer.get("account_name"))
	}
	if channel.Name == "" {
		channel.Name = rendererText(renderer.get("name"))
	}
	if channel.ChannelID == "" {
		channel.ChannelID, _ = renderer.get("channelId").(string)
	}
	if channel.ChannelID == "" {
		channel.ChannelID = accountChannelPageID(renderer.get("serviceEndpoint"))
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer.get("handle").(string)
	}
	if channel.Handle == "" {
		channel.Handle, _ = renderer.get("channel_handle").(string)
	}
	if channel.Byline == "" {
		channel.Byline = rendererText(renderer.get("account_byline"))
	}
	if channel.Thumbnail == "" {
		channel.Thumbnail = rendererThumbnail(renderer.get("account_photo"))
	}
	channel.AccountIndex = accountChannelIndex(renderer.get("serviceEndpoint"), defaultIndex)
	if channel.AccountIndex < 0 {
		channel.Disabled = true
	}
	return channel
}

// accountChannelIndex reads authuser from the server-issued signin URL, not
// the position of the item in the list (brand channels share an account index).
// Responses without signin tokens describe the client's current Google account.
// An explicitly supplied but invalid token must not select account zero.
func accountChannelIndex(serviceEndpoint any, defaultIndex int) int {
	identity, ok := nestedObject(serviceEndpoint, "selectActiveIdentityEndpoint")
	if !ok {
		return defaultIndex
	}
	tokens, _ := identity.get("supportedTokens").([]any)
	for _, value := range tokens {
		token, ok := value.(*jsonObject)
		if !ok || token.get("accountSigninToken") == nil {
			continue
		}
		signinURL := nestedString(token.get("accountSigninToken"), "signinUrl")
		parsed, err := url.Parse(signinURL)
		if err != nil || signinURL == "" {
			return -1
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil || len(query["authuser"]) != 1 {
			return -1
		}
		value := query.Get("authuser")
		// Accept decimal indices only, not signs, spaces, or email addresses.
		if value == "" {
			return -1
		}
		for _, digit := range value {
			if digit < '0' || digit > '9' {
				return -1
			}
		}
		index, err := strconv.Atoi(value)
		if err != nil {
			return -1
		}
		return index
	}
	return defaultIndex
}

// accountChannelPageID reads the brand channel ID from the identity token the
// account switcher returns. The primary account has no pageIdToken and uses
// the empty channel ID.
func accountChannelPageID(serviceEndpoint any) string {
	identity, ok := nestedObject(serviceEndpoint, "selectActiveIdentityEndpoint")
	if !ok {
		return ""
	}
	tokens, ok := identity.get("supportedTokens").([]any)
	if !ok {
		return ""
	}
	for _, token := range tokens {
		if token, ok := token.(*jsonObject); ok {
			if pageID := nestedString(token.get("pageIdToken"), "pageId"); pageID != "" {
				return pageID
			}
		}
	}
	return ""
}

func hasAccountChannelFields(value *jsonObject) bool {
	for _, key := range []string{"channelId", "accountName", "account_name", "channelHandle", "channel_handle"} {
		if value.get(key) != nil {
			return true
		}
	}
	return false
}

func rendererBool(value any) bool {
	result, _ := value.(bool)
	return result
}
