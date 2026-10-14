package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elianiva/meiro/internal/artwork"
	"github.com/elianiva/meiro/internal/cookies"
	"github.com/elianiva/meiro/internal/proc"
	"github.com/elianiva/meiro/player"
	"github.com/elianiva/meiro/youtube"
)

// searches counts the search requests the fake YouTube has answered,
// searchesMore the continuations of them, searchAsked is the last search
// request's body, fakePageID records the channel a request acted as, and the
// playlist counters count the library landings served and the playlists made.
var (
	searches        atomic.Int32
	searchesMore    atomic.Int32
	searchAsked     atomic.Value
	fakePageID      atomic.Value
	libraryLoads    atomic.Int32
	playlistCreates atomic.Int32
	// likeWrites counts the like writes the fake has been asked for, and
	// refuseWrites makes it answer them, and the library and subscription
	// writes after them, as YouTube answers a request it rejects: with a 400.
	likeWrites atomic.Int32
	// subscribeWrites counts the subscription writes, refused the same way.
	subscribeWrites atomic.Int32
	refuseWrites    atomic.Bool
	fakeLikeState   atomic.Value // what /next says of vid-3, as a string; "" says nothing
	// lastWrite is the last playlist write the fake YouTube was asked for:
	// which endpoint, and the fields of its body.
	lastWrite atomic.Value
)

// searchBody is the last search request the fake YouTube was asked for.
func searchBody() string {
	body, _ := searchAsked.Load().(string)
	return body
}

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
	status := http.StatusOK
	switch {
	case strings.HasSuffix(request.URL.Path, "/like/like"), strings.HasSuffix(request.URL.Path, "/like/removelike"):
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
	case strings.HasSuffix(request.URL.Path, "/next") && fakeLikes() != "" && strings.Contains(asked, "vid-3"):
		reply = nextResponse(fakeLikes())
	case strings.HasSuffix(request.URL.Path, "/account/accounts_list"):
		reply = accountsResponse
	case strings.HasSuffix(request.URL.Path, "/music/get_search_suggestions"):
		reply = suggestionsResponse
	case strings.Contains(asked, "FEmusic_explore"):
		reply = exploreResponse
	case strings.Contains(asked, "FEmusic_listening_review"):
		reply = recapResponse
	case strings.Contains(asked, "VLPL_video"):
		reply = playlistPage()
	case strings.Contains(asked, "VLPL_songs"):
		reply = playlistSongsResponse
	case strings.Contains(asked, "MPREb_test"):
		reply = albumPage()
	case strings.Contains(asked, "FEmusic_library_landing"):
		libraryLoads.Add(1)
		reply = libraryPage()
	case strings.HasSuffix(request.URL.Path, "/playlist/create"):
		playlistCreates.Add(1)
		recordWrite(request.URL.Path, asked)
		reply = createPlaylistResponse
	case strings.Contains(asked, "UCartist"):
		reply = artistPage()
	case strings.HasSuffix(request.URL.Path, "/browse/edit_playlist"):
		recordWrite(request.URL.Path, asked)
		reply = editPlaylistResponse
	case strings.Contains(asked, "search-more"):
		searchesMore.Add(1)
		reply = searchMoreResponse
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

// fakeLikes is the like the fake's /next reports for vid-3, as YouTube names
// it, or "".
func fakeLikes() string {
	status, _ := fakeLikeState.Load().(string)
	return status
}

// likeResponse is what YouTube answers a like it took: the toast its own
// client shows. Meiro reads nothing from it.
const likeResponse = `{"responseContext":{},"actions":[{"addToToastAction":{"item":{"notificationActionRenderer":{"responseText":{"runs":[{"text":"Added to liked songs"}]}}}}}]}`

// subscribeResponse is what YouTube answers a subscription it took.
const subscribeResponse = `{"responseContext":{},"actions":[{"updateSubscribeButtonAction":{"subscribed":true}}]}`

// rejectedResponse is what YouTube answers a write it rejects.
const rejectedResponse = `{"error":{"code":400,"message":"Request contains an invalid argument.","status":"INVALID_ARGUMENT"}}`

// nextResponse is what YouTube answers for the track vid-3 in the player: the
// track itself, first in the panel, with the like button its menu carries.
func nextResponse(like string) string {
	return `{"contents":{"singleColumnMusicWatchNextResultsRenderer":{"tabbedRenderer":{"watchNextTabbedResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"musicQueueRenderer":{"content":{"playlistPanelRenderer":{"playlistId":"RDAMVM","contents":[
		{"playlistPanelVideoRenderer":{"videoId":"vid-3","title":{"runs":[{"text":"Album Track One"}]},
			"menu":{"menuRenderer":{"topLevelButtons":[{"likeButtonRenderer":{"target":{"videoId":"vid-3"},"likeStatus":"` + like + `"}}]}}}}
	]}}}}}}]}}}}}`
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

// albumPage is an album as YouTube Music answers one: a header with the
// button that saves it, and the rows of its tracks. The button says whether the
// fake account has the album saved.
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

// playlistPage is a playlist the account does not own: a header with the
// button that saves it, and its videos.
func playlistPage() string {
	return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"Road Trip"}]},"buttons":[` + libraryToggleButton(fakePlaylist) + `]}},` +
		`"contents":{"playlistVideoListRenderer":{"contents":[
	{"playlistVideoRenderer":{"videoId":"playlist-video","playlistItemData":{"playlistSetVideoId":"set-video-1"},"title":{"simpleText":"Playlist video"},"lengthText":{"simpleText":"5:21"}}}
]}}}`
}

// playlistSongsResponse is a playlist page of three songs, each with an artist
// beside it, for searching the page.
const playlistSongsResponse = `{"contents":{"playlistVideoListRenderer":{"contents":[
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
]}}}`

// The playlists the fake's pages offer to save. An album is saved as its audio
// playlist, which is not its browse ID.
const (
	fakeAlbumPlaylist = "OLAK5uy_test"
	fakePlaylist      = "PL_video"
)

// fakeLibrary is what the fake account has saved or follows, by playlist ID
// or channel ID. The fake's writes change it, and its pages read it, as
// YouTube's do.
var fakeLibrary sync.Map

// resetFakes puts the fake YouTube back as a test found it: no write refused,
// nothing counted, and the library holding the two playlists its landing page
// starts with.
func resetFakes() {
	likeWrites.Store(0)
	subscribeWrites.Store(0)
	refuseWrites.Store(false)
	fakeLikeState.Store("")
	fakeLibrary.Clear()
	fakeLibrary.Store("PLhype", true)
	fakeLibrary.Store("PLcar", true)
}

func init() { resetFakes() }

func fakeSaved(id string) bool {
	saved, _ := fakeLibrary.Load(id)
	return saved == true
}

// writtenPlaylist is the playlist the last write named as its target, or "".
func writtenPlaylist() string {
	target, _ := written().body["target"].(map[string]any)
	id, _ := target["playlistId"].(string)
	return id
}

// libraryToggleButton is the save button of a header, a toggle that likes the
// playlist and says whether it is toggled.
func libraryToggleButton(id string) string {
	return fmt.Sprintf(`{"toggleButtonRenderer":{"isToggled":%t,
		"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":%q}}},
		"toggledServiceEndpoint":{"likeEndpoint":{"status":"INDIFFERENT","target":{"playlistId":%q}}}}}`, fakeSaved(id), id, id)
}

// libraryToggleMenu is the menu of a card that can be saved: an item whose
// default endpoint is the write the item offers now.
func libraryToggleMenu(id string) string {
	offered, other := "LIKE", "INDIFFERENT"
	if fakeSaved(id) {
		offered, other = other, offered
	}
	return fmt.Sprintf(`"menu":{"menuRenderer":{"items":[{"toggleMenuServiceItemRenderer":{
		"defaultServiceEndpoint":{"likeEndpoint":{"status":%q,"target":{"playlistId":%q}}},
		"toggledServiceEndpoint":{"likeEndpoint":{"status":%q,"target":{"playlistId":%q}}}}}]}}`, offered, id, other, id)
}

// fakeArtist is the channel of the artist the fake's artist page is about.
const fakeArtist = "UCartist"

// artistPage carries the heading an artist page puts over its content, with
// the subscribe button in it, which says whether the fake account follows the
// artist.
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

// searchResponse is a search page as YouTube Music answers one: the filters it
// offers for the query, the card the results lead with, the rows of the
// results, and the token of the page after them.
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

// searchMixedResponse is a search that found one of everything: a top result,
// and a row each for an album, an artist, a playlist and a song.
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
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Someone"}]}}}
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
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Playlist"},{"text":" • "},{"text":"Someone"}]}}}
				]}},
			{"musicResponsiveListItemRenderer":{
				"playlistItemData":{"videoId":"vid-row"},
				"flexColumns":[
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Row Song"}]}}},
					{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"2:20"}]}}}
				]}}
		]}}
	]}}}}]}}}`

// searchFilteredResponse is a search narrowed to one of its filters: the chip
// that clears them leads the row, and the results are the ones of the filter.
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

// searchMoreResponse is the page after the first: the rows a search adds as its
// list reaches the end of what it has.
const searchMoreResponse = `{"contents":{"musicShelfRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
	"playlistItemData":{"videoId":"vid-6"},
	"flexColumns":[
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Later Song"}]}}},
		{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"0:59"}]}}}
	]}}]}}}`

// suggestionsResponse is what a query being typed is answered with: the
// completed queries, and the items the partial query already matches.
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
