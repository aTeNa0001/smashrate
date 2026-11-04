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

func frame(cfg config.VisionConfig, players int, charSeeds, rankSeeds []int64, nameSeeds ...int64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 320, 180))
	paint(img, img.Bounds(), 999) // background noise
	layout, _ := cfg.Layout(players)
	paint(img, Rect(img.Bounds(), layout.Screen), 1000+int64(players))
	for i, s := range layout.Slots {
		paint(img, Rect(img.Bounds(), s.Char), charSeeds[i])
		paint(img, Rect(img.Bounds(), s.Rank), rankSeeds[i])
		if i < len(nameSeeds) {
			paint(img, Rect(img.Bounds(), s.Name), nameSeeds[i])
		}
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

	// teach from one 1v1 frame: P1 Taro/mario 1st, P2 Hanako/link 2nd
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	if err := Teach(c, cfg, f1, 2, []string{"マリオ", "link"}, []string{"1st", "2nd"}, []string{"Taro", "Hanako"}); err != nil {
		t.Fatal(err)
	}

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
	w.Process(frame(cfg, 2, []int64{13, 12}, []int64{21, 22}, 41, 42)) // break stability
	f2 := frame(cfg, 2, []int64{31, 12}, []int64{22, 21}, 41, 42)      // P2 wins, P1 plays unknown char
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

func TestNameTagsIdentifyPlayersWithoutPorts(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision
	cfg.StableFrames = 1
	cfg.CooldownSeconds = 1

	// teach names along with everything else; no ports configured at all
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	if err := Teach(c, cfg, f1, 2, []string{"mario", "link"}, []string{"1", "2"}, []string{"Taro", "Hanako"}); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	w, err := NewWatcher(cfg, c, t.TempDir(), func(s string) { msgs = append(msgs, s) })
	if err != nil {
		t.Fatal(err)
	}
	w.Process(f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 || ms[0].Entries[0].PlayerName != "Taro" || ms[0].Entries[1].PlayerName != "Hanako" {
		t.Fatalf("name-tag identification failed: %+v msgs=%v", ms, msgs)
	}

	// players swap seats: names follow the tags, not the ports
	f2 := frame(cfg, 2, []int64{12, 11}, []int64{21, 22}, 42, 41)
	w.Process(f2)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Entries[0].PlayerName != "Hanako" || ms[0].Entries[0].Rank != 1 {
		t.Fatalf("swapped seats: %+v", ms[0].Entries)
	}

	// a newcomer with an unknown tag: nothing recorded, tag stashed, learnable
	f3 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 77)
	w.Process(f3)
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatalf("unknown tag should block recording, got %d matches", len(ms))
	}
	pend := w.Pending()
	if len(pend) != 1 || !strings.HasPrefix(pend[0], KindName) {
		t.Fatalf("pending = %v msgs=%v", pend, msgs)
	}
	if err := w.Learn(pend[0], "Jiro"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PlayerByName("Jiro"); err != nil {
		t.Fatal("learning a name tag should create the player")
	}
	w.Process(f3)
	if ms, _ = c.Matches(10, 0); len(ms) != 3 || ms[0].Entries[1].PlayerName != "Jiro" {
		t.Fatalf("after learning the tag: %+v", ms)
	}
}

// fakeOCR answers with a fixed text per name-region seed; it records calls.
type fakeOCR struct {
	byHash map[uint64]string
	calls  int
}

func (f *fakeOCR) Name() string { return "fake" }
func (f *fakeOCR) Recognize(img image.Image) (string, error) {
	f.calls++
	h := DHash(img, img.Bounds())
	return f.byHash[h], nil
}

func TestOCRRegistersNewcomersOnce(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision
	cfg.StableFrames = 1
	cfg.CooldownSeconds = 1
	cfg.OCR.Engine = "none"

	// only the screen/char/rank templates are taught; no name tags at all
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	if err := Teach(c, cfg, f1, 2, []string{"mario", "link"}, []string{"1", "2"}, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetOrCreatePlayer("Hanako") // an existing member, to be fuzzy-matched

	layout, _ := cfg.Layout(2)
	b := f1.Bounds()
	tag := func(i int) uint64 { cr := Crop(f1, Rect(b, layout.Slots[i].Name)); return DHash(cr, cr.Bounds()) }
	fake := &fakeOCR{byHash: map[uint64]string{tag(0): "た ろ う", tag(1): "Hanak0"}}

	var msgs []string
	w, err := NewWatcher(cfg, c, t.TempDir(), func(s string) { msgs = append(msgs, s) })
	if err != nil {
		t.Fatal(err)
	}
	w.SetOCR(fake)

	w.Process(f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("expected the match to be recorded via OCR, got %d; msgs=%v", len(ms), msgs)
	}
	if ms[0].Entries[0].PlayerName != "たろう" || ms[0].Entries[1].PlayerName != "Hanako" {
		t.Fatalf("names: %+v", ms[0].Entries)
	}
	if _, err := c.PlayerByName("Hanak0"); err == nil {
		t.Fatal("fuzzy match should have reused Hanako instead of creating Hanak0")
	}
	if fake.calls != 2 {
		t.Fatalf("expected 2 OCR calls, got %d", fake.calls)
	}
	if len(msgs) == 0 || !strings.Contains(msgs[0], "新しいプレイヤーを登録しました: たろう") {
		t.Fatalf("newcomer notice missing: %v", msgs)
	}

	// same tags again: matched by hash, no OCR
	w.Process(frame(cfg, 2, []int64{12, 11}, []int64{21, 22}, 42, 41)) // seats swapped, Hanako (now P1) wins
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Entries[0].PlayerName != "Hanako" || ms[0].Entries[0].Rank != 1 || ms[0].Entries[1].PlayerName != "たろう" {
		t.Fatalf("second match: %+v", ms[0].Entries)
	}
	if fake.calls != 2 {
		t.Fatalf("hash templates should have avoided OCR, calls=%d", fake.calls)
	}

	// OCR reads nothing: not recorded, crop stashed for manual labelling
	f3 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 99)
	w.Process(f3)
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatal("unreadable tag must block recording")
	}
	if p := w.Pending(); len(p) != 1 {
		t.Fatalf("pending = %v", p)
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
