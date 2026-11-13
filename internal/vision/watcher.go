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

	mu        sync.Mutex
	pending   map[string]pendingCrop
	lastFrame image.Image
	stats     Status
	events    []Event // ring of recent notifications
	lastKey   string
	stable    int
	lastSeen  string
	cooldown  time.Time
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

// recognize runs OCR on a crop; "" when OCR is disabled or fails.
func (w *Watcher) recognize(crop image.Image) string {
	if w.ocr == nil {
		return ""
	}
	text, err := w.ocr.Recognize(crop)
	if err != nil {
		log.Printf("vision: ocr: %v", err)
		return ""
	}
	return text
}

// learn stores a hash under a label and reloads the detector.
func (w *Watcher) learn(kind, label string, hash uint64) {
	if err := w.core.AddTemplate(kind, label, hash); err == nil {
		_ = w.det.Reload(w.core)
	}
}

// readTag OCRs a name-tag crop and resolves it to a player name, creating
// the player when the text matches nobody. The tag's hash is stored so the
// next appearance is matched without OCR. placeholder is true for default
// labels (プレイヤー1, コンピューター2, P1, CPU), which are remembered too.
func (w *Watcher) readTag(crop image.Image, hash uint64) (name string, created, placeholder bool) {
	if w.ocr == nil {
		return "", false, false
	}
	players, _ := w.core.Players()
	names := make([]string, 0, len(players))
	for _, p := range players {
		names = append(names, p.Name)
	}
	// Try the smooth enlargement first (best for kana), then the isolated
	// ink; the first reading that is a placeholder or matches a known
	// player wins, otherwise the first non-empty reading becomes a new player.
	first := ""
	for _, v := range ocr.Variants(crop) {
		clean := ocr.NormalizeName(w.recognize(v))
		if clean == "" {
			continue
		}
		if ocr.IsPlaceholder(clean) {
			w.learn(KindName, PlaceholderLabel, hash)
			return "", false, true
		}
		if m := ocr.MatchName(clean, names); m != "" {
			w.learn(KindName, m, hash)
			return m, false, false
		}
		if first == "" {
			first = clean
		}
	}
	if first == "" {
		return "", false, false
	}
	p, err := w.core.GetOrCreatePlayer(first)
	if err != nil {
		log.Printf("vision: create player %q: %v", first, err)
		return "", false, false
	}
	w.learn(KindName, p.Name, hash)
	return p.Name, true, false
}

// readChar OCRs the fighter-name caption and learns its hash. The isolated
// ink variant reads the bold Latin captions best, so it is tried first.
func (w *Watcher) readChar(crop image.Image, hash uint64) string {
	if w.ocr == nil {
		return ""
	}
	vs := ocr.Variants(crop)
	for i := len(vs) - 1; i >= 0; i-- {
		if key, ok := ocr.MatchCharacter(w.recognize(vs[i])); ok {
			w.learn(KindChar, key, hash)
			return key
		}
	}
	return ""
}

// LastFrame returns the most recent frame seen (nil before the first).
func (w *Watcher) LastFrame() image.Image {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastFrame
}

// Status is a snapshot of what the watcher has been doing, for /status.
type Status struct {
	Frames          int64          `json:"frames"`
	LastFrameAt     time.Time      `json:"last_frame_at"`
	Detections      int64          `json:"detections"`
	LastDetectionAt time.Time      `json:"last_detection_at"`
	LastMessage     string         `json:"last_message"`
	LastMessageAt   time.Time      `json:"last_message_at"`
	Pending         []string       `json:"pending"`
	Templates       map[string]int `json:"templates"`
	OCR             string         `json:"ocr"`
}

// ScreenTemplates is how many results-screen templates are known (0 = the
// watcher cannot recognise anything yet).
func (s Status) ScreenTemplates() int { return s.Templates[KindScreen] }

// Status returns the current counters and the learned-template counts.
func (w *Watcher) Status() Status {
	w.mu.Lock()
	s := w.stats
	w.mu.Unlock()
	s.Pending = w.Pending()
	s.Templates = map[string]int{}
	for _, k := range []string{KindScreen, KindChar, KindRank, KindName} {
		s.Templates[k] = w.det.TemplateCount(k)
	}
	s.OCR = "none"
	if w.ocr != nil {
		s.OCR = w.ocr.Name()
	}
	return s
}

// StatusText is the one-paragraph form used by Slack `status`.
func (w *Watcher) StatusText() string {
	s := w.Status()
	var sb strings.Builder
	if s.LastFrameAt.IsZero() {
		sb.WriteString("キャプチャ: 映像がまだ来ていない（Switch の電源とキャプチャボードの USB を確認）")
	} else {
		fmt.Fprintf(&sb, "キャプチャ: 受信中（最後のフレーム %s、%d 枚）", s.LastFrameAt.Local().Format("15:04:05"), s.Frames)
	}
	if s.Templates[KindScreen] == 0 {
		sb.WriteString("\nリザルト画面: 未学習（vision teach が必要）")
	} else if s.LastDetectionAt.IsZero() {
		fmt.Fprintf(&sb, "\nリザルト画面: 学習済み %d 種、まだ検出なし", s.Templates[KindScreen])
	} else {
		fmt.Fprintf(&sb, "\nリザルト画面: 最後の検出 %s（累計 %d 回）", s.LastDetectionAt.Local().Format("01/02 15:04"), s.Detections)
	}
	fmt.Fprintf(&sb, "\n学習済み: キャラ %d、名前 %d、OCR %s", s.Templates[KindChar], s.Templates[KindName], s.OCR)
	if len(s.Pending) > 0 {
		fmt.Fprintf(&sb, "\nラベル待ち: %s", strings.Join(s.Pending, ", "))
	}
	if s.LastMessage != "" {
		fmt.Fprintf(&sb, "\n最後の通知 (%s): %s", s.LastMessageAt.Local().Format("15:04"), s.LastMessage)
	}
	return sb.String()
}

func (w *Watcher) note(msg string) {
	w.mu.Lock()
	w.stats.LastMessage, w.stats.LastMessageAt = msg, time.Now()
	w.events = append(w.events, Event{At: time.Now(), Text: msg})
	if len(w.events) > 50 {
		w.events = w.events[len(w.events)-50:]
	}
	w.mu.Unlock()
	w.notify(msg)
}

// Run consumes frames until ctx ends or the source is exhausted.
func (w *Watcher) Run(ctx context.Context, src FrameSource) error {
	if w.det.TemplateCount(KindScreen) == 0 {
		w.notify("リザルト画面のテンプレートが未登録です。リザルト画面のスクショで `smashrate vision teach -image shot.png -players N` を一度実行してください（キャラ・順位・名前は以後自動で学習します）。")
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
	w.mu.Lock()
	w.lastFrame = img
	w.stats.Frames++
	w.stats.LastFrameAt = time.Now()
	w.mu.Unlock()
	reading, ok := w.det.Detect(img)
	if !ok {
		w.lastKey, w.stable = "", 0
		return
	}
	w.mu.Lock()
	w.stats.Detections++
	w.stats.LastDetectionAt = time.Now()
	w.mu.Unlock()
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
	layout := r.Layout
	var in core.MatchInput
	in.Source = "vision"
	var unknown []string
	var unidentified, placeholders []int
	var newcomers []string
	b := img.Bounds()
	for i, s := range r.Slots {
		if i >= len(layout.Slots) {
			break
		}
		slot := layout.Slots[i]
		// Regions as the detector used them (shifted for the winner's panel).
		nameRect, charRect := Rect(b, s.NameRegion), Rect(b, s.CharRegion)
		// The name tag identifies the human: learned hash first, OCR for a
		// tag never seen before, manual labelling as the last resort.
		name, placeholder := s.Name, s.Placeholder
		if name == "" && !placeholder {
			var created bool
			name, created, placeholder = w.readTag(Crop(img, nameRect), s.NameHash)
			if created {
				newcomers = append(newcomers, name)
			}
		}
		if placeholder {
			placeholders = append(placeholders, s.Port)
			continue
		}
		if name == "" {
			unidentified = append(unidentified, s.Port)
			id := w.stash(KindName, s.NameHash, img, nameRect)
			unknown = append(unknown, fmt.Sprintf("P%d 名前タグ → %s", s.Port, id))
			continue
		}
		// Character: learned hash, else OCR of the caption (self-learning),
		// else a crop for manual labelling.
		char := s.Char
		if char == "" {
			char = w.readChar(Crop(img, charRect), s.CharHash)
		}
		if char == "" {
			id := w.stash(KindChar, s.CharHash, img, charRect)
			unknown = append(unknown, fmt.Sprintf("P%d キャラ → %s", s.Port, id))
		}
		// Placement numerals are matched by shape (built-in 1 and 2, the
		// rest learned once); an unknown digit is stashed for `learn`.
		rank := s.Rank
		if rank == 0 {
			id := w.stash(KindRank, s.RankHash, img, Rect(b, slot.Rank))
			unknown = append(unknown, fmt.Sprintf("P%d 順位 → %s", s.Port, id))
		}
		in.Entries = append(in.Entries, core.EntryInput{Player: name, Port: s.Port, Team: s.Port, Rank: rank, Character: char})
	}

	// Team battle and free-for-all share the same results screen; what tells
	// them apart is that teammates get the same placement. So players who
	// share a rank are recorded as one team, everyone else as their own.
	// (A timed free-for-all can tie too, which is accepted as a team result.)
	seenRank := map[int]int{}
	for _, e := range in.Entries {
		seenRank[e.Rank]++
	}
	tied := false
	for rk, n := range seenRank {
		if rk > 0 && n > 1 {
			tied = true
		}
	}
	if tied || r.Team {
		for i := range in.Entries {
			in.Entries[i].Team = in.Entries[i].Rank
		}
	}
	if tied && len(seenRank) == 1 {
		w.note("全員が同順位のため記録しません（チーム分けが判定できません）。")
		return
	}

	var msg []string
	if len(placeholders) > 0 {
		// CPU fighters or humans without a name tag: never rate such a match.
		w.note(fmt.Sprintf("列 %v が CPU か名前タグ未設定のため、この試合は記録しません。人同士の試合は本体の「名前」で各自の名前を選んでください。", placeholders))
		return
	}
	if len(newcomers) > 0 {
		msg = append(msg, fmt.Sprintf("新しいプレイヤーを登録しました: %s（読み間違いなら `rename <今の名前> <正しい名前>`、既存の人なら `merge <今の名前> <既存の名前>`）", strings.Join(newcomers, ", ")))
	}
	if len(unidentified) > 0 {
		msg = append(msg, fmt.Sprintf("列 %v のプレイヤーを特定できなかったため記録していません（名前タグ未設定の P1/P2 表示や CPU も対象外です）。本人のタグなら `learn <画像ID> <名前>` で教えると次回から自動で判別します。", unidentified))
	}
	if len(unknown) > 0 {
		msg = append(msg, "未知の画像があります。`learn <画像ID> <ラベル>` で教えてください:\n  "+strings.Join(unknown, "\n  "))
	}
	complete := len(in.Entries) >= 2
	for _, e := range in.Entries {
		if e.Rank == 0 {
			complete = false
		}
	}
	if len(unidentified) == 0 && complete {
		m, err := w.core.RecordMatch(in)
		if err != nil {
			msg = append(msg, "自動記録に失敗: "+err.Error())
		} else {
			msg = append(msg, "自動記録しました "+describe(*m))
		}
	} else if len(unidentified) == 0 {
		msg = append(msg, "順位が読めなかったため記録していません。手動で `record` してください。")
	}
	w.note(strings.Join(msg, "\n"))
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

// PendingCrop describes one crop waiting for a label (for the web UI).
type PendingCrop struct {
	ID   string    `json:"id"`
	Kind string    `json:"kind"` // char | rank | name
	Seen time.Time `json:"seen"`
}

// PendingCrops lists the crops waiting for a label, oldest first.
func (w *Watcher) PendingCrops() []PendingCrop {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]PendingCrop, 0, len(w.pending))
	for id, p := range w.pending {
		out = append(out, PendingCrop{ID: id, Kind: p.Kind, Seen: p.Seen})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seen.Before(out[j].Seen) })
	return out
}

// CropPath returns the PNG file of a pending crop.
func (w *Watcher) CropPath(id string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	p, ok := w.pending[id]
	return p.Path, ok
}

// Discard drops a pending crop without learning anything.
func (w *Watcher) Discard(id string) {
	w.mu.Lock()
	p, ok := w.pending[id]
	delete(w.pending, id)
	w.mu.Unlock()
	if ok {
		_ = os.Remove(p.Path)
	}
}

// Event is one notification the watcher produced.
type Event struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// Events returns the most recent notifications, newest first.
func (w *Watcher) Events() []Event {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Event, len(w.events))
	for i, e := range w.events {
		out[len(w.events)-1-i] = e
	}
	return out
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
