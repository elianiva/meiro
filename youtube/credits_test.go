package youtube

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestSearchRowCreditsArtistFromLinksNotTheSubtitle(t *testing.T) {
	result, err := searchServer(t, searchPageResponse, nil).Search(context.Background(), "ambient", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	song := searchItem(t, result, "First Light")
	if song.Artist != "Someone" {
		t.Errorf("Artist = %q, want Someone: the kind and the length are not names", song.Artist)
	}
	if song.Artists != "Someone" {
		t.Errorf("Artists = %q, want the one linked artist", song.Artists)
	}
	if song.Subtitle != "Song • Someone • 3:42" && song.Subtitle != "Song • Someone" {
		t.Errorf("the display subtitle changed: %q", song.Subtitle)
	}
}

func TestPanelItemsCreditArtistAndAlbum(t *testing.T) {
	const response = `{"contents":{"playlistPanelRenderer":{"contents":[
		{"playlistPanelVideoRenderer":{"videoId":"a","title":{"runs":[{"text":"Duet"}]},"longBylineText":{"runs":[
			{"text":"First","navigationEndpoint":{"browseEndpoint":{"browseId":"UCfirst"}}},
			{"text":" & "},
			{"text":"Second","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsecond"}}},
			{"text":" & "},
			{"text":"Third","navigationEndpoint":{"browseEndpoint":{"browseId":"UCthird"}}},
			{"text":" • "},
			{"text":"The Album","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_album"}}},
			{"text":" • "},
			{"text":"2020"}]}}},
		{"playlistPanelVideoRenderer":{"videoId":"b","title":{"runs":[{"text":"Unlinked"}]},"longBylineText":{"runs":[{"text":"Plain Text Artist"},{"text":" • "},{"text":"2020"}]}}},
		{"playlistPanelVideoRenderer":{"videoId":"c","title":{"runs":[{"text":"Bare"}]}}}
	]}}}`
	result := upNextResult(decodeResponse(json.RawMessage(response)))
	got := map[string]MusicItem{}
	for _, item := range result.Items {
		got[item.VideoID] = item
	}
	if a := got["a"]; a.Subtitle != "First & Second & Third • The Album • 2020" {
		t.Errorf("a queue track shows no byline: subtitle %q", a.Subtitle)
	}
	if a := got["a"]; a.Artist != "First" || a.Artists != "First, Second, Third" || a.Album != "The Album" {
		t.Errorf("a: artist %q artists %q album %q, want every linked artist in order and the album", a.Artist, a.Artists, a.Album)
	}
	if a := got["a"]; a.ArtistID() != "UCfirst" {
		t.Errorf("a queue track cannot open its artist: %q", a.ArtistID())
	}
	var ids []string
	var text strings.Builder
	for _, part := range got["a"].PlayerByline() {
		text.WriteString(part.Text)
		if target := part.Target(); target.Opens() {
			ids = append(ids, target.ID)
		}
	}
	if text.String() != got["a"].Subtitle || !slices.Equal(ids, []string{"UCfirst", "UCsecond", "UCthird", "MPREb_album"}) {
		t.Fatalf("player lost the original byline or its individual links: %q, %v", text.String(), ids)
	}
	if got["b"].Byline != nil || got["c"].Byline != nil {
		t.Fatal("unlinked credits acquired destinations")
	}
	if b := got["b"]; b.Artist != "" {
		t.Errorf("an artist that is only text was guessed: %q", b.Artist)
	}
	if c := got["c"]; c.Artist != "" || c.Album != "" {
		t.Errorf("a track with no byline credits %q %q", c.Artist, c.Album)
	}
}

func TestPlayerBylineKeepsSeparateAlbumColumnAndExactArtistRuns(t *testing.T) {
	const response = `{"playlistItemData":{"videoId":"song"},"lengthText":{"simpleText":"3:42"},"flexColumns":[
	{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Title"}]}}},
	{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Song • "},
	{"text":"Song","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsong"}}}]}}},
	{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Record","navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_record"}}}]}}}
	]}`
	item := parseMusicItem(KindTrack, decodeResponse(json.RawMessage(response)).(*jsonObject))
	want := []BylinePart{{Text: "Song • "}, {Text: "Song", BrowseID: "UCsong"}, {Text: " • "}, {Text: "Record", BrowseID: "MPREb_record"}}
	if !slices.Equal(item.PlayerByline(), want) || item.Album != "Record" || item.Duration != "3:42" {
		t.Fatalf("separate album or exact name boundaries were lost: %+v, %+v", item, item.PlayerByline())
	}
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var restored MusicItem
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(restored.PlayerByline(), want) {
		t.Fatal("saved metadata lost its destinations")
	}
}

func TestCardPlayerBylineDoesNotLinkMetadataOrUnknownDestinations(t *testing.T) {
	const response = `{"title":{"runs":[{"text":"Title","navigationEndpoint":{"watchEndpoint":{"videoId":"song"}}}]},
	"subtitle":{"runs":[{"text":"Song • "},{"text":"Band","navigationEndpoint":{"browseEndpoint":{"browseId":"UCband"}}},
	{"text":" • "},{"text":"496M plays","navigationEndpoint":{"browseEndpoint":{"browseId":"VLnot-an-artist"}}},
	{"text":" • 3:42"}]}}`
	item, ok := parseMusicCardShelf(decodeResponse(json.RawMessage(response)).(*jsonObject))
	if !ok {
		t.Fatal("card was not parsed")
	}
	want := []BylinePart{{Text: "Song • "}, {Text: "Band", BrowseID: "UCband"}, {Text: " • "}, {Text: "496M plays"}}
	if !slices.Equal(item.PlayerByline(), want) {
		t.Fatalf("card linked non-credit metadata or retained duration: %+v", item.PlayerByline())
	}
}

func TestLegacyPlayerBylineLinksOnlyAnUnambiguousCredit(t *testing.T) {
	item := MusicItem{VideoID: "song", BrowseID: "UCband", Artist: "Band", Subtitle: "Song • Band • 2020"}
	want := []BylinePart{{Text: "Song"}, {Text: " • "}, {Text: "Band", BrowseID: "UCband"}, {Text: " • "}, {Text: "2020"}}
	if !slices.Equal(item.PlayerByline(), want) {
		t.Fatal("legacy artist ID was not preserved")
	}
	item.Artist, item.Subtitle = "Song", "Song • Song • 2020"
	for _, part := range item.PlayerByline() {
		if part.Target().Opens() {
			t.Fatal("ambiguous legacy metadata guessed which Song was the artist")
		}
	}
}

func TestTwoRowSubtitleCreditsArtist(t *testing.T) {
	const renderer = `{"title":{"runs":[{"text":"Cover"}]},"subtitle":{"runs":[
		{"text":"Song"},{"text":" • "},{"text":"Singer","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsinger"}}}]},
		"navigationEndpoint":{"watchEndpoint":{"videoId":"vid-9"}}}`
	// A home shelf's cards are two-row items that are not told apart from an
	// album's by their renderer, only by the video they carry.
	for _, kind := range []ItemKind{KindVideo, KindMusicItem} {
		item := parseMusicItem(kind, decodeResponse(json.RawMessage(renderer)).(*jsonObject))
		if item.Artist != "Singer" || item.Subtitle != "Song • Singer" {
			t.Errorf("%s: Artist = %q, subtitle %q", kind, item.Artist, item.Subtitle)
		}
	}
	album := `{"title":{"runs":[{"text":"Record"}]},"subtitle":{"runs":[{"text":"Album"},{"text":" • "},{"text":"Singer","navigationEndpoint":{"browseEndpoint":{"browseId":"UCsinger"}}}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_x"}}}`
	if item := parseMusicItem(KindMusicItem, decodeResponse(json.RawMessage(album)).(*jsonObject)); item.Artist != "" || item.Artists != "" {
		t.Errorf("an album is not a track to scrobble: artist %q artists %q", item.Artist, item.Artists)
	}
}
