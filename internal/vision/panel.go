package vision

import (
	"image"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// The caption, badge and name tag of every results panel slide into place
// during the first second the screen is up (the black header is taller at
// first and its contents sit lower), while the stat rows below and the
// numeral are fixed from the start. A screen that both players skip at once
// is only ever seen with the tags 40-90 px (at 1080p) below their settled
// position, where fixed regions find nothing. The caption is white text on
// the black header, so its rows are found per frame and the name tag is
// taken from a fixed distance below it; this also absorbs the winner's
// enlarged panel and one- versus two-line captions.

// captionGap is how far below the caption's last text row the name tag
// starts, and tagFromCaption how tall the tag region is (fractions of the
// frame height). Measured on 1080p frames: 20-30 px gap, 30 px of text.
const (
	captionGap     = 0.018
	narrowTagGap   = 0.062 // caption bottom to tag top on a narrow panel (badge in between)
	narrowTagH     = 0.05
	captionMargin  = 0.008
	captionMaxGap  = 0.013 // a longer run of empty rows ends the caption
	captionMinRows = 0.012 // less than this is a stray highlight, not text
)

// captionRows finds the first and last row of the caption's white text in
// slot's caption column (from the top of the frame down to the stat rows),
// as fractions of the frame height.
func captionRows(img image.Image, slot config.Slot) (top, bottom float64, ok bool) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	x0 := b.Min.X + int(slot.Char.X*float64(w))
	x1 := b.Min.X + int((slot.Char.X+slot.Char.W)*float64(w))
	if x0 < b.Min.X {
		x0 = b.Min.X
	}
	if x1 > b.Max.X {
		x1 = b.Max.X
	}
	if x1-x0 < 8 {
		return 0, 0, false
	}
	step := (x1 - x0) / 64
	if step < 1 {
		step = 1
	}
	// a caption row has several white samples and is otherwise mostly the
	// header's black: the blurred stage showing past the panel's slanted
	// edge, and the sparkles on the winner's panel, are not captions
	isText := func(y int) bool {
		white, dark, n := 0, 0, 0
		for x := x0; x < x1; x += step {
			r, g, bl := rgbAt(img, x, y)
			n++
			switch {
			case r > 200 && g > 200 && bl > 200:
				white++
			case (299*int(r)+587*int(g)+114*int(bl))/1000 < 40:
				dark++
			}
		}
		// anti-aliased edges of bold text are neither white nor dark, so
		// ask only that the rest of the row is black and white
		return white >= 3 && (white+dark)*3 >= n*2
	}
	// The caption is the tallest run of text rows (runs are bridged across
	// gaps of up to captionMaxGap, so two-line captions hold together).
	// The first run is not enough: the sparkles that drift over the panel
	// pass the row test and sit above the caption on some frames.
	limit := b.Min.Y + h*40/100
	maxGap := int(captionMaxGap * float64(h))
	bestFirst, bestLast := -1, -1
	first, last, gap := -1, -1, 0
	// a caption starts in the header (the top 28% even on the winner's
	// enlarged panel or while the headers slide in); white rows further down
	// are the portrait (Piranha Plant's teeth were taken for its caption)
	headerEnd := b.Min.Y + h*28/100
	close := func() {
		if first >= 0 && first < headerEnd && (bestFirst < 0 || last-first > bestLast-bestFirst) {
			bestFirst, bestLast = first, last
		}
		first, last, gap = -1, -1, 0
	}
	for y := b.Min.Y; y < limit; y++ {
		if isText(y) {
			if first < 0 {
				first = y
			}
			last, gap = y, 0
			continue
		}
		if first >= 0 {
			gap++
			if gap > maxGap {
				close()
			}
		}
	}
	close()
	if bestFirst < 0 || bestLast-bestFirst < int(captionMinRows*float64(h)) {
		return 0, 0, false
	}
	return float64(bestFirst-b.Min.Y) / float64(h), float64(bestLast-b.Min.Y+1) / float64(h), true
}

// SlotRegions returns slot i's caption, name-tag and numeral regions as they
// sit in this frame. When the caption's text rows are found, the caption
// region is fitted to them and the name tag placed below them; otherwise
// the layout's fixed regions are used, shifted for the winner's enlarged
// panel as before. The numeral's region is fixed either way.
func SlotRegions(img image.Image, layout config.Layout, i int, winner bool) (char, name, rank config.Region) {
	slot := layout.Slots[i]
	rank = slot.Rank
	top, bottom, ok := captionRows(img, slot)
	if !ok {
		return layout.CharRegion(i, winner), layout.NameRegion(i, winner), rank
	}
	char = config.Region{X: slot.Char.X, Y: top - captionMargin, W: slot.Char.W, H: bottom - top + 2*captionMargin}
	name = config.Region{X: slot.Name.X, Y: bottom + captionGap, W: slot.Name.W, H: slot.Name.H}
	if slot.Rank.Y >= 0.1 {
		// narrow panels (5-8 players): the 1P/CP badge has a row of its own
		// under the caption and the tag is below it, not beside it
		name.Y, name.H = bottom+narrowTagGap, narrowTagH
	}
	return char, name, rank
}

// CaptionTop reports where the caption was found (for diagnostics), -1
// when it was not.
func CaptionTop(img image.Image, layout config.Layout, i int) float64 {
	if top, _, ok := captionRows(img, layout.Slots[i]); ok {
		return top
	}
	return -1
}
