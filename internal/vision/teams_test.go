package vision

import (
	"image"
	"image/draw"
	"path/filepath"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/config"
)

// The free-for-all of 2026-11-18 22:49: 2P and 3P were knocked out at the
// same moment and share 2nd place, on a blue and a yellow panel. That is
// a tie, not a team. Painting 3P's panel blue like 2P's makes it a team.
func TestTiesAreTeamsOnlyWithOneTeamColour(t *testing.T) {
	src, err := OpenFiles(filepath.Join("testdata", "corpus", "results-3p-tie-ffa.jpg")).Next()
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default().Vision
	layout, _ := cfg.Layout(3, "")
	ranks := []int{1, 2, 2}
	if tiesAreTeams(src, layout, ranks) {
		t.Fatal("a blue and a yellow panel tied for 2nd are a free-for-all tie, not a team")
	}
	for i, want := range []float64{0, 220, 50} { // red, blue, yellow (roughly)
		h, ok := panelHue(src, layout, i)
		if !ok || hueDist(h, want) > 30 {
			t.Errorf("P%d panel hue %.0f (ok=%v), want about %.0f", i+1, h, ok, want)
		}
	}
	img := image.NewRGBA(src.Bounds())
	draw.Draw(img, img.Bounds(), src, src.Bounds().Min, draw.Src)
	b := img.Bounds()
	from := Rect(b, layout.Slots[1].Char)
	to := Rect(b, layout.Slots[2].Char)
	for y := b.Dy() * 50 / 100; y < b.Dy()*70/100; y++ {
		for x := 0; x < to.Dx() && x < from.Dx(); x++ {
			img.Set(to.Min.X+x, y, img.At(from.Min.X+x, y))
		}
	}
	if !tiesAreTeams(img, layout, ranks) {
		t.Fatal("two blue panels sharing a placement are a team")
	}
	// grey (CPU) panels tied are never taken for a team
	cpus, _ := OpenFiles(filepath.Join("testdata", "corpus", "results-5p-terry-cpus.jpg")).Next()
	l5, _ := cfg.Layout(5, "")
	if tiesAreTeams(cpus, l5, []int{5, 2, 4, 2, 1}) {
		t.Fatal("two grey CPU panels tied for 2nd are not a team")
	}
}
