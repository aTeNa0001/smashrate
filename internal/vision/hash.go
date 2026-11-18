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
	"math"
	"math/bits"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// Rect converts a fractional region into pixel coordinates of bounds. The
// size is derived from W/H alone so that the same region shape yields the
// same pixel size in every column, whatever the column's offset.
func Rect(bounds image.Rectangle, r config.Region) image.Rectangle {
	w, h := float64(bounds.Dx()), float64(bounds.Dy())
	x0 := bounds.Min.X + int(math.Round(r.X*w))
	y0 := bounds.Min.Y + int(math.Round(r.Y*h))
	pw, ph := int(math.Round(r.W*w)), int(math.Round(r.H*h))
	if pw < 1 {
		pw = 1
	}
	if ph < 1 {
		ph = 1
	}
	return image.Rect(x0, y0, x0+pw, y0+ph).Intersect(bounds)
}

// maxSamples caps how many pixels per axis a grid cell averages. A 9x8
// grid of a 1080p frame does not get more accurate from reading all 28,800
// pixels of each cell; at 10 fps that was the biggest CPU user on the mini PC.
const maxSamples = 16

// grayGrid box-filters the region down to cols x rows brightness cells.
func grayGrid(img image.Image, r image.Rectangle, cols, rows int) []float64 {
	out := make([]float64, cols*rows)
	// a region outside the frame (a mistyped layout) must not index the
	// pixel buffer out of range
	if r = r.Intersect(img.Bounds()); r.Empty() {
		return out
	}
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
			// sample at most maxSamples pixels per axis, evenly spread
			sx, sy := (x1-x0+maxSamples-1)/maxSamples, (y1-y0+maxSamples-1)/maxSamples
			var sum float64
			n := 0
			for y := y0; y < y1; y += sy {
				for x := x0; x < x1; x += sx {
					cr, cg, cb := rgbAt(img, x, y)
					sum += 0.299*float64(cr) + 0.587*float64(cg) + 0.114*float64(cb)
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

// WhiteHash marks which cells of a cols x rows grid over r contain
// near-white pixels (text), one bit per cell, row-major from the top left.
//
// The results screen is recognised by its stat-row labels (全滅/撃墜/落下/
// 自滅), white text drawn over a translucent panel with the fighter's
// portrait behind it. A brightness dHash of that area changes with the
// fighter, the capture format and the winner's enlarged panel (measured
// 13-16 bits between two real screens), while the text itself sits in the
// same cells every time: on the screenshots we have, this hash differs by
// 0-2 bits between screens of the same player count and by 12 or more
// between different ones (6 vs 7 players excepted, which the full-width
// band told apart by 27).
func WhiteHash(img image.Image, r image.Rectangle, cols, rows int) uint64 {
	var h uint64
	if r = r.Intersect(img.Bounds()); r.Empty() {
		return 0
	}
	cw := float64(r.Dx()) / float64(cols)
	ch := float64(r.Dy()) / float64(rows)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			x0, x1 := r.Min.X+int(float64(gx)*cw), r.Min.X+int(float64(gx+1)*cw)
			y0, y1 := r.Min.Y+int(float64(gy)*ch), r.Min.Y+int(float64(gy+1)*ch)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			sx, sy := (x1-x0+maxSamples-1)/maxSamples, (y1-y0+maxSamples-1)/maxSamples
			n, white := 0, 0
			for y := y0; y < y1; y += sy {
				for x := x0; x < x1; x += sx {
					cr, cg, cb := rgbAt(img, x, y)
					if cr > 200 && cg > 200 && cb > 200 {
						white++
					}
					n++
				}
			}
			h <<= 1
			// more than 2% of the cell is white: text, not a stray highlight
			if white*50 > n {
				h |= 1
			}
		}
	}
	return h
}

// Hamming is the number of differing bits between two hashes (0..64).
func Hamming(a, b uint64) int { return bits.OnesCount64(a ^ b) }

// TagHash identifies a name tag by where its white text sits: a 32x2
// WhiteHash of the text's own bounding box inside the region. A brightness
// dHash of the region barely saw the small text against the coloured band
// (two different players' tags matched the same template live on
// 2026-11-18); the text's column profile tells names apart while staying
// the same between frames and layouts.
func TagHash(img image.Image, r image.Rectangle) uint64 {
	return TagHashFrames([]image.Image{Crop(img, r)})
}

// TagHashFrames hashes a tag from several crops of it taken from different
// frames of the same screen. The sparkles that drift over a panel are
// white too and, when one touches the text, widen its box; they only ever
// add, so the crop with the narrowest text box is the one without them,
// and that is the one hashed (the earliest on a tie). One crop hashes as is.
func TagHashFrames(crops []image.Image) uint64 {
	best := -1
	bestW := 0
	for i, c := range crops {
		box, ok := whiteBox(c, c.Bounds())
		if !ok {
			continue
		}
		if best < 0 || box.Dx() < bestW {
			best, bestW = i, box.Dx()
		}
	}
	if best < 0 {
		if len(crops) == 0 {
			return 0
		}
		best = 0
	}
	c := crops[best]
	r := c.Bounds()
	w, h := r.Dx(), r.Dy()
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cr, cg, cb := rgbAt(c, r.Min.X+x, r.Min.Y+y)
			mask[y*w+x] = cr > 200 && cg > 200 && cb > 200
		}
	}
	box, ok := maskBox(mask, w, h)
	if !ok {
		box = image.Rect(0, 0, w, h)
	}
	return maskHash(mask, w, box, 32, 2)
}

// maskHash is WhiteHash over a boolean mask: one bit per cell of a cols x
// rows grid over box, set when more than 2% of the cell is on.
func maskHash(mask []bool, w int, box image.Rectangle, cols, rows int) uint64 {
	var hsh uint64
	cw := float64(box.Dx()) / float64(cols)
	ch := float64(box.Dy()) / float64(rows)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			x0, x1 := box.Min.X+int(float64(gx)*cw), box.Min.X+int(float64(gx+1)*cw)
			y0, y1 := box.Min.Y+int(float64(gy)*ch), box.Min.Y+int(float64(gy+1)*ch)
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			n, on := 0, 0
			for y := y0; y < y1 && y < box.Max.Y; y++ {
				for x := x0; x < x1 && x < box.Max.X; x++ {
					if mask[y*w+x] {
						on++
					}
					n++
				}
			}
			hsh <<= 1
			if n > 0 && on*50 > n {
				hsh |= 1
			}
		}
	}
	return hsh
}

// whiteBox is the bounding box of the tag's text inside r of img (see
// maskBox); used by diagnostics.
func whiteBox(img image.Image, r image.Rectangle) (image.Rectangle, bool) {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return r, false
	}
	w, h := r.Dx(), r.Dy()
	mask := make([]bool, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cr, cg, cb := rgbAt(img, r.Min.X+x, r.Min.Y+y)
			mask[y*w+x] = cr > 200 && cg > 200 && cb > 200
		}
	}
	box, ok := maskBox(mask, w, h)
	return box.Add(r.Min), ok
}

// maskBox is the bounding box of the text in a w x h mask: the longest
// run of rows holding on pixels, and within those rows the heaviest
// cluster of on columns; the badge's white edge to the left and the
// highlight along the winner's band to the right are separated from the
// text by clear gaps and fall outside. ok is false when there is too
// little to be text.
func maskBox(mask []bool, w, h int) (image.Rectangle, bool) {
	rows := make([]int, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mask[y*w+x] {
				rows[y]++
			}
		}
	}
	// the text band: the longest run of rows with a few on pixels,
	// bridging gaps of up to two rows (the dakuten sit above the kana)
	y0, y1 := longestRun(rows, 3, 2)
	if y1-y0 < 6 {
		return image.Rectangle{}, false
	}
	cols := make([]int, w)
	for y := y0; y < y1; y++ {
		for x := 0; x < w; x++ {
			if mask[y*w+x] {
				cols[x]++
			}
		}
	}
	// the text itself: the heaviest cluster of on columns, bridging the
	// spaces between glyphs but not the gap to the badge or the highlight
	x0, x1 := heaviestCluster(cols, 12)
	if x1-x0 < 16 {
		return image.Rectangle{}, false
	}
	return image.Rect(x0, y0, x1, y1), true
}

// longestRun returns [start, end) of the longest run of indexes whose count
// is at least min, allowing up to bridge consecutive misses inside a run.
func longestRun(counts []int, min, bridge int) (int, int) {
	bestS, bestE := 0, 0
	s, miss := -1, 0
	for i, c := range counts {
		if c >= min {
			if s < 0 {
				s = i
			}
			miss = 0
			if i+1-s > bestE-bestS {
				bestS, bestE = s, i+1
			}
			continue
		}
		if s >= 0 {
			miss++
			if miss > bridge {
				s = -1
			}
		}
	}
	return bestS, bestE
}

// heaviestCluster groups the columns with white pixels into clusters whose
// members are at most gap apart and returns [start, end) of the one with
// the most white pixels.
func heaviestCluster(cols []int, gap int) (int, int) {
	bestS, bestE, bestSum := 0, 0, 0
	s, last, sum := -1, -1, 0
	flush := func() {
		if s >= 0 && sum > bestSum {
			bestS, bestE, bestSum = s, last+1, sum
		}
	}
	for x, c := range cols {
		if c == 0 {
			continue
		}
		if s >= 0 && x-last > gap {
			flush()
			s, sum = -1, 0
		}
		if s < 0 {
			s = x
		}
		last = x
		sum += c
	}
	flush()
	return bestS, bestE
}
