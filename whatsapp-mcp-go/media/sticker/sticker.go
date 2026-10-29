// Package sticker turns any image into a WhatsApp sticker: a 512x512 WebP
// with the picture centred on a transparent canvas.
//
// WebP encoding comes from github.com/gen2brain/webp, libwebp transpiled to
// pure Go: no cgo and no system libwebp (absent here), while still producing
// lossy WebP like Pillow does. A lossless-only pure-Go encoder would make
// photo stickers several times larger than WhatsApp's 100 KB sticker limit.
// When a shared libwebp is installed, gen2brain/webp uses it instead, faster.
package sticker

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"io"

	"github.com/gen2brain/webp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/internal/thumb"
)

// Size is the side of a WhatsApp sticker.
const Size = 512

// Quality is the WebP quality, Pillow's default for save(format='WEBP').
const Quality = 80

// Canvas returns img shrunk to fit 512x512 (never enlarged, as Pillow's
// thumbnail) and centred on a transparent 512x512 canvas.
func Canvas(img image.Image) *image.NRGBA {
	small := thumb.Fit(img, Size, Size)
	b := small.Bounds()
	canvas := image.NewNRGBA(image.Rect(0, 0, Size, Size))
	at := image.Pt((Size-b.Dx())/2, (Size-b.Dy())/2)
	draw.Draw(canvas, b.Add(at), small, b.Min, draw.Src)
	return canvas
}

// Encode writes img as a sticker to w.
func Encode(w io.Writer, img image.Image) error {
	return webp.Encode(w, Canvas(img), webp.Options{Quality: Quality, Method: 4})
}

// Convert decodes an image (JPEG, PNG, GIF, WebP, BMP or TIFF; the first frame
// of an animation) and returns it as sticker WebP bytes.
func Convert(r io.Reader) ([]byte, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}
	var out bytes.Buffer
	if err := Encode(&out, img); err != nil {
		return nil, fmt.Errorf("encode sticker: %w", err)
	}
	return out.Bytes(), nil
}
