package youtube

import (
	"encoding/json"
	"testing"
)

// An artist's page lists a few songs under a Songs shelf whose title opens the
// playlist of all of them. The section keeps that playlist, so that playing the
// artist can play more than the few songs the page lists.
func TestArtistSongsShelfKeepsTheListItsTitleOpens(t *testing.T) {
	const page = `{"contents":{"sectionListRenderer":{"contents":[
		{"musicShelfRenderer":{
			"title":{"runs":[{"text":"Songs","navigationEndpoint":{"browseEndpoint":{"browseId":"VLOLAKall"}}}]},
			"contents":[{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"vid-1"},"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"First"}]}}}]}}]}},
		{"musicShelfRenderer":{
			"title":{"runs":[{"text":"Plain"}]},
			"contents":[{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"vid-2"},"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Second"}]}}}]}}]}},
		{"musicShelfRenderer":{
			"title":{"runs":[{"text":"Elsewhere","navigationEndpoint":{"browseEndpoint":{"browseId":"MPADother"}}}]},
			"contents":[]}},
		{"musicCarouselShelfRenderer":{
			"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Albums","navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLalbums"}}}]}}},
			"contents":[]}}
	]}}}`
	got := browseResult(decodeResponse(json.RawMessage(page))).Sections
	if len(got) != 4 {
		t.Fatalf("sections = %d, want 4", len(got))
	}
	for i, want := range []string{"VLOLAKall", "", "", ""} {
		if got[i].MoreID != want {
			t.Errorf("section %d (%q) MoreID = %q, want %q", i, got[i].Title, got[i].MoreID, want)
		}
	}
}
