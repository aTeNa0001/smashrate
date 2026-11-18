package vision

import (
	"fmt"
	"image"
	"sort"
	"strconv"
	"strings"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/ocr"
)

// One appearance of the results screen goes through up to three pages, all
// with the same panel per player in the same column:
//
//   - results: the stat rows 全滅/撃墜/落下/自滅 and the placement numeral
//     on each panel. Names, characters and placements can all be read.
//   - detail: the next page (落とした数/落とされた数/与えたダメージ...). The
//     headers (caption, badge, name tag) are the same, the numerals are
//     gone: names and characters can be read, placements cannot.
//   - ready: a panel whose player pressed OK flips to "READY FOR THE NEXT
//     BATTLE" with only its placement numeral left at the top. Placements
//     can be read, names and characters cannot.
//
// A skipped screen may show the results page for a few frames only; the
// appearance is the union of every page seen, merged per column (see
// mergeBurst), so names can come from one page and placements from another.
const (
	PageResults = "results"
	PageDetail  = "detail"
	PageReady   = "ready"
)

// Pages lists the page kinds, in the order they are tried.
var Pages = []string{PageResults, PageDetail, PageReady}

// pageLabel is the template label of a layout's page ("results:4",
// "detail:4:team", ...).
func pageLabel(layout config.Layout, page string) string {
	return strings.Replace(layout.ScreenLabel(), PageResults, page, 1)
}

// pageRegions are the two regions hashed to recognise a page: the first
// panel's text and the same band across the whole frame. The results and
// detail pages are told by their stat-row labels (same place, different
// words); the ready page by the READY FOR THE NEXT BATTLE plates, which
// sit higher. A blank frame hashes to zero in every region, which is far
// from any of these, so a black screen is never taken for a page.
func pageRegions(layout config.Layout, page string) (screen, band config.Region) {
	if page != PageReady {
		return layout.Screen, layout.Band
	}
	panelW := layout.Screen.W / 0.3 // the stat-label region is 30% of the first panel
	return config.Region{X: layout.Screen.X, Y: 0.36, W: panelW, H: 0.22},
		config.Region{X: 0, Y: 0.36, W: 1, H: 0.22}
}

// pageHashes hashes the two regions of a page.
func pageHashes(img image.Image, layout config.Layout, page string) (panel, band uint64) {
	b := img.Bounds()
	s, bd := pageRegions(layout, page)
	return WhiteHash(img, Rect(b, s), 8, 8), WhiteHash(img, Rect(b, bd), 32, 2)
}

// readyRankRegion is where slot's numeral sits on the ready page: at the
// top of the column. On the wide panels (2-4 players) that is where it
// always is; on the narrow ones it moves up from the portrait.
func readyRankRegion(slot config.Slot) config.Region {
	if slot.Rank.Y < 0.1 {
		return slot.Rank
	}
	c := slot.Char // spans the panel's width
	return config.Region{X: c.X + c.W*0.35, Y: 0, W: c.W * 0.65, H: 0.22}
}

// screenPage reports which page of layout's results screen img is, if any:
// both the first panel's text and the full-width band must match a
// template learned for that page of this layout.
func (d *Detector) screenPage(img image.Image, layout config.Layout) (string, bool) {
	for _, page := range Pages {
		want := pageLabel(layout, page)
		ph, bh := pageHashes(img, layout, page)
		if label, _, ok := d.classify(KindScreen, ph, d.cfg.ScreenThreshold); !ok || label != want {
			continue
		}
		if label, _, ok := d.classify(KindBand, bh, d.cfg.BandThreshold); ok && label == want {
			return page, true
		}
	}
	return "", false
}

// readRank reads the numeral in region r.
func (d *Detector) readRank(img image.Image, r config.Region, sr *SlotReading) {
	sig, ok := ocr.GlyphSignature(Crop(img, Rect(img.Bounds(), r)))
	if !ok {
		sr.RankDist = 64
		return
	}
	sr.RankHash = sig
	lbl, dist, ok := d.classifyRank(sig)
	sr.RankDist = dist
	if ok {
		sr.Rank, _ = strconv.Atoi(lbl)
	}
}

// TeachPage registers a page of a layout's results screen from a known
// frame (see Teach for the arguments). The ready page teaches the screen
// and the numerals; the detail page the screen, captions and name tags.
func TeachPage(c *core.Core, cfg config.VisionConfig, img image.Image, players int, mode, page string, chars, ranks, names []string) error {
	layout, ok := cfg.Layout(players, mode)
	if !ok {
		return fmt.Errorf("no layout for %d players (mode %q)", players, mode)
	}
	if page == PageResults {
		return Teach(c, cfg, img, players, mode, chars, ranks, names)
	}
	ph, bh := pageHashes(img, layout, page)
	label := pageLabel(layout, page)
	if err := c.AddTemplate(KindScreen, label, ph); err != nil {
		return err
	}
	if err := c.AddTemplate(KindBand, label, bh); err != nil {
		return err
	}
	b := img.Bounds()
	for i, slot := range layout.Slots {
		switch page {
		case PageReady:
			if i < len(ranks) && strings.TrimSpace(ranks[i]) != "" {
				lbl, err := NormalizeLabel(KindRank, ranks[i])
				if err != nil {
					return err
				}
				sig, ok := ocr.GlyphSignature(Crop(img, Rect(b, readyRankRegion(slot))))
				if !ok {
					return fmt.Errorf("column %d: no numeral found at the top of the ready page", i+1)
				}
				if err := c.AddTemplate(KindRank, lbl, sig); err != nil {
					return err
				}
			}
		case PageDetail:
			charRegion, nameRegion, _ := SlotRegions(img, layout, i, false)
			if i < len(chars) && strings.TrimSpace(chars[i]) != "" {
				lbl, err := NormalizeLabel(KindChar, chars[i])
				if err != nil {
					return err
				}
				if err := c.AddTemplate(KindChar, lbl, DHash(img, Rect(b, charRegion))); err != nil {
					return err
				}
			}
			if i < len(names) && strings.TrimSpace(names[i]) != "" {
				p, err := c.GetOrCreatePlayer(names[i])
				if err != nil {
					return err
				}
				if err := c.AddTemplate(KindName, p.Name, TagHash(img, Rect(b, nameRegion))); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unknown page %q (results, detail or ready)", page)
		}
	}
	return nil
}

// mergeBurst combines every reading of one appearance into one, column by
// column: the placement most often read on the results and ready pages,
// the name and character most often read on the results and detail pages.
// identity is the most frequent entry of a page that shows the headers
// (results or detail): its frames are the ones to OCR new name tags from
// and to keep as evidence. A burst seen only on the ready page has no
// identity and cannot be recorded.
func mergeBurst(b *burst) (merged *Reading, identity *burstEntry) {
	for _, k := range b.order {
		e := b.readings[k]
		if e.reading.Page == PageReady {
			continue
		}
		if identity == nil || e.count > identity.count || (e.count == identity.count && e.last.After(identity.last)) {
			identity = e
		}
	}
	if identity == nil {
		return nil, nil
	}
	base := identity.reading
	m := *base
	m.Slots = append([]SlotReading(nil), base.Slots...)
	type votes map[string]int
	for i := range m.Slots {
		ranks, names, chars := votes{}, votes{}, votes{}
		rankSlot := map[string]SlotReading{}
		nameSlot := map[string]SlotReading{}
		charSlot := map[string]SlotReading{}
		for _, k := range b.order {
			e := b.readings[k]
			r := e.reading
			if r.Players != base.Players || r.Team != base.Team || i >= len(r.Slots) {
				continue
			}
			s := r.Slots[i]
			if r.Page != PageDetail && s.Rank > 0 && !s.Inferred {
				key := strconv.Itoa(s.Rank)
				ranks[key] += e.count
				rankSlot[key] = s
			}
			if r.Page != PageReady {
				switch {
				case s.Placeholder:
					names["#"] += e.count
					nameSlot["#"] = s
				case s.Name != "":
					names[s.Name] += e.count
					nameSlot[s.Name] = s
				}
				if s.Char != "" {
					chars[s.Char] += e.count
					charSlot[s.Char] = s
				}
			}
		}
		s := &m.Slots[i]
		s.Rank, s.Inferred = 0, false
		if k := top(ranks); k != "" {
			o := rankSlot[k]
			s.Rank, s.RankDist, s.RankHash = o.Rank, o.RankDist, o.RankHash
		}
		if k := top(names); k != "" {
			o := nameSlot[k]
			s.Name, s.Placeholder, s.NameDist, s.NameHash = o.Name, o.Placeholder, o.NameDist, o.NameHash
		}
		if k := top(chars); k != "" {
			o := charSlot[k]
			s.Char, s.CharDist, s.CharHash = o.Char, o.CharDist, o.CharHash
		}
	}
	m.Page = PageResults
	fillMissingRank(&m)
	for i := range m.Slots {
		m.Slots[i].Winner = m.Slots[i].Rank == 1
	}
	return &m, identity
}

// top is the key with the most votes (the smallest key on a tie, so the
// result does not depend on map order).
func top(v map[string]int) string {
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	best, bestN := "", 0
	for _, k := range keys {
		if v[k] > bestN {
			best, bestN = k, v[k]
		}
	}
	return best
}

// ranksComplete reports whether every column of r has a placement.
func ranksComplete(r *Reading) bool {
	if r == nil {
		return false
	}
	for _, s := range r.Slots {
		if s.Rank == 0 {
			return false
		}
	}
	return true
}
