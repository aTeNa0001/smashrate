package vision

import (
	"image"
	"math"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// A tie in placements means one of two things: teammates in a team battle
// (they share a placement), or players of a free-for-all knocked out at
// the same moment. The panel colours tell them apart: in a free-for-all
// every human has their own port colour (red, blue, yellow, green, ...)
// and CPUs are grey, while in a team battle teammates share their team's
// colour. Live on 2026-11-18 a free-for-all tie for 2nd was recorded as
// a team battle; the colours were red, blue and yellow.

// panelHue is the colour of slot i's panel, sampled in the box below the
// portrait (the knock-out time), ignoring the white digits and dark
// pixels. ok is false for an unsaturated (grey: CPU) panel.
func panelHue(img image.Image, layout config.Layout, i int) (hue float64, ok bool) {
	b := img.Bounds()
	c := layout.Slots[i].Char // spans the panel's width
	r := Rect(b, config.Region{X: c.X + c.W*0.05, Y: 0.58, W: c.W * 0.9, H: 0.07})
	var sx, sy float64
	n, total := 0, 0
	for y := r.Min.Y; y < r.Max.Y; y += 3 {
		for x := r.Min.X; x < r.Max.X; x += 3 {
			cr, cg, cb := rgbAt(img, x, y)
			total++
			h, s, v := hsv(cr, cg, cb)
			if s < 0.35 || v < 0.25 {
				continue
			}
			sx += math.Cos(h * math.Pi / 180)
			sy += math.Sin(h * math.Pi / 180)
			n++
		}
	}
	if total == 0 || n*3 < total { // mostly grey or white: a CPU panel
		return 0, false
	}
	hue = math.Atan2(sy, sx) * 180 / math.Pi
	if hue < 0 {
		hue += 360
	}
	return hue, true
}

func hsv(r, g, b uint8) (h, s, v float64) {
	rf, gf, bf := float64(r)/255, float64(g)/255, float64(b)/255
	mx := math.Max(rf, math.Max(gf, bf))
	mn := math.Min(rf, math.Min(gf, bf))
	v = mx
	if mx == 0 {
		return 0, 0, 0
	}
	s = (mx - mn) / mx
	if mx == mn {
		return 0, s, v
	}
	d := mx - mn
	switch mx {
	case rf:
		h = 60 * math.Mod((gf-bf)/d, 6)
	case gf:
		h = 60 * ((bf-rf)/d + 2)
	default:
		h = 60 * ((rf-gf)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	return h, s, v
}

func hueDist(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// tiesAreTeams reports whether the players sharing a placement are
// teammates: every tied group's panels are coloured (not CPU grey) and of
// one colour. Any tied pair of different colours, or of grey panels,
// makes it a free-for-all with ties.
func tiesAreTeams(img image.Image, layout config.Layout, ranks []int) bool {
	groups := map[int][]int{}
	for i, rk := range ranks {
		if rk > 0 && i < len(layout.Slots) {
			groups[rk] = append(groups[rk], i)
		}
	}
	tied := false
	for _, cols := range groups {
		if len(cols) < 2 {
			continue
		}
		tied = true
		h0, ok := panelHue(img, layout, cols[0])
		if !ok {
			return false
		}
		for _, c := range cols[1:] {
			h, ok := panelHue(img, layout, c)
			if !ok || hueDist(h, h0) > 25 {
				return false
			}
		}
	}
	return tied
}
