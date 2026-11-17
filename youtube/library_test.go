package youtube

import (
	"encoding/json"
	"testing"
)

func TestPagesSayWhetherTheyAreInTheLibrary(t *testing.T) {
	toggle := func(rest string) string {
		return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"T"}]},"buttons":[` + rest + `]}}}`
	}
	const (
		song           = `{"toggleButtonRenderer":{"isToggled":true,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"videoId":"v"}}}}}`
		notSaved       = `{"toggleButtonRenderer":{"isToggled":false,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"OLAK5uy_a"}}}}}`
		saved          = `{"toggleButtonRenderer":{"isToggled":true,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"OLAK5uy_a"}}}}}`
		offeredRemoval = `{"toggleButtonRenderer":{"defaultServiceEndpoint":{"likeEndpoint":{"status":"INDIFFERENT","target":{"playlistId":"VLPL1"}}}}}`
	)
	for _, test := range []struct {
		name, page string
		want       LibraryState
	}{
		{"not saved", toggle(notSaved), LibraryState{ID: "OLAK5uy_a"}},
		{"saved", toggle(saved), LibraryState{ID: "OLAK5uy_a", Saved: true}},
		{"offered removal", toggle(offeredRemoval), LibraryState{ID: "PL1", Saved: true}},
		{"a song's toggle is not the library's", toggle(song), LibraryState{}},
		{"after a song's toggle", toggle(song + "," + saved), LibraryState{ID: "OLAK5uy_a", Saved: true}},
		{"no toggle", toggle(""), LibraryState{}},
		{"no header", `{"contents":{}}`, LibraryState{}},
	} {
		got := browseResult(decodeResponse(json.RawMessage(test.page))).Library
		if got != test.want {
			t.Errorf("%s: library %+v, want %+v", test.name, got, test.want)
		}
	}
}

func TestCardsSayWhetherTheyAreInTheLibrary(t *testing.T) {
	const page = `{"contents":{"musicCarouselShelfRenderer":{"contents":[
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Saved"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLa"}},
			"menu":{"menuRenderer":{"items":[{"menuNavigationItemRenderer":{}},{"toggleMenuServiceItemRenderer":{
				"defaultServiceEndpoint":{"likeEndpoint":{"status":"INDIFFERENT","target":{"playlistId":"PLa"}}}}}]}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Offered"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"MPREb_b"}},
			"menu":{"menuRenderer":{"items":[{"toggleMenuServiceItemRenderer":{
				"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"OLAK5uy_b"}}}}}]}}}},
		{"musicTwoRowItemRenderer":{"title":{"runs":[{"text":"Plain"}]},"navigationEndpoint":{"browseEndpoint":{"browseId":"VLPLc"}}}}
	]}}}`
	got := map[string]LibraryState{}
	for _, item := range browseResult(decodeResponse(json.RawMessage(page))).Items {
		got[item.Title] = item.InLibrary()
	}
	want := map[string]LibraryState{
		"Saved":   {ID: "PLa", Saved: true},
		"Offered": {ID: "OLAK5uy_b"},
		"Plain":   {},
	}
	for title, state := range want {
		if got[title] != state {
			t.Errorf("%s: library %+v, want %+v", title, got[title], state)
		}
	}
}
