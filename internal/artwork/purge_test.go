package artwork

import (
	"bytes"
	"image"
	"image/png"
	"io"
	"net/http"
	"testing"
	"time"
)

// A purged cache holds no pixels, and a cover asked for again is fetched again.
func TestPurgeDropsArtworkUntilAskedAgain(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	fetches := 0
	client := &http.Client{Transport: notificationTransport(func(*http.Request) (*http.Response, error) {
		fetches++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(source.Bytes())), Header: make(http.Header)}, nil
	})}
	landed := make(chan struct{}, 4)
	cache := New(client, func() { landed <- struct{}{} })
	t.Cleanup(cache.Close)
	const url = "https://lh3.googleusercontent.com/cover=w544-h544"
	wait := func() {
		select {
		case <-landed:
		case <-time.After(5 * time.Second):
			t.Fatal("the cover never landed")
		}
	}

	cache.Bitmap(url, 64)
	wait()
	if cache.Bitmap(url, 64) == nil {
		t.Fatal("the cover did not land in the cache")
	}
	cache.Purge()
	if s := cache.Stats(); s.Bitmaps != 0 || s.Bytes != 0 || s.Sources != 0 || s.SourceBytes != 0 {
		t.Fatalf("after Purge the cache holds %+v, want nothing", s)
	}
	if cache.Bitmap(url, 64) != nil {
		t.Fatal("a purged cover is still drawn")
	}
	wait()
	if cache.Bitmap(url, 64) == nil || fetches != 2 {
		t.Fatalf("the cover came back after %d fetches, want it fetched again once", fetches)
	}
}
