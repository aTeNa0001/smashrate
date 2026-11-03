package vision

import (
	"fmt"
	"image"
	_ "image/jpeg" // enable JPEG decoding for FileSource
	"strconv"
	"strings"

	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
)

// Template kinds stored in the database.
const (
	KindScreen = "screen" // label "results:<players>"
	KindChar   = "char"   // label = character key
	KindRank   = "rank"   // label = "1".."8"
)

// SlotReading is what was recognised in one player column.
type SlotReading struct {
	Port     int
	Char     string // character key, "" if unknown
	CharDist int
	CharHash uint64
	Rank     int // 0 if unknown
	RankDist int
	RankHash uint64
}

// Reading is a recognised results screen.
type Reading struct {
	Players int
	Slots   []SlotReading
}

// Key is a stable string used to compare consecutive readings.
func (r Reading) Key() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d:", r.Players)
	for _, s := range r.Slots {
		fmt.Fprintf(&sb, "P%d=%s/%d;", s.Port, s.Char, s.Rank)
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

// Detector matches frames against learned templates.
type Detector struct {
	cfg       config.VisionConfig
	templates map[string][]core.Template
}

// NewDetector builds a detector from the templates in the database.
func NewDetector(cfg config.VisionConfig, c *core.Core) (*Detector, error) {
	d := &Detector{cfg: cfg}
	return d, d.Reload(c)
}

// Reload re-reads templates.
func (d *Detector) Reload(c *core.Core) error {
	d.templates = map[string][]core.Template{}
	for _, kind := range []string{KindScreen, KindChar, KindRank} {
		ts, err := c.Templates(kind)
		if err != nil {
			return err
		}
		d.templates[kind] = ts
	}
	return nil
}

// TemplateCount returns how many templates of a kind are loaded.
func (d *Detector) TemplateCount(kind string) int { return len(d.templates[kind]) }

// classify returns the nearest template label within threshold.
func (d *Detector) classify(kind string, h uint64, threshold int) (string, int, bool) {
	best, bestDist := "", 65
	for _, t := range d.templates[kind] {
		if dist := Hamming(t.Hash, h); dist < bestDist {
			best, bestDist = t.Label, dist
		}
	}
	return best, bestDist, best != "" && bestDist <= threshold
}

// Detect tries every layout and returns a reading when the frame looks like
// a results screen. Unknown characters/ranks are left zero.
func (d *Detector) Detect(img image.Image) (*Reading, bool) {
	b := img.Bounds()
	for _, layout := range d.cfg.Layouts {
		want := fmt.Sprintf("results:%d", layout.Players)
		h := DHash(img, Rect(b, layout.Screen))
		label, _, ok := d.classify(KindScreen, h, d.cfg.ScreenThreshold)
		if !ok || label != want {
			continue
		}
		r := &Reading{Players: layout.Players}
		for _, slot := range layout.Slots {
			sr := SlotReading{Port: slot.Port}
			sr.CharHash = DHash(img, Rect(b, slot.Char))
			if lbl, dist, ok := d.classify(KindChar, sr.CharHash, d.cfg.CharThreshold); ok {
				sr.Char, sr.CharDist = lbl, dist
			} else {
				sr.CharDist = dist
			}
			sr.RankHash = DHash(img, Rect(b, slot.Rank))
			if lbl, dist, ok := d.classify(KindRank, sr.RankHash, d.cfg.RankThreshold); ok {
				sr.Rank, _ = strconv.Atoi(lbl)
				sr.RankDist = dist
			} else {
				sr.RankDist = dist
			}
			r.Slots = append(r.Slots, sr)
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
	case KindScreen:
		l := strings.ToLower(label)
		if strings.HasPrefix(l, "results:") {
			return l, nil
		}
		return "", fmt.Errorf("screen label must be results:<players>, got %q", label)
	}
	return "", fmt.Errorf("unknown template kind %q", kind)
}

// Teach registers templates for every region of one layout from a known
// frame: the screen hash, each slot's character and each slot's rank.
// chars[i] / ranks[i] correspond to layout.Slots[i]; empty strings are skipped.
func Teach(c *core.Core, cfg config.VisionConfig, img image.Image, players int, chars, ranks []string) error {
	layout, ok := cfg.Layout(players)
	if !ok {
		return fmt.Errorf("no layout for %d players", players)
	}
	b := img.Bounds()
	if err := c.AddTemplate(KindScreen, fmt.Sprintf("results:%d", players), DHash(img, Rect(b, layout.Screen))); err != nil {
		return err
	}
	for i, slot := range layout.Slots {
		if i < len(chars) && strings.TrimSpace(chars[i]) != "" {
			lbl, err := NormalizeLabel(KindChar, chars[i])
			if err != nil {
				return err
			}
			if err := c.AddTemplate(KindChar, lbl, DHash(img, Rect(b, slot.Char))); err != nil {
				return err
			}
		}
		if i < len(ranks) && strings.TrimSpace(ranks[i]) != "" {
			lbl, err := NormalizeLabel(KindRank, ranks[i])
			if err != nil {
				return err
			}
			if err := c.AddTemplate(KindRank, lbl, DHash(img, Rect(b, slot.Rank))); err != nil {
				return err
			}
		}
	}
	return nil
}
