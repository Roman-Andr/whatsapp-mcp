// Package modelimage makes a picture small enough to hand to the model: a JPEG or PNG that already fits
// (longest side at most 1568 px, at most 1 MB) goes through untouched;
// anything else is shrunk to fit 1568x1568 and re-encoded as JPEG quality 85.
package modelimage

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/internal/thumb"
)

const (
	// MaxSide is the longest side sent to the model; larger images are
	// scaled down by the API anyway, so sending more only costs tokens.
	MaxSide = 1568
	// MaxBytes is the largest image passed through as is.
	MaxBytes = 1_000_000
	// Quality of the JPEG re-encode.
	Quality = 85
)

// Image is an encoded picture and its format, "jpeg" or "png", which is what
// an MCP image content needs (as image/<format>).
type Image struct {
	Data   []byte
	Format string
}

// MIMEType returns the image's media type.
func (i Image) MIMEType() string { return "image/" + i.Format }

// FromBytes prepares an encoded image (JPEG, PNG, GIF, WebP, BMP or TIFF) for
// the model.
func FromBytes(data []byte) (Image, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode image: %w", err)
	}
	if max(cfg.Width, cfg.Height) <= MaxSide && len(data) <= MaxBytes && (format == "jpeg" || format == "png") {
		return Image{Data: data, Format: format}, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("decode image: %w", err)
	}
	return Encode(img)
}

// Encode shrinks img to fit MaxSide and encodes it as JPEG. Transparency
// (stickers, screenshots with alpha) is laid over white: Pillow's
// convert('RGB') drops alpha and shows whatever colour transparent pixels
// hold, often black, which hides a black sticker on its own background.
func Encode(img image.Image) (Image, error) {
	small := thumb.Fit(img, MaxSide, MaxSide)
	if !thumb.Opaque(small) {
		small = thumb.Flatten(small, color.White)
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, small, &jpeg.Options{Quality: Quality}); err != nil {
		return Image{}, fmt.Errorf("encode jpeg: %w", err)
	}
	return Image{Data: out.Bytes(), Format: "jpeg"}, nil
}
