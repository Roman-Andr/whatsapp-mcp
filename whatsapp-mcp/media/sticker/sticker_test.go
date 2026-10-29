package sticker

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/gen2brain/webp"
)

// photo returns a w x h image with a gradient, so the encoder has detail.
func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	return img
}

func decodeSticker(t *testing.T, data []byte) image.Image {
	t.Helper()
	if !bytes.HasPrefix(data, []byte("RIFF")) || string(data[8:12]) != "WEBP" {
		t.Fatalf("not a WebP file: % x", data[:min(len(data), 16)])
	}
	img, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != Size || b.Dy() != Size {
		t.Fatalf("sticker is %v, want 512x512", b)
	}
	return img
}

func alpha(img image.Image, x, y int) uint8 {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA).A
}

func TestConvertWidePhotoIsPadded(t *testing.T) {
	var src bytes.Buffer
	jpeg.Encode(&src, photo(1920, 1080), &jpeg.Options{Quality: 90})
	data, err := Convert(&src)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeSticker(t, data)
	// 1920x1080 shrinks to 512x288, centred: rows 112..399 are the picture.
	for _, p := range []image.Point{{256, 0}, {256, 111}, {256, 400}, {0, 511}} {
		if a := alpha(img, p.X, p.Y); a != 0 {
			t.Errorf("padding at %v has alpha %d", p, a)
		}
	}
	for _, p := range []image.Point{{256, 112}, {0, 256}, {511, 399}} {
		if a := alpha(img, p.X, p.Y); a != 255 {
			t.Errorf("picture at %v has alpha %d", p, a)
		}
	}
	// Lossy, like Pillow: a photo sticker stays well under WhatsApp's 100 KB.
	if len(data) > 100_000 {
		t.Errorf("sticker is %d bytes", len(data))
	}
}

func TestConvertKeepsTransparencyAndSmallSize(t *testing.T) {
	// A 100x50 PNG with a transparent left half is not enlarged (as Pillow's
	// thumbnail) and keeps its own transparency.
	src := image.NewNRGBA(image.Rect(0, 0, 100, 50))
	for y := range 50 {
		for x := 50; x < 100; x++ {
			src.SetNRGBA(x, y, color.NRGBA{255, 0, 0, 255})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, src)
	data, err := Convert(&buf)
	if err != nil {
		t.Fatal(err)
	}
	img := decodeSticker(t, data)
	// Placed at (206, 231): its opaque half spans x 256..305.
	if a := alpha(img, 230, 256); a != 0 {
		t.Errorf("transparent half has alpha %d", a)
	}
	if a := alpha(img, 280, 256); a != 255 {
		t.Errorf("opaque half has alpha %d", a)
	}
	if a := alpha(img, 320, 256); a != 0 {
		t.Errorf("right padding has alpha %d", a)
	}
}

func TestConvertFormats(t *testing.T) {
	var g bytes.Buffer
	pal := image.NewPaletted(image.Rect(0, 0, 600, 600), []color.Color{color.Black, color.White})
	gif.Encode(&g, pal, nil)
	var w bytes.Buffer
	webp.Encode(&w, photo(800, 600))
	for name, data := range map[string][]byte{"gif": g.Bytes(), "webp": w.Bytes()} {
		out, err := Convert(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		decodeSticker(t, out)
	}
	if _, err := Convert(bytes.NewReader([]byte("not an image"))); err == nil {
		t.Fatal("garbage accepted")
	}
}

func BenchmarkConvertPhoto(b *testing.B) {
	var src bytes.Buffer
	jpeg.Encode(&src, photo(1600, 1200), &jpeg.Options{Quality: 90})
	data := src.Bytes()
	for b.Loop() {
		if _, err := Convert(bytes.NewReader(data)); err != nil {
			b.Fatal(err)
		}
	}
}
