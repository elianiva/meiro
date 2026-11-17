package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"testing"
)

type playlistResponse string

func (p playlistResponse) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(string(p))),
		Request:    request,
	}, nil
}

const duplicatePlaylistResponse = `{"status":"STATUS_FAILED","actions":[{"addToToastAction":{"item":{"notificationActionRenderer":{"actionButton":{"buttonRenderer":{"command":{"playlistEditEndpoint":{"playlistId":"PL1","actions":[{"action":"ACTION_ADD_VIDEO","addedVideoId":"vid-1","dedupeOption":"DEDUPE_OPTION_SKIP"}]}}}}}}}}]}`

func TestAddToPlaylistTellsADuplicateFromARefusal(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	add := func(reply string) error {
		client := NewClient(Options{
			CookieAuth: auth,
			HTTPClient: &http.Client{Transport: playlistResponse(reply)},
		})
		return client.AddToPlaylist(context.Background(), "VLPL1", "vid-1")
	}
	if err := add(duplicatePlaylistResponse); !errors.Is(err, ErrAlreadyInPlaylist) {
		t.Fatalf("a song the playlist holds already = %v", err)
	}
	if err := add(`{"status":"STATUS_FAILED"}`); err == nil || errors.Is(err, ErrAlreadyInPlaylist) {
		t.Fatalf("a plain refusal = %v", err)
	}
}

func TestRemoveFromPlaylistSendsOccurrenceID(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	client := NewClient(Options{
		CookieAuth: auth,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{"status":"STATUS_SUCCEEDED"}`)),
				Request:    request,
			}, nil
		})},
	})
	if err := client.RemoveFromPlaylist(context.Background(), "VLPL1", "entry-1"); err != nil {
		t.Fatal(err)
	}
	if got["playlistId"] != "PL1" {
		t.Fatalf("playlist ID = %#v", got["playlistId"])
	}
	actions, ok := got["actions"].([]any)
	if !ok || len(actions) != 1 {
		t.Fatalf("actions = %#v", got["actions"])
	}
	action, ok := actions[0].(map[string]any)
	if !ok || action["action"] != "ACTION_REMOVE_VIDEO" || action["setVideoId"] != "entry-1" {
		t.Fatalf("action = %#v", actions[0])
	}
}

func TestRemoveFromPlaylistRefusal(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{
		CookieAuth: auth,
		HTTPClient: &http.Client{Transport: playlistResponse(`{"status":"STATUS_INVALID_PLAYLIST_SET_VIDEO_ID"}`)},
	})
	var logged strings.Builder
	flags := log.Flags()
	log.SetOutput(&logged)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	}()
	if err := client.RemoveFromPlaylist(context.Background(), "VLPL1", "entry-1"); err == nil || !strings.Contains(err.Error(), "STATUS_INVALID_PLAYLIST_SET_VIDEO_ID") {
		t.Fatalf("refusal = %v", err)
	}
	if !strings.Contains(logged.String(), "body=") || !strings.Contains(logged.String(), "STATUS_INVALID_PLAYLIST_SET_VIDEO_ID") {
		t.Errorf("the refusal is not in the log in full: %q", logged.String())
	}
}
