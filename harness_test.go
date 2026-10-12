package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/youtube"
)

// searches counts the search requests the fake YouTube has answered,
// fakePageID records the channel a request acted as, and the playlist counters
// count the library landings served and the playlists made.
var (
	searches        atomic.Int32
	fakePageID      atomic.Value
	libraryLoads    atomic.Int32
	playlistCreates atomic.Int32
	// lastWrite is the last playlist write the fake YouTube was asked for:
	// which endpoint, and the fields of its body.
	lastWrite atomic.Value
)

// playlistWrite is one write the fake YouTube was asked to make.
type playlistWrite struct {
	endpoint string
	body     map[string]any
}

// fakeMusic answers the InnerTube endpoints with canned responses, so the
// app's tests need no network.
type fakeMusic struct{}

func (fakeMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	asked := ""
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		asked = string(body)
	}
	if id := request.Header.Get("X-Goog-PageId"); id != "" {
		fakePageID.Store(id)
	}
	reply := homeResponse
	switch {
	case strings.HasSuffix(request.URL.Path, "/account/accounts_list"):
		reply = accountsResponse
	case strings.HasSuffix(request.URL.Path, "/music/get_search_suggestions"):
		reply = suggestionsResponse
	case strings.Contains(asked, "FEmusic_explore"):
		reply = exploreResponse
	case strings.Contains(asked, "FEmusic_listening_review"):
		reply = recapResponse
	case strings.Contains(asked, "VLPL_video"):
		reply = playlistResponse
	case strings.Contains(asked, "MPREb_test"):
		reply = albumResponse
	case strings.Contains(asked, "FEmusic_library_landing"):
		libraryLoads.Add(1)
		reply = libraryResponse
	case strings.HasSuffix(request.URL.Path, "/playlist/create"):
		playlistCreates.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = createPlaylistResponse
	case strings.Contains(asked, "UCartist"):
		reply = artistResponse
	case strings.HasSuffix(request.URL.Path, "/browse/edit_playlist"):
		recordWrite(request.URL.Path, asked)
		reply = editPlaylistResponse
	case strings.Contains(asked, `"query"`):
		searches.Add(1)
		reply = searchResponse
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

// newTestApp builds an app whose work runs inline, and whose YouTube and
// artwork requests are answered without the network.
func newTestApp() *app {
	a := newApp()
	// No sound card: the player decodes with the real ffmpeg and listens
	// to the result on the null device.
	options := []player.Option{player.WithNullAudio()}
	if ffmpeg, err := proc.Tool("ffmpeg"); err == nil {
		options = append(options, player.WithFFmpeg(ffmpeg))
	}
	a.player = player.New(options...)
	a.player.SetVolume(a.volume / 100)
	a.run = func(work func()) { work() }
	// Delayed work runs at once, so a frame sees the suggestion request's
	// answer without waiting.
	a.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	a.thumbs = artwork.New(&http.Client{Transport: noArtwork{}}, func() {})
	a.account.newClient = newFakeClient
	a.account.public = newFakeClient(nil)
	return a
}

// newFakeClient is a YouTube client answered by fakeMusic.
func newFakeClient(auth *youtube.CookieAuth) *youtube.Client {
	return youtube.NewClient(youtube.Options{
		APIKey:     "test",
		HTTPClient: &http.Client{Transport: fakeMusic{}},
		CookieAuth: auth,
	})
}

// noArtwork answers every picture with a 404, which the artwork cache never
// asks for again.
type noArtwork struct{}

func (noArtwork) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    request,
	}, nil
}

const homeResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Quick picks"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Ambient One"}]},
				"subtitle":{"runs":[{"text":"Someone"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/a=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-1"}}
			}},
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Deep Focus"}]},
				"subtitle":{"runs":[{"text":"Album"}]},
				"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/b=w544-h544-l90-rj"}]}}},
				"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_test"}}
			}}
		]
	}}
]}}}`

const exploreResponse = `{"contents":{"sectionListRenderer":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Moods"}]}}},
		"contents":[
			{"musicTwoRowItemRenderer":{
				"title":{"runs":[{"text":"Night Drive"}]},
				"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-2"}}
			}}
		]
	}}
]}}}`

const albumResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-3"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track One"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"4:12"}]}}}
		]
	}}
]}}}`

const playlistResponse = `{"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"playlist-video","title":{"simpleText":"Playlist video"},"lengthText":{"simpleText":"5:21"}}}
]}}}`

// artistResponse carries the heading an artist page puts over its content.
const artistResponse = `{"header":{"musicImmersiveHeaderRenderer":{
	"title":{"runs":[{"text":"Aurora Vale"}]},
	"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/aurora"}]}}}}},
	"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-9"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Artist Track"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Aurora Vale"},{"text":" • "},{"text":"3:00"}]}}}
		]
	}}
]}}}`

const searchResponse = `{"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-4"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Search Result Song"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
		]
	}}
]}}}`

const recapResponse = `{"contents":{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Your recap"}]}}},
	"contents":[
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Top song of the year"}]},
			"subtitle":{"runs":[{"text":"Someone"}]},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-9"}}
		}}
	]
}}}`

const accountsResponse = `{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
	{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"channelId":"UC-main","isSelected":true,"hasChannel":true}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Brand channel"},"channelId":"UC-brand","hasChannel":true}}
]}}]}}`

const suggestionsResponse = `{"contents":[{"searchSuggestionsSectionRenderer":{"contents":[
	{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika"}]}}},
	{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika songs"}]}}}
]}}]}`

// refusingMusic answers the account menu the way YouTube answers for a session
// it has stopped accepting, until it is asked with the cookie the browser
// holds.
type refusingMusic struct {
	fresh string
}

func (m refusingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	reply := signedOutAccountMenu
	if strings.Contains(request.Header.Get("Cookie"), m.fresh) {
		reply = signedInAccountMenu
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

// signedInAccountMenu names the account, which is what tells an answer from a
// signed-in session from one YouTube has forgotten.
const signedInAccountMenu = `{"actions":[{"openPopupAction":{"popup":{"multiPageMenuRenderer":{"header":{"activeAccountHeaderRenderer":{"accountName":{"runs":[{"text":"Me"}]}}}}}}}]}`

// signedOutAccountMenu is what a session YouTube has forgotten is answered
// with: a page with no account in it.
const signedOutAccountMenu = `{"contents":{}}`

// createPlaylistResponse is what YouTube answers a playlist it made with.
const createPlaylistResponse = `{"playlistId":"PLnew"}`

// editPlaylistResponse is what YouTube answers an edit it made.
const editPlaylistResponse = `{"status":"STATUS_SUCCEEDED"}`

// recordWrite remembers the body of a playlist write, so a test can say what
// the app asked YouTube to do.
func recordWrite(endpoint, body string) {
	var fields map[string]any
	_ = json.Unmarshal([]byte(body), &fields)
	lastWrite.Store(playlistWrite{endpoint: endpoint, body: fields})
}

// written is the last playlist write the fake YouTube was asked for.
func written() playlistWrite {
	value, _ := lastWrite.Load().(playlistWrite)
	return value
}

// libraryResponse is a library landing page: the playlist a thumb fills, two
// playlists, and an artist.
const libraryResponse = `{"contents":{"musicCarouselShelfRenderer":{
	"contents":[
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Liked Music"}]},
			"subtitle":{"runs":[{"text":"Auto playlist"}]},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/liked=w544-h544-l90-rj"}]}}},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"VLLM"}}
		}},
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Hype"}]},
			"subtitle":{"runs":[{"text":"Nuarth · 37 tracks"}]},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/hype=w544-h544-l90-rj"}]}}},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLhype"}}
		}},
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"car radio"}]},
			"subtitle":{"runs":[{"text":"Nuarth · 26 tracks"}]},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/car=w544-h544-l90-rj"}]}}},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLcar"}}
		}},
		{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Aurora Vale"}]},
			"subtitle":{"runs":[{"text":"Artist"}]},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/aurora=w544-h544-l90-rj"}]}}},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"UCaurora"}}
		}}
	]
}}}`

// memoryStore is a cookie store held in memory.
type memoryStore struct {
	cookie    string
	deleteErr error
	onSave    func()
}

func (m *memoryStore) Load(context.Context) (string, error) {
	if m.cookie == "" {
		return "", cookies.ErrNotSaved
	}
	return m.cookie, nil
}

func (m *memoryStore) Save(_ context.Context, cookie string) error {
	m.cookie = cookie
	if m.onSave != nil {
		m.onSave()
	}
	return nil
}

func (m *memoryStore) Delete(context.Context) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.cookie = ""
	return nil
}
