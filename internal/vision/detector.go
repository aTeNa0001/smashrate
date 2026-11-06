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
	"github.com/aTeNa0001/smashrate/internal/ocr"
)

// Template kinds stored in the database.
const (
	KindScreen = "screen" // label "results:<players>"
	KindChar   = "char"   // label = character key
	KindRank   = "rank"   // label = "1".."8"
	KindName   = "name"   // label = player name (as shown on the name tag)
)

// PlaceholderLabel marks a learned name-tag hash that is not a person
// (P1 / プレイヤー1 / CPU / コンピューター2).
const PlaceholderLabel = "#placeholder"

// SlotReading is what was recognised in one player column.
type SlotReading struct {
	Port        int
	Name        string // player name from the name tag, "" if unknown
	Placeholder bool   // the tag is a default label or a CPU, not a person
	NameDist    int
	NameHash    uint64
	Char        string // character key, "" if unknown
	CharDist    int
	CharHash    uint64
	Rank        int // 0 if unknown
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
	for _, kind := range []string{KindScreen, KindChar, KindRank, KindName} {
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

// builtinRanks are placement-numeral signatures measured on a real 4-player
// results screen (640x360 capture), so 1st and 2nd work before anything has
// been learned. The same digit varies by a few bits between columns, hence
// more than one entry per label. Further digits are learned once (Slack
// `learn` or `vision teach -ranks`).
var builtinRanks = []core.Template{
	{Kind: KindRank, Label: "1", Hash: 0xd630f0f030303030},
	{Kind: KindRank, Label: "2", Hash: 0x1e7f670f3c70fffe},
	{Kind: KindRank, Label: "2", Hash: 0x1e3f33071cf8bffe},
}

// classifyRank matches a numeral signature against learned and built-in ones.
func (d *Detector) classifyRank(sig uint64) (string, int, bool) {
	best, bestDist := "", 65
	for _, list := range [][]core.Template{d.templates[KindRank], builtinRanks} {
		for _, t := range list {
			if dist := Hamming(t.Hash, sig); dist < bestDist {
				best, bestDist = t.Label, dist
			}
		}
	}
	return best, bestDist, best != "" && bestDist <= d.cfg.RankThreshold
}

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
		want := layout.ScreenLabel()
		h := DHash(img, Rect(b, layout.Screen))
		label, _, ok := d.classify(KindScreen, h, d.cfg.ScreenThreshold)
		if !ok || label != want {
			continue
		}
		r := &Reading{Players: layout.Players, Team: layout.Mode == "team", Layout: layout}
		for _, slot := range layout.Slots {
			sr := SlotReading{Port: slot.Port}
			sr.NameHash = DHash(img, Rect(b, slot.Name))
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
			sr.CharHash = DHash(img, Rect(b, slot.Char))
			if lbl, dist, ok := d.classify(KindChar, sr.CharHash, d.cfg.CharThreshold); ok {
				sr.Char, sr.CharDist = lbl, dist
			} else {
				sr.CharDist = dist
			}
			// The placement numeral is matched by its shape signature, which is
			// independent of the column and of what is drawn behind it.
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
	if err := c.AddTemplate(KindScreen, layout.ScreenLabel(), DHash(img, Rect(b, layout.Screen))); err != nil {
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
			if err := c.AddTemplate(KindName, p.Name, DHash(img, Rect(b, slot.Name))); err != nil {
				return err
			}
		}
	}
	return nil
}
