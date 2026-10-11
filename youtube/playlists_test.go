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

// The write names the playlist without the prefix a browse ID carries, and the
// video it adds.
func TestAddToPlaylistPostsTheActionAndTheVideo(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/youtubei/v1/browse/edit_playlist" {
			t.Errorf("request path = %q", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("Authorization = %q", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request["playlistId"] != "PLabc" {
			t.Errorf("playlistId = %#v, want the id without the VL prefix a browse ID carries", request["playlistId"])
		}
		actions, ok := request["actions"].([]any)
		if !ok || len(actions) != 1 {
			t.Fatalf("actions = %#v", request["actions"])
		}
		action, _ := actions[0].(map[string]any)
		if action["action"] != "ACTION_ADD_VIDEO" || action["addedVideoId"] != "vid-1" {
			t.Errorf("action = %#v", action)
		}
		client := request["context"].(map[string]any)["client"].(map[string]any)
		if client["clientName"] != "WEB_REMIX" {
			t.Errorf("write client context = %#v", client)
		}
		_, _ = w.Write([]byte(`{"status":"STATUS_SUCCEEDED"}`))
	}))
	defer server.Close()

	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	if err := client.AddToPlaylist(context.Background(), "VLPLabc", "vid-1"); err != nil {
		t.Fatalf("AddToPlaylist: %v", err)
	}
	if requests != 1 {
		t.Errorf("the write was sent %d times", requests)
	}
}

// A status that does not say it succeeded is a refusal: YouTube answers a
// video the playlist already holds with one, in an ordinary response.
func TestAddToPlaylistReportsARefusal(t *testing.T) {
	refusals := []struct {
		name string
		body string
		want string
	}{
		{"a refused edit", `{"status":"STATUS_FAILED"}`, "STATUS_FAILED"},
		{"a panel that asks for the feature", `{"actions":[{"showEngagementPanelEndpoint":{"identifier":{"tag":"PAfeature_enablement"}}}]}`, "PAfeature_enablement"},
		{"no result at all", `{}`, "made no change"},
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
			err = client.AddToPlaylist(context.Background(), "VLPLabc", "vid-1")
			if err == nil {
				t.Fatal("a refused edit was reported as made")
			}
			if !strings.Contains(err.Error(), refusal.want) {
				t.Errorf("refusal error = %q, want it to name %q", err, refusal.want)
			}
		})
	}
}

// A write with nothing to write is refused before a request is made.
func TestAddToPlaylistChecksItsArguments(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"status":"STATUS_SUCCEEDED"}`))
	}))
	defer server.Close()

	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	for _, write := range [][2]string{{"", "vid-1"}, {"   ", "vid-1"}, {"VLPLabc", ""}, {"", ""}} {
		if err := client.AddToPlaylist(context.Background(), write[0], write[1]); err == nil {
			t.Errorf("AddToPlaylist(%q, %q) was accepted", write[0], write[1])
		}
	}
	if requests != 0 {
		t.Errorf("the argument check still sent %d requests", requests)
	}
	if err := NewClient(Options{APIKey: "test-key"}).AddToPlaylist(context.Background(), "VLPLabc", "vid-1"); err == nil {
		t.Error("a signed-out write was accepted")
	}
}

// A playlist made with songs is made in one write, which spares it the first
// edit YouTube refuses until a new playlist settles.
func TestCreatePlaylistSeedsTheVideosItIsGiven(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request = nil
		_ = json.NewDecoder(r.Body).Decode(&request)
		_, _ = w.Write([]byte(`{"playlistId":"PLnew"}`))
	}))
	defer server.Close()

	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	if _, err := client.CreatePlaylist(context.Background(), "Late night", "vid-1", "", "vid-1", "vid-2"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	seeded, ok := request["videoIds"].([]any)
	if !ok || len(seeded) != 2 || seeded[0] != "vid-1" || seeded[1] != "vid-2" {
		t.Errorf("videoIds = %#v, want the videos given, without the blank or the repeat", request["videoIds"])
	}

	if _, err := client.CreatePlaylist(context.Background(), "Late night"); err != nil {
		t.Fatalf("CreatePlaylist: %v", err)
	}
	if _, ok := request["videoIds"]; ok {
		t.Errorf("an empty playlist was seeded with %#v", request["videoIds"])
	}
}
