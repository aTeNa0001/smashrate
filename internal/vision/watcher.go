package vision

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/ocr"
)

// pendingCrop is an unrecognised region waiting for a human label.
type pendingCrop struct {
	Kind string
	Hash uint64
	Path string
	Seen time.Time
}

// Watcher turns frames into recorded matches.
type Watcher struct {
	cfg     config.VisionConfig
	core    *core.Core
	det     *Detector
	dataDir string
	notify  func(string)
	ocr     ocr.Engine // nil = disabled

	mu       sync.Mutex
	pending  map[string]pendingCrop
	lastKey  string
	stable   int
	lastSeen string
	cooldown time.Time
}

// NewWatcher wires the detector to the core. notify may be nil.
func NewWatcher(cfg config.VisionConfig, c *core.Core, dataDir string, notify func(string)) (*Watcher, error) {
	det, err := NewDetector(cfg, c)
	if err != nil {
		return nil, err
	}
	if notify == nil {
		notify = func(s string) { log.Println("vision:", s) }
	}
	eng, err := ocr.New(cfg.OCR)
	if err != nil {
		return nil, err
	}
	if eng != nil {
		log.Printf("vision: name-tag OCR engine %q", eng.Name())
	} else {
		log.Printf("vision: name-tag OCR disabled; unknown tags must be labelled by hand")
	}
	return &Watcher{cfg: cfg, core: c, det: det, dataDir: dataDir, notify: notify, ocr: eng, pending: map[string]pendingCrop{}}, nil
}

// SetOCR replaces the OCR engine (tests, or a custom engine).
func (w *Watcher) SetOCR(e ocr.Engine) { w.ocr = e }

// readTag OCRs a name-tag crop and resolves it to a player name, creating
// the player when the text matches nobody. The tag's hash is stored so the
// next appearance is matched without OCR. Returns "" when nothing was read.
func (w *Watcher) readTag(crop image.Image, hash uint64) (name string, created bool) {
	if w.ocr == nil {
		return "", false
	}
	text, err := w.ocr.Recognize(crop)
	if err != nil {
		log.Printf("vision: ocr: %v", err)
		return "", false
	}
	clean := ocr.NormalizeName(text)
	if clean == "" {
		return "", false
	}
	players, _ := w.core.Players()
	names := make([]string, 0, len(players))
	for _, p := range players {
		names = append(names, p.Name)
	}
	if m := ocr.MatchName(clean, names); m != "" {
		name = m
	} else {
		p, err := w.core.GetOrCreatePlayer(clean)
		if err != nil {
			log.Printf("vision: create player %q: %v", clean, err)
			return "", false
		}
		name, created = p.Name, true
	}
	if err := w.core.AddTemplate(KindName, name, hash); err == nil {
		_ = w.det.Reload(w.core)
	}
	return name, created
}

// Run consumes frames until ctx ends or the source is exhausted.
func (w *Watcher) Run(ctx context.Context, src FrameSource) error {
	if w.det.TemplateCount(KindScreen) == 0 {
		w.notify("リザルト画面のテンプレートが未登録です。`smashrate vision teach` で最初の画面（キャラ・順位・名前タグ）を学習させてください。")
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		img, err := src.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		w.Process(img)
	}
}

// Process handles one frame. Exported so tests and `vision test` can drive it.
func (w *Watcher) Process(img image.Image) {
	reading, ok := w.det.Detect(img)
	if !ok {
		w.lastKey, w.stable = "", 0
		return
	}
	key := reading.Key()
	if key == w.lastKey {
		w.stable++
	} else {
		w.lastKey, w.stable = key, 1
	}
	need := w.cfg.StableFrames
	if need <= 0 {
		need = 1
	}
	if w.stable != need {
		return
	}
	if time.Now().Before(w.cooldown) && key == w.lastSeen {
		return
	}
	cd := time.Duration(w.cfg.CooldownSeconds) * time.Second
	if cd <= 0 {
		cd = 60 * time.Second
	}
	w.cooldown = time.Now().Add(cd)
	w.lastSeen = key
	w.handle(img, reading)
}

func (w *Watcher) handle(img image.Image, r *Reading) {
	layout, _ := w.cfg.Layout(r.Players)
	var in core.MatchInput
	in.Source = "vision"
	var unknown []string
	var unidentified []int
	var newcomers []string
	for i, s := range r.Slots {
		// The name tag identifies the human: learned hash first, OCR for a
		// tag never seen before, manual labelling as the last resort.
		name := s.Name
		if name == "" && i < len(layout.Slots) {
			crop := Crop(img, Rect(img.Bounds(), layout.Slots[i].Name))
			var created bool
			if name, created = w.readTag(crop, s.NameHash); created {
				newcomers = append(newcomers, name)
			}
		}
		if name == "" {
			unidentified = append(unidentified, s.Port)
			if i < len(layout.Slots) {
				id := w.stash(KindName, s.NameHash, img, Rect(img.Bounds(), layout.Slots[i].Name))
				unknown = append(unknown, fmt.Sprintf("P%d 名前タグ → %s", s.Port, id))
			}
			continue
		}
		e := core.EntryInput{Player: name, Port: s.Port, Team: s.Port, Rank: s.Rank, Character: s.Char}
		if s.Char == "" && i < len(layout.Slots) {
			id := w.stash(KindChar, s.CharHash, img, Rect(img.Bounds(), layout.Slots[i].Char))
			unknown = append(unknown, fmt.Sprintf("P%d キャラ → %s", s.Port, id))
		}
		if s.Rank == 0 && i < len(layout.Slots) {
			id := w.stash(KindRank, s.RankHash, img, Rect(img.Bounds(), layout.Slots[i].Rank))
			unknown = append(unknown, fmt.Sprintf("P%d 順位 → %s", s.Port, id))
		}
		in.Entries = append(in.Entries, e)
	}

	var msg []string
	if len(newcomers) > 0 {
		msg = append(msg, fmt.Sprintf("新しいプレイヤーを登録しました: %s（読み間違いなら `rename <今の名前> <正しい名前>`、既存の人なら `merge <今の名前> <既存の名前>`）", strings.Join(newcomers, ", ")))
	}
	if len(unidentified) > 0 {
		msg = append(msg, fmt.Sprintf("列 %v のプレイヤーを特定できなかったため記録していません。名前タグを `learn <画像ID> <名前>` で教えると次回から自動で判別します。", unidentified))
	}
	if len(unknown) > 0 {
		msg = append(msg, "未知の画像があります。`learn <画像ID> <ラベル>` で教えてください:\n  "+strings.Join(unknown, "\n  "))
	}
	if len(unidentified) == 0 && r.Complete() && len(in.Entries) >= 2 {
		m, err := w.core.RecordMatch(in)
		if err != nil {
			msg = append(msg, "自動記録に失敗: "+err.Error())
		} else {
			msg = append(msg, "自動記録しました "+describe(*m))
		}
	} else if len(unidentified) == 0 {
		msg = append(msg, "順位が読めなかったため記録していません。手動で `record` してください。")
	}
	w.notify(strings.Join(msg, "\n"))
}

func describe(m core.Match) string {
	es := append([]core.Entry(nil), m.Entries...)
	sort.Slice(es, func(i, j int) bool { return es[i].Rank < es[j].Rank })
	var parts []string
	for _, e := range es {
		s := fmt.Sprintf("%d位 %s", e.Rank, e.PlayerName)
		if ch, ok := characters.ByKey(e.Character); ok {
			s += "(" + ch.NameJA + ")"
		}
		if e.DisplayAfter != 0 {
			s += fmt.Sprintf(" %+d", e.DisplayAfter-e.DisplayBefore)
		}
		parts = append(parts, s)
	}
	return fmt.Sprintf("#%d [%s] %s", m.ID, m.Mode, strings.Join(parts, ", "))
}

// stash saves a crop for later labelling and returns its id.
func (w *Watcher) stash(kind string, hash uint64, img image.Image, r image.Rectangle) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	// reuse an id if the same hash is already pending
	for id, p := range w.pending {
		if p.Kind == kind && Hamming(p.Hash, hash) <= 2 {
			return id
		}
	}
	id := fmt.Sprintf("%s-%d", kind, time.Now().Unix()%100000)
	for i := 0; ; i++ {
		if _, exists := w.pending[id]; !exists {
			break
		}
		id = fmt.Sprintf("%s-%d-%d", kind, time.Now().Unix()%100000, i)
	}
	dir := filepath.Join(w.dataDir, "unknown")
	_ = os.MkdirAll(dir, 0o755)
	path := filepath.Join(dir, id+".png")
	if err := SavePNG(path, Crop(img, r)); err != nil {
		log.Printf("vision: save crop: %v", err)
	}
	w.pending[id] = pendingCrop{Kind: kind, Hash: hash, Path: path, Seen: time.Now()}
	return id
}

// Pending lists crop ids waiting for labels.
func (w *Watcher) Pending() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	ids := make([]string, 0, len(w.pending))
	for id := range w.pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Learn labels a pending crop, stores it as a template and reloads.
func (w *Watcher) Learn(id, label string) error {
	w.mu.Lock()
	p, ok := w.pending[id]
	w.mu.Unlock()
	if !ok {
		return fmt.Errorf("no pending crop %q", id)
	}
	lbl, err := NormalizeLabel(p.Kind, label)
	if err != nil {
		return err
	}
	if p.Kind == KindName {
		pl, err := w.core.GetOrCreatePlayer(lbl)
		if err != nil {
			return err
		}
		lbl = pl.Name
	}
	if err := w.core.AddTemplate(p.Kind, lbl, p.Hash); err != nil {
		return err
	}
	w.mu.Lock()
	delete(w.pending, id)
	w.mu.Unlock()
	_ = os.Remove(p.Path)
	// a corrected reading should be recorded on the next identical screen
	w.lastSeen = ""
	return w.det.Reload(w.core)
}

// Reload refreshes templates (after `vision teach`).
func (w *Watcher) Reload() error { return w.det.Reload(w.core) }
