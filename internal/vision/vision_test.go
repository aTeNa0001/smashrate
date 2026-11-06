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

// paintGlyph draws a bright digit-like shape (a tall block with a
// seed-dependent notch) on a dark background, as the placement numeral
// isolation expects. Different seeds give clearly different shapes.
func paintGlyph(img *image.RGBA, r image.Rectangle, seed int64) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, y, color.RGBA{30, 30, 30, 255})
		}
	}
	w, h := r.Dx(), r.Dy()
	gw, gh := w*45/100, h*70/100
	x0, y0 := r.Min.X+(w-gw)/2, r.Min.Y+(h-gh)/2
	notchX := x0 + int(seed%5)*gw/6
	notchY := y0 + int(seed%7)*gh/8
	for y := y0; y < y0+gh; y++ {
		for x := x0; x < x0+gw; x++ {
			if x >= notchX && x < notchX+gw/4 && y >= notchY && y < notchY+gh/4 {
				continue // the notch stays dark
			}
			img.Set(x, y, color.RGBA{230, 230, 230, 255})
		}
	}
}

func frame(cfg config.VisionConfig, players int, charSeeds, rankSeeds []int64, nameSeeds ...int64) *image.RGBA {
	return frameMode(cfg, players, "", charSeeds, rankSeeds, nameSeeds...)
}

func frameMode(cfg config.VisionConfig, players int, mode string, charSeeds, rankSeeds []int64, nameSeeds ...int64) *image.RGBA {
	// same size as the real analysis frame, so that a region has the same
	// pixel size in every column (rounding differs at other sizes)
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	paint(img, img.Bounds(), 999) // background noise
	layout, _ := cfg.Layout(players, mode)
	screenSeed := 1000 + int64(players)
	if mode == "team" {
		screenSeed += 500
	}
	paint(img, Rect(img.Bounds(), layout.Screen), screenSeed)
	for i, s := range layout.Slots {
		paint(img, Rect(img.Bounds(), s.Char), charSeeds[i])
		paintGlyph(img, Rect(img.Bounds(), s.Rank), rankSeeds[i])
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
	if err := Teach(c, cfg, f1, 2, "", []string{"マリオ", "link"}, []string{"1st", "2nd"}, []string{"Taro", "Hanako"}); err != nil {
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
	noise := image.NewRGBA(image.Rect(0, 0, 640, 360))
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
	if err := Teach(c, cfg, f1, 2, "", []string{"mario", "link"}, []string{"1", "2"}, []string{"Taro", "Hanako"}); err != nil {
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

// Recognize answers by the nearest registered hash: the watcher hands the
// engine enlarged / binarized variants of the crop, whose dHash is close to,
// but not identical with, the raw crop's.
func (f *fakeOCR) Recognize(img image.Image) (string, error) {
	f.calls++
	// undo the 4x enlargement so the hash is comparable with the raw crop;
	// binarized variants (gray images) are not answered
	if _, gray := img.(*image.Gray); gray {
		return "", nil
	}
	b := img.Bounds()
	small := image.NewRGBA(image.Rect(0, 0, b.Dx()/4, b.Dy()/4))
	for y := 0; y < small.Rect.Dy(); y++ {
		for x := 0; x < small.Rect.Dx(); x++ {
			small.Set(x, y, img.At(b.Min.X+x*4+2, b.Min.Y+y*4+2))
		}
	}
	h := DHash(small, small.Bounds())
	best, bestDist := "", 65
	for k, v := range f.byHash {
		if d := Hamming(k, h); d < bestDist {
			best, bestDist = v, d
		}
	}
	if bestDist > 20 {
		return "", nil
	}
	return best, nil
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
	if err := Teach(c, cfg, f1, 2, "", []string{"mario", "link"}, []string{"1", "2"}, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = c.GetOrCreatePlayer("Hanako") // an existing member, to be fuzzy-matched

	layout, _ := cfg.Layout(2, "")
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
	// P1 is brand new (both variants are tried before creating), P2 matched
	// on the first variant: at most 4 calls, never more
	if fake.calls < 2 || fake.calls > 4 {
		t.Fatalf("expected 2-4 OCR calls, got %d", fake.calls)
	}
	ocrCalls := fake.calls
	if len(msgs) == 0 || !strings.Contains(msgs[0], "新しいプレイヤーを登録しました: たろう") {
		t.Fatalf("newcomer notice missing: %v", msgs)
	}

	// same tags again: matched by hash, no OCR
	w.Process(frame(cfg, 2, []int64{12, 11}, []int64{21, 22}, 42, 41)) // seats swapped, Hanako (now P1) wins
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Entries[0].PlayerName != "Hanako" || ms[0].Entries[0].Rank != 1 || ms[0].Entries[1].PlayerName != "たろう" {
		t.Fatalf("second match: %+v", ms[0].Entries)
	}
	if fake.calls != ocrCalls {
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

	// a console without a name tag shows "P2": never registered as a player
	f4 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 77)
	l4, _ := cfg.Layout(2, "")
	cr := Crop(f4, Rect(f4.Bounds(), l4.Slots[1].Name))
	fake.byHash[DHash(cr, cr.Bounds())] = "P 2"
	w.Process(f4)
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatal("placeholder tag must block recording")
	}
	if _, err := c.PlayerByName("P2"); err == nil {
		t.Fatal("placeholder tag must not create a player")
	}
}

func TestOCRLearnsCharactersRanksAndSkipsCPUs(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision
	cfg.StableFrames = 1
	cfg.CooldownSeconds = 1
	cfg.OCR.Engine = "none"

	// teach "this is the results screen" plus the two placement numerals
	// (digits are matched by shape, not OCR); names and characters come from OCR
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	if err := Teach(c, cfg, f1, 2, "", nil, []string{"2", "1"}, nil); err != nil {
		t.Fatal(err)
	}
	layout, _ := cfg.Layout(2, "")
	b := f1.Bounds()
	hashOf := func(img image.Image, r config.Region) uint64 {
		cr := Crop(img, Rect(img.Bounds(), r))
		return DHash(cr, cr.Bounds())
	}
	fake := &fakeOCR{byHash: map[uint64]string{
		hashOf(f1, layout.Slots[0].Name): "た ろ う", hashOf(f1, layout.Slots[1].Name): "Hanako",
		hashOf(f1, layout.Slots[0].Char): "DONKEY\nKONG", hashOf(f1, layout.Slots[1].Char): "MARI0",
	}}
	w, err := NewWatcher(cfg, c, t.TempDir(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	w.SetOCR(fake)
	_ = b

	w.Process(f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("expected a recorded match, got %d", len(ms))
	}
	e := ms[0].Entries
	if e[0].PlayerName != "たろう" || e[0].Character != "donkey_kong" || e[0].Rank != 2 || e[1].Character != "mario" || e[1].Rank != 1 {
		t.Fatalf("entries: %+v", e)
	}
	calls := fake.calls
	// everything is now learned: the same screen needs no OCR at all
	w.lastSeen = ""
	w.Process(f1)
	if fake.calls != calls {
		t.Fatalf("expected no further OCR, got %d more calls", fake.calls-calls)
	}
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatal("second identical screen should record again after cooldown reset")
	}

	// a CPU opponent: the tag reads コンピューター2, nothing is recorded or stashed
	f2 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 77)
	fake.byHash[hashOf(f2, layout.Slots[1].Name)] = "コ ン ピ ュ ー タ ー 2"
	var msgs []string
	w.notify = func(s string) { msgs = append(msgs, s) }
	w.Process(f2)
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatal("match with a CPU must not be recorded")
	}
	if len(w.Pending()) != 0 || len(msgs) != 1 || !strings.Contains(msgs[0], "CPU") {
		t.Fatalf("cpu handling: pending=%v msgs=%v", w.Pending(), msgs)
	}
	// and the CPU tag is remembered as a placeholder: no OCR next time
	calls = fake.calls
	w.lastSeen = ""
	w.Process(f2)
	if fake.calls != calls {
		t.Fatal("placeholder tag should be matched by hash, not OCR'd again")
	}
}

func TestTeamBattleResultsScreen(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision
	cfg.StableFrames = 1
	cfg.CooldownSeconds = 1
	cfg.OCR.Engine = "none"

	// a 2v2 team results screen: columns A,B (1st) and C,D (2nd)
	tf := frameMode(cfg, 4, "team", []int64{11, 12, 13, 14}, []int64{21, 21, 22, 22}, 41, 42, 43, 44)
	if err := Teach(c, cfg, tf, 4, "team", []string{"mario", "link", "fox", "kirby"}, []string{"1", "1", "2", "2"}, []string{"A", "B", "C", "D"}); err != nil {
		t.Fatal(err)
	}
	// and a 4-player free-for-all screen with the same people, taught under its own label
	ff := frameMode(cfg, 4, "", []int64{11, 12, 13, 14}, []int64{21, 22, 23, 24}, 41, 42, 43, 44)
	if err := Teach(c, cfg, ff, 4, "", nil, []string{"1", "2", "3", "4"}, nil); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(cfg, c, t.TempDir(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	w.Process(tf)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 || ms[0].Mode != core.ModeTeam {
		t.Fatalf("team screen should record a team battle: %+v", ms)
	}
	teams := map[string]int{}
	for _, e := range ms[0].Entries {
		teams[e.PlayerName] = e.Team
	}
	if teams["A"] != teams["B"] || teams["C"] != teams["D"] || teams["A"] == teams["C"] {
		t.Fatalf("teammates should share a team: %v", teams)
	}
	if w := ms[0].Winners(); len(w) != 2 {
		t.Fatalf("winners = %v", w)
	}

	// the free-for-all screen is told apart by its own screen template
	w.Process(ff)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Mode != core.ModeFFA {
		t.Fatalf("ffa screen should record a free-for-all: %+v", ms)
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
