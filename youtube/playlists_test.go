package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The write names the playlist in the body, speaks as the Music client, and
// answers with the ID YouTube gave it.
func TestCreatePlaylistPostsTheNameAndReadsTheID(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/youtubei/v1/playlist/create" {
			t.Errorf("request path = %q", r.URL.Path)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q", r.Method)
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("Authorization = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["title"] != "Late night" {
			t.Errorf("title = %#v", request["title"])
		}
		if request["privacyStatus"] != playlistPrivacy {
			t.Errorf("privacyStatus = %#v, want %q", request["privacyStatus"], playlistPrivacy)
		}
		if description, ok := request["description"].(string); !ok || description != "" {
			t.Errorf("description = %#v, want an empty one", request["description"])
		}
		client := request["context"].(map[string]any)["client"].(map[string]any)
		if client["clientName"] != "WEB_REMIX" {
			t.Errorf("write client context = %#v", client)
		}
		if request["isAudioOnly"] != true {
			t.Errorf("the write does not speak as the Music client: %#v", request)
		}
		_, _ = w.Write([]byte(`{"playlistId":"PLnew"}`))
	}))
	defer server.Close()

	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	id, err := client.CreatePlaylist(context.Background(), "  Late night  ")
	if err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if id != "PLnew" {
		t.Errorf("playlist id = %q, want the one YouTube answered", id)
	}
	if requests != 1 {
		t.Errorf("the write was sent %d times", requests)
	}
}

// A 200 that carries no playlist is a refusal: YouTube answers an account it
// will not let make one with a panel that asks for the feature.
func TestCreatePlaylistReportsARefusal(t *testing.T) {
	refusals := []struct {
		name string
		body string
	}{
		{"a panel that asks for the feature", `{"actions":[{"showEngagementPanelEndpoint":{"identifier":{"tag":"PAfeature_enablement"}}}]}`},
		{"no result at all", `{}`},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(refusal.body))
			}))
			defer server.Close()

			auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
			if err != nil {
				t.Fatal(err)
			}
			client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
			id, err := client.CreatePlaylist(context.Background(), "Late night")
			if err == nil || id != "" {
				t.Fatalf("a refused write returned %q, %v", id, err)
			}
			if !strings.Contains(err.Error(), "made no change") {
				t.Errorf("refusal error = %q", err)
			}
			if wanted := strings.Contains(refusal.body, "PAfeature_enablement"); wanted != strings.Contains(err.Error(), "PAfeature_enablement") {
				t.Errorf("refusal error = %q, want the panel's tag named only when it has one", err)
			}
		})
	}
}

// A name YouTube will not take is refused before a request is made.
func TestCreatePlaylistChecksTheName(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"playlistId":"PLnew"}`))
	}))
	defer server.Close()

	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	for _, name := range []string{"", "   ", "Late <night>", "a > b"} {
		if id, err := client.CreatePlaylist(context.Background(), name); err == nil {
			t.Errorf("CreatePlaylist(%-14q) returned %q", name, id)
		}
	}
	if requests != 0 {
		t.Errorf("the name check still sent %d requests", requests)
	}
}

func TestCreatePlaylistNeedsASession(t *testing.T) {
	client := NewClient(Options{APIKey: "test-key"})
	if id, err := client.CreatePlaylist(context.Background(), "Late night"); err == nil {
		t.Fatalf("a signed-out write returned %q", id)
	}
}
