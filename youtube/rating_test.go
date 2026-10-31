package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// writeClient is a signed-in client whose requests are answered with status
// and body, and whose last request's path and payload are recorded.
func writeClient(t *testing.T, status int, body string) (*Client, *string, *map[string]any) {
	t.Helper()
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var path string
	var payload map[string]any
	client := NewClient(Options{
		CookieAuth: auth,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			path = request.URL.Path
			payload = nil
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		})},
	})
	return client, &path, &payload
}

func TestRateTrackRefusals(t *testing.T) {
	if err := NewClient(Options{}).RateTrack(context.Background(), "vid-1", Liked); err == nil {
		t.Error("a signed-out like was sent")
	}
	client, path, _ := writeClient(t, http.StatusOK, `{}`)
	if err := client.RateTrack(context.Background(), "  ", Liked); err == nil || *path != "" {
		t.Errorf("a like without a video was sent to %q: %v", *path, err)
	}
	client, _, _ = writeClient(t, http.StatusBadRequest, `{"error":{"code":400,"message":"Request contains an invalid argument."}}`)
	if err := client.RateTrack(context.Background(), "vid-1", Liked); err == nil || !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("a 400 was not an error: %v", err)
	}
	client, _, _ = writeClient(t, http.StatusOK, `{"error":{"message":"Precondition check failed."}}`)
	if err := client.RateTrack(context.Background(), "vid-1", Liked); err == nil || !strings.Contains(err.Error(), "Precondition check failed.") {
		t.Errorf("an error object in a 200 was not an error: %v", err)
	}
}

func TestRateTrackUsesEachRatingEndpoint(t *testing.T) {
	for _, test := range []struct {
		status   LikeStatus
		endpoint string
	}{
		{Liked, "/youtubei/v1/like/like"},
		{Disliked, "/youtubei/v1/like/dislike"},
		{Indifferent, "/youtubei/v1/like/removelike"},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			client, path, _ := writeClient(t, http.StatusOK, `{}`)
			if err := client.RateTrack(context.Background(), "vid-1", test.status); err != nil || *path != test.endpoint {
				t.Fatalf("RateTrack(%d) = path %q, error %v; want %q", test.status, *path, err, test.endpoint)
			}
		})
	}
	client, path, _ := writeClient(t, http.StatusOK, `{}`)
	if err := client.RateTrack(context.Background(), "vid-1", LikeUnknown); err == nil || *path != "" {
		t.Fatalf("unknown rating wrote to %q: %v", *path, err)
	}
}

func TestPanelItemsCarryTheirLikeStatus(t *testing.T) {
	const response = `{"contents":{"playlistPanelRenderer":{"contents":[
		{"playlistPanelVideoRenderer":{"videoId":"a","title":{"runs":[{"text":"A"}]},"menu":{"menuRenderer":{"topLevelButtons":[{"likeButtonRenderer":{"likeStatus":"LIKE"}}]}}}},
		{"playlistPanelVideoRenderer":{"videoId":"b","title":{"runs":[{"text":"B"}]},"menu":{"menuRenderer":{"topLevelButtons":[{"likeButtonRenderer":{"likeStatus":"INDIFFERENT"}}]}}}},
		{"playlistPanelVideoRenderer":{"videoId":"c","title":{"runs":[{"text":"C"}]},"menu":{"menuRenderer":{"topLevelButtons":[{"likeButtonRenderer":{"likeStatus":"SOMETHING_NEW"}}]}}}},
		{"playlistPanelVideoRenderer":{"videoId":"d","title":{"runs":[{"text":"D"}]}}}
	]}}}`
	result := upNextResult(decodeResponse(json.RawMessage(response)))
	got := map[string]LikeStatus{}
	for _, item := range result.Items {
		got[item.VideoID] = item.Like
	}
	want := map[string]LikeStatus{"a": Liked, "b": Indifferent, "c": LikeUnknown, "d": LikeUnknown}
	for id, status := range want {
		if got[id] != status {
			t.Errorf("track %s: like %d, want %d", id, got[id], status)
		}
	}
}
