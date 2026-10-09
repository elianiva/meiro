package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/elianiva/meiro/youtube"
)

// searches counts the search requests the fake YouTube has answered, and
// fakePageID records the channel a request acted as.
var (
	searches   atomic.Int32
	fakePageID atomic.Value
)

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
	case strings.Contains(asked, "UCartist"):
		reply = artistResponse
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

// newTestApp builds an app whose loads run inline and whose YouTube requests
// are answered by fakeMusic.
func newTestApp() *app {
	a := newApp()
	a.volume = a.settings.Volume
	a.player.SetVolume(a.volume / 100)
	a.run = func(work func()) { work() }
	// Debounced work runs at once, so a frame sees the suggestion request's
	// answer without waiting.
	a.schedule = func(_ time.Duration, work func()) func() {
		work()
		return func() {}
	}
	newFakeClient := func(auth *youtube.CookieAuth) *youtube.Client {
		return youtube.NewClient(youtube.Options{
			APIKey:     "test",
			HTTPClient: &http.Client{Transport: fakeMusic{}},
			CookieAuth: auth,
		})
	}
	a.newClient = newFakeClient
	a.public, a.authed = newFakeClient(nil), newFakeClient(nil)
	return a
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

// failingMusic answers every request with a server error.
type failingMusic struct{}

func (failingMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusInternalServerError,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("down")),
		Request:    request,
	}, nil
}

// memoryStore is a cookie store held in memory.
type memoryStore struct {
	cookie    string
	deleteErr error
	onSave    func()
}

func (m *memoryStore) Load(context.Context) (string, error) {
	if m.cookie == "" {
		return "", errNotSignedIn
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
