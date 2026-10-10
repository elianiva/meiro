package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"
)

func TestArtworkColour(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: 30, G: 60, B: 200, A: 255})
		}
	}
	for y := 0; y < 6; y++ {
		for x := 0; x < 6; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	got, ok := dominantColour(decoded(t, data.Bytes()))
	if !ok || got.B < 150 || got.R > 80 {
		t.Errorf("dominantColour = %v, %v; want the blue", got, ok)
	}
	grey := image.NewRGBA(image.Rect(0, 0, 8, 8))
	data.Reset()
	if err := png.Encode(&data, grey); err != nil {
		t.Fatal(err)
	}
	if _, ok := dominantColour(decoded(t, data.Bytes())); ok {
		t.Errorf("a grey picture should have no dominant colour")
	}
}

// decoded reads a picture the way the thumbnail cache does.
func decoded(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func TestClockAndDuration(t *testing.T) {
	if got := clock(3*time.Minute + 42*time.Second); got != "3:42" {
		t.Errorf("clock = %q", got)
	}
	if got := clock(time.Hour + 2*time.Minute + 3*time.Second); got != "1:02:03" {
		t.Errorf("clock = %q", got)
	}
	if got := parseDuration("3:42"); got != 3*time.Minute+42*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("1:02:03"); got != time.Hour+2*time.Minute+3*time.Second {
		t.Errorf("parseDuration = %v", got)
	}
	if got := parseDuration("2023"); got != 0 {
		t.Errorf("parseDuration of a year = %v", got)
	}
}
