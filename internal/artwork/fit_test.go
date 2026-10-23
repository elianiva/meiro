package artwork

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/elianiva/meiro/m3"
)

func solid(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 100, 50, 255}), image.Point{}, draw.Src)
	return img
}

func TestFitCropsAndShrinks(t *testing.T) {
	got := fit(solid(400, 200), 100)
	if got.Bounds().Dx() != 100 || got.Bounds().Dy() != 100 {
		t.Fatalf("size = %v, want 100x100", got.Bounds())
	}
	if a := got.RGBAAt(0, 0).A; a != 255 {
		t.Errorf("corner alpha = %d, want 255 (fit keeps square pixels)", a)
	}
	if a := got.RGBAAt(50, 50).A; a != 255 {
		t.Errorf("centre alpha = %d, want 255", a)
	}
	if a := got.RGBAAt(50, 0).A; a != 255 {
		t.Errorf("edge middle alpha = %d, want 255", a)
	}
}

func TestFitKeepsSmallPicture(t *testing.T) {
	got := fit(solid(60, 60), 320)
	if got.Bounds().Dx() != 60 {
		t.Fatalf("size = %v, want 60 (not enlarged)", got.Bounds())
	}
	if got.RGBAAt(0, 59).A != 255 || got.RGBAAt(30, 30).A != 255 {
		t.Error("fit should preserve the square picture")
	}
}

func TestArtAndAvatarRoundImagesAtDrawTime(t *testing.T) {
	const side = 32
	pic := ui.NewBitmap(solid(side, side))
	views := map[string]func(*ui.Context){
		"art": func(c *ui.Context) {
			m3.Art(c, pic, side, 8)
		},
		"avatar": func(c *ui.Context) {
			m3.Avatar(c, "A", pic, side)
		},
	}
	for name, view := range views {
		t.Run(name, func(t *testing.T) {
			tt := ui.NewTester(func(c *ui.Context) {
				m3.Provide(c, m3.New(m3.Config{Mode: m3.Light}, false))
				view(c)
			}, side, side)
			corner := tt.Image().RGBAAt(0, 0)
			center := tt.Image().RGBAAt(side/2, side/2)
			if center == corner {
				t.Fatalf("corner pixel matches artwork center: %v", center)
			}
		})
	}
}
