package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/audio"
	"github.com/elianiva/meiro/internal/lyrics"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/internal/report"
	"github.com/elianiva/meiro/internal/secretfile"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/youtube"
)

type artworkTransport func(*http.Request) (*http.Response, error)

func (f artworkTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var (
	searches          atomic.Int32
	searchesMore      atomic.Int32
	homeMoreRequests  atomic.Int32
	searchAsked       atomic.Value
	fakePageID        atomic.Value
	libraryLoads      atomic.Int32
	playlistCreates   atomic.Int32
	playlistDeletes   atomic.Int32
	likeWrites        atomic.Int32
	subscribeWrites   atomic.Int32
	refuseWrites      atomic.Bool
	aiChecks          atomic.Int32
	duplicateWrites   atomic.Bool
	hypeSongAdded     atomic.Bool
	fakePlaylistGrows atomic.Bool
	fakeLikeState     atomic.Value
	pickNextAsked     atomic.Value
	fakeAI            sync.Map
	lastWrite         atomic.Value
	homeLoads         atomic.Int32
)

func searchBody() string {
	body, _ := searchAsked.Load().(string)
	return body
}

type playlistWrite struct {
	endpoint string
	body     map[string]any
}

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
	status := http.StatusOK
	webNext := request.Header.Get("X-YouTube-Client-Name") == "1" && strings.HasSuffix(request.URL.Path, "/next")
	switch {
	case webNext:
		aiChecks.Add(1)
		reply = webNextResponse(nextVideoID(asked))
	case strings.HasSuffix(request.URL.Path, "/like/like"), strings.HasSuffix(request.URL.Path, "/like/dislike"), strings.HasSuffix(request.URL.Path, "/like/removelike"):
		likeWrites.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = likeResponse
		if refuseWrites.Load() {
			reply, status = rejectedResponse, http.StatusBadRequest
		} else if id := writtenPlaylist(); id != "" {
			fakeLibrary.Store(id, strings.HasSuffix(request.URL.Path, "/like/like"))
		}
	case strings.HasSuffix(request.URL.Path, "/subscription/subscribe"), strings.HasSuffix(request.URL.Path, "/subscription/unsubscribe"):
		subscribeWrites.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = subscribeResponse
		if refuseWrites.Load() {
			reply, status = rejectedResponse, http.StatusBadRequest
		} else {
			fakeLibrary.Store(fakeArtist, strings.HasSuffix(request.URL.Path, "/subscription/subscribe"))
		}
	case strings.HasSuffix(request.URL.Path, "/next") && strings.Contains(asked, `"ep-1"`):
		reply = episodeNextResponse
	case strings.Contains(asked, "MPPEep-1"):
		reply = episodeDetailsResponse
	case strings.HasSuffix(request.URL.Path, "/next") && fakeLikes() != "" && strings.Contains(asked, "vid-3"):
		reply = nextResponse(fakeLikes())
	case strings.HasSuffix(request.URL.Path, "/account/account_menu"):
		reply = signedInAccountMenu
	case strings.HasSuffix(request.URL.Path, "/account/accounts_list"), strings.HasSuffix(request.URL.Path, "/getAccountSwitcherEndpoint"):
		reply = accountsResponse
	case strings.HasSuffix(request.URL.Path, "/music/get_search_suggestions") && strings.Contains(asked, `"input":"pick`):
		reply = pickSuggestionsResponse
	case strings.HasSuffix(request.URL.Path, "/music/get_search_suggestions"):
		reply = suggestionsResponse
	case strings.HasSuffix(request.URL.Path, "/next") && strings.Contains(asked, `"pick-`):
		pickNextAsked.Store(asked)
		reply = `{"contents":{"playlistPanelRenderer":{"contents":[]}}}`
		if strings.Contains(asked, `"playlistId":"RDAMVMpick-two"`) {
			reply = pickRadioResponse
		}
	case strings.Contains(asked, "FEmusic_explore"):
		reply = exploreResponse
	case strings.Contains(asked, "FEmusic_home"):
		homeLoads.Add(1)
		reply = homeResponse
	case strings.Contains(asked, `"continuation":"home-more"`):
		homeMoreRequests.Add(1)
		reply = homeMoreResponse
	case strings.Contains(asked, "VLPL_page_token"):
		reply = paginatedPlaylistResponse(0, 100, "", "playlist-tracks")
	case strings.Contains(asked, "VLPL_paginated"):
		reply = paginatedPlaylistResponse(0, 100, "playlist-tracks", "playlist-more")
	case strings.Contains(asked, `"continuation":"playlist-tracks"`):
		reply = paginatedPlaylistResponse(100, 146, "", "")
	case strings.Contains(asked, `"continuation":"playlist-more"`):
		reply = playlistSuggestionsResponse
	case strings.Contains(asked, "VLPL_appended"):
		reply = appendedPlaylistResponse(0, 100, "playlist-appended-more")
	case strings.Contains(asked, `"continuation":"playlist-appended-more"`):
		reply = appendedPlaylistResponse(100, 200, "playlist-appended-more-2")
	case strings.Contains(asked, `"continuation":"playlist-appended-more-2"`):
		reply = appendedPlaylistResponse(200, 246, "")
	case strings.Contains(asked, "VLPL_video"):
		reply = playlistPage()
	case strings.Contains(asked, "VLPL_songs"):
		reply = playlistSongsResponse
	case strings.Contains(asked, "VLPL_grows"):
		reply = growingPlaylistResponse()
	case strings.Contains(asked, "VLPL_metadata"):
		reply = metadataPlaylistResponse
	case strings.Contains(asked, "VLPL_album_cover"):
		reply = strings.NewReplacer("Deep Focus", "After Hours", "MPREb_test", "MPREb_nested").Replace(metadataPlaylistResponse)
	case strings.Contains(asked, "MPREb_nested"):
		reply = nestedAlbumResponse
	case strings.Contains(asked, "UCguest"):
		reply = strings.ReplaceAll(strings.ReplaceAll(artistPage(), fakeArtist, "UCguest"), "Aurora Vale", "Guest Artist")
	case strings.Contains(asked, "VLPLhype"):
		reply = `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Hype"}]}}},"contents":{"playlistVideoListRenderer":{"contents":[]}},"continuations":[{"nextContinuationData":{"continuation":"playlist-more"}}]}`
		if hypeSongAdded.Load() {
			reply = strings.Replace(reply, `"contents":[]`, `"contents":[{"playlistVideoRenderer":{"videoId":"suggestion-one","title":{"simpleText":"Suggestion One"}}}]`, 1)
		}
	case strings.HasSuffix(request.URL.Path, "/next") && strings.Contains(asked, `"song-related"`):
		reply = songRelatedNextResponse
	case strings.Contains(asked, "MPTRsong-related"):
		reply = songRelatedResponse
	case strings.Contains(asked, "MPREb_ai_run"):
		reply = aiRunAlbumPage()
	case strings.Contains(asked, "MPREb_ai"):
		reply = aiAlbumPage
	case strings.Contains(asked, "MPREb_test"):
		reply = albumPage()
	case strings.Contains(asked, "MPSPPLshow"):
		reply = podcastResponse
	case strings.HasSuffix(request.URL.Path, "/next") && strings.Contains(asked, `"ep-related"`):
		reply = episodeRelatedNextResponse
	case strings.Contains(asked, "MPPRep-related"):
		reply = episodeRelatedResponse
	case strings.Contains(asked, "FEmusic_library_landing"):
		libraryLoads.Add(1)
		reply = libraryPage()
	case strings.HasSuffix(request.URL.Path, "/playlist/create"):
		playlistCreates.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = createPlaylistResponse
	case strings.HasSuffix(request.URL.Path, "/playlist/delete"):
		playlistDeletes.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = editPlaylistResponse
		if refuseWrites.Load() {
			reply, status = rejectedResponse, http.StatusBadRequest
		}
	case strings.HasSuffix(request.URL.Path, "/next") && strings.Contains(asked, `"song-mixed"`):
		reply = mixedRelatedNextResponse
	case strings.Contains(asked, "MPTRsong-mixed"):
		reply = mixedRelatedResponse
	case strings.Contains(asked, "UCartist"):
		reply = artistPage()
	case strings.HasSuffix(request.URL.Path, "/browse/edit_playlist"):
		recordWrite(request.URL.Path, asked)
		reply = editPlaylistResponse
		switch {
		case refuseWrites.Load():
			reply, status = rejectedResponse, http.StatusBadRequest
		case duplicateWrites.Load() && strings.Contains(asked, "ACTION_ADD_VIDEO"):
			reply = duplicatePlaylistResponse(asked)
		case strings.Contains(asked, "PLhype") && strings.Contains(asked, "suggestion-one") && strings.Contains(asked, "ACTION_ADD_VIDEO"):
			hypeSongAdded.Store(true)
		}
	case strings.Contains(asked, "search-more"):
		searchesMore.Add(1)
		reply = searchMoreResponse
	case strings.Contains(asked, `"query":"play episode result"`):
		searches.Add(1)
		searchAsked.Store(asked)
		reply = searchEpisodeResponse
	case strings.Contains(asked, "yor mixed"):
		reply = searchMixedResponse
	case strings.Contains(asked, `"query"`):
		searches.Add(1)
		searchAsked.Store(asked)
		if strings.Contains(asked, `"params"`) {
			reply = searchFilteredResponse
		} else {
			reply = searchResponse
		}
	}
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

func fakeLikes() string {
	status, _ := fakeLikeState.Load().(string)
	return status
}

const likeResponse = `{"responseContext":{},"actions":[{"addToToastAction":{"item":{"notificationActionRenderer":{"responseText":{"runs":[{"text":"Added to liked songs"}]}}}}}]}`

const subscribeResponse = `{"responseContext":{},"actions":[{"updateSubscribeButtonAction":{"subscribed":true}}]}`

const rejectedResponse = `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`

func nextResponse(like string) string {
	return `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"musicQueueRenderer":{"content":{"playlistPanelRenderer":{"playlistId":"RDAMVM","contents":[
		{"playlistPanelVideoRenderer":{"videoId":"vid-3","title":{"runs":[{"text":"Album Track One"}]},
			"menu":{"menuRenderer":{"topLevelButtons":[{"likeButtonRenderer":{"target":{"videoId":"vid-3"},"likeStatus":"` + like + `"}}]}}}}
	]}}}}}}]}}}}}`
}

func nextVideoID(asked string) string {
	var body struct {
		VideoID string `json:"videoId"`
	}
	_ = json.Unmarshal([]byte(asked), &body)
	return body.VideoID
}

func markFakeAI(videoID string) {
	fakeAI.Store(videoID, true)
}

func fakeMarksAI(videoID string) bool {
	marked, _ := fakeAI.Load(videoID)
	return marked == true
}

func webNextResponse(videoID string) string {
	badges := `"badges":[]`
	if fakeMarksAI(videoID) {
		badges = `"badges":[{"metadataBadgeRenderer":{"icon":{"iconType":"INFO"},"style":"BADGE_STYLE_TYPE_SIMPLE","label":"AI","accessibilityData":{"label":"AI: Content was made with AI"}}}]`
	}
	return fmt.Sprintf(`{"contents":{"twoColumnWatchNextResults":{"results":{"results":{"contents":[
		{"videoPrimaryInfoRenderer":{"title":{"runs":[{"text":"Fake track"}]},%s}}
	]}}}},"related":{"videoRenderer":{"title":{"runs":[{"text":"Related AI"}]},"badges":[
		{"metadataBadgeRenderer":{"icon":{"iconType":"INFO"},"style":"BADGE_STYLE_TYPE_SIMPLE","label":"AI","accessibilityData":{"label":"AI: Content was made with AI"}}}
	]}}}`, badges)
}

const episodeNextResponse = `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[
	{"tabRenderer":{"title":"Up next"}},
	{"tabRenderer":{"title":"Details","endpoint":{"browseEndpoint":{"browseId":"MPPEep-1","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_NON_MUSIC_AUDIO_DETAILS"}}}}}}
]}}}}}`

const episodeDetailsResponse = `{"contents":{"sectionListRenderer":{"contents":[{"musicDescriptionShelfRenderer":{
	"header":{"runs":[{"text":"Reality Checking Productivity"}]},
	"subheader":{"runs":[{"text":"Deep Questions","navigationEndpoint":{"browseEndpoint":{"browseId":"MPSPPLshow"}}}]},
	"strapline":{"runs":[{"text":"3.7K views"},{"text":" • "},{"text":"Oct 26, 2026"}]},
	"description":{"runs":[
		{"text":"Sponsors: "},
		{"text":"https://example.com/deep","navigationEndpoint":{"urlEndpoint":{"url":"https://example.com/deep-full"}}},
		{"text":"\n"},
		{"text":"0:00","navigationEndpoint":{"watchEndpoint":{"videoId":"ep-1"}}},
		{"text":" Opening\n"},
		{"text":"4:26","navigationEndpoint":{"watchEndpoint":{"videoId":"ep-1","startTimeSeconds":266}}},
		{"text":" Time blocking"}
	]}
}}]}}}`

func newTestApp(t testing.TB) *app {
	a := newApp()
	options := []player.Option{player.WithNullAudio()}
	if ffmpeg, err := proc.Tool("ffmpeg"); err == nil {
		options = append(options, player.WithFFmpeg(ffmpeg))
	}
	a.player = player.New(options...)
	a.player.SetVolume(a.volume / 100)
	a.run = func(work func()) { work() }
	a.keepRunning = func(string) func() { return func() {} }
	a.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	a.thumbs.Close()
	a.thumbs = artwork.New(&http.Client{Transport: noArtwork{}}, func() {})
	t.Cleanup(a.thumbs.Close)
	a.streams = audio.NewURLs(func(context.Context, string) (string, time.Duration, error) {
		return "", 0, errors.New("no network in tests")
	})
	a.hasPremium = func(context.Context, string) (bool, error) { return false, nil }
	a.collectInfo = func(_ context.Context, version string) report.Info {
		return report.Info{App: version, OS: runtime.GOOS, Arch: runtime.GOARCH, YtDlp: "2026.01.01", FFmpeg: "7.1"}
	}
	a.notifications = func() bool { return true }
	a.lyricsSource = func(context.Context, lyrics.Query) (lyrics.Result, error) { return lyrics.Result{}, nil }
	a.account.newClient = newFakeClient
	a.account.public = newFakeClient(nil)
	return a
}

func newFakeClient(auth *youtube.CookieAuth) *youtube.Client {
	return youtube.NewClient(youtube.Options{
		APIKey:     "test",
		HTTPClient: &http.Client{Transport: fakeMusic{}},
		CookieAuth: auth,
	})
}

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

const homeMoreResponse = `{"continuationContents":{"sectionListContinuation":{"contents":[
	{"musicCarouselShelfRenderer":{
		"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"More picks"}]}}},
		"contents":[{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"More Ambient"}]},
			"subtitle":{"runs":[{"text":"Someone"}]},
			"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/c=w544-h544-l90-rj"}]}}},
			"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-4"}}
		}}]
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

const songRelatedNextResponse = `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[
	{"tabRenderer":{"title":"Up next"}},
	{"tabRenderer":{"title":"Related","endpoint":{"browseEndpoint":{"browseId":"MPTRsong-related","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_RELATED"}}}}}}
]}}}}}`

const songRelatedResponse = `{"contents":{"sectionListRenderer":{"contents":[{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"You might also like"}]}}},
	"contents":[{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Related Song"}]},"subtitle":{"runs":[{"text":"Some Artist"}]},
		"navigationEndpoint":{"watchEndpoint":{"videoId":"rel-song"}}}}]
}},{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Recommended playlists"}]}}},
	"contents":[{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Related Playlist"}]},"subtitle":{"runs":[{"text":"Playlist"}]},
		"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPL_songs","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_PLAYLIST"}}}}}}]
}}]}}}`

func albumPage() string {
	return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Deep Focus"}]},"buttons":[` + libraryToggleButton(fakeAlbumPlaylist) + `]}},` +
		`"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-3"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track One"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"4:12"}]}}}
		]
	}}
]}}}`
}

const aiAlbumPage = `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Generated and Human"}]}}},
"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"ai-track"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"AI Track"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Generated Artist"},{"text":" • "},{"text":"3:00"}]}}}
		]
	}},
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"human-track"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Human Track"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Human Artist"},{"text":" • "},{"text":"3:00"}]}}}
		]
	}}
]}}}`

func aiRunAlbumPage() string {
	var items []string
	for _, id := range []string{"ai-one", "ai-two", "ai-three", "ai-four", "human"} {
		items = append(items, fmt.Sprintf(`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":%q},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":%q}]}}}]}}`, id, id))
	}
	return `{"contents":{"musicShelfRenderer":{"contents":[` + strings.Join(items, ",") + `]}}}`
}

type aiQueueMusic struct {
	batches    int
	humanBatch int
}

func (s *aiQueueMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Header.Get("X-YouTube-Client-Name") != "67" || !strings.HasSuffix(request.URL.Path, "/next") {
		return fakeMusic{}.RoundTrip(request)
	}
	s.batches++
	var items []string
	if s.batches < 4 {
		for i := 1; i <= 4; i++ {
			id := fmt.Sprintf("rec-%d-%d", s.batches, i)
			if s.batches != s.humanBatch || i != 4 {
				markFakeAI(id)
			}
			items = append(items, fmt.Sprintf(`{"playlistPanelVideoRenderer":{"videoId":%q,"title":{"runs":[{"text":%q}]}}}`, id, id))
		}
	}
	response := `{"contents":{"playlistPanelRenderer":{"contents":[` + strings.Join(items, ",") + `]}}}`
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: request}, nil
}

const nestedAlbumResponse = `{"contents":{"twoColumnBrowseResultsRenderer":{
"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[
{"musicResponsiveHeaderRenderer":{
"title":{"runs":[{"text":"After Hours"}]},
"straplineThumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/artist.png"}]}}},
"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/album-small.png"},{"url":"https://img.test/album.png"}]}}}
}}
]}}}}],
"secondaryContents":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"contents":[
{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"nested-track"},"flexColumns":[
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track One"}]}}}
]}}
]}}]}}
}}}`

const metadataPlaylistResponse = `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Linked credits"}]}}},
"contents":{"musicShelfRenderer":{"contents":[
{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"linked-track"},"flexColumns":[
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Linked Track"}]}}},
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song • "},
{"text":"Aurora Vale","navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}},{"text":" & "},
{"text":"Guest Artist","navigationEndpoint":{"browseEndpoint":{"browseId":"UCguest"}}},{"text":" • 496M plays"}]}}},
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Deep Focus","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_test"}}}]}}}
],"lengthText":{"simpleText":"0:30"}}},
{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"unlinked-track"},"flexColumns":[
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Unlinked Track"}]}}},
{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Unknown artist • Unknown album"}]}}}
],"lengthText":{"simpleText":"0:30"}}}
]}}}`

const podcastResponse = `{"contents":{"twoColumnBrowseResultsRenderer":{
	"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Slow Talk"}]},"description":{"musicDescriptionShelfRenderer":{"description":{"runs":[{"text":"Two people, talking slowly."}]}}}}}]}}}}],
	"secondaryContents":{"sectionListRenderer":{"contents":[{"musicShelfRenderer":{"contents":[
		{"musicMultiRowListItemRenderer":{"title":{"runs":[{"text":"Episode Two"}]},"subtitle":{"runs":[{"text":"May 13"}]},
			"onTap":{"watchEndpoint":{"videoId":"ep-2","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}}}},
		{"musicMultiRowListItemRenderer":{"title":{"runs":[{"text":"Episode One"}]},"subtitle":{"runs":[{"text":"May 6"}]},
			"onTap":{"watchEndpoint":{"videoId":"ep-1","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}}}}
	]}}]}}
}}}`

const episodeRelatedNextResponse = `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[
	{"tabRenderer":{"title":"Up next"}},
	{"tabRenderer":{"title":"Related","endpoint":{"browseEndpoint":{"browseId":"MPPRep-related","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_NON_MUSIC_AUDIO_RELATED"}}}}}}
]}}}}}`

const episodeRelatedResponse = `{"contents":{"sectionListRenderer":{"contents":[{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"You might also like"}]}}},
	"contents":[{"musicMultiRowListItemRenderer":{"title":{"runs":[{"text":"Another Deep Episode"}]},"subtitle":{"runs":[{"text":"3d ago • 1 hr 9 min"}]},
		"onTap":{"watchEndpoint":{"videoId":"ep-9","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}}}}]
}}]}}}`

func playlistPage() string {
	return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Road Trip"}]},"buttons":[` + libraryToggleButton(fakePlaylist) + `]}},` +
		`"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"playlist-video","playlistItemData":{"playlistSetVideoId":"set-video-1"},"title":{"simpleText":"Playlist video"},"lengthText":{"simpleText":"5:21"}}}
]}}}`
}

func growingPlaylistResponse() string {
	second := ""
	if fakePlaylistGrows.Load() {
		second = `,
	{"musicResponsiveListItemRenderer": {
		"playlistItemData": {"videoId": "grow-two"},
		"flexColumns": [
			{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Growing Track Two"}]}}}
		]
	}}`
	}
	return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Growing Playlist"}]}}},
	"contents":{"playlistVideoListRenderer":{"contents":[
	{"musicResponsiveListItemRenderer": {
		"playlistItemData": {"videoId": "grow-one"},
		"flexColumns": [
			{"musicResponsiveListItemFlexColumnRenderer": {"text": {"runs": [{"text": "Growing Track One"}]}}}
		]
	}}` + second + `
	]}}}`
}

const playlistSongsResponse = `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Ameiro Playlist"}]},"subtitle":{"runs":[{"text":"Playlist"},{"text":" • "},{"text":"Nova"},{"text":" • "},{"text":"3 songs"}]},"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/ameiro.png"}]}}}}},"contents":{"playlistVideoListRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"song-one"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ameiro"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Nova"},{"text":" • "},{"text":"3:00"}]}}}
		]
	}},
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"song-two"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Meteorite"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Kaori"},{"text":" • "},{"text":"4:00"}]}}}
		]
	}},
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"song-three"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ameiro Two"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Nova"},{"text":" • "},{"text":"2:00"}]}}}
		]
	}}
]}},
"continuations":[{"nextContinuationData":{"continuation":"playlist-more"}}]}`

const (
	fakeAlbumPlaylist = "OLAK5uy_test"
	fakePlaylist      = "PL_video"
)

var fakeLibrary sync.Map

func resetFakes() {
	likeWrites.Store(0)
	playlistDeletes.Store(0)
	subscribeWrites.Store(0)
	homeMoreRequests.Store(0)
	homeLoads.Store(0)
	aiChecks.Store(0)
	refuseWrites.Store(false)
	duplicateWrites.Store(false)
	hypeSongAdded.Store(false)
	fakeLikeState.Store("")
	pickNextAsked.Store("")
	fakeAI.Clear()
	fakeLibrary.Clear()
	fakeLibrary.Store("PLhype", true)
	fakeLibrary.Store("PLcar", true)
}

func init() { resetFakes() }

func fakeSaved(id string) bool {
	saved, _ := fakeLibrary.Load(id)
	return saved == true
}

func writtenPlaylist() string {
	target, _ := written().body["target"].(map[string]any)
	id, _ := target["playlistId"].(string)
	return id
}

func libraryToggleButton(id string) string {
	return fmt.Sprintf(`{"toggleButtonRenderer":{"isToggled":%t,
		"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":%q}}},
		"toggledServiceEndpoint":{"likeEndpoint":{"status":"INDIFFERENT","target":{"playlistId":%q}}}}}`, fakeSaved(id), id, id)
}

func libraryToggleMenu(id string) string {
	offered, other := "LIKE", "INDIFFERENT"
	if fakeSaved(id) {
		offered, other = other, offered
	}
	return fmt.Sprintf(`"menu":{"menuRenderer":{"items":[{"toggleMenuServiceItemRenderer":{
		"defaultServiceEndpoint":{"likeEndpoint":{"status":%q,"target":{"playlistId":%q}}},
		"toggledServiceEndpoint":{"likeEndpoint":{"status":%q,"target":{"playlistId":%q}}}}}]}}`, offered, id, other, id)
}

const fakeArtist = "UCartist"

const mixedRelatedNextResponse = `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[
	{"tabRenderer":{"title":"Up next"}},
	{"tabRenderer":{"title":"Related","endpoint":{"browseEndpoint":{"browseId":"MPTRsong-mixed","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_TRACK_RELATED"}}}}}}
]}}}}}`

const mixedRelatedResponse = `{"contents":{"sectionListRenderer":{"contents":[{"musicCarouselShelfRenderer":{
	"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"You might also like"}]}}},
	"contents":[
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"First Related Song"}]},"navigationEndpoint":{"watchEndpoint":{"videoId":"mixed-1"}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Mixed Playlist"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPL_songs"}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Mixed Artist"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Second Related Song"}]},"navigationEndpoint":{"watchEndpoint":{"videoId":"mixed-2"}}}}
	]
}}]}}}`

func artistPage() string {
	return `{"header":{"musicImmersiveHeaderRenderer":{
	"title":{"runs":[{"text":"Aurora Vale"}]},
	"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.example/aurora"}]}}},
	"subscriptionButton":{"subscribeButtonRenderer":{"subscribed":` + fmt.Sprint(fakeSaved(fakeArtist)) + `,"channelId":"` + fakeArtist + `","enabled":true}}}},
	"contents":{"musicShelfRenderer":{"contents":[
	{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-9"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Artist Track"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Aurora Vale"},{"text":" • "},{"text":"3:00"}]}}}
		]
	}}
]}}}`
}

const searchResponse = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{
	"header":{"chipCloudRenderer":{"chips":[
		{"chipCloudChipRenderer":{"text":{"runs":[{"text":"Songs"}]},"navigationEndpoint":{"searchEndpoint":{"query":"yor","params":"EgWKAQIIAWoSEAUQAxAJ"}}}},
		{"chipCloudChipRenderer":{"text":{"runs":[{"text":"Albums"}]},"navigationEndpoint":{"searchEndpoint":{"query":"yor","params":"EgWKAQIYAWoSEAUQAxAJ"}}}}
	]}},
	"contents":[
		{"musicCardShelfRenderer":{
			"title":{"runs":[{"text":"Yorushika","navigationEndpoint":{"browseEndpoint":{"browseId":"UCyorushika"}}}]},
			"subtitle":{"runs":[{"text":"Artist"},{"text":" • "},{"text":"16M monthly audience"}]},
			"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/yorushika=w544-h544-l90-rj"}]}}},
			"contents":[{"musicResponsiveListItemRenderer":{
				"playlistItemData":{"videoId":"vid-card"},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Card Song"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"},{"text":" • "},{"text":"Yorushika"}]}}}
				]}}]
		}},
		{"musicShelfRenderer":{
			"contents":[{"musicResponsiveListItemRenderer":{
				"playlistItemData":{"videoId":"vid-4"},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Search Result Song"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
				]}}],
			"continuations":[{"nextContinuationData":{"continuation":"search-more"}}]
		}}
	]}}}}]}}}`

const searchEpisodeResponse = `{"contents":{"musicCardShelfRenderer":{
	"title":{"runs":[{"text":"Find Me feat.Hina from miolly","navigationEndpoint":{"browseEndpoint":{"browseId":"MPEDUZ83quirqLA"}}}]},
	"subtitle":{"runs":[{"text":"Episode"},{"text":" • "},{"text":"18h ago"},{"text":" • "},{"text":"*Luna"}]},
	"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/find-me"}]}}}
}}}`

const searchMixedResponse = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{
	"contents":[
		{"musicCardShelfRenderer":{
			"title":{"runs":[{"text":"Mixed Album","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_mixed"}}}]},
			"subtitle":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Someone"}]},
			"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/mixed=w544-h544-l90-rj"}]}}},
			"contents":[]
		}},
		{"musicShelfRenderer":{"contents":[
			{"musicResponsiveListItemRenderer":{
				"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_row"}},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row Album"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}}]}}}
				]}},
			{"musicResponsiveListItemRenderer":{
				"navigationEndpoint":{"browseEndpoint":{"browseId":"UCrowartist"}},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row Artist"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Artist"}]}}}
				]}},
			{"musicResponsiveListItemRenderer":{
				"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLrow"}},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row Playlist"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Playlist"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}}]}}}
				]}},
			{"musicResponsiveListItemRenderer":{
				"playlistItemData":{"videoId":"vid-row"},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row Song"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
				]}}
		]}}
	]}}}}]}}}`

const searchFilteredResponse = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{
	"header":{"chipCloudRenderer":{"chips":[
		{"chipCloudChipRenderer":{"navigationEndpoint":{"searchEndpoint":{"query":"yor"}}}},
		{"chipCloudChipRenderer":{"text":{"runs":[{"text":"Songs"}]},"navigationEndpoint":{"searchEndpoint":{"query":"yor","params":"EgWKAQIIAWoSEAUQAxAJ"}}}},
		{"chipCloudChipRenderer":{"text":{"runs":[{"text":"Albums"}]},"navigationEndpoint":{"searchEndpoint":{"query":"yor","params":"EgWKAQIYAWoSEAUQAxAJ"}}}}
	]}},
	"contents":[
		{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"vid-5"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Filtered Song"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"1:11"}]}}}
			]}}]}}
	]}}}}]}}}`

const searchMoreResponse = `{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
	"playlistItemData":{"videoId":"vid-6"},
	"flexColumns":[
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Later Song"}]}}},
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"0:59"}]}}}
	]}}]}}}`

const playlistSuggestionsResponse = `{"contents":{"musicShelfRenderer":{
	"title":{"runs":[{"text":"Suggestions"}]},
	"contents":[
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-one"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion One"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:00"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-two"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Two"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:10"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-three"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Three"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-four"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Four"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:30"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-five"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Five"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:40"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-six"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Six"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:50"}]}}}]}},
		{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"suggestion-seven"},"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggestion Seven"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"3:00"}]}}}]}}
	]}}}`

const suggestionsResponse = `{"contents":[
	{"searchSuggestionsSectionRenderer":{"contents":[
		{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika"}]}}},
		{"searchSuggestionRenderer":{"suggestion":{"runs":[{"text":"Yorushika songs"}]}}}
	]}},
	{"searchSuggestionsSectionRenderer":{"contents":[
		{"musicResponsiveListItemRenderer":{
			"navigationEndpoint":{"browseEndpoint":{"browseId":"UCyorushika"}},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Yorushika"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"16M monthly audience"}]}}}
			]}},
		{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"vid-suggest"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Suggested Song"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
			]}}
	]}}
]}`

const pickSuggestionsResponse = `{"contents":[
	{"searchSuggestionsSectionRenderer":{"contents":[
		{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"pick-one"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Pick One"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"3:10"}]}}}
			]}},
		{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"pick-two"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Pick Two"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone Else"},{"text":" • "},{"text":"2:40"}]}}}
			]}}
	]}}
]}`

const pickRadioResponse = `{"contents":{"playlistPanelRenderer":{"playlistId":"RDAMVMpick-two","contents":[
	{"playlistPanelVideoRenderer":{"videoId":"pick-two","title":{"runs":[{"text":"Pick Two"}]}}},
	{"playlistPanelVideoRenderer":{"videoId":"radio-one","title":{"runs":[{"text":"Radio One"}]}}},
	{"playlistPanelVideoRenderer":{"videoId":"radio-two","title":{"runs":[{"text":"Radio Two"}]}}}
]}}}`

const accountsResponse = `{"accountSectionListRenderer":{"contents":[{"accountItemSectionRenderer":{"contents":[
	{"accountItemRenderer":{"accountName":{"simpleText":"Main channel"},"isSelected":true,"hasChannel":true}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Brand channel"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"pageIdToken":{"pageId":"UC-brand"}}]}}}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Music channel"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"pageIdToken":{"pageId":"UC-music"}}]}}}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Studio channel"},"hasChannel":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"pageIdToken":{"pageId":"UC-studio"}}]}}}}
]}}]}}`

type multiAccountMusic struct {
	fresh             string
	swapped           bool
	omitEmail         bool
	omitSwitcherEmail bool
	index             string
	pageID            string
}

func (m *multiAccountMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	var reply string
	switch {
	case strings.HasSuffix(request.URL.Path, "/account/accounts_list"), strings.HasSuffix(request.URL.Path, "/getAccountSwitcherEndpoint"):
		reply = multiAccountsResponse
		if m.swapped {
			reply = strings.NewReplacer("personal@example.test", "music@example.test", "music@example.test", "personal@example.test").Replace(reply)
		}
		if m.omitSwitcherEmail {
			reply = strings.NewReplacer("personal@example.test", "", "music@example.test", "").Replace(reply)
		}
	case strings.HasSuffix(request.URL.Path, "/account/account_menu"):
		m.index, m.pageID = request.Header.Get("X-Goog-Authuser"), request.Header.Get("X-Goog-PageId")
		reply = signedOutAccountMenu
		if strings.Contains(request.Header.Get("Cookie"), m.fresh) {
			name, email := "Personal account", "personal@example.test"
			if m.index == "1" && !m.swapped {
				name, email = "Music account", "music@example.test"
			}
			if m.pageID == "UC-music-brand" {
				name = "Music brand"
			}
			if m.omitEmail {
				email = ""
			}
			reply = fmt.Sprintf(`{"activeAccountHeaderRenderer":{"accountName":{"simpleText":%q},"email":{"simpleText":%q}}}`, name, email)
		}
	default:
		return fakeMusic{}.RoundTrip(request)
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(reply)), Request: request}, nil
}

const multiAccountsResponse = `{"sections":[{"accountSectionListRenderer":{"header":{"googleAccountHeaderRenderer":{"email":{"simpleText":"personal@example.test"}}},"contents":[{"accountItemSectionRenderer":{"contents":[
	{"accountItemRenderer":{"accountName":{"simpleText":"Personal account"},"accountByline":{"simpleText":"personal@example.test"},"isSelected":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"https://www.youtube.com/signin?authuser=0"}}]}}}},
	{"compactLinkRenderer":{"title":{"simpleText":"View all channels"}}}
]}}]}},{"accountSectionListRenderer":{"header":{"googleAccountHeaderRenderer":{"email":{"simpleText":"music@example.test"}}},"contents":[{"accountItemSectionRenderer":{"contents":[
	{"accountItemRenderer":{"accountName":{"simpleText":"Music account"},"accountByline":{"simpleText":"music@example.test"},"isSelected":true,"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"https://www.youtube.com/signin?authuser=1"}}]}}}},
	{"accountItemRenderer":{"accountName":{"simpleText":"Music brand"},"channelHandle":{"simpleText":"@music-brand"},"serviceEndpoint":{"selectActiveIdentityEndpoint":{"supportedTokens":[{"accountSigninToken":{"signinUrl":"/signin?authuser=1"}},{"pageIdToken":{"pageId":"UC-music-brand"}}]}}}}
]}}]}}]}`

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

const signedInAccountMenu = `{"actions":[{"openPopupAction":{"popup":{"multiPageMenuRenderer":{"header":{"activeAccountHeaderRenderer":{"accountName":{"runs":[{"text":"Me"}]}}}}}}}]}`

const signedOutAccountMenu = `{"contents":{}}`

const createPlaylistResponse = `{"playlistId":"PLnew"}`

const editPlaylistResponse = `{"status":"STATUS_SUCCEEDED"}`

func duplicatePlaylistResponse(asked string) string {
	var body struct {
		Actions []struct {
			AddedVideoID string `json:"addedVideoId"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(asked), &body); err != nil || len(body.Actions) == 0 {
		return editPlaylistResponse
	}
	return `{"status":"STATUS_FAILED","actions":[{"addToToastAction":{"item":{"notificationActionRenderer":{"actionButton":{"buttonRenderer":{"command":{"playlistEditEndpoint":{"actions":[{"action":"ACTION_ADD_VIDEO","addedVideoId":"` + body.Actions[0].AddedVideoID + `","dedupeOption":"DEDUPE_OPTION_SKIP"}]}}}}}}}}]}`
}

func recordWrite(endpoint, body string) {
	var fields map[string]any
	_ = json.Unmarshal([]byte(body), &fields)
	lastWrite.Store(playlistWrite{endpoint: endpoint, body: fields})
}

func written() playlistWrite {
	value, _ := lastWrite.Load().(playlistWrite)
	return value
}

func libraryPage() string {
	album := ""
	if fakeSaved(fakeAlbumPlaylist) {
		album = `{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Deep Focus"}]},
			"subtitle":{"runs":[{"text":"Album"}]},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_test"}},
			` + libraryToggleMenu(fakeAlbumPlaylist) + `
		}},`
	}
	if fakeSaved(fakeArtist) {
		album += `{"musicTwoRowItemRenderer":{
			"title":{"runs":[{"text":"Aurora Vale Trio"}]},
			"subtitle":{"runs":[{"text":"Artist"}]},
			"navigationEndpoint":{"browseEndpoint":{"browseId":"UCartist"}}
		}},`
	}
	return `{"contents":{"musicCarouselShelfRenderer":{
	"contents":[` + album + `
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
			"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLhype"}},
			` + libraryToggleMenu("PLhype") + `
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
}

type memoryStore struct {
	cookie string
}

func (m *memoryStore) Load(context.Context) (string, error) {
	if m.cookie == "" {
		return "", secretfile.ErrNotSaved
	}
	return m.cookie, nil
}

func (m *memoryStore) Save(_ context.Context, cookie string) error {
	m.cookie = cookie
	return nil
}

func (m *memoryStore) Delete(context.Context) error {
	m.cookie = ""
	return nil
}

func lastFMSession(dir string) secretfile.File {
	return secretfile.File{Path: filepath.Join(dir, "session"), Keychain: secretfile.NoKeychain{}, Name: "lastfm-session"}
}

func paginatedPlaylistResponse(start, end int, next, suggestions string) string {
	var items []string
	for i := start; i < end; i++ {
		items = append(items, fmt.Sprintf(`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"track-%d"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Track %d"}]}}}]}}`, i, i))
	}
	kind := "musicPlaylistShelfRenderer"
	if start > 0 {
		kind = "musicPlaylistShelfContinuation"
	}
	return fmt.Sprintf(`{"contents":{"%s":{"contents":[%s],"continuations":[{"nextContinuationData":{"continuation":%q}}]}},"continuations":[{"nextContinuationData":{"continuation":%q}}]}`, kind, strings.Join(items, ","), next, suggestions)
}

func appendedPlaylistResponse(start, end int, next string) string {
	var items []string
	for i := start; i < end; i++ {
		items = append(items, fmt.Sprintf(`{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"track-%d"},"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Track %d"}]}}}]}}`, i, i))
	}
	if next != "" {
		items = append(items, fmt.Sprintf(`{"continuationItemRenderer":{"continuationEndpoint":{"continuationCommand":{"token":%q,"request":"CONTINUATION_REQUEST_TYPE_BROWSE"}}}}`, next))
	}
	if start == 0 {
		return fmt.Sprintf(`{"contents":{"twoColumnBrowseResultsRenderer":{"secondaryContents":{"sectionListRenderer":{"contents":[{"musicPlaylistShelfRenderer":{"contents":[%s]}}]}}}}}`, strings.Join(items, ","))
	}
	return fmt.Sprintf(`{"onResponseReceivedActions":[{"appendContinuationItemsAction":{"continuationItems":[%s]}}]}`, strings.Join(items, ","))
}
