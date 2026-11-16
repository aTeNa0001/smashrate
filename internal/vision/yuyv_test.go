package vision

import (
	"image"
	"testing"
)

// rgbToYUYV packs an RGBA image the way the capture board does (BT.601
// limited range, chroma shared by each horizontal pair).
func rgbToYUYV(src *image.RGBA) *YUYV {
	b := src.Bounds()
	out := NewYUYV(b.Dx(), b.Dy())
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x += 2 {
			i := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			r0, g0, b0 := int(src.Pix[i]), int(src.Pix[i+1]), int(src.Pix[i+2])
			r1, g1, b1 := int(src.Pix[i+4]), int(src.Pix[i+5]), int(src.Pix[i+6])
			y0 := ((66*r0 + 129*g0 + 25*b0 + 128) >> 8) + 16
			y1 := ((66*r1 + 129*g1 + 25*b1 + 128) >> 8) + 16
			r, g, bb := (r0+r1)/2, (g0+g1)/2, (b0+b1)/2
			u := ((-38*r - 74*g + 112*bb + 128) >> 8) + 128
			v := ((112*r - 94*g - 18*bb + 128) >> 8) + 128
			o := y*out.Stride + x*2
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(y0), uint8(u), uint8(y1), uint8(v)
		}
	}
	return out
}

func TestYUYVRoundTripsColours(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x, c := range [][3]uint8{{255, 255, 255}, {255, 255, 255}, {0, 0, 0}, {0, 0, 0}} {
		i := img.PixOffset(x, 0)
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c[0], c[1], c[2], 255
	}
	p := rgbToYUYV(img)
	for x := 0; x < 4; x++ {
		r, g, b := p.RGBAt(x, 0)
		want := img.Pix[img.PixOffset(x, 0):]
		for k, got := range []uint8{r, g, b} {
			if d := int(got) - int(want[k]); d > 3 || d < -3 {
				t.Fatalf("pixel %d channel %d: got %d want %d", x, k, got, want[k])
			}
		}
	}
	if r, g, b := rgbAt(p, 0, 0); r < 250 || g < 250 || b < 250 {
		t.Fatalf("white through rgbAt: %d %d %d", r, g, b)
	}
	if !p.ToRGBA().Bounds().Eq(img.Bounds()) {
		t.Fatal("ToRGBA bounds")
	}
}

// Detection must see the same screen whether the frame arrived as RGBA
// (a PNG, `vision test`) or as the board's YUYV (live capture).
func TestYUYVHashesMatchRGBA(t *testing.T) {
	cfg := testVisionCfg()
	rgba := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	yuyv := rgbToYUYV(rgba)
	for _, l := range cfg.Layouts {
		pr, br := screenHashes(rgba, l)
		py, by := screenHashes(yuyv, l)
		if pr != py || br != by {
			t.Fatalf("layout %d: white hashes differ: %x/%x vs %x/%x", l.Players, pr, br, py, by)
		}
	}
	if d := Hamming(DHash(rgba, rgba.Bounds()), DHash(yuyv, yuyv.Bounds())); d > 2 {
		t.Fatalf("dHash differs by %d bits", d)
	}
	cr, cy := Crop(rgba, image.Rect(10, 10, 50, 30)), Crop(yuyv, image.Rect(10, 10, 50, 30))
	for i := 0; i < len(cr.Pix); i += 4 {
		if d := int(cr.Pix[i]) - int(cy.Pix[i]); d > 6 || d < -6 {
			t.Fatalf("crop pixel %d: %d vs %d", i/4, cr.Pix[i], cy.Pix[i])
		}
	}
}
