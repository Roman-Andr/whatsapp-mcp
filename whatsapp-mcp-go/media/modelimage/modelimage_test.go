package modelimage

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/gen2brain/webp"
)

func photo(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 90, 255})
		}
	}
	return img
}

// noise returns an image that compresses badly, to make large files.
func noise(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	return img
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func encodeJPEG(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 90})
	return b.Bytes()
}

func size(t *testing.T, img Image) (int, int) {
	t.Helper()
	cfg, format, err := image.DecodeConfig(bytes.NewReader(img.Data))
	if err != nil {
		t.Fatal(err)
	}
	if format != img.Format {
		t.Fatalf("data is %s, labelled %s", format, img.Format)
	}
	return cfg.Width, cfg.Height
}

func TestSmallJPEGAndPNGPassThrough(t *testing.T) {
	for format, data := range map[string][]byte{
		"jpeg": encodeJPEG(photo(1568, 800)),
		"png":  encodePNG(photo(640, 480)),
	} {
		got, err := FromBytes(data)
		if err != nil {
			t.Fatal(err)
		}
		if got.Format != format || !bytes.Equal(got.Data, data) {
			t.Errorf("%s: not passed through (got %s, %d bytes)", format, got.Format, len(got.Data))
		}
		if got.MIMEType() != "image/"+format {
			t.Errorf("MIMEType %q", got.MIMEType())
		}
	}
}

func TestLargeImageIsShrunk(t *testing.T) {
	got, err := FromBytes(encodeJPEG(photo(4000, 3000)))
	if err != nil {
		t.Fatal(err)
	}
	if w, h := size(t, got); got.Format != "jpeg" || w != 1568 || h != 1176 {
		t.Fatalf("got %s %dx%d, want jpeg 1568x1176", got.Format, w, h)
	}
}

func TestHeavySmallPNGIsReencoded(t *testing.T) {
	data := encodePNG(noise(1000, 1000))
	if len(data) <= MaxBytes {
		t.Fatalf("fixture is only %d bytes", len(data))
	}
	got, err := FromBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	// Fits already, so the size stays; only the encoding changes.
	if w, h := size(t, got); got.Format != "jpeg" || w != 1000 || h != 1000 {
		t.Fatalf("got %s %dx%d", got.Format, w, h)
	}
}

func TestOtherFormatsBecomeJPEG(t *testing.T) {
	var g, w bytes.Buffer
	gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 50, 40), []color.Color{color.Black, color.White}), nil)
	webp.Encode(&w, photo(300, 200))
	for name, data := range map[string][]byte{"gif": g.Bytes(), "webp": w.Bytes()} {
		got, err := FromBytes(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.Format != "jpeg" {
			t.Fatalf("%s: format %s", name, got.Format)
		}
		size(t, got)
	}
}

func TestTransparencyOverWhite(t *testing.T) {
	sticker := image.NewNRGBA(image.Rect(0, 0, 512, 512)) // fully transparent
	sticker.SetNRGBA(256, 256, color.NRGBA{0, 0, 0, 255})
	var w bytes.Buffer
	webp.Encode(&w, sticker, webp.Options{Lossless: true})
	got, err := FromBytes(w.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(got.Data))
	if err != nil {
		t.Fatal(err)
	}
	if r, g, b, _ := img.At(10, 10).RGBA(); r>>8 < 250 || g>>8 < 250 || b>>8 < 250 {
		t.Fatalf("transparent area is %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
}

func TestErrors(t *testing.T) {
	if _, err := FromBytes([]byte("nope")); err == nil {
		t.Error("garbage accepted")
	}
	if got, err := FromBytes(encodePNG(photo(10, 10))); err != nil || got.Format != "png" {
		t.Errorf("FromBytes: %v %v", got.Format, err)
	}
}

func BenchmarkPhoto12MP(b *testing.B) {
	data := encodeJPEG(photo(4000, 3000))
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := FromBytes(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPassThrough(b *testing.B) {
	data := encodeJPEG(photo(1200, 900))
	for b.Loop() {
		if _, err := FromBytes(data); err != nil {
			b.Fatal(err)
		}
	}
}
