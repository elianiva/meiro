package youtube

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestLibraryWritesAimTheLikeAtThePlaylist(t *testing.T) {
	for _, test := range []struct {
		save bool
		path string
	}{
		{true, "/youtubei/v1/like/like"},
		{false, "/youtubei/v1/like/removelike"},
	} {
		client, path, payload := writeClient(t, http.StatusOK, `{"responseContext":{}}`)
		var err error
		if test.save {
			err = client.AddToLibrary(context.Background(), "VLPL1")
		} else {
			err = client.RemoveFromLibrary(context.Background(), "PL1")
		}
		if err != nil {
			t.Fatal(err)
		}
		target, _ := (*payload)["target"].(map[string]any)
		if *path != test.path || target["playlistId"] != "PL1" || len(target) != 1 {
			t.Errorf("save=%v: path %q, target %#v", test.save, *path, target)
		}
	}
}

func TestLibraryWritesRefusals(t *testing.T) {
	if err := NewClient(Options{}).AddToLibrary(context.Background(), "PL1"); err == nil {
		t.Error("a signed-out save was sent")
	}
	client, path, _ := writeClient(t, http.StatusOK, `{}`)
	if err := client.RemoveFromLibrary(context.Background(), " VL "); err == nil || *path != "" {
		t.Errorf("a removal without a playlist was sent to %q: %v", *path, err)
	}
	client, _, _ = writeClient(t, http.StatusForbidden, `{"error":{"code":403}}`)
	if err := client.AddToLibrary(context.Background(), "PL1"); err == nil {
		t.Error("a 403 was not an error")
	}
}

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
