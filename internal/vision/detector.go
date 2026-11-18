package vision

import (
	"fmt"
	"image"
	_ "image/jpeg" // enable JPEG decoding for FileSource
	"strconv"
	"strings"
	"sync"

	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/ocr"
)

// Template kinds stored in the database.
const (
	// KindScreen and KindBand together identify the results screen and the
	// player count: the white-text hash of the first panel's stat labels
	// and of the stat-row band across the whole frame (see WhiteHash).
	// The kind names changed when the hash did, so templates learned with
	// the old brightness hash are simply never loaded.
	KindScreen = "screen-text" // label "results:<players>"
	KindBand   = "band-text"   // label "results:<players>"
	KindChar   = "char"        // label = character key
	KindRank   = "rank"        // label = "1".."8"
	KindName   = "name"        // label = player name (as shown on the name tag)
)

// PlaceholderLabel marks a learned name-tag hash that is not a person
// (P1 / プレイヤー1 / CPU / コンピューター2).
const PlaceholderLabel = "#placeholder"

// SlotReading is what was recognised in one player column.
type SlotReading struct {
	Port        int
	Winner      bool          // placement 1: the panel is enlarged, regions shift
	CharRegion  config.Region // regions actually used (shifted for the winner)
	NameRegion  config.Region
	Name        string // player name from the name tag, "" if unknown
	Placeholder bool   // the tag is a default label or a CPU, not a person
	NameDist    int
	NameHash    uint64
	Char        string // character key, "" if unknown
	CharDist    int
	CharHash    uint64
	Rank        int  // 0 if unknown
	Inferred    bool // the numeral was unread and Rank is the one placement left over
	RankDist    int
	RankHash    uint64
}

// Reading is a recognised results screen.
type Reading struct {
	Players int
	Team    bool // team-battle results screen: players sharing a rank are one team
	Layout  config.Layout
	Slots   []SlotReading
}

// Key is a stable string used to compare consecutive readings.
func (r Reading) Key() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d:%v:", r.Players, r.Team)
	for _, s := range r.Slots {
		fmt.Fprintf(&sb, "P%d=%s/%v/%s/%d;", s.Port, s.Name, s.Placeholder, s.Char, s.Rank)
	}
	return sb.String()
}

// Complete reports whether every slot has both character and rank.
func (r Reading) Complete() bool {
	for _, s := range r.Slots {
		if s.Rank == 0 {
			return false
		}
	}
	return len(r.Slots) > 0
}

// Detector matches frames against learned templates. Reload is called from
// the web UI (learn page, teach button) while the capture goroutine is
// detecting, so the template set is guarded by a lock.
type Detector struct {
	cfg       config.VisionConfig
	mu        sync.RWMutex
	templates map[string][]core.Template
}

// NewDetector builds a detector from the templates in the database.
func NewDetector(cfg config.VisionConfig, c *core.Core) (*Detector, error) {
	d := &Detector{cfg: cfg}
	return d, d.Reload(c)
}

// Reload re-reads templates.
func (d *Detector) Reload(c *core.Core) error {
	ts := map[string][]core.Template{}
	for _, kind := range []string{KindScreen, KindBand, KindChar, KindRank, KindName} {
		list, err := c.Templates(kind)
		if err != nil {
			return err
		}
		ts[kind] = list
	}
	d.mu.Lock()
	d.templates = ts
	d.mu.Unlock()
	return nil
}

// TemplateCount returns how many templates of a kind are loaded.
func (d *Detector) TemplateCount(kind string) int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.templates[kind])
}

// builtinRanks are placement-numeral signatures measured on a real 4-player
// results screen (640x360 capture), so 1st and 2nd work before anything has
// been learned. The same digit varies by a few bits between columns, hence
// more than one entry per label. Further digits are learned once (Slack
// `learn` or `vision teach -ranks`).
var builtinRanks = []core.Template{
	{Kind: KindRank, Label: "1", Hash: 0x0f3fff4f0f0f0f0f},
	{Kind: KindRank, Label: "1", Hash: 0x0f1fff6f0f0f0f0f},
	{Kind: KindRank, Label: "1", Hash: 0x0f1fff7f0f1f1f1e},
	{Kind: KindRank, Label: "1", Hash: 0x1f3fffdf1f1f1f1e},
	{Kind: KindRank, Label: "2", Hash: 0x1e7f670e3c70fffe},
	{Kind: KindRank, Label: "2", Hash: 0x1e7f670f3c70fffe},
	{Kind: KindRank, Label: "2", Hash: 0x3e7fe70e3c70fffe},
	{Kind: KindRank, Label: "3", Hash: 0x1c7eff071e3e3f00},
	{Kind: KindRank, Label: "3", Hash: 0x3e7fe7073e3f0700},
	{Kind: KindRank, Label: "3", Hash: 0x1e7fe7071e3e0f00},
	{Kind: KindRank, Label: "4", Hash: 0x0e1e3e6e4efffe0e},
	{Kind: KindRank, Label: "4", Hash: 0x0e1e3e2e6efffe06},
	{Kind: KindRank, Label: "4", Hash: 0x0e1e3e6eeeffee0e},
	{Kind: KindRank, Label: "5", Hash: 0x1e7ee0fe6707ff7c},
	{Kind: KindRank, Label: "6", Hash: 0x1e7ee0fef7e37f3c},
	{Kind: KindRank, Label: "6", Hash: 0x3e7ee0fef7e3ff7e},
	{Kind: KindRank, Label: "7", Hash: 0x1fff0e1c38383870},
	{Kind: KindRank, Label: "8", Hash: 0x1e77677e7fe3f77c},
}

// classifyRank matches a numeral signature against learned and built-in
// ones. Digits 5 and 6 are only ~8 bits apart on the 8x8 grid, so besides
// the distance threshold the best label must beat every other label by a
// margin; otherwise the numeral is reported unknown (and stashed to learn).
func (d *Detector) classifyRank(sig uint64) (string, int, bool) {
	best, bestDist := "", 65
	second := 65 // nearest template with a different label
	d.mu.RLock()
	learned := d.templates[KindRank]
	d.mu.RUnlock()
	for _, list := range [][]core.Template{learned, builtinRanks} {
		for _, t := range list {
			dist := Hamming(t.Hash, sig)
			switch {
			case dist < bestDist:
				if t.Label != best {
					second = bestDist
				}
				best, bestDist = t.Label, dist
			case t.Label != best && dist < second:
				second = dist
			}
		}
	}
	ok := best != "" && bestDist <= d.cfg.RankThreshold && second-bestDist >= 3
	return best, bestDist, ok
}

// classify returns the nearest template label within threshold.
func (d *Detector) classify(kind string, h uint64, threshold int) (string, int, bool) {
	best, bestDist := "", 65
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, t := range d.templates[kind] {
		if dist := Hamming(t.Hash, h); dist < bestDist {
			best, bestDist = t.Label, dist
		}
	}
	return best, bestDist, best != "" && bestDist <= threshold
}

// ScreenMatch is how close a frame came to one layout's results-screen
// template, for diagnosing "not a results screen".
type ScreenMatch struct {
	Want     string // the layout's screen label
	Best     string // nearest learned first-panel template ("" when none learned)
	Dist     int    // Hamming distance to it (65 when none)
	BandBest string // nearest learned band template
	BandDist int
}

// screenHashes are the two hashes that identify a layout's results screen.
func screenHashes(img image.Image, layout config.Layout) (panel, band uint64) {
	b := img.Bounds()
	return WhiteHash(img, Rect(b, layout.Screen), 8, 8), WhiteHash(img, Rect(b, layout.Band), 32, 2)
}

// ScreenMatches reports, for every layout, the nearest screen templates.
func (d *Detector) ScreenMatches(img image.Image) []ScreenMatch {
	var out []ScreenMatch
	for _, layout := range d.cfg.Layouts {
		ph, bh := screenHashes(img, layout)
		best, dist, _ := d.classify(KindScreen, ph, 64)
		bbest, bdist, _ := d.classify(KindBand, bh, 64)
		out = append(out, ScreenMatch{Want: layout.ScreenLabel(), Best: best, Dist: dist, BandBest: bbest, BandDist: bdist})
	}
	return out
}

// isScreen reports whether img is layout's results screen: both the first
// panel's labels and the full-width band must match a template learned for
// this layout (the band is what tells 6 from 7 players).
func (d *Detector) isScreen(img image.Image, layout config.Layout) bool {
	want := layout.ScreenLabel()
	ph, bh := screenHashes(img, layout)
	if label, _, ok := d.classify(KindScreen, ph, d.cfg.ScreenThreshold); !ok || label != want {
		return false
	}
	label, _, ok := d.classify(KindBand, bh, d.cfg.BandThreshold)
	return ok && label == want
}

// Detect tries every layout and returns a reading when the frame looks like
// a results screen. Unknown characters/ranks are left zero.
func (d *Detector) Detect(img image.Image) (*Reading, bool) {
	b := img.Bounds()
	for _, layout := range d.cfg.Layouts {
		if !d.isScreen(img, layout) {
			continue
		}
		r := &Reading{Players: layout.Players, Team: layout.Mode == "team", Layout: layout}
		// The placement numerals first: each is matched by its shape
		// signature (independent of the column and background), and knowing
		// who won tells where that panel's text sits. One unread numeral is
		// inferred from the others before the text is read, so the winner's
		// shifted regions are used even when the gold "1" itself was unread.
		for _, slot := range layout.Slots {
			sr := SlotReading{Port: slot.Port}
			if sig, ok := ocr.GlyphSignature(Crop(img, Rect(b, slot.Rank))); ok {
				sr.RankHash = sig
				if lbl, dist, ok := d.classifyRank(sig); ok {
					sr.Rank, _ = strconv.Atoi(lbl)
					sr.RankDist = dist
				} else {
					sr.RankDist = dist
				}
			} else {
				sr.RankDist = 64
			}
			r.Slots = append(r.Slots, sr)
		}
		fillMissingRank(r)
		for i := range r.Slots {
			sr := &r.Slots[i]
			sr.Winner = sr.Rank == 1
			sr.CharRegion = layout.CharRegion(i, sr.Winner)
			sr.NameRegion = layout.NameRegion(i, sr.Winner)

			sr.NameHash = DHash(img, Rect(b, sr.NameRegion))
			if lbl, dist, ok := d.classify(KindName, sr.NameHash, d.cfg.NameThreshold); ok {
				sr.NameDist = dist
				if strings.HasPrefix(lbl, "#") {
					sr.Placeholder = true
				} else {
					sr.Name = lbl
				}
			} else {
				sr.NameDist = dist
			}
			sr.CharHash = DHash(img, Rect(b, sr.CharRegion))
			if lbl, dist, ok := d.classify(KindChar, sr.CharHash, d.cfg.CharThreshold); ok {
				sr.Char, sr.CharDist = lbl, dist
			} else {
				sr.CharDist = dist
			}
		}
		return r, true
	}
	return nil, false
}

// NormalizeLabel turns a human label into the stored form for a kind.
func NormalizeLabel(kind, label string) (string, error) {
	label = strings.TrimSpace(label)
	switch kind {
	case KindChar:
		ch, ok := characters.Resolve(label)
		if !ok {
			return "", fmt.Errorf("unknown character %q", label)
		}
		return ch.Key, nil
	case KindRank:
		l := strings.ToLower(label)
		l = strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(l, "st"), "nd"), "rd"), "th")
		l = strings.TrimSuffix(l, "位")
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 8 {
			return "", fmt.Errorf("rank must be 1-8, got %q", label)
		}
		return strconv.Itoa(n), nil
	case KindScreen, KindBand:
		l := strings.ToLower(label)
		if strings.HasPrefix(l, "results:") {
			return l, nil
		}
		return "", fmt.Errorf("screen label must be results:<players> or results:<players>:team, got %q", label)
	case KindName:
		if label == "" {
			return "", fmt.Errorf("player name is empty")
		}
		return label, nil
	}
	return "", fmt.Errorf("unknown template kind %q", kind)
}

// Teach registers templates for every region of one layout from a known
// frame: the screen hash, each slot's character, rank and player name tag.
// mode is "" (1on1 / free-for-all) or "team". chars[i] / ranks[i] / names[i]
// correspond to layout.Slots[i]; empty strings are skipped. Names create the
// player if needed.
func Teach(c *core.Core, cfg config.VisionConfig, img image.Image, players int, mode string, chars, ranks, names []string) error {
	layout, ok := cfg.Layout(players, mode)
	if !ok {
		return fmt.Errorf("no layout for %d players (mode %q)", players, mode)
	}
	b := img.Bounds()
	ph, bh := screenHashes(img, layout)
	if err := c.AddTemplate(KindScreen, layout.ScreenLabel(), ph); err != nil {
		return err
	}
	if err := c.AddTemplate(KindBand, layout.ScreenLabel(), bh); err != nil {
		return err
	}
	for i, slot := range layout.Slots {
		// the winner's panel is enlarged: its caption and tag sit lower
		winner := i < len(ranks) && strings.TrimSpace(ranks[i]) != "" && func() bool {
			lbl, err := NormalizeLabel(KindRank, ranks[i])
			return err == nil && lbl == "1"
		}()
		if i < len(chars) && strings.TrimSpace(chars[i]) != "" {
			lbl, err := NormalizeLabel(KindChar, chars[i])
			if err != nil {
				return err
			}
			if err := c.AddTemplate(KindChar, lbl, DHash(img, Rect(b, layout.CharRegion(i, winner)))); err != nil {
				return err
			}
		}
		if i < len(ranks) && strings.TrimSpace(ranks[i]) != "" {
			lbl, err := NormalizeLabel(KindRank, ranks[i])
			if err != nil {
				return err
			}
			sig, ok := ocr.GlyphSignature(Crop(img, Rect(b, slot.Rank)))
			if !ok {
				return fmt.Errorf("column %d: no numeral found in the rank region", i+1)
			}
			if err := c.AddTemplate(KindRank, lbl, sig); err != nil {
				return err
			}
		}
		if i < len(names) && strings.TrimSpace(names[i]) != "" {
			p, err := c.GetOrCreatePlayer(names[i])
			if err != nil {
				return err
			}
			if err := c.AddTemplate(KindName, p.Name, DHash(img, Rect(b, layout.NameRegion(i, winner)))); err != nil {
				return err
			}
		}
	}
	return nil
}

// fillMissingRank infers one unread placement numeral: in a free-for-all
// the placements are 1..N, so when every other slot reads a distinct
// placement the remaining one is the number left over. (A team battle
// shares placements between teammates; then nothing can be inferred and
// the slot stays unknown.) The 1st-place numeral is the one most often
// unread, being gold, larger and partly behind the laurels, and a 1v1
// screen was going unrecorded because of it.
func fillMissingRank(r *Reading) {
	missing := -1
	seen := map[int]bool{}
	for i, s := range r.Slots {
		if s.Rank == 0 {
			if missing >= 0 {
				return // two unknowns: nothing to infer
			}
			missing = i
			continue
		}
		if seen[s.Rank] || s.Rank > len(r.Slots) {
			return // a shared placement (teams) or a misread: do not guess
		}
		seen[s.Rank] = true
	}
	if missing < 0 {
		return
	}
	for n := 1; n <= len(r.Slots); n++ {
		if !seen[n] {
			r.Slots[missing].Rank = n
			r.Slots[missing].Inferred = true
			return
		}
	}
}
