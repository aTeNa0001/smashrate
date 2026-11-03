package vision

import (
	"image"
	"image/color"
	"math/rand"
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
)

// synthetic "results screen": each region is painted with a distinct
// deterministic pattern so hashes are stable yet different per label.
func paint(img *image.RGBA, r image.Rectangle, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := uint8(rng.Intn(256))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
}

func frame(cfg config.VisionConfig, players int, charSeeds, rankSeeds []int64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	paint(img, img.Bounds(), 999) // background noise
	layout, _ := cfg.Layout(players)
	paint(img, Rect(img.Bounds(), layout.Screen), 1000+int64(players))
	for i, s := range layout.Slots {
		paint(img, Rect(img.Bounds(), s.Char), charSeeds[i])
		paint(img, Rect(img.Bounds(), s.Rank), rankSeeds[i])
	}
	return img
}

func TestDHashStability(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	paint(img, img.Bounds(), 1)
	a := DHash(img, img.Bounds())
	b := DHash(img, img.Bounds())
	if a != b {
		t.Fatal("hash not deterministic")
	}
	other := image.NewRGBA(image.Rect(0, 0, 100, 100))
	paint(other, other.Bounds(), 2)
	if Hamming(a, DHash(other, other.Bounds())) < 10 {
		t.Fatal("different images should hash far apart")
	}
}

func TestTeachDetectAndRecord(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision
	cfg.StableFrames = 2
	cfg.CooldownSeconds = 1

	// teach from one 1v1 frame: P1 mario 1st, P2 link 2nd
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22})
	if err := Teach(c, cfg, f1, 2, []string{"マリオ", "link"}, []string{"1st", "2nd"}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetPort(1, "Taro")
	_ = c.SetPort(2, "Hanako")

	var msgs []string
	w, err := NewWatcher(cfg, c, t.TempDir(), func(s string) { msgs = append(msgs, s) })
	if err != nil {
		t.Fatal(err)
	}
	// same screen appears twice -> recorded once
	w.Process(f1)
	w.Process(f1)
	w.Process(f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("expected 1 recorded match, got %d; msgs=%v", len(ms), msgs)
	}
	if ms[0].Entries[0].PlayerName != "Taro" || ms[0].Entries[0].Rank != 1 || ms[0].Entries[0].Character != "mario" {
		t.Fatalf("bad entry: %+v", ms[0].Entries[0])
	}

	// a frame with a known rank but unknown character: match recorded without
	// the character, crop stashed, learnable afterwards.
	w.Process(frame(cfg, 2, []int64{13, 12}, []int64{21, 22})) // break stability
	f2 := frame(cfg, 2, []int64{31, 12}, []int64{22, 21})      // P2 wins, P1 plays unknown char
	w.Process(f2)
	w.Process(f2)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 {
		t.Fatalf("expected 2 matches, got %d; msgs=%v", len(ms), msgs)
	}
	pend := w.Pending()
	if len(pend) != 1 || !strings.HasPrefix(pend[0], KindChar) {
		t.Fatalf("pending = %v", pend)
	}
	if err := w.Learn(pend[0], "ピカチュウ"); err != nil {
		t.Fatal(err)
	}
	r, ok := w.det.Detect(f2)
	if !ok || r.Slots[0].Char != "pikachu" || r.Slots[1].Rank != 1 {
		t.Fatalf("after learning: %+v ok=%v", r, ok)
	}

	// non-results frame is ignored
	noise := image.NewRGBA(image.Rect(0, 0, 320, 180))
	paint(noise, noise.Bounds(), 4242)
	if _, ok := w.det.Detect(noise); ok {
		t.Fatal("noise should not be detected as a results screen")
	}
}

func TestNormalizeLabel(t *testing.T) {
	for in, want := range map[string]string{"1st": "1", "2位": "2", "3": "3", "4th": "4"} {
		if got, err := NormalizeLabel(KindRank, in); err != nil || got != want {
			t.Fatalf("%s: got %q err %v", in, got, err)
		}
	}
	if _, err := NormalizeLabel(KindRank, "9th"); err == nil {
		t.Fatal("expected error")
	}
	if got, _ := NormalizeLabel(KindChar, "ゲムヲ"); got != "mr_game_and_watch" {
		t.Fatalf("got %q", got)
	}
}
