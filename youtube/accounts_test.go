package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAccountChannelsReadBrandIDsFromIdentityTokens(t *testing.T) {
	const response = `{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
		{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"isSelected":true,"hasChannel":true}},
		{"accountItemRenderer":{"accountName":{"simpleText":"Brand channel"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[
			{"accountSigninToken":{"signinUrl":"/signin?authuser=1"}},
			{"pageIdToken":{"pageId":"UC-brand"}}
		]}}}}
	]}}]}}`

	channels := extractAccountChannels(decodeResponse(json.RawMessage(response)), 0)
	if len(channels) != 2 {
		t.Fatalf("got %d account channels, want 2: %+v", len(channels), channels)
	}
	if got := channels[0]; got.Name != "Main channel" || !got.Selected || got.ChannelID != "" || got.AccountIndex != 0 {
		t.Errorf("primary account = %+v, want selected without a page ID", got)
	}
	if got := channels[1]; got.Name != "Brand channel" || got.Selected || got.ChannelID != "UC-brand" || got.AccountIndex != 1 {
		t.Errorf("brand account = %+v, want page ID UC-brand and account index 1", got)
	}
}

func TestAccountChannelIndices(t *testing.T) {
	for _, test := range []struct {
		name     string
		url      string
		index    int
		disabled bool
	}{
		{name: "default", url: "/signin?authuser=0", index: 0},
		{name: "secondary", url: "/signin?action_handle_signin=true&authuser=2&next=%2F", index: 2},
		{name: "absolute", url: "https://www.youtube.com/signin?authuser=3", index: 3},
		{name: "protocol relative", url: "//www.youtube.com/signin?authuser=4", index: 4},
		{name: "missing index", url: "/signin?next=%2F", index: -1, disabled: true},
		{name: "empty URL", index: -1, disabled: true},
		{name: "empty index", url: "/signin?authuser=", index: -1, disabled: true},
		{name: "negative", url: "/signin?authuser=-1", index: -1, disabled: true},
		{name: "email", url: "/signin?authuser=person@example.com", index: -1, disabled: true},
		{name: "signed", url: "/signin?authuser=%2B1", index: -1, disabled: true},
		{name: "overflow", url: "/signin?authuser=999999999999999999999999999999", index: -1, disabled: true},
		{name: "bad escape", url: "/signin?authuser=%zz", index: -1, disabled: true},
		{name: "bad URL", url: "https://[invalid/signin?authuser=2", index: -1, disabled: true},
		{name: "duplicate indices", url: "/signin?authuser=1&authuser=2", index: -1, disabled: true},
		{name: "invalid other query", url: "/signin?authuser=1&next=%zz", index: -1, disabled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			encodedURL, err := json.Marshal(test.url)
			if err != nil {
				t.Fatal(err)
			}
			response := `{"accountItem":{"accountName":{"simpleText":"Primary"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":` + string(encodedURL) + `}}]}}}}`
			channels := extractAccountChannels(decodeResponse(json.RawMessage(response)), 7)
			if len(channels) != 1 {
				t.Fatalf("got %d channels, want 1", len(channels))
			}
			got := channels[0]
			if got.AccountIndex != test.index || got.Disabled != test.disabled || got.ChannelID != "" || !got.HasChannel {
				t.Errorf("primary account = %+v, want index %d, disabled %t, empty channel ID", got, test.index, test.disabled)
			}
		})
	}
}

func TestAccountChannelsDeduplicateWithinGoogleAccount(t *testing.T) {
	const response = `{"contents":[
		{"accountItemRenderer":{"accountName":{"simpleText":"Same name"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=0"}}]}}}},
		{"accountItemRenderer":{"accountName":{"simpleText":"Same name"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=2"}}]}}}},
		{"accountItem":{"accountName":{"simpleText":"Same name"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=2"}}]}}}},
		{"accountItem":{"accountName":{"simpleText":"Same name"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=bad"}}]}}}}
	]}`
	channels := extractAccountChannels(decodeResponse(json.RawMessage(response)), 0)
	if len(channels) != 3 {
		t.Fatalf("got %d channels, want 3: %+v", len(channels), channels)
	}
	for i, index := range []int{0, 2, -1} {
		if got := channels[i]; got.AccountIndex != index || got.Disabled != (index < 0) {
			t.Errorf("channel %d = %+v, want index %d", i, got, index)
		}
	}
}

func TestGetAccountsDefaultsToClientAccountIndex(t *testing.T) {
	const response = `{"contents":[
		{"accountItemRenderer":{"accountName":{"simpleText":"Primary"},"hasChannel":true}},
		{"accountItemRenderer":{"accountName":{"simpleText":"Brand"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"pageIdToken":{"pageId":"UC-brand"}}]}}}},
		{"accountItemRenderer":{"accountName":{"simpleText":"Other user"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=3"}}]}}}}
	]}`
	auth, err := NewCookieAuth("SAPISID=test-only", CookieOptions{AccountIndex: 1})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{
		CookieAuth: auth,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if got := r.Header.Get("X-Goog-AuthUser"); got != "1" {
				t.Errorf("X-Goog-AuthUser = %q, want 1", got)
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
		})},
	})
	accounts, err := client.GetAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.Items) != 3 {
		t.Fatalf("got %d accounts, want 3", len(accounts.Items))
	}
	for i, index := range []int{1, 1, 3} {
		if got := accounts.Items[i]; got.AccountIndex != index || got.Disabled {
			t.Errorf("account %d = %+v, want enabled with index %d", i, got, index)
		}
	}
	if accounts.Items[0].ChannelID != "" || accounts.Items[1].ChannelID != "UC-brand" {
		t.Errorf("unexpected channel IDs: %+v", accounts.Items)
	}
}

func TestSignInAccountsUseTheWebSwitcher(t *testing.T) {
	const response = `{"data":{"actions":[{"getMultiPageMenuAction":{"menu":{"multiPageMenuRenderer":{"sections":[
		{"accountSectionListRenderer":{"header":{"googleAccountHeaderRenderer":{"email":{"simpleText":"personal@example.test"}}},"contents":[{"accountItemSectionRenderer":{"contents":[
			{"accountItem":{"accountName":{"simpleText":"Personal"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=0"}}]}}}}
		]}}]}},
		{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
			{"accountItem":{"accountName":{"simpleText":"Music"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=2"}}]}}}}
		]}}],"header":{"googleAccountHeaderRenderer":{"email":{"simpleText":"music@example.test"}}}}}
	]}}}}]}}`
	for _, body := range []string{response, ")]}'\n" + response, "<html>Sign in</html>"} {
		auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
		if err != nil {
			t.Fatal(err)
		}
		client := NewClient(Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodGet || r.URL.String() != "https://www.youtube.com/getAccountSwitcherEndpoint" {
				t.Fatalf("account switcher request = %s %s", r.Method, r.URL)
			}
			if r.Header.Get("Cookie") != "SAPISID=test" || r.Header.Get("X-Goog-AuthUser") != "0" {
				t.Fatal("account switcher request omitted the browser session")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		})}})
		list, err := client.GetSignInAccounts(context.Background())
		if strings.HasPrefix(body, "<html>") {
			if err == nil {
				t.Fatal("an HTML sign-in page was accepted as an account list")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Items) != 2 || list.Items[0].AccountIndex != 0 || list.Items[1].AccountIndex != 2 || list.Items[1].Name != "Music" {
			t.Fatalf("Google account choices = %+v", list.Items)
		}
		if list.Items[0].Email != "personal@example.test" || list.Items[1].Email != "music@example.test" {
			t.Fatalf("account section emails = %+v", list.Items)
		}
	}
}

func TestSignInAccountsRejectUnsafeHosts(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"https://example.test", "http://www.youtube.com"} {
		client := NewClient(Options{BaseURL: base, CookieAuth: auth, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Fatal("credentials were sent to an unsafe host")
			return nil, nil
		})}})
		if _, err := client.GetSignInAccounts(context.Background()); err == nil {
			t.Fatal("unsafe cookie destination was accepted")
		}
	}
}
