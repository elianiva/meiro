package m3

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

func handleWidth(it *image.RGBA) int {
	bounds := it.Bounds()
	background := it.At(bounds.Min.X, bounds.Min.Y)
	y := bounds.Min.Y + bounds.Dy()/2 - 8
	n := 0
	for x := bounds.Min.X + 2; x < bounds.Max.X-2; x++ {
		if it.At(x, y) != background {
			n++
		}
	}
	return n
}

func TestTheSliderHandleKeepsItsWidthAsItMoves(t *testing.T) {
	value := 40.0
	tt := ui.NewTester(func(c *ui.Context) {
		Provide(c, New(Config{}, false))
		Slider(c, &value, 0, 200, SliderSpec{Label: "seek", Key: "seek"})
	}, 400, 80)
	tt.SetScale(1.2)
	tt.Frame()
	want := handleWidth(tt.Image())
	if want == 0 {
		t.Fatal("the slider painted no handle")
	}
	for step := range 40 {
		value = 40 + float64(step)*0.07
		tt.Frame()
		if got := handleWidth(tt.Image()); got != want {
			t.Fatalf("at %v the handle is %d pixels wide, want %d, as it was at the start", value, got, want)
		}
	}
}
