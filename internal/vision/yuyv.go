package vision

import (
	"image"
	"image/color"
)

// YUYV is a frame in the capture board's native packed 4:2:2 layout
// (ffmpeg's yuyv422: Y0 U Y1 V per pair of pixels, BT.601 limited range).
//
// The board delivers YUY2 and every detection step samples only a few
// hundred pixels per frame, so converting the whole 1080p frame to RGB in
// ffmpeg (a full core at 30 fps on the mini PC) was wasted work. ffmpeg now
// passes the bytes through and pixels are converted where they are read.
type YUYV struct {
	Pix    []byte
	Stride int // bytes per row
	Rect   image.Rectangle
}

// NewYUYV allocates a frame of the given even width.
func NewYUYV(w, h int) *YUYV {
	w &^= 1
	return &YUYV{Pix: make([]byte, w*h*2), Stride: w * 2, Rect: image.Rect(0, 0, w, h)}
}

func (p *YUYV) ColorModel() color.Model { return color.RGBAModel }
func (p *YUYV) Bounds() image.Rectangle { return p.Rect }

// RGBAt returns the pixel at (x, y) as 8-bit RGB. (x, y) must be inside
// Bounds.
func (p *YUYV) RGBAt(x, y int) (r, g, b uint8) {
	i := (y-p.Rect.Min.Y)*p.Stride + (x-p.Rect.Min.X)/2*4
	q := p.Pix[i : i+4 : i+4]
	yy := q[0]
	if (x-p.Rect.Min.X)&1 == 1 {
		yy = q[2]
	}
	return yuvToRGB(yy, q[1], q[3])
}

func (p *YUYV) At(x, y int) color.Color {
	if !(image.Point{x, y}.In(p.Rect)) {
		return color.RGBA{}
	}
	r, g, b := p.RGBAt(x, y)
	return color.RGBA{r, g, b, 255}
}

// ToRGBA converts the whole frame (for PNG output).
func (p *YUYV) ToRGBA() *image.RGBA {
	out := image.NewRGBA(p.Rect)
	for y := p.Rect.Min.Y; y < p.Rect.Max.Y; y++ {
		o := out.PixOffset(p.Rect.Min.X, y)
		for x := p.Rect.Min.X; x < p.Rect.Max.X; x++ {
			r, g, b := p.RGBAt(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = r, g, b, 255
			o += 4
		}
	}
	return out
}

// yuvToRGB is the BT.601 limited-range conversion ffmpeg applies to yuyv422
// by default, so hashes and name-tag crops look the same as they did when
// ffmpeg did the conversion.
func yuvToRGB(y, u, v uint8) (r, g, b uint8) {
	c := int(y) - 16
	d := int(u) - 128
	e := int(v) - 128
	return clamp8((298*c + 409*e + 128) >> 8),
		clamp8((298*c - 100*d - 208*e + 128) >> 8),
		clamp8((298*c + 516*d + 128) >> 8)
}

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// rgbSampler is implemented by frame types that can hand out a pixel's RGB
// without boxing a color.Color; the hash functions use it to avoid an
// allocation per sampled pixel.
type rgbSampler interface {
	RGBAt(x, y int) (r, g, b uint8)
}

// rgbAt reads one pixel through the fastest path the image offers.
func rgbAt(img image.Image, x, y int) (r, g, b uint8) {
	switch p := img.(type) {
	case *image.RGBA:
		i := p.PixOffset(x, y)
		return p.Pix[i], p.Pix[i+1], p.Pix[i+2]
	case rgbSampler:
		return p.RGBAt(x, y)
	}
	r16, g16, b16, _ := img.At(x, y).RGBA()
	return uint8(r16 >> 8), uint8(g16 >> 8), uint8(b16 >> 8)
}

// toRGBA returns img as an *image.RGBA, converting a YUYV frame; other
// image types are returned unchanged.
func toRGBA(img image.Image) image.Image {
	if p, ok := img.(*YUYV); ok {
		return p.ToRGBA()
	}
	return img
}
