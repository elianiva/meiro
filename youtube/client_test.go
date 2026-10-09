package youtube

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchUsesMusicContextAndMapsItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/search" {
			t.Errorf("request path = %q", r.URL.Path)
			return
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Errorf("API key = %q", r.URL.Query().Get("key"))
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if request["query"] != "ambient" || request["client"] != nil || request["isAudioOnly"] != true {
			t.Errorf("request fields = %#v", request)
		}
		clientContext := request["context"].(map[string]any)["client"].(map[string]any)
		if clientContext["clientName"] != "WEB_REMIX" || clientContext["clientVersion"] != "1.2026.test" {
			t.Errorf("client context = %#v", clientContext)
		}
		encoded, ok := request["params"].(string)
		if !ok {
			t.Errorf("song search is missing filter params")
			return
		}
		decoded, err := url.QueryUnescape(encoded)
		if err != nil {
			t.Errorf("unescaping search params: %v", err)
			return
		}
		filter, err := base64.StdEncoding.DecodeString(decoded)
		if err != nil {
			t.Errorf("decoding search params: %v", err)
			return
		}
		if len(filter) == 0 || filter[0] != 0x12 {
			t.Errorf("search filter protobuf = %x", filter)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First track"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"An artist"}]}}}],"fixedColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"simpleText":"3:42"}}}],"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/small"},{"url":"https://img.example/large"}]}}}}}],"continuations":[{"nextContinuationData":{"continuation":"SEARCH_NEXT"}}]}}}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL, APIKey: "test-key", ClientVersion: "1.2026.test"})
	result, err := client.Search(context.Background(), "ambient", SearchOptions{Type: SearchSongs})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("got %d items, want one: %#v", len(result.Items), result.Items)
	}
	item := result.Items[0]
	if item.VideoID != "track-1" || item.Title != "First track" || item.Subtitle != "An artist" || item.Duration != "3:42" || item.Thumbnail != "https://img.example/large" {
		t.Errorf("mapped item = %#v", item)
	}
	if result.ContinuationToken != "SEARCH_NEXT" {
		t.Errorf("search continuation = %q", result.ContinuationToken)
	}
}

func TestReadBoundedBodyEnforcesTheLimit(t *testing.T) {
	if got, err := readBoundedBody(strings.NewReader("1234"), 4, "test body"); err != nil || string(got) != "1234" {
		t.Fatalf("reading an exact-limit body = %q, %v", got, err)
	}
	if got, err := readBoundedBody(strings.NewReader("12345"), 4, "test body"); err == nil || got != nil {
		t.Fatalf("reading an oversized body = %q, %v, want an error and no data", got, err)
	}
}

func TestVideoRenderersKeepTheirKindAndThumbnail(t *testing.T) {
	client := NewClient(Options{})
	result := client.newSearchResult(json.RawMessage(`{"contents":{"items":[
		{"musicVideoRenderer":{"videoId":"music-video","title":{"simpleText":"Music video"},"thumbnail":{"thumbnails":[{"url":"https://img.example/music-video"}]}}},
		{"videoRenderer":{"videoId":"regular-video","title":{"simpleText":"Regular video"},"thumbnail":{"thumbnails":[{"url":"https://img.example/regular-video"}]}}}
	]}}`))
	if len(result.Items) != 2 {
		t.Fatalf("video search items = %#v", result.Items)
	}
	items := make(map[string]MusicItem, len(result.Items))
	for _, item := range result.Items {
		items[item.VideoID] = item
	}
	for id, thumbnail := range map[string]string{
		"music-video":   "https://img.example/music-video",
		"regular-video": "https://img.example/regular-video",
	} {
		item, ok := items[id]
		if !ok || item.Kind != "video" || item.Thumbnail != thumbnail {
			t.Errorf("video %q = %#v, want kind video and thumbnail %q", id, item, thumbnail)
		}
	}
}

func TestContinueSearchSendsContinuationToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if r.URL.Path != "/youtubei/v1/search" || request["continuation"] != "SEARCH_NEXT" || request["query"] != nil {
			t.Errorf("continuation request path=%q body=%#v", r.URL.Path, request)
		}
		_, _ = w.Write([]byte(`{"continuationContents":{"musicShelfContinuation":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"track-2","title":{"simpleText":"Second track"}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	result, err := client.ContinueSearch(context.Background(), "SEARCH_NEXT")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].VideoID != "track-2" {
		t.Errorf("continued search items = %#v", result.Items)
	}
}

func TestGetAllLibraryLoadsEverySectionContinuation(t *testing.T) {
	var continuationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if request["browseId"] == "FEmusic_library_landing" {
			_, _ = w.Write([]byte(`{"contents":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"title":{"simpleText":"Songs"},"contents":[{"musicResponsiveListItemRenderer":{"videoId":"saved-song","title":{"simpleText":"Saved song"}}}],"continuations":[{"nextContinuationData":{"continuation":"SONGS_NEXT"}}]}},{"gridRenderer":{"title":{"simpleText":"Playlists"},"items":[{"gridPlaylistRenderer":{"playlistId":"PL1","title":{"simpleText":"Saved playlist"}}}],"continuations":[{"nextContinuationData":{"continuation":"PLAYLISTS_NEXT"}}]}}]}}}`))
			return
		}
		token, _ := request["continuation"].(string)
		continuationRequests.Add(1)
		switch token {
		case "SONGS_NEXT":
			_, _ = w.Write([]byte(`{"continuationContents":{"musicShelfContinuation":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"saved-song-2","title":{"simpleText":"More saved songs"}}}]}}}`))
		case "PLAYLISTS_NEXT":
			_, _ = w.Write([]byte(`{"continuationContents":{"gridContinuation":{"items":[{"gridPlaylistRenderer":{"playlistId":"PL2","title":{"simpleText":"More playlists"}}}]}}}`))
		default:
			t.Errorf("unexpected browse request %#v", request)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	library, err := client.GetAllLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if continuationRequests.Load() != 2 {
		t.Errorf("continuation requests = %d, want 2", continuationRequests.Load())
	}
	if len(library.Items) != 4 {
		t.Errorf("library items = %#v", library.Items)
	}
	if len(library.Sections) != 2 || library.Sections[0].Title == "" || library.Sections[1].Title == "" {
		t.Errorf("library sections = %#v", library.Sections)
	}
	if library.ContinuationToken != "" {
		t.Errorf("library continuation = %q, want none after every page loaded", library.ContinuationToken)
	}
}

// TestRequestsSkipBootstrapAndSetAUserAgent pins the request policy: a client
// with no API key talks to the InnerTube API directly, without first fetching
// the Music homepage, and sends a browser User-Agent rather than Go's default.
func TestRequestsSkipBootstrapAndSetAUserAgent(t *testing.T) {
	var apiRequests, configRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/browse" {
			configRequests.Add(1)
			t.Errorf("unexpected non-API request %s %s", r.Method, r.URL.Path)
			return
		}
		apiRequests.Add(1)
		if key := r.URL.Query().Get("key"); key != "" {
			t.Errorf("API request key = %q, want none without configured APIKey", key)
		}
		if ua := r.Header.Get("User-Agent"); ua == "" || ua == "Go-http-client/1.1" {
			t.Errorf("User-Agent = %q, want a browser User-Agent", ua)
		}
		_, _ = w.Write([]byte(`{"contents":{}}`))
	}))
	defer server.Close()

	client := NewClient(Options{BaseURL: server.URL})
	if _, err := client.GetHomeFeed(context.Background()); err != nil {
		t.Fatal(err)
	}
	if apiRequests.Load() != 1 || configRequests.Load() != 0 {
		t.Errorf("received %d API requests and %d config requests, want 1 and 0", apiRequests.Load(), configRequests.Load())
	}
}

func TestExecuteReportsAnErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", http.StatusForbidden)
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL})
	if _, err := client.GetHomeFeed(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("error = %v, want the status and response body", err)
	}
}

func TestExecuteStopsWithItsContext(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)
	client := NewClient(Options{BaseURL: server.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := client.GetHomeFeed(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
}

func TestExecuteRejectsAnOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("a"), maxResponseBytes+1))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL})
	if _, err := client.GetHomeFeed(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want an oversized-response error", err)
	}
}

// TestHTTPClientBoundsHeadersButNotBodyReads separates the two lifetimes: the
// default client caps how long it waits for response headers, while a body may
// keep streaming past that cap as long as its context allows.
func TestHTTPClientBoundsHeadersButNotBodyReads(t *testing.T) {
	const chunk = "chunk"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		if r.URL.Query().Get("stall") == "headers" {
			time.Sleep(200 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusOK)
		if flusher != nil {
			flusher.Flush()
		}
		for i := 0; i < 5; i++ {
			time.Sleep(40 * time.Millisecond)
			_, _ = w.Write([]byte(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer server.Close()

	client := newHTTPClient(50 * time.Millisecond)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("streaming request failed: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("reading the streamed body failed: %v", err)
	}
	if want := 5 * len(chunk); len(body) != want {
		t.Errorf("read %d body bytes, want %d past the 50ms header timeout", len(body), want)
	}
	if _, err := client.Get(server.URL + "?stall=headers"); err == nil {
		t.Error("a response whose headers stalled past the header timeout was accepted")
	}
}

func TestPlaylistAndUpNextAreReadOnlyBrowseCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("request method = %q, want POST", r.Method)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch r.URL.Path {
		case "/youtubei/v1/browse":
			if request["browseId"] != "VLPL123" {
				t.Errorf("playlist browse ID = %v", request["browseId"])
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistVideoListRenderer":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-track","title":{"simpleText":"Playlist song"},"lengthText":{"simpleText":"2:58"}}}]}}}`))
		case "/youtubei/v1/next":
			if request["videoId"] != "playlist-track" {
				t.Errorf("up-next video ID = %v", request["videoId"])
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelVideoRenderer":{"videoId":"next-track","title":{"simpleText":"Next song"}}}}`))
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	playlist, err := client.GetPlaylist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.Items) != 1 || playlist.Items[0].VideoID != "playlist-track" || playlist.Items[0].Title != "Playlist song" || playlist.Items[0].Kind != "video" {
		t.Errorf("playlist items = %#v", playlist.Items)
	}
	if len(playlist.Sections) != 1 || playlist.Sections[0].Kind != "playlistVideoListRenderer" || len(playlist.Sections[0].Items) != 1 {
		t.Errorf("playlist sections = %#v", playlist.Sections)
	}
	next, err := client.GetUpNext(context.Background(), UpNextOptions{VideoID: "playlist-track"})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].VideoID != "next-track" || next.Items[0].Title != "Next song" {
		t.Errorf("up-next items = %#v", next.Items)
	}
}

// TestSearchKeepsItsTopCardResult reads the card of a search's top result. The
// media lives in the card itself: its title run navigates to the video, its
// subtitle names the artist and trails the length. The mix queue of the
// card's menu entries must not become the video's destination.
func TestSearchKeepsItsTopCardResult(t *testing.T) {
	client := NewClient(Options{})
	card := `{"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/top-card"}]}}},"title":{"runs":[{"text":"Top video","navigationEndpoint":{"watchEndpoint":{"videoId":"top-video"}}}]},"subtitle":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Top Artist","navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}},{"text":" • "},{"text":"8.4M views"},{"text":" • "},{"text":"2:05"}]},"onTap":{"watchEndpoint":{"videoId":"top-video"}},"menu":{"menuRenderer":{"items":[{"menuNavigationItemRenderer":{"navigationEndpoint":{"watchEndpoint":{"videoId":"top-video","playlistId":"RDAMVMtop-video"}}}}]}}}`
	row := `{"playlistItemData":{"videoId":"row-video"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row video"}]}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Row Artist"}]}}}]}`

	raw := `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicCardShelfRenderer":` + card + `},{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":` + row + `}]}}]}}}}]}}}`
	result := client.newSearchResult(json.RawMessage(raw))
	if len(result.Items) != 2 {
		t.Fatalf("search items = %#v", result.Items)
	}
	top := result.Items[0]
	if top.VideoID != "top-video" || top.Title != "Top video" {
		t.Errorf("top card = %#v, want the card's video", top)
	}
	if top.Subtitle != "Video • Top Artist • 8.4M views" || top.Duration != "2:05" {
		t.Errorf("top card subtitle %q with length %q", top.Subtitle, top.Duration)
	}
	if top.BrowseID != "UCartist" {
		t.Errorf("top card artist = %q, want UCartist", top.BrowseID)
	}
	if top.Thumbnail != "https://img.example/top-card" {
		t.Errorf("top card thumbnail = %q", top.Thumbnail)
	}
}

func TestTrackReadsArtistBrowseIDFromItsSubtitle(t *testing.T) {
	var renderer map[string]any
	if err := json.Unmarshal([]byte(`{"videoId":"track-1","flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"simpleText":"Track"}}},{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album","navigationEndpoint":{"browseEndpoint":{"browseId":"MPRalbum"}}},{"text":" • "},{"text":"Artist","navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}}]}}}]}`), &renderer); err != nil {
		t.Fatal(err)
	}
	item := parseMusicItem("track", renderer)
	if item.BrowseID != "UCartist" {
		t.Errorf("track artist browse ID = %q, want UCartist", item.BrowseID)
	}
}

func TestUpNextIgnoresItsMixQueueID(t *testing.T) {
	// A queue entry names both its track and the mix offered beside it. The
	// queue ID must not become the track's destination: opening it browses
	// empty.
	client := NewClient(Options{})
	result := client.newUpNextResult(json.RawMessage(`{"contents":{"playlistPanelRenderer":{"contents":[
		{"playlistPanelVideoRenderer":{
			"videoId":"queue-video",
			"title":{"simpleText":"Queue video"},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"queue-video","playlistId":"RDAMVMqueue-video"}}
		}}
	]}}}`))
	if len(result.Items) != 1 {
		t.Fatalf("up-next items = %#v", result.Items)
	}
	item := result.Items[0]
	if item.VideoID != "queue-video" || item.BrowseID != "" {
		t.Errorf("queue entry = %#v, want only its video ID", item)
	}
}

func TestUpNextKeepsPlaylistContextAndContinuesRadioQueue(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Errorf("request path = %q, want /next", r.URL.Path)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "track-1" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(2) {
				t.Errorf("initial up-next request = %#v", request)
			}
			if request["continuation"] != nil {
				t.Errorf("initial up-next request unexpectedly has continuation: %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"track-2","title":{"simpleText":"Next song"}}}],"continuations":[{"nextContinuationData":{"continuation":"STANDARD_MORE"},"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		case 2:
			if request["videoId"] != "track-1" || request["playlistId"] != "PL123" || request["playlistIndex"] != float64(2) || request["continuation"] != "RADIO_MORE" {
				t.Errorf("continuation request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"continuationContents":{"playlistPanelContinuation":{"contents":[{"playlistPanelVideoRenderer":{"videoId":"track-3","title":{"simpleText":"Radio song"}}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	index := 2
	options := UpNextOptions{VideoID: "track-1", PlaylistID: "PL123", PlaylistIndex: &index}
	next, err := client.GetUpNext(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.Items[0].VideoID != "track-2" || next.ContinuationToken != "RADIO_MORE" {
		t.Fatalf("initial queue = items %#v, continuation %q", next.Items, next.ContinuationToken)
	}
	more, err := client.ContinueUpNext(context.Background(), options, next.ContinuationToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Items) != 1 || more.Items[0].VideoID != "track-3" || nextRequests.Load() != 2 {
		t.Errorf("continued queue = %#v after %d requests", more.Items, nextRequests.Load())
	}
}

func TestGetUpNextResolvesAutomixPreview(t *testing.T) {
	var nextRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/next" {
			t.Errorf("request path = %q, want /next", r.URL.Path)
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		switch nextRequests.Add(1) {
		case 1:
			if request["videoId"] != "seed-track" {
				t.Errorf("initial request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"","contents":[{"automixPreviewVideoRenderer":{"content":{"automixPlaylistVideoRenderer":{"navigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"RDAMVMseed-track","params":"RADIO_PARAMS"}}}}}}]}}}`))
		case 2:
			if request["videoId"] != "seed-track" || request["playlistId"] != "RDAMVMseed-track" || request["params"] != "RADIO_PARAMS" {
				t.Errorf("automix request = %#v", request)
			}
			_, _ = w.Write([]byte(`{"contents":{"playlistPanelRenderer":{"playlistId":"RDAMVMseed-track","contents":[{"playlistPanelVideoRenderer":{"videoId":"radio-1","title":{"simpleText":"Radio track"}}}],"continuations":[{"nextRadioContinuationData":{"continuation":"RADIO_MORE"}}]}}}`))
		default:
			t.Errorf("unexpected /next request #%d", nextRequests.Load())
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	result, err := client.GetUpNext(context.Background(), UpNextOptions{VideoID: "seed-track"})
	if err != nil {
		t.Fatal(err)
	}
	if nextRequests.Load() != 2 || result.QueuePlaylistID != "RDAMVMseed-track" || result.ContinuationToken != "RADIO_MORE" || len(result.Items) != 1 || result.Items[0].VideoID != "radio-1" {
		t.Fatalf("automix result after %d requests = %#v", nextRequests.Load(), result)
	}
}

func TestGetAllPlaylistLoadsContinuationPages(t *testing.T) {
	var continuationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if r.URL.Path != "/youtubei/v1/browse" {
			t.Errorf("request path = %q, want /browse", r.URL.Path)
			return
		}
		if request["browseId"] == "VLPL123" {
			_, _ = w.Write([]byte(`{"contents":{"musicPlaylistShelfRenderer":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-1","title":{"simpleText":"First song"}}}],"continuations":[{"nextContinuationData":{"continuation":"PLAYLIST_MORE"}}]}}}`))
			return
		}
		if request["continuation"] != "PLAYLIST_MORE" {
			t.Errorf("continuation request = %#v", request)
		}
		continuationRequests.Add(1)
		_, _ = w.Write([]byte(`{"continuationContents":{"musicPlaylistShelfContinuation":{"contents":[{"playlistVideoRenderer":{"videoId":"playlist-2","title":{"simpleText":"Second song"}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	playlist, err := client.GetAllPlaylist(context.Background(), "PL123")
	if err != nil {
		t.Fatal(err)
	}
	if continuationRequests.Load() != 1 || len(playlist.Items) != 2 {
		t.Fatalf("playlist has %d items after %d continuation calls: %#v", len(playlist.Items), continuationRequests.Load(), playlist.Items)
	}
	if playlist.Items[0].VideoID != "playlist-1" || playlist.Items[1].VideoID != "playlist-2" || playlist.ContinuationToken != "" {
		t.Errorf("playlist continuation result = %#v", playlist)
	}
}

func TestLyricsRelatedAndRecapUseReadOnlyEndpoints(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		switch r.URL.Path {
		case "/youtubei/v1/next":
			if request["videoId"] != "track-lyrics" {
				t.Errorf("track-tab video ID = %v", request["videoId"])
			}
			_, _ = w.Write([]byte(`{"tabs":[{"tabRenderer":{"endpoint":{"browseEndpoint":{"browseId":"lyrics-id","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_LYRICS"}}}}}},{"tabRenderer":{"endpoint":{"browseEndpoint":{"browseId":"related-id","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_RELATED"}}}}}}]}`))
		case "/youtubei/v1/browse":
			switch request["browseId"] {
			case "lyrics-id":
				_, _ = w.Write([]byte(`{"contents":{"musicDescriptionShelfRenderer":{"description":{"runs":[{"text":"Line one\nLine two"}]},"footer":{"simpleText":"Lyrics provided by partner"}}}}`))
			case "related-id":
				_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"related-track","title":{"simpleText":"Related song"}}}]}}}`))
			case "FEmusic_listening_review":
				_, _ = w.Write([]byte(`{"contents":{"musicCarouselShelfRenderer":{"title":{"simpleText":"Your recap"}}}}`))
			default:
				t.Errorf("unexpected browse ID %v", request["browseId"])
			}
		default:
			t.Errorf("unexpected endpoint %q", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	lyrics, err := client.GetLyrics(context.Background(), "track-lyrics")
	if err != nil {
		t.Fatal(err)
	}
	if lyrics.Description != "Line one\nLine two" || lyrics.Footer != "Lyrics provided by partner" {
		t.Errorf("lyrics = %#v", lyrics)
	}
	related, err := client.GetRelated(context.Background(), "track-lyrics")
	if err != nil {
		t.Fatal(err)
	}
	if len(related.Items) != 1 || related.Items[0].VideoID != "related-track" {
		t.Errorf("related items = %#v", related.Items)
	}
	recap, err := client.GetRecap(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(recap.Sections) != 1 || recap.Sections[0].Title != "Your recap" {
		t.Errorf("recap sections = %#v", recap.Sections)
	}
}

func TestGetAccountDetailsReadsTheAccountMenu(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/account/account_menu" {
			t.Errorf("request path = %q", r.URL.Path)
			return
		}
		if got := r.Header.Get("Authorization"); !strings.HasPrefix(got, "SAPISIDHASH ") {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-YouTube-Client-Name"); got != "67" {
			t.Errorf("client name header = %q, want the Music client ID 67", got)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		client := request["context"].(map[string]any)["client"].(map[string]any)
		if client["clientName"] != "WEB_REMIX" {
			t.Errorf("account request client context = %#v", client)
		}
		_, _ = w.Write([]byte(`{"actions":[{"openPopupAction":{"popup":{"multiPageMenuRenderer":{"header":{"activeAccountHeaderRenderer":{"accountName":{"runs":[{"text":"Me"}]},"email":{"runs":[{"text":"me@example.com"}]},"channelHandle":{"runs":[{"text":"@me"}]},"accountPhoto":{"thumbnails":[{"url":"https://img.example/small"},{"url":"https://img.example/me"}]}}},"sections":[{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[{"accountItemRenderer":{"accountName":{"runs":[{"text":"Me"}]},"endpoint":{"browseEndpoint":{"browseId":"UC-me"}},"isSelected":true}}]}}]}}]}}}}]}`))
	}))
	defer server.Close()
	cookieAuth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{HTTPClient: server.Client(), BaseURL: server.URL, APIKey: "key", CookieAuth: cookieAuth, AllowInsecureCookieAuth: true})
	account, err := client.GetAccountDetails(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.Name != "Me" || account.Email != "me@example.com" || account.ChannelID != "UC-me" || account.Thumbnail != "https://img.example/me" {
		t.Errorf("account details = %#v", account)
	}
}

func TestCookieAuthenticationAndAllAccounts(t *testing.T) {
	cookieAuth, err := NewCookieAuth("SAPISID=secret; SID=other", CookieOptions{AccountIndex: 2, OnBehalfOfUser: "UC-channel"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/account/accounts_list" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		if r.Header.Get("Cookie") != "SAPISID=secret; SID=other" {
			t.Errorf("Cookie header = %q", r.Header.Get("Cookie"))
		}
		if r.Header.Get("X-Goog-Authuser") != "2" || r.Header.Get("X-Goog-PageId") != "UC-channel" {
			t.Errorf("account selection headers = %#v", r.Header)
		}
		authorization := strings.TrimPrefix(r.Header.Get("Authorization"), "SAPISIDHASH ")
		timestampText, digest, ok := strings.Cut(authorization, "_")
		if !ok {
			t.Errorf("malformed cookie authorization %q", r.Header.Get("Authorization"))
		} else if timestamp, err := strconv.ParseInt(timestampText, 10, 64); err != nil {
			t.Errorf("authorization timestamp %q is invalid: %v", timestampText, err)
		} else if got := r.Header.Get("Authorization"); got != cookieAuth.authorization(time.Unix(timestamp, 0)) {
			t.Errorf("authorization digest does not match SAPISID")
		} else if digest == "" {
			t.Error("authorization digest is empty")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["requestType"] != "ACCOUNTS_LIST_REQUEST_TYPE_CHANNEL_SWITCHER" || request["callCircumstance"] != "SWITCHING_USERS_FULL" {
			t.Errorf("account-list request = %#v", request)
		}
		user := request["context"].(map[string]any)["user"].(map[string]any)
		if user["onBehalfOfUser"] != "UC-channel" {
			t.Errorf("user context = %#v", user)
		}
		_, _ = w.Write([]byte(`{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"accountByline":{"simpleText":"Creator"},"channelHandle":{"runs":[{"text":"@main"}]},"endpoint":{"browseEndpoint":{"browseId":"UC-channel"}},"isSelected":true,"hasChannel":true,"accountPhoto":{"thumbnails":[{"url":"https://img.example/main"}]}}}]}}]}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: cookieAuth, AllowInsecureCookieAuth: true})
	accounts, err := client.GetAccounts(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts.Items) != 1 {
		t.Errorf("accounts response = %#v", accounts)
	} else if channel := accounts.Items[0]; channel.Name != "Main channel" || channel.Byline != "Creator" || channel.ChannelID != "UC-channel" || channel.Handle != "@main" || !channel.Selected || !channel.HasChannel || channel.Thumbnail != "https://img.example/main" {
		t.Errorf("parsed account channel = %#v", channel)
	}
	if _, err := NewCookieAuth("SID=not-enough", CookieOptions{}); err == nil {
		t.Fatal("NewCookieAuth accepted cookies without SAPISID")
	}
}

func TestCookieAuthRejectsUntrustedBaseURLByDefault(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{"accountName":"Me"}`))
	}))
	defer server.Close()
	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: auth})
	if _, err := client.GetAccountDetails(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to send cookie authentication") {
		t.Fatalf("GetAccountDetails error = %v, want refusal to send credentials", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("sent %d requests to untrusted base URL", requests.Load())
	}
}

func TestIsYouTubeURLRequiresHTTPSAndAYouTubeHostname(t *testing.T) {
	for input, want := range map[string]bool{
		"https://youtube.com":              true,
		"https://www.youtube.com":          true,
		"https://music.youtube.com":        true,
		"http://youtube.com":               false,
		"https://youtube.com.evil.example": false,
		"https://notyoutube.com":           false,
		"https://youtube.com@evil.example": false,
	} {
		parsed, err := url.Parse(input)
		if err != nil {
			t.Fatal(err)
		}
		if got := isYouTubeURL(parsed); got != want {
			t.Errorf("isYouTubeURL(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestGetSearchSuggestionsParsesCompletionsInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/youtubei/v1/music/get_search_suggestions" {
			t.Errorf("request path = %q", r.URL.Path)
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request["input"] != "yor" {
			t.Errorf("suggestion input = %v", request["input"])
		}
		_, _ = w.Write([]byte(`{"contents":[{"searchSuggestionsSectionRenderer":{"contents":[` +
			`{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"yorushika"}]}}},` +
			`{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"yorushika songs"}]}}},` +
			`{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"yorushika"}]}}}]}}]}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	suggestions, err := client.GetSearchSuggestions(context.Background(), "yor")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"yorushika", "yorushika songs"}; !slices.Equal(suggestions, want) {
		t.Errorf("suggestions = %#v, want %#v", suggestions, want)
	}
	if _, err := client.GetSearchSuggestions(context.Background(), "  "); err == nil {
		t.Error("GetSearchSuggestions accepted a blank input")
	}
}

func TestExtractMusicItemsKeepsASongListedTwice(t *testing.T) {
	entry := func(setID string) string {
		return `{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"V1","playlistSetVideoId":"` + setID + `"},` +
			`"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"}]}}}]}}`
	}
	twice := []byte(`{"contents":[` + entry("S1") + `,` + entry("S2") + `]}`)
	if got := extractMusicItems(decodeResponse(twice)); len(got) != 2 {
		t.Errorf("a playlist holding a song twice gave %d items, want 2", len(got))
	}
	// The same entry met twice, as a response may repeat one, stays one.
	repeated := []byte(`{"contents":[` + entry("S1") + `,` + entry("S1") + `]}`)
	if got := extractMusicItems(decodeResponse(repeated)); len(got) != 1 {
		t.Errorf("a repeated entry gave %d items, want 1", len(got))
	}
}

// TestCookieAuthAuthorizationMatchesAKnownDigest pins SAPISIDHASH to a digest
// computed outside the client, so a change to the signed input or the hash
// cannot pass by comparing the client's output with itself.
func TestCookieAuthAuthorizationMatchesAKnownDigest(t *testing.T) {
	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := auth.authorization(time.Unix(1700000000, 0))
	// sha1("1700000000 secret https://www.youtube.com")
	const want = "SAPISIDHASH 1700000000_8826f35e8fadc232ceb4c889bdb7a3a586eb7379"
	if got != want {
		t.Errorf("authorization = %q, want %q", got, want)
	}
}

// TestClientRejectsInvalidInputWithoutARequest covers the public input
// contracts: each rejected call must fail before any network request.
func TestClientRejectsInvalidInputWithoutARequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	auth, err := NewCookieAuth("SAPISID=secret", CookieOptions{})
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key", CookieAuth: auth, AllowInsecureCookieAuth: true})
	calls := map[string]func() error{
		"blank search query": func() error { _, err := client.Search(context.Background(), "  ", SearchOptions{}); return err },
		"unsupported search type": func() error {
			_, err := client.Search(context.Background(), "a", SearchOptions{Type: SearchType("bogus")})
			return err
		},
		"blank browse continuation": func() error { _, err := client.ContinueBrowse(context.Background(), " "); return err },
		"blank search continuation": func() error { _, err := client.ContinueSearch(context.Background(), "\t"); return err },
		"invalid artist ID":         func() error { _, err := client.GetArtist(context.Background(), "not-an-artist"); return err },
		"invalid album ID":          func() error { _, err := client.GetAlbum(context.Background(), "not-an-album"); return err },
		"blank playlist ID":         func() error { _, err := client.GetPlaylist(context.Background(), ""); return err },
		"blank up-next video ID":    func() error { _, err := client.GetUpNext(context.Background(), UpNextOptions{}); return err },
		"blank up-next token": func() error {
			_, err := client.ContinueUpNext(context.Background(), UpNextOptions{VideoID: "v"}, " ")
			return err
		},
		"blank lyrics video ID":  func() error { _, err := client.GetLyrics(context.Background(), " "); return err },
		"blank related video ID": func() error { _, err := client.GetRelated(context.Background(), ""); return err },
		"blank suggestion input": func() error { _, err := client.GetSearchSuggestions(context.Background(), " "); return err },
	}
	for name, call := range calls {
		if err := call(); err == nil {
			t.Errorf("%s: got nil error, want a validation error", name)
		}
	}
	unauthenticated := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	if _, err := unauthenticated.GetAccountDetails(context.Background()); err == nil {
		t.Error("GetAccountDetails without cookie authentication returned no error")
	}
	if _, err := unauthenticated.GetAccounts(context.Background()); err == nil {
		t.Error("GetAccounts without cookie authentication returned no error")
	}
	if requests.Load() != 0 {
		t.Errorf("rejected input still sent %d requests", requests.Load())
	}
}

// TestDrainBrowseStopsAtARepeatedToken pins the runaway guard: a page that hands
// back a token already visited ends the walk instead of looping forever.
func TestDrainBrowseStopsAtARepeatedToken(t *testing.T) {
	var continuationRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		if request["continuation"] == "REPEAT" {
			continuationRequests.Add(1)
			_, _ = w.Write([]byte(`{"continuationContents":{"musicShelfContinuation":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"loop","title":{"simpleText":"Loop"}}}],"continuations":[{"nextContinuationData":{"continuation":"REPEAT"}}]}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"contents":{"sectionListRenderer":{"contents":[]}},"continuations":[{"nextContinuationData":{"continuation":"REPEAT"}}]}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	library, err := client.GetAllLibrary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if continuationRequests.Load() != 1 {
		t.Errorf("continuation requests = %d, want the repeated token to stop the walk after one page", continuationRequests.Load())
	}
	if len(library.Items) != 1 || library.Items[0].VideoID != "loop" {
		t.Errorf("library items = %#v, want the single item once", library.Items)
	}
}

// TestDrainBrowseStopsAfter1000Pages bounds a chain of fresh tokens so a broken
// or hostile response cannot make the walk run forever.
func TestDrainBrowseStopsAfter1000Pages(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decoding request: %v", err)
			return
		}
		token, _ := request["continuation"].(string)
		if token == "" {
			_, _ = w.Write([]byte(`{"continuations":[{"nextContinuationData":{"continuation":"0"}}]}`))
			return
		}
		page, err := strconv.Atoi(token)
		if err != nil {
			t.Errorf("unexpected continuation token %q", token)
			return
		}
		requests.Add(1)
		_, _ = w.Write([]byte(`{"continuations":[{"nextContinuationData":{"continuation":"` + strconv.Itoa(page+1) + `"}}]}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	if _, err := client.GetAllLibrary(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeded 1000 continuation pages") {
		t.Fatalf("GetAllLibrary error = %v, want the runaway-continuation guard", err)
	}
	if requests.Load() != 1000 {
		t.Errorf("continuation requests = %d, want the 1000 pages the guard allows", requests.Load())
	}
}

// TestClientIsSafeForConcurrentUse runs one client from several goroutines. It
// guards the read-only contract the race detector checks, not a return value.
func TestClientIsSafeForConcurrentUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{"videoId":"concurrent","title":{"simpleText":"Concurrent"}}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Options{BaseURL: server.URL, APIKey: "key"})
	var group sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 10; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, err := client.GetHomeFeed(context.Background()); err != nil {
				errs <- err
			}
			if _, err := client.Search(context.Background(), "concurrent", SearchOptions{Type: SearchSongs}); err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call failed: %v", err)
	}
}
