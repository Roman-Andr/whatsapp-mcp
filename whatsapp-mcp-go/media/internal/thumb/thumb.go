// Package thumb shrinks images the way Pillow's Image.thumbnail does, so the
// sticker and model-image conversions produce Pillow's sizes.
package thumb

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"

	// Every format Pillow commonly opens; webp from gen2brain/webp, which
	// unlike x/image/webp also reads animated WebP (most WhatsApp stickers),
	// giving the first frame as Pillow does.
	_ "github.com/gen2brain/webp"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// Size returns the size Pillow's thumbnail((maxW, maxH)) gives a w x h image:
// unchanged when it already fits, else the largest size within the box that
// keeps the aspect ratio, each side rounded (down or up) to whichever keeps
// the ratio closer, and at least 1.
func Size(w, h, maxW, maxH int) (int, int) {
	if maxW >= w && maxH >= h {
		return w, h
	}
	aspect := float64(w) / float64(h)
	x, y := maxW, maxH
	if float64(x)/float64(y) >= aspect {
		x = roundAspect(float64(y)*aspect, func(n float64) float64 { return math.Abs(aspect - n/float64(y)) })
	} else {
		y = roundAspect(float64(x)/aspect, func(n float64) float64 {
			if n == 0 {
				return 0
			}
			return math.Abs(aspect - float64(x)/n)
		})
	}
	return x, y
}

// roundAspect is Pillow's round_aspect: floor or ceil, whichever key ranks
// lower (floor on a tie, as Python's min keeps the first).
func roundAspect(n float64, key func(float64) float64) int {
	lo, hi := math.Floor(n), math.Ceil(n)
	best := lo
	if key(hi) < key(lo) {
		best = hi
	}
	return max(int(best), 1)
}

// Fit returns img shrunk to fit maxW x maxH (Size) as RGBA, or as Gray for a
// grey image, with its origin at 0,0 (RGBA because x/image/draw's fast paths
// write only RGBA). Catmull-Rom is the
// bicubic filter Pillow's thumbnail uses; x/image/draw widens the kernel when
// shrinking, so a large reduction averages instead of aliasing.
func Fit(img image.Image, maxW, maxH int) image.Image {
	b := img.Bounds()
	w, h := Size(b.Dx(), b.Dy(), maxW, maxH)
	var dst draw.Image
	if _, gray := img.(*image.Gray); gray {
		dst = image.NewGray(image.Rect(0, 0, w, h))
	} else {
		dst = image.NewRGBA(image.Rect(0, 0, w, h))
	}
	if w == b.Dx() && h == b.Dy() {
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	}
	return dst
}

// Flatten draws img over an opaque background, for formats without alpha.
func Flatten(img image.Image, bg color.Color) *image.RGBA {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Over)
	return dst
}

// Opaque reports whether every pixel of img is fully opaque.
func Opaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	return false
}
