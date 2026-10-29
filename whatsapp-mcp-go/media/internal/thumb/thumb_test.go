package thumb

import (
	"image"
	"image/color"
	"testing"
)

func TestSizeMatchesPillow(t *testing.T) {
	// Expected values from Pillow 13: Image.new('RGB', (w, h)).thumbnail(box).
	for _, c := range []struct{ w, h, box, ww, wh int }{
		{4000, 3000, 1568, 1568, 1176},
		{3000, 4000, 1568, 1176, 1568},
		{1000, 1000, 512, 512, 512},
		{1920, 1080, 512, 512, 288},
		{1080, 1920, 512, 288, 512},
		{1000, 333, 512, 512, 170},
		{333, 1000, 512, 170, 512},
		{10000, 3, 512, 512, 1},
		{100, 50, 512, 100, 50},
		{512, 512, 512, 512, 512},
		{513, 100, 512, 512, 100},
		{1569, 1568, 1568, 1568, 1567},
	} {
		if w, h := Size(c.w, c.h, c.box, c.box); w != c.ww || h != c.wh {
			t.Errorf("Size(%dx%d, %d) = %dx%d, want %dx%d", c.w, c.h, c.box, w, h, c.ww, c.wh)
		}
	}
}

func TestFit(t *testing.T) {
	src := image.NewRGBA(image.Rect(10, 10, 2010, 1010))
	for y := 10; y < 1010; y++ {
		for x := 10; x < 2010; x++ {
			src.Set(x, y, color.RGBA{200, 100, 50, 255})
		}
	}
	got := Fit(src, 512, 512)
	if b := got.Bounds(); b != image.Rect(0, 0, 512, 256) {
		t.Fatalf("bounds %v", b)
	}
	if c := color.NRGBAModel.Convert(got.At(256, 128)).(color.NRGBA); c != (color.NRGBA{200, 100, 50, 255}) {
		t.Fatalf("colour %v", c)
	}
	gray := Fit(image.NewGray(image.Rect(0, 0, 2000, 2000)), 100, 100)
	if _, ok := gray.(*image.Gray); !ok || gray.Bounds().Dx() != 100 {
		t.Fatalf("grey image came back as %T %v", gray, gray.Bounds())
	}
}

func TestFlatten(t *testing.T) {
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 0})
	src.SetNRGBA(1, 0, color.NRGBA{255, 0, 0, 255})
	got := Flatten(src, color.White)
	if got.RGBAAt(0, 0) != (color.RGBA{255, 255, 255, 255}) || got.RGBAAt(1, 0) != (color.RGBA{255, 0, 0, 255}) {
		t.Fatalf("got %v %v", got.RGBAAt(0, 0), got.RGBAAt(1, 0))
	}
	if Opaque(src) || !Opaque(got) {
		t.Fatal("Opaque wrong")
	}
}
