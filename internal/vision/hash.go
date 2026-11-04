// Package vision reads the capture board and recognises the results screen
// without OCR: fixed-layout regions are reduced to 64-bit perceptual hashes
// (dHash) and compared against hashes learned from real frames.
//
// Why not OCR: the results screen is a fixed layout, so "which fighter
// portrait is in this box" is a template-matching problem. A dHash is a few
// hundred integer operations per region, needs no model files, no C
// libraries and no network, and is robust to the mild scaling/compression
// a capture board introduces. Player name tags are matched the same way, so
// no OCR is needed for a fixed group of members (see docs/VISION.md).
package vision

import (
	"image"
	"image/color"
	"math/bits"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// Rect converts a fractional region into pixel coordinates of bounds.
func Rect(bounds image.Rectangle, r config.Region) image.Rectangle {
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	x0 := bounds.Min.X + int(r.X*w)
	y0 := bounds.Min.Y + int(r.Y*h)
	x1 := bounds.Min.X + int((r.X+r.W)*w)
	y1 := bounds.Min.Y + int((r.Y+r.H)*h)
	if x1 <= x0 {
		x1 = x0 + 1
	}
	if y1 <= y0 {
		y1 = y0 + 1
	}
	return image.Rect(x0, y0, x1, y1).Intersect(bounds)
}

// luma returns the perceived brightness of a pixel.
func luma(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
}

// grayGrid box-filters the region down to cols x rows brightness cells.
func grayGrid(img image.Image, r image.Rectangle, cols, rows int) []float64 {
	out := make([]float64, cols*rows)
	cw := float64(r.Dx()) / float64(cols)
	ch := float64(r.Dy()) / float64(rows)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			x0 := r.Min.X + int(float64(gx)*cw)
			y0 := r.Min.Y + int(float64(gy)*ch)
			x1 := r.Min.X + int(float64(gx+1)*cw)
			y1 := r.Min.Y + int(float64(gy+1)*ch)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var sum float64
			n := 0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					sum += luma(img.At(x, y))
					n++
				}
			}
			out[gy*cols+gx] = sum / float64(n)
		}
	}
	return out
}

// DHash computes a 64-bit difference hash of region r of img.
func DHash(img image.Image, r image.Rectangle) uint64 {
	g := grayGrid(img, r, 9, 8)
	var h uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			h <<= 1
			if g[y*9+x] < g[y*9+x+1] {
				h |= 1
			}
		}
	}
	return h
}

// Hamming is the number of differing bits between two hashes (0..64).
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }
