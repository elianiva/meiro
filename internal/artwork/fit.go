package artwork

import (
	"image"
	"image/draw"

	xdraw "golang.org/x/image/draw"
)

// fit crops img to the square its Cover fit would show and shrinks it to side
// pixels across when it is larger. A picture smaller than side is not enlarged.
func fit(img image.Image, side int) *image.RGBA {
	b := img.Bounds()
	crop := min(b.Dx(), b.Dy())
	x, y := b.Min.X+(b.Dx()-crop)/2, b.Min.Y+(b.Dy()-crop)/2
	src := image.Rect(x, y, x+crop, y+crop)
	out := crop
	if side > 0 && side < crop {
		out = side
	}
	dst := image.NewRGBA(image.Rect(0, 0, out, out))
	if out == crop {
		draw.Draw(dst, dst.Bounds(), img, src.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, src, xdraw.Src, nil)
	}
	return dst
}
