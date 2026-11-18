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
// deterministic pattern so hashes are stable yet different per label. The
// noise never reaches white, so only paintText cells count as text.
func paint(img *image.RGBA, r image.Rectangle, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			v := uint8(rng.Intn(200))
			img.Set(x, y, color.RGBA{v, v, v, 255})
		}
	}
}

// paintText fills a seed-dependent half of the 8x8 cells of r with white,
// standing in for the white stat labels the screen detector looks for.
func paintText(img *image.RGBA, r image.Rectangle, seed int64) { paintTextGrid(img, r, seed, 8, 8) }

// paintTextGrid fills a random half of a cols x rows grid of cells with
// white: a stand-in for text, seen by WhiteHash/TagHash.
func paintTextGrid(img *image.RGBA, r image.Rectangle, seed int64, cols, rows int) {
	rng := rand.New(rand.NewSource(seed))
	cw, ch := float64(r.Dx())/float64(cols), float64(r.Dy())/float64(rows)
	for gy := 0; gy < rows; gy++ {
		for gx := 0; gx < cols; gx++ {
			if rng.Intn(2) == 0 {
				continue
			}
			x0, x1 := r.Min.X+int(float64(gx)*cw), r.Min.X+int(float64(gx+1)*cw)
			y0, y1 := r.Min.Y+int(float64(gy)*ch), r.Min.Y+int(float64(gy+1)*ch)
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					img.Set(x, y, color.RGBA{250, 250, 250, 255})
				}
			}
		}
	}
}

// testVisionCfg is the default config without the winner-panel shift: the
// synthetic frames paint every column at the plain regions. The shift itself
// is verified against real screenshots.
func testVisionCfg() config.VisionConfig {
	cfg := config.Default().Vision
	for i := range cfg.Layouts {
		cfg.Layouts[i].WinnerShiftY = 0
	}
	return cfg
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
	paintText(img, Rect(img.Bounds(), layout.Screen), screenSeed)
	for i, s := range layout.Slots {
		paint(img, Rect(img.Bounds(), s.Char), charSeeds[i])
		paintGlyph(img, Rect(img.Bounds(), s.Rank), rankSeeds[i])
		if i < len(nameSeeds) {
			paintTextGrid(img, Rect(img.Bounds(), s.Name), nameSeeds[i], 32, 2) // a name tag: white text
			paintPanelColour(img, s, i)                                         // a human's panel has their port colour
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
	cfg := testVisionCfg()
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
	// the screen stays for three frames, then disappears -> recorded once
	// (stable_frames = 2: a single-frame flash would be ignored)
	w.Process(f1)
	w.Process(f1)
	w.Process(f1)
	w.Flush()
	feed(w, f1) // shown again within the cooldown: not recorded twice
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("expected 1 recorded match, got %d; msgs=%v", len(ms), msgs)
	}
	if ms[0].Entries[0].PlayerName != "Taro" || ms[0].Entries[0].Rank != 1 || ms[0].Entries[0].Character != "mario" {
		t.Fatalf("bad entry: %+v", ms[0].Entries[0])
	}

	// a frame with a known rank but unknown character: match recorded without
	// the character, crop stashed, learnable afterwards.
	f2 := frame(cfg, 2, []int64{31, 12}, []int64{22, 21}, 41, 42) // P2 wins, P1 plays unknown char
	w.Process(f2)
	w.Process(f2)
	w.Flush()
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
	cfg := testVisionCfg()
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
	feed(w, f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 || ms[0].Entries[0].PlayerName != "Taro" || ms[0].Entries[1].PlayerName != "Hanako" {
		t.Fatalf("name-tag identification failed: %+v msgs=%v", ms, msgs)
	}

	// players swap seats: names follow the tags, not the ports
	f2 := frame(cfg, 2, []int64{12, 11}, []int64{21, 22}, 42, 41)
	feed(w, f2)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Entries[0].PlayerName != "Hanako" || ms[0].Entries[0].Rank != 1 {
		t.Fatalf("swapped seats: %+v", ms[0].Entries)
	}

	// a newcomer with an unknown tag: nothing recorded, tag stashed, learnable
	f3 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 77)
	feed(w, f3)
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
	st := w.Status()
	if st.Frames != 3 || st.Detections != 3 || st.Templates[KindName] != 3 || st.LastMessage == "" {
		t.Fatalf("status: %+v", st)
	}
	if txt := w.StatusText(); !strings.Contains(txt, "受信中") {
		t.Fatalf("status text: %q", txt)
	}
	if _, err := c.PlayerByName("Jiro"); err != nil {
		t.Fatal("learning a name tag should create the player")
	}
	feed(w, f3)
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
	cfg := testVisionCfg()
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

	feed(w, f1)
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
	feed(w, frame(cfg, 2, []int64{12, 11}, []int64{21, 22}, 42, 41)) // seats swapped, Hanako (now P1) wins
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Entries[0].PlayerName != "Hanako" || ms[0].Entries[0].Rank != 1 || ms[0].Entries[1].PlayerName != "たろう" {
		t.Fatalf("second match: %+v; msgs=%v", ms[0].Entries, msgs)
	}
	if fake.calls != ocrCalls {
		t.Fatalf("hash templates should have avoided OCR, calls=%d", fake.calls)
	}

	// OCR reads nothing: not recorded, crop stashed for manual labelling
	f3 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 99)
	feed(w, f3)
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
	feed(w, f4)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 3 || ms[0].Rated || !ms[0].Entries[1].CPU {
		t.Fatalf("a placeholder tag records an unrated match with a CPU entry: %+v", ms)
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
	cfg := testVisionCfg()
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

	feed(w, f1)
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
	feed(w, f1)
	if fake.calls != calls {
		t.Fatalf("expected no further OCR, got %d more calls", fake.calls-calls)
	}
	if ms, _ = c.Matches(10, 0); len(ms) != 2 {
		t.Fatal("second identical screen should record again after cooldown reset")
	}

	// a CPU opponent: the tag reads コンピューター2; the match is kept for the
	// record as unrated, nothing is stashed
	f2 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 77)
	fake.byHash[hashOf(f2, layout.Slots[1].Name)] = "コ ン ピ ュ ー タ ー 2"
	var msgs []string
	w.notify = func(s string) { msgs = append(msgs, s) }
	feed(w, f2)
	if ms, _ = c.Matches(10, 0); len(ms) != 3 || ms[0].Rated {
		t.Fatalf("match with a CPU must be recorded unrated: %+v", ms)
	}
	if len(w.Pending()) != 0 || len(msgs) != 1 || !strings.Contains(msgs[0], "CPU") {
		t.Fatalf("cpu handling: pending=%v msgs=%v", w.Pending(), msgs)
	}
	// and the CPU tag is remembered as a placeholder: no OCR next time
	calls = fake.calls
	w.lastSeen = ""
	feed(w, f2)
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
	cfg := testVisionCfg()
	cfg.StableFrames = 1
	cfg.CooldownSeconds = 1
	cfg.OCR.Engine = "none"

	// a 2v2 result on the ordinary 4-player screen: columns A,B (1st) and
	// C,D (2nd) share placements, which is what makes it a team battle
	tf := frameMode(cfg, 4, "", []int64{11, 12, 13, 14}, []int64{21, 21, 22, 22}, 41, 42, 43, 44)
	// in a team battle teammates' panels share the team colour: A,B red, C,D blue
	lay4, _ := cfg.Layout(4, "")
	paintPanelColour(tf, lay4.Slots[1], 0)
	paintPanelColour(tf, lay4.Slots[2], 1)
	paintPanelColour(tf, lay4.Slots[3], 1)
	if err := Teach(c, cfg, tf, 4, "", []string{"mario", "link", "fox", "kirby"}, []string{"1", "1", "2", "2"}, []string{"A", "B", "C", "D"}); err != nil {
		t.Fatal(err)
	}
	// and a 4-player free-for-all with the same people and distinct placements
	ff := frameMode(cfg, 4, "", []int64{11, 12, 13, 14}, []int64{21, 22, 23, 24}, 41, 42, 43, 44)
	if err := Teach(c, cfg, ff, 4, "", nil, []string{"1", "2", "3", "4"}, nil); err != nil {
		t.Fatal(err)
	}

	w, err := NewWatcher(cfg, c, t.TempDir(), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	feed(w, tf)
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

	// distinct placements on the same screen: a free-for-all
	feed(w, ff)
	ms, _ = c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Mode != core.ModeFFA {
		t.Fatalf("distinct placements should record a free-for-all: %+v", ms)
	}

	// everyone tied: nothing to rate, nothing recorded
	var msgs []string
	w.notify = func(s string) { msgs = append(msgs, s) }
	w.lastSeen = ""
	all := frameMode(cfg, 4, "", []int64{11, 12, 13, 14}, []int64{21, 21, 21, 21}, 41, 42, 43, 44)
	feed(w, all)
	if ms, _ = c.Matches(10, 0); len(ms) != 2 || len(msgs) != 1 || !strings.Contains(msgs[0], "全員が同順位") {
		t.Fatalf("all tied: matches=%d msgs=%v", len(ms), msgs)
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

// feed processes one frame and flushes the burst, as a screen that is
// shown and then disappears would.
func feed(w *Watcher, img image.Image) { w.Process(img); w.Flush() }

// paintPanelColour fills the knock-out-time box of slot i with a port
// colour, as the game does for a human (a CPU's panel stays grey).
func paintPanelColour(img *image.RGBA, s config.Slot, i int) {
	cols := []color.RGBA{{220, 40, 50, 255}, {40, 110, 220, 255}, {230, 180, 20, 255}, {30, 170, 90, 255}}
	r := Rect(img.Bounds(), config.Region{X: s.Char.X, Y: 0.57, W: s.Char.W, H: 0.09})
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.Set(x, y, cols[i%len(cols)])
		}
	}
}

// The same placements on panels of four different colours are a
// free-for-all with two ties, not a team battle.
func TestTiedPlacementsWithPortColoursAreAFreeForAll(t *testing.T) {
	c, _ := core.Open(":memory:")
	defer c.Close()
	cfg := testVisionCfg()
	cfg.CooldownSeconds = 1
	cfg.OCR.Engine = "none"
	f := frameMode(cfg, 4, "", []int64{11, 12, 13, 14}, []int64{21, 21, 22, 22}, 41, 42, 43, 44)
	if err := Teach(c, cfg, f, 4, "", []string{"mario", "link", "fox", "kirby"}, []string{"1", "1", "2", "2"}, []string{"A", "B", "C", "D"}); err != nil {
		t.Fatal(err)
	}
	w, _ := NewWatcher(cfg, c, t.TempDir(), func(string) {})
	feed(w, f)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 || ms[0].Mode != core.ModeFFA {
		t.Fatalf("ties on differently coloured panels are a free-for-all: %+v", ms)
	}
}
