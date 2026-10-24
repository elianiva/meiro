package artwork

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"
)

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNotificationIconFetchesAndKeepsArtworkColor(t *testing.T) {
	var source bytes.Buffer
	cover := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			cover.SetRGBA(x, y, color.RGBA{R: 210, G: 80, B: 30, A: 255})
		}
	}
	if err := png.Encode(&source, cover); err != nil {
		t.Fatal(err)
	}
	var requested string
	client := &http.Client{Transport: notificationTransport(func(request *http.Request) (*http.Response, error) {
		requested = request.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(source.Bytes())),
			Header:     make(http.Header),
		}, nil
	})}
	cache := New(client, func() {})
	t.Cleanup(cache.Close)

	got, err := cache.NotificationIcon(context.Background(), "https://lh3.googleusercontent.com/cover=w544-h544")
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://lh3.googleusercontent.com/cover=w96-h96"; requested != want {
		t.Errorf("notification artwork URL = %q, want %q", requested, want)
	}
	icon, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if icon.Bounds().Dx() != notificationIconSize || icon.Bounds().Dy() != notificationIconSize {
		t.Fatalf("icon size = %v, want %dx%d", icon.Bounds(), notificationIconSize, notificationIconSize)
	}
	if pixel := color.RGBAModel.Convert(icon.At(notificationIconSize/2, notificationIconSize/2)).(color.RGBA); pixel != (color.RGBA{R: 210, G: 80, B: 30, A: 255}) {
		t.Errorf("center pixel = %v, want the full-color cover, not a white template", pixel)
	}
}

func TestNotificationIconAddsOpaqueBackgroundToTransparentArtwork(t *testing.T) {
	var source bytes.Buffer
	if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	got, err := notificationIconPNG(source.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	icon, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	if pixel := color.RGBAModel.Convert(icon.At(0, 0)).(color.RGBA); pixel != (color.RGBA{R: 32, G: 33, B: 36, A: 255}) {
		t.Errorf("transparent corner = %v, want opaque dark background", pixel)
	}
}
