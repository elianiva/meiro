package youtube

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPagesSayWhetherTheyAreInTheLibrary(t *testing.T) {
	toggle := func(rest string) string {
		return `{"header":{"musicResponsiveHeaderRenderer":{"title":{"runs":[{"text":"T"}]},"buttons":[` + rest + `]}}}`
	}
	const (
		song     = `{"toggleButtonRenderer":{"isToggled":true,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"videoId":"v"}}}}}`
		notSaved = `{"toggleButtonRenderer":{"isToggled":false,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"OLAK5uy_a"}}}}}`
		saved    = `{"toggleButtonRenderer":{"isToggled":true,"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"OLAK5uy_a"}}}}}`
		// No isToggled: the default endpoint is the write on offer, so a
		// removal on offer is a playlist already in the library.
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

func TestSongsSayWhetherTheyAreInTheLibrary(t *testing.T) {
	row := func(title, menu string) string {
		return `{"musicResponsiveListItemRenderer":{"playlistItemData":{"videoId":"` + title + `"},
			"flexColumns":[{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"` + title + `"}]}}}]` + menu + `}}`
	}
	toggle := func(fields string) string {
		return `,"menu":{"menuRenderer":{"items":[{"menuNavigationItemRenderer":{}},{"toggleMenuServiceItemRenderer":{` + fields + `}}]}}`
	}
	const (
		add    = `"defaultServiceEndpoint":{"feedbackEndpoint":{"feedbackToken":"A"}},"toggledServiceEndpoint":{"feedbackEndpoint":{"feedbackToken":"R"}}`
		remove = `"defaultServiceEndpoint":{"feedbackEndpoint":{"feedbackToken":"R"}},"toggledServiceEndpoint":{"feedbackEndpoint":{"feedbackToken":"A"}}`
	)
	page := `{"contents":{"musicShelfRenderer":{"contents":[` + strings.Join([]string{
		row("toggled", toggle(`"defaultIcon":{"iconType":"BOOKMARK_BORDER"},"isToggled":true,`+add)),
		row("untoggled", toggle(`"defaultIcon":{"iconType":"BOOKMARK_BORDER"},"isToggled":false,`+add)),
		row("offers save", toggle(`"defaultIcon":{"iconType":"LIBRARY_ADD"},`+add)),
		row("offers removal", toggle(`"defaultIcon":{"iconType":"LIBRARY_SAVED"},`+remove)),
		row("pin", toggle(`"defaultIcon":{"iconType":"KEEP"},`+add)),
		row("playlist toggle", toggle(`"defaultServiceEndpoint":{"likeEndpoint":{"status":"LIKE","target":{"playlistId":"PL1"}}}`)),
		row("plain", ""),
	}, ",") + `]}}}`
	got := map[string]*SongLibrary{}
	for _, item := range browseResult(decodeResponse(json.RawMessage(page))).Items {
		got[item.Title] = item.SongLibrary
	}
	want := map[string]*SongLibrary{
		"toggled":         {Saved: true, Add: "A", Remove: "R"},
		"untoggled":       {Add: "A", Remove: "R"},
		"offers save":     {Add: "A", Remove: "R"},
		"offers removal":  {Saved: true, Add: "A", Remove: "R"},
		"pin":             nil,
		"playlist toggle": nil,
		"plain":           nil,
	}
	for title, state := range want {
		g, ok := got[title]
		if !ok {
			t.Errorf("%s: no item parsed", title)
			continue
		}
		if (g == nil) != (state == nil) || (g != nil && *g != *state) {
			t.Errorf("%s: song library %+v, want %+v", title, g, state)
		}
	}
}
