package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// searchPageResponse is a page of search results as YouTube Music answers one.
// The filter bar sits in the header of the shelf, the card the results lead
// with comes first, and every other row is a musicResponsiveListItemRenderer
// that names its own page in its navigation endpoint: the artist a row credits
// sits beside it in a run of the second column. The chip that clears the
// filters carries no parameters.
const searchPageResponse = `{"contents":{"tabbedSearchResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{
	"header":{"chipCloudRenderer":{"chips":[
		{"chipCloudChipRenderer":{"text":{"runs":[{"text":"Songs"}]},"navigationEndpoint":{"searchEndpoint":{"query":"ambient","params":"EgWKAQIIAWoSEAUQAxAJ"}}}},
		{"chipCloudChipRenderer":{"text":{"simpleText":"Albums"},"navigationEndpoint":{"searchEndpoint":{"query":"ambient","params":"EgWKAQIYAWoSEAUQAxAJ"}}}},
		{"chipCloudChipRenderer":{"navigationEndpoint":{"searchEndpoint":{"query":"ambient"}}}}
	]}},
	"contents":[
	{"musicCardShelfRenderer":{
		"title":{"runs":[{"text":"Ambient Works","navigationEndpoint":{"browseEndpoint":{"browseId":"UCambient","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ARTIST"}}}}}]},
		"subtitle":{"runs":[{"text":"Artist"},{"text":" • "},{"text":"12M monthly audience"}]},
		"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/ambient=w544-h544-l90-rj"}]}}},
		"contents":[{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"vid-card"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Card Song"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}}]}}}
			]}}]
	}},
	{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_ambient","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_ALBUM"}}}},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ambient Works"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}},{"text":" • "},{"text":"2019"}]}}}
		]}}]}},
	{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLambient","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_PLAYLIST"}}}},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ambient Mix"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"24 tracks"}]}}}
		]}}]}},
	{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"navigationEndpoint":{"browseEndpoint":{"browseId":"MPSPPLambient","browseEndpointContextSupportedConfigs":{"browseEndpointContextMusicConfig":{"pageType":"MUSIC_PAGE_TYPE_PODCAST_SHOW_DETAIL_PAGE"}}}},
		"overlay":{"musicItemThumbnailOverlayRenderer":{"content":{"musicPlayButtonRenderer":{"playNavigationEndpoint":{"watchPlaylistEndpoint":{"playlistId":"PLambient"}}}}}},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Ambient Talk"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Podcast"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}}]}}}
		]}}]}},
	{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-1"},
		"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-1"}},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First Light"}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}},{"text":" • "},{"text":"3:42"}]}}}
		]}}]}},
	{"itemSectionRenderer":{"contents":[{"musicResponsiveListItemRenderer":{
		"playlistItemData":{"videoId":"vid-2"},
		"flexColumns":[
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First Light (Official Video)","navigationEndpoint":{"watchEndpoint":{"videoId":"vid-2","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_OMV"}}}}}]}}},
			{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Someone","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsomeone"}}},{"text":" • "},{"text":"3:50"}]}}}
		]}}]}}
]}}}}]}}}`

// searchItem is the item of a result whose title is given.
func searchItem(t *testing.T, result *SearchResult, title string) MusicItem {
	t.Helper()
	for _, item := range result.Items {
		if item.Title == title {
			return item
		}
	}
	t.Fatalf("the results hold no %q: %#v", title, result.Items)
	return MusicItem{}
}

// searchServer answers every request with body, and hands back what was asked.
func searchServer(t *testing.T, body string, asked *map[string]any) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if asked != nil {
			_ = json.NewDecoder(r.Body).Decode(asked)
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(Options{BaseURL: server.URL, APIKey: "k"})
}

// A search row opens what it names: an album, a playlist or a podcast keeps its own page
// although the row credits the artist beside it, and a song still plays and
// still knows its artist.
func TestSearchRowsOpenWhatTheyName(t *testing.T) {
	result, err := searchServer(t, searchPageResponse, nil).Search(context.Background(), "ambient", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		title string
		kind  TargetKind
		id    string
	}{
		{"Ambient Works", TargetAlbum, "MPREb_ambient"},
		{"Ambient Mix", TargetPlaylist, "VLPLambient"},
		{"Ambient Talk", TargetPodcast, "MPSPPLambient"},
		{"First Light", TargetTrack, "vid-1"},
		{"First Light (Official Video)", TargetTrack, "vid-2"},
	}
	for _, item := range want {
		got := searchItem(t, result, item.title)
		if target := got.Target(); target.Kind != item.kind || target.ID != item.id {
			t.Errorf("%q opens %d %q, want %d %q", item.title, target.Kind, target.ID, item.kind, item.id)
		}
	}
	// A row says in its watch endpoint whether it has a picture; a song's
	// endpoint says nothing.
	if song, video := searchItem(t, result, "First Light"), searchItem(t, result, "First Light (Official Video)"); song.IsVideo() || !video.IsVideo() {
		t.Errorf("song IsVideo=%v, video IsVideo=%v; want false, true", song.IsVideo(), video.IsVideo())
	}
	if artist := searchItem(t, result, "First Light").ArtistID(); artist != "UCsomeone" {
		t.Errorf("the song credits artist %q, want UCsomeone", artist)
	}
	if subtitle := searchItem(t, result, "Ambient Works").Subtitle; subtitle != "Album • Someone • 2019" {
		t.Errorf("the album row reads %q", subtitle)
	}
}

// podcastShowResponse is a podcast show's page as YouTube Music answers one:
// the heading sits among the contents, the filter chips' reload token comes
// before the shelf, and each episode is a multi-row item that keeps its length
// beside its progress. The continuation answer goes on with the same shelf.
const podcastShowResponse = `{"contents":{"twoColumnBrowseResultsRenderer":{
	"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[{"musicResponsiveHeaderRenderer":{
		"title":{"runs":[{"text":"Ambient Talk"}]},
		"description":{"musicDescriptionShelfRenderer":{"header":{"runs":[{"text":"Ambient Talk"}]},"description":{"runs":[{"text":"Quiet music,\n\nquietly "},{"text":"discussed","navigationEndpoint":{"urlEndpoint":{"url":"https://example.test"}}}]}}},
		"thumbnail":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/show-small"},{"url":"https://img.test/show"}]}}}
	}}]}}}}],
	"secondaryContents":{"sectionListRenderer":{
		"header":{"chipCloudRenderer":{"chips":[{"chipCloudChipRenderer":{"navigationEndpoint":{"browseSectionListReloadEndpoint":{"continuation":{"reloadContinuationData":{"continuation":"show-reload"}}}}}}]}},
		"contents":[{"musicShelfRenderer":{
			"contents":[
				{"musicMultiRowListItemRenderer":{
					"title":{"runs":[{"text":"Episode Two","navigationEndpoint":{"browseEndpoint":{"browseId":"MPEDep-2"}}}]},
					"subtitle":{"runs":[{"text":"1.2K views"},{"text":" • "},{"text":"May 13"}]},
					"description":{"runs":[{"text":"Rain on a tin roof,\n\nand then "},{"text":"https://example.test","navigationEndpoint":{"urlEndpoint":{"url":"https://example.test"}}}]},
					"onTap":{"watchEndpoint":{"videoId":"ep-2","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}},
					"playbackProgress":{"musicPlaybackProgressRenderer":{"playbackProgressPercentage":25,"durationText":{"runs":[{"text":" • "},{"text":"1 hr 30 min"}]}}}
				}},
				{"musicMultiRowListItemRenderer":{
					"title":{"runs":[{"text":"Episode One"}]},
					"subtitle":{"runs":[{"text":"May 6"}]},
					"onTap":{"watchEndpoint":{"videoId":"ep-1","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}},
					"playbackProgress":{"musicPlaybackProgressRenderer":{"durationText":{"runs":[{"text":" • "},{"text":"42 min"}]}}}
				}}
			],
			"continuations":[{"nextContinuationData":{"continuation":"show-more"}}]
		}}]
	}}
}}}`

const podcastMoreResponse = `{"continuationContents":{"musicShelfContinuation":{
	"contents":[{"musicMultiRowListItemRenderer":{
		"title":{"runs":[{"text":"Episode Zero"}]},
		"onTap":{"watchEndpoint":{"videoId":"ep-0","watchEndpointMusicSupportedConfigs":{"watchEndpointMusicConfig":{"musicVideoType":"MUSIC_VIDEO_TYPE_PODCAST_EPISODE"}}}}
	}}],
	"continuations":[{"nextContinuationData":{"continuation":"show-more-2"}}]
}}}`

// A podcast show lists its episodes, newest first, as tracks that play, with
// the length beside the date and what each is about on one line. Its next
// episodes follow from the shelf's continuation, not from the token that
// reloads the shelf under a filter.
func TestPodcastShowListsItsEpisodes(t *testing.T) {
	ctx := context.Background()
	show, err := searchServer(t, podcastShowResponse, nil).GetPodcast(ctx, "MPSPPLambient")
	if err != nil {
		t.Fatal(err)
	}
	if show.Title != "Ambient Talk" || show.Thumbnail != "https://img.test/show" || show.Description != "Quiet music, quietly discussed" {
		t.Errorf("the show is headed %q with %q, described as %q", show.Title, show.Thumbnail, show.Description)
	}
	var got []string
	for _, item := range show.Items {
		got = append(got, item.VideoID+" "+item.Title+" | "+item.Subtitle)
		if target := item.Target(); target.Kind != TargetTrack || !item.IsEpisode() {
			t.Errorf("%q opens %d and is an episode: %v", item.Title, target.Kind, item.IsEpisode())
		}
	}
	want := []string{"ep-2 Episode Two | 1.2K views • May 13 • 1 hr 30 min", "ep-1 Episode One | May 6 • 42 min"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("the episodes read\n%q\nwant\n%q", got, want)
	}
	if got := show.Items[0].Description; got != "Rain on a tin roof, and then https://example.test" {
		t.Errorf("episode two is described as %q, on one line", got)
	}
	if got := show.Items[0].ProgressPercent; got != 25 {
		t.Errorf("episode two progress is %d%%, want 25%%", got)
	}
	if show.ContinuationToken != "show-more" {
		t.Errorf("the show continues with %q, want the shelf's show-more", show.ContinuationToken)
	}

	more, err := searchServer(t, podcastMoreResponse, nil).ContinuePodcast(ctx, show.ContinuationToken)
	if err != nil {
		t.Fatal(err)
	}
	if len(more.Items) != 1 || more.Items[0].VideoID != "ep-0" || more.ContinuationToken != "show-more-2" {
		t.Errorf("the next episodes are %#v, continuing with %q", more.Items, more.ContinuationToken)
	}
	if _, err := searchServer(t, podcastShowResponse, nil).GetPodcast(ctx, "VLPLambient"); err == nil {
		t.Error("a playlist ID was read as a show")
	}
}

// The card a search leads with is its top result, drawn as a card rather than a
// row, and the songs it lists are still rows of the results.
func TestSearchTopResultIsTheCard(t *testing.T) {
	result, err := searchServer(t, searchPageResponse, nil).Search(context.Background(), "ambient", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	top := result.TopResult
	if top.Title != "Ambient Works" || top.Subtitle != "Artist • 12M monthly audience" {
		t.Fatalf("the top result is %#v", top)
	}
	if target := top.Target(); target.Kind != TargetArtist || target.ID != "UCambient" {
		t.Errorf("the top result opens %d %q, want %d UCambient", target.Kind, target.ID, TargetArtist)
	}
	for _, item := range result.Items {
		if item.Title == top.Title && item.Target().Kind == TargetArtist {
			t.Errorf("the card %q is also a row of the results", item.Title)
		}
	}
	if title := searchItem(t, result, "Card Song").VideoID; title != "vid-card" {
		t.Errorf("the card's songs are missing from the results: %#v", result.Items)
	}
}

// The card of a song plays its video, and says as much rather than claiming to
// be a page.
func TestSearchTopResultOfASongPlaysIt(t *testing.T) {
	const body = `{"contents":{"musicCardShelfRenderer":{
		"title":{"runs":[{"text":"Creep","navigationEndpoint":{"watchEndpoint":{"videoId":"vid-creep"}}}]},
		"subtitle":{"runs":[{"text":"Video"},{"text":" • "},{"text":"Radiohead"},{"text":" • "},{"text":"3:57"}]}}}}`
	result, err := searchServer(t, body, nil).Search(context.Background(), "creep", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	top := result.TopResult
	if top.VideoID != "vid-creep" || !top.IsVideo() || top.Duration != "3:57" {
		t.Fatalf("the top result is %#v", top)
	}
	if target := top.Target(); target.Kind != TargetTrack || target.ID != "vid-creep" {
		t.Errorf("the top result opens %d %q, want the track vid-creep", target.Kind, target.ID)
	}
	if top.Subtitle != "Video • Radiohead" {
		t.Errorf("the top result reads %q", top.Subtitle)
	}
}
