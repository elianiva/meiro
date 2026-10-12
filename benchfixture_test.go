package main

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The benchmark fixtures: pages the size of a real account's, answered
// without the network. The same file runs against every version compared.

const (
	benchAlbumTracks  = 500
	benchLibraryItems = 600
)

func benchTwoRow(title, nav string, i int) string {
	return fmt.Sprintf(`{"musicTwoRowItemRenderer":{
		"title":{"runs":[{"text":"%s %d"}]},
		"subtitle":{"runs":[{"text":"Someone %d"}]},
		"thumbnailRenderer":{"musicThumbnailRenderer":{"thumbnail":{"thumbnails":[{"url":"https://img.test/%s%d=w544-h544-l90-rj"}]}}},
		"navigationEndpoint":%s
	}}`, title, i, i, title, i, nav)
}

func benchHome() string {
	var shelves []string
	for s := range 8 {
		var items []string
		for i := range 20 {
			nav := fmt.Sprintf(`{"watchEndpoint":{"videoId":"home-%d-%d"}}`, s, i)
			if s%2 == 1 {
				nav = fmt.Sprintf(`{"browseEndpoint":{"browseId":"MPREb_home%d_%d"}}`, s, i)
			}
			items = append(items, benchTwoRow(fmt.Sprintf("Shelf%d", s), nav, i))
		}
		shelves = append(shelves, fmt.Sprintf(`{"musicCarouselShelfRenderer":{
			"header":{"musicCarouselShelfBasicHeaderRenderer":{"title":{"runs":[{"text":"Shelf %d"}]}}},
			"contents":[%s]}}`, s, strings.Join(items, ",")))
	}
	return `{"contents":{"sectionListRenderer":{"contents":[` + strings.Join(shelves, ",") + `]}}}`
}

func benchAlbum() string {
	var rows []string
	for i := range benchAlbumTracks {
		rows = append(rows, fmt.Sprintf(`{"musicResponsiveListItemRenderer":{
			"playlistItemData":{"videoId":"album-%d"},
			"flexColumns":[
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Album Track %d"}]}}},
				{"musicResponsiveListItemFlexColumnRenderer":{"text":{"runs":[{"text":"Someone"},{"text":" • "},{"text":"3:%02d"}]}}}
			]}}`, i, i, i%60))
	}
	return `{"header":{"musicImmersiveHeaderRenderer":{"title":{"runs":[{"text":"Big Album"}]}}},
		"contents":{"musicShelfRenderer":{"contents":[` + strings.Join(rows, ",") + `]}}}`
}

func benchLibrary() string {
	var items []string
	for i := range benchLibraryItems {
		items = append(items, benchTwoRow("Playlist", fmt.Sprintf(`{"browseEndpoint":{"browseId":"VLPLbench%d"}}`, i), i))
	}
	return `{"contents":{"musicCarouselShelfRenderer":{"contents":[` + strings.Join(items, ",") + `]}}}`
}

var (
	benchHomeJSON    = benchHome()
	benchAlbumJSON   = benchAlbum()
	benchLibraryJSON = benchLibrary()
)

// benchMusic answers the pages the benchmarks open.
type benchMusic struct{}

func (benchMusic) RoundTrip(request *http.Request) (*http.Response, error) {
	asked := ""
	if request.Body != nil {
		body, _ := io.ReadAll(request.Body)
		asked = string(body)
	}
	reply := `{"contents":{}}`
	switch {
	case strings.Contains(asked, "MPREb_big"):
		reply = benchAlbumJSON
	case strings.Contains(asked, "FEmusic_library_landing"):
		reply = benchLibraryJSON
	case strings.Contains(asked, "FEmusic_home"):
		reply = benchHomeJSON
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    request,
	}, nil
}

// benchNoArtwork answers every picture with a 404.
type benchNoArtwork struct{}

func (benchNoArtwork) RoundTrip(request *http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusNotFound, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
}
