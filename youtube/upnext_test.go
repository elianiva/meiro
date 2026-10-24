package youtube

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGetUpNextSeedsALoneTrackWithItsOwnMix(t *testing.T) {
	var payloads []map[string]any
	client := NewClient(Options{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		payloads = append(payloads, payload)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: r}, nil
	})}})
	index := 3
	for _, options := range []UpNextOptions{
		{VideoID: "vid"},
		{VideoID: "vid", PlaylistID: "PLx", PlaylistIndex: &index},
	} {
		if _, err := client.GetUpNext(context.Background(), options); err != nil {
			t.Fatal(err)
		}
	}
	if payloads[0]["playlistId"] != "RDAMVMvid" {
		t.Errorf("a lone track is not seeded with its own mix: %v", payloads[0])
	}
	if payloads[1]["playlistId"] != "PLx" || payloads[1]["playlistIndex"] != float64(3) {
		t.Errorf("a playlist's own queue was replaced: %v", payloads[1])
	}
}

func TestLoudnessUsesTheSignedInWebPlayer(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	client := NewClient(Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		body := `{"playabilityStatus":{"status":"OK"},"playerConfig":{"audioConfig":{"loudnessDb":2.290}}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}})
	got, err := client.Loudness(context.Background(), "vid")
	if err != nil {
		t.Fatal(err)
	}
	if got.LoudnessDB != 2.290 {
		t.Errorf("loudness = %#v, want 2.290", got)
	}
	contextPayload, ok := payload["context"].(map[string]any)
	if !ok {
		t.Fatalf("request context = %#v", payload["context"])
	}
	clientPayload, ok := contextPayload["client"].(map[string]any)
	if !ok || clientPayload["clientName"] != "WEB" || payload["videoId"] != "vid" {
		t.Errorf("web player request = %#v", payload)
	}
}

func TestReportPlaybackPingsTheTrackingURL(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ping *http.Request
	client := NewClient(Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"playbackTracking":{"videostatsPlaybackUrl":{"baseUrl":"https://s.youtube.com/api/stats/playback?docid=vid"}}}`
		if r.Method == http.MethodGet {
			ping, body = r, ""
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}})
	if err := client.ReportPlayback(context.Background(), "vid"); err != nil {
		t.Fatal(err)
	}
	q := ping.URL.Query()
	if q.Get("docid") != "vid" || q.Get("ver") != "2" || q.Get("c") != "WEB_REMIX" || q.Get("state") != "" || len(q.Get("cpn")) != 16 || ping.Header.Get("Cookie") == "" {
		t.Errorf("ping %s, cookie %q", ping.URL, ping.Header.Get("Cookie"))
	}
	if err := NewClient(Options{}).ReportPlayback(context.Background(), "vid"); err != nil {
		t.Errorf("signed out must do nothing: %v", err)
	}
}

func TestEpisodePlaybackReportsIntervalsUnderItsPlaybackNonce(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=test", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var started, heartbeat, stopped *http.Request
	client := NewClient(Options{CookieAuth: auth, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"playbackTracking":{
			"videostatsPlaybackUrl":{"baseUrl":"https://s.youtube.com/api/stats/playback?docid=ep&ei=event&vm=token&of=sig&plid=player"},
			"videostatsWatchtimeUrl":{"baseUrl":"https://s.youtube.com/api/stats/watchtime?docid=ep&ei=event&vm=token&of=sig&plid=player"}
		}}`
		if r.Method == http.MethodGet {
			switch r.URL.Path {
			case "/api/stats/playback":
				started = r
			case "/api/stats/watchtime":
				if r.URL.Query().Get("final") == "1" {
					stopped = r
				} else {
					heartbeat = r
				}
			}
			body = ""
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}})

	session, err := client.BeginEpisodePlayback(context.Background(), "ep", 3*time.Minute, 29*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ReportWatchtime(context.Background(), 3*time.Minute, 3*time.Minute+30*time.Second, 29*time.Minute, "playing", false); err != nil {
		t.Fatal(err)
	}
	if err := session.ReportWatchtime(context.Background(), 3*time.Minute+30*time.Second, 4*time.Minute, 29*time.Minute, "paused", true); err != nil {
		t.Fatal(err)
	}
	if started == nil || heartbeat == nil || stopped == nil {
		t.Fatalf("tracking requests: start=%v heartbeat=%v stop=%v", started, heartbeat, stopped)
	}
	cpn := started.URL.Query().Get("cpn")
	if len(cpn) != 16 || started.URL.Query().Get("cmt") != "180.000" || started.URL.Query().Get("len") != "1740.000" {
		t.Errorf("playback ping query = %v", started.URL.Query())
	}
	for _, request := range []*http.Request{started, heartbeat, stopped} {
		q := request.URL.Query()
		if q.Get("docid") != "ep" || q.Get("ei") != "event" || q.Get("vm") != "token" || q.Get("of") != "sig" || q.Get("plid") != "player" || q.Get("cpn") != cpn || request.Header.Get("Cookie") == "" {
			t.Errorf("tracking request %s, cookie %q", request.URL, request.Header.Get("Cookie"))
		}
	}
	if q := heartbeat.URL.Query(); q.Get("st") != "180.000" || q.Get("et") != "210.000" || q.Get("cmt") != "210.000" || q.Get("state") != "playing" {
		t.Errorf("heartbeat query = %v", q)
	}
	if q := stopped.URL.Query(); q.Get("st") != "210.000" || q.Get("et") != "240.000" || q.Get("cmt") != "240.000" || q.Get("state") != "paused" || q.Get("final") != "1" {
		t.Errorf("final query = %v", q)
	}
}

func TestUpNextTellsSongsFromVideos(t *testing.T) {
	root := decodeResponse(json.RawMessage(`{"contents":{"playlistPanelRenderer":{"contents":[
		{"playlistPanelVideoRenderer":{"videoId":"song","title":{"runs":[{"text":"Song"}]},"navigationEndpoint":{"watchEndpoint":{"watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_ATV"}}}}}},
		{"playlistPanelVideoRenderer":{"videoId":"clip","title":{"runs":[{"text":"Clip"}]},"navigationEndpoint":{"watchEndpoint":{"watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_OMV"}}}}}}
	]}}}`))
	items := upNextResult(root).Items
	if len(items) != 2 || items[0].IsVideo() || !items[1].IsVideo() {
		t.Errorf("song and video not told apart: %+v", items)
	}
}

// The cards of an artist's Videos and Live performances shelves are drawn as
// songs' are; only their watch endpoint says they have a picture.
func TestCardsTellSongsFromVideos(t *testing.T) {
	card := func(id, videoType string) MusicItem {
		item, _, ok := parseMusicRenderer("musicTwoRowItemRenderer", decodeResponse(json.RawMessage(`{"title":{"runs":[{"text":"`+id+`"}]},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"`+id+`","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"`+videoType+`"}}}}}`)))
		if !ok {
			t.Fatalf("card %s not parsed", id)
		}
		return item
	}
	if song := card("song", "MUSIC_VIDEO_TYPE_ATV"); song.IsVideo() {
		t.Errorf("a song's card is a video: %+v", song)
	}
	if clip := card("clip", "MUSIC_VIDEO_TYPE_OMV"); !clip.IsVideo() {
		t.Errorf("a music video's card is not a video: %+v", clip)
	}
}
