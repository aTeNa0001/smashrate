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
	"strconv"
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
	burst     *burst  // readings collected while the results screen is visible
	lastSeen  string
	cooldown  time.Time
	// picture-change tracking for idling
	lastHash   uint64
	lastChange time.Time
	now        func() time.Time // tests
}

// burst is one appearance of the results screen: every reading made while
// it was visible, so that the most frequent one is recorded even when the
// screen was shown for a single frame or the panels were still animating.
type burst struct {
	start, last time.Time
	frames      int
	readings    map[string]*burstEntry
	order       []string
	// recorded: the screen has already been recorded during this
	// appearance (after SettleSeconds); readings keep being counted for
	// the status page but nothing more is recorded until the screen goes
	// away and comes back.
	recorded bool
	// dir is where this appearance's frames are being saved (see bursts.go)
	dir   string
	saved int
}

type burstEntry struct {
	reading *Reading
	img     image.Image
	// samples are a few frames of this reading (the first and the latest
	// two): a sparkle or a panel still animating on one frame must not
	// decide what OCR reads from a tag seen for the first time
	samples []image.Image
	count   int
	last    time.Time
}

// changedBits is how many bits of the whole-frame dHash must differ for the
// picture to count as changed, both for entering idle and for leaving it.
const changedBits = 6

// goneGrace is how long the results screen may fail to be recognised before
// its appearance is considered over. A single frame that misses the
// thresholds (a panel mid-animation, a dropped line from the board) used to
// split one appearance into two bursts, and the second was recorded again
// because the reading had changed meanwhile (a caption learned by OCR in
// between).
const goneGrace = 500 * time.Millisecond

// errIdle is returned by runUntilIdle when the picture stopped changing.
var errIdle = errors.New("picture unchanged")

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
	return &Watcher{cfg: cfg, core: c, det: det, dataDir: dataDir, notify: notify, ocr: eng, pending: map[string]pendingCrop{}, now: time.Now}, nil
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
func (w *Watcher) readTag(crops []image.Image, hash uint64) (name string, created, placeholder bool) {
	if w.ocr == nil || len(crops) == 0 {
		return "", false, false
	}
	players, _ := w.core.Players()
	names := make([]string, 0, len(players))
	for _, p := range players {
		names = append(names, p.Name)
	}
	// Every frame, smooth enlargement first (best for kana), then the
	// isolated ink; the first reading that is a placeholder or matches a
	// known player wins. Otherwise the longest reading becomes a new player:
	// a sparkle crossing a glyph on one frame loses that glyph, never adds
	// one.
	first := ""
	for _, crop := range crops {
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
			if len([]rune(clean)) > len([]rune(first)) {
				first = clean
			}
			break // one reading per frame is enough to compare lengths
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

// LastFrame returns the most recent frame seen (nil before the first) as an
// RGBA image ready for PNG encoding.
func (w *Watcher) LastFrame() image.Image {
	w.mu.Lock()
	img := w.lastFrame
	w.mu.Unlock()
	return toRGBA(img) // outside the lock: a 1080p conversion takes ~10 ms
}

// Status is a snapshot of what the watcher has been doing, for /status.
type Status struct {
	Frames          int64     `json:"frames"`
	LastFrameAt     time.Time `json:"last_frame_at"`
	Detections      int64     `json:"detections"`
	LastDetectionAt time.Time `json:"last_detection_at"`
	// LastScreenFrames / LastScreenMs: how long the results screen was
	// visible the last time it appeared (frames seen, milliseconds).
	LastScreenFrames int `json:"last_screen_frames"`
	LastScreenMs     int `json:"last_screen_ms"`
	// Idle: ffmpeg is stopped because the picture has not changed; the board
	// is probed periodically.
	Idle          bool           `json:"idle"`
	IdleSince     time.Time      `json:"idle_since"`
	LastMessage   string         `json:"last_message"`
	LastMessageAt time.Time      `json:"last_message_at"`
	Pending       []string       `json:"pending"`
	Templates     map[string]int `json:"templates"`
	OCR           string         `json:"ocr"`
}

// ScreenTemplates is how many results-screen templates are known (0 = the
// watcher cannot recognise anything yet).
func (s Status) ScreenTemplates() int { return s.Templates[KindScreen] }

// ScreenSeconds is LastScreenMs in seconds, for templates.
func (s Status) ScreenSeconds() float64 { return float64(s.LastScreenMs) / 1000 }

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
	} else if s.Idle {
		fmt.Fprintf(&sb, "キャプチャ: 待機中（%s から映像に変化なし。定期的に確認し、変化があれば再開）", s.IdleSince.Local().Format("15:04"))
	} else {
		fmt.Fprintf(&sb, "キャプチャ: 受信中（最後のフレーム %s、%d 枚）", s.LastFrameAt.Local().Format("15:04:05"), s.Frames)
	}
	if s.LastScreenFrames > 0 {
		fmt.Fprintf(&sb, "\nリザルト画面が前回見えていた時間: %.1f 秒（%d フレーム）", float64(s.LastScreenMs)/1000, s.LastScreenFrames)
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

// Run consumes frames until ctx ends or the source is exhausted. It never
// idles; see RunLoop for the capture-board loop used by `serve`.
func (w *Watcher) Run(ctx context.Context, src FrameSource) error {
	w.warnUntaught()
	err := w.consume(ctx, src, false)
	w.Flush()
	return err
}

func (w *Watcher) warnUntaught() {
	if w.det.TemplateCount(KindScreen) == 0 {
		w.notify("リザルト画面のテンプレートが未登録です。リザルト画面のスクショで `smashrate vision teach -image shot.png -players N` を一度実行してください（キャラ・順位・名前は以後自動で学習します）。")
	}
}

// consume processes frames from src until it ends, ctx is cancelled or, when
// idling is enabled, the picture has not changed for IdleAfterSeconds
// (errIdle). The source is not closed.
func (w *Watcher) consume(ctx context.Context, src FrameSource, idling bool) error {
	idleAfter := time.Duration(w.cfg.IdleAfterSeconds) * time.Second
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
		if idling && idleAfter > 0 && w.burst == nil && w.now().Sub(w.lastChange) >= idleAfter {
			return errIdle
		}
	}
}

// RunLoop keeps the capture board open through `serve`: it reopens the
// source when it fails, and when the picture has not changed for
// IdleAfterSeconds it closes ffmpeg (the decoding costs CPU and heat even
// when the Switch is off) and only probes the board for a few seconds every
// IdlePollSeconds until something changes.
func (w *Watcher) RunLoop(ctx context.Context, open func() (FrameSource, error)) {
	w.warnUntaught()
	retry := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(10 * time.Second):
			return true
		}
	}
	var src FrameSource
	for ctx.Err() == nil {
		if src == nil {
			s, err := open()
			if err != nil {
				log.Printf("vision: %v (retrying in 10s)", err)
				if !retry() {
					return
				}
				continue
			}
			src = s
			w.lastChange = w.now()
		}
		err := w.consume(ctx, src, true)
		src.Close()
		src = nil
		w.Flush()
		if ctx.Err() != nil {
			return
		}
		if !errors.Is(err, errIdle) {
			log.Printf("vision: source ended: %v (restarting in 10s)", err)
			if !retry() {
				return
			}
			continue
		}
		src = w.idle(ctx, open)
	}
}

// idle waits for the picture to change. It returns an open source positioned
// on live video, or nil when ctx ended.
func (w *Watcher) idle(ctx context.Context, open func() (FrameSource, error)) FrameSource {
	ref := w.lastHash
	w.mu.Lock()
	w.stats.Idle, w.stats.IdleSince = true, w.now()
	w.mu.Unlock()
	log.Printf("vision: picture unchanged for %ds; capture paused, probing every %ds", w.cfg.IdleAfterSeconds, w.pollSeconds())
	defer func() {
		w.mu.Lock()
		w.stats.Idle = false
		w.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Duration(w.pollSeconds()) * time.Second):
		}
		src, err := open()
		if err != nil {
			log.Printf("vision: probe: %v", err)
			continue
		}
		// The board replays its logo for a couple of seconds after every
		// open, so the decision is made on the last frame of a few-second
		// window, not on the first frame that differs (the logo always does).
		// Every probe frame still goes through detection, so a results
		// screen that happens to be up during the probe is recorded too.
		deadline := w.now().Add(4 * time.Second)
		w.mu.Lock()
		detections := w.stats.Detections
		w.mu.Unlock()
		live, got := false, false
		for w.now().Before(deadline) && ctx.Err() == nil {
			img, err := src.Next()
			if err != nil {
				break
			}
			got = true
			w.Process(img)
			// same threshold as the one that declared the picture static
			live = Hamming(DHash(img, img.Bounds()), ref) > changedBits
		}
		w.Flush()
		w.mu.Lock()
		seenScreen := w.stats.Detections > detections
		w.mu.Unlock()
		if got && (live || seenScreen) {
			log.Printf("vision: picture changed; capture resumed")
			w.lastChange = w.now()
			return src
		}
		src.Close()
	}
}

func (w *Watcher) pollSeconds() int {
	if w.cfg.IdlePollSeconds > 0 {
		return w.cfg.IdlePollSeconds
	}
	return 30
}

// Process handles one frame. Readings of the results screen are collected
// into a burst; the burst is recorded when the screen disappears or has
// been visible for SettleSeconds (see Flush). Exported for tests.
func (w *Watcher) Process(img image.Image) {
	now := w.now()
	h := DHash(img, img.Bounds())
	w.mu.Lock()
	w.lastFrame = img
	w.stats.Frames++
	w.stats.LastFrameAt = now
	w.mu.Unlock()
	if w.lastChange.IsZero() || Hamming(h, w.lastHash) > changedBits {
		w.lastHash, w.lastChange = h, now
	}
	reading, ok := w.det.Detect(img)
	if !ok {
		// the screen is gone: record what was collected, unless it was seen
		// a moment ago (one unrecognised frame does not end an appearance)
		if b := w.burst; b == nil || now.Sub(b.last) >= goneGrace {
			w.Flush()
		}
		return
	}
	w.mu.Lock()
	w.stats.Detections++
	w.stats.LastDetectionAt = now
	w.mu.Unlock()
	if w.burst == nil {
		w.burst = &burst{start: now, readings: map[string]*burstEntry{}}
	}
	b := w.burst
	b.last = now
	b.frames++
	key := reading.Key()
	e := b.readings[key]
	if e == nil {
		e = &burstEntry{}
		b.readings[key] = e
		b.order = append(b.order, key)
	}
	w.saveBurstFrame(b, b.frames, img, reading)
	e.reading, e.img, e.last = reading, img, now
	if len(e.samples) < 3 {
		e.samples = append(e.samples, img)
	} else {
		e.samples[1], e.samples[2] = e.samples[2], img
	}
	e.count++
	settle := time.Duration(w.cfg.SettleSeconds * float64(time.Second))
	if settle <= 0 {
		settle = 1500 * time.Millisecond
	}
	if !b.recorded && now.Sub(b.start) >= settle {
		// the panels have settled: record now rather than waiting for the
		// screen to go away (which may take as long as the players like)
		w.record(b)
		b.recorded = true
	}
}

// Flush ends the current appearance of the results screen: the status page
// gets how long it was visible, and it is recorded unless the settle
// timer already did that. Exported for tests.
func (w *Watcher) Flush() {
	b := w.burst
	if b == nil {
		return
	}
	w.burst = nil
	ms := int(b.last.Sub(b.start)/time.Millisecond) + int(1000/w.frameRate())
	w.mu.Lock()
	w.stats.LastScreenFrames, w.stats.LastScreenMs = b.frames, ms
	w.mu.Unlock()
	if !b.recorded {
		w.record(b)
	}
}

// record stores the burst's most frequent reading (the latest on a tie),
// provided it was seen at least StableFrames times and the same screen was
// not recorded a moment ago.
func (w *Watcher) record(b *burst) {
	var best *burstEntry
	for _, k := range b.order {
		e := b.readings[k]
		if best == nil || e.count > best.count || (e.count == best.count && e.last.After(best.last)) {
			best = e
		}
	}
	if best == nil {
		return
	}
	need := w.cfg.StableFrames
	if need <= 0 {
		need = 1
	}
	defer func() {
		w.burstLog(b, fmt.Sprintf("vote: %d frames, %d readings, best seen %d times", b.frames, len(b.readings), best.count))
	}()
	if best.count < need {
		ms := int(b.last.Sub(b.start)/time.Millisecond) + int(1000/w.frameRate())
		w.note(fmt.Sprintf("リザルト画面が %.1f 秒（%d フレーム）だけ見えましたが、同じ読み取りが %d 回未満だったので記録しませんでした。smashrate.toml の stable_frames を 1 にすると 1 フレームでも記録します。", float64(ms)/1000, b.frames, need))
		return
	}
	w.handle(best.img, best.samples, best.reading)
	w.mu.Lock()
	last := w.stats.LastMessage
	w.mu.Unlock()
	w.burstLog(b, "result: "+last)
}

// repeat reports whether the same result (players and placements) was
// recorded within CooldownSeconds, and otherwise remembers it. It is
// checked on the resolved entries, not on the detector's raw reading: that
// reading changes while the screen is up (a caption or tag learned by OCR
// fills in), and two bursts of one screen must still count as one match.
func (w *Watcher) repeat(in core.MatchInput) bool {
	parts := make([]string, 0, len(in.Entries))
	for _, e := range in.Entries {
		parts = append(parts, fmt.Sprintf("%s=%d", e.Player, e.Rank))
	}
	sort.Strings(parts)
	key := strings.Join(parts, ";")
	now := w.now()
	cd := time.Duration(w.cfg.CooldownSeconds) * time.Second
	if cd <= 0 {
		cd = 60 * time.Second
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if now.Before(w.cooldown) && key == w.lastSeen {
		return true
	}
	w.cooldown, w.lastSeen = now.Add(cd), key
	return false
}

func (w *Watcher) frameRate() float64 {
	if w.cfg.FPS > 0 {
		return w.cfg.FPS
	}
	return 4
}

func (w *Watcher) handle(img image.Image, samples []image.Image, r *Reading) {
	w.keepFrame(img)
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
		// Regions as the detector used them (shifted for the winner's panel).
		nameRect, charRect := Rect(b, s.NameRegion), Rect(b, s.CharRegion)
		// The name tag identifies the human: learned hash first, OCR for a
		// tag never seen before, manual labelling as the last resort.
		name, placeholder := s.Name, s.Placeholder
		if name == "" && !placeholder {
			var created bool
			name, created, placeholder = w.readTag(tagCrops(img, samples, layout, i, s.Winner), s.NameHash)
			if created {
				newcomers = append(newcomers, name)
			}
		}
		cpu := false
		if placeholder {
			// a CPU (or a human without a name tag): kept in the record as
			// the CPU pseudo-player with its character and placement; the
			// match is then not rated
			placeholders = append(placeholders, s.Port)
			name, cpu = core.CPUName, true
		} else if name == "" {
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
			_, _, rankRegion := SlotRegions(img, layout, i, false)
			id := w.stash(KindRank, s.RankHash, img, Rect(b, rankRegion))
			unknown = append(unknown, fmt.Sprintf("P%d 順位 → %s", s.Port, id))
		}
		if s.Inferred && s.RankHash != 0 {
			// the numeral was unread but its placement followed from the
			// others: remember the shape so it is read directly next time
			w.learn(KindRank, strconv.Itoa(rank), s.RankHash)
		}
		in.Entries = append(in.Entries, core.EntryInput{Player: name, Port: s.Port, Team: s.Port, Rank: rank, Character: char, CPU: cpu})
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
		// CPU fighters or humans without a name tag: the match is kept for
		// the record (see core.Rated) but moves nobody's rating
		msg = append(msg, fmt.Sprintf("列 %v は CPU か名前タグ未設定なので、この試合はレート対象外として記録します。人同士の試合は本体の「名前」で各自の名前を選んでください。", placeholders))
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
		if w.repeat(in) {
			return // the same screen, recorded a moment ago
		}
		m, err := w.core.RecordMatch(in)
		if err != nil {
			msg = append(msg, "自動記録に失敗: "+err.Error())
		} else {
			if m.Rated {
				msg = append(msg, "自動記録しました "+describe(*m))
			} else {
				msg = append(msg, "CPU 戦として記録しました（レート対象外） "+describe(*m))
			}
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
	w.forgetLastSeen()
	return w.det.Reload(w.core)
}

func (w *Watcher) forgetLastSeen() {
	w.mu.Lock()
	w.lastSeen = ""
	w.mu.Unlock()
}

// Reload refreshes templates (after `vision teach`).
func (w *Watcher) Reload() error { return w.det.Reload(w.core) }

// TeachScreen learns the frame on screen right now as the results screen
// for the given player count (the web UI's "this is a results screen"
// button), keeps a copy under data/screens and reloads the templates.
func (w *Watcher) TeachScreen(players int, mode string) error {
	img := w.LastFrame()
	if img == nil {
		return errors.New("まだ映像が来ていない")
	}
	if err := Teach(w.core, w.cfg, img, players, mode, nil, nil, nil); err != nil {
		return err
	}
	dir := filepath.Join(w.dataDir, "screens")
	_ = os.MkdirAll(dir, 0o755)
	name := fmt.Sprintf("results-%d%s-%s.png", players, map[bool]string{true: "-team", false: ""}[mode == "team"], w.now().Format("20060102-150405"))
	if err := SavePNG(filepath.Join(dir, name), img); err != nil {
		log.Printf("vision: save screen: %v", err)
	}
	w.forgetLastSeen()
	return w.det.Reload(w.core)
}

// keepFrame saves the frame a results screen was read from under
// data/frames (the newest 40 are kept), so a wrong reading can be looked
// at afterwards without asking anyone to stop on the screen again.
func (w *Watcher) keepFrame(img image.Image) {
	dir := filepath.Join(w.dataDir, "frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	if err := SavePNG(filepath.Join(dir, w.now().Format("20060102-150405")+".png"), img); err != nil {
		log.Printf("vision: keep frame: %v", err)
		return
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil {
		return
	}
	sort.Strings(names)
	for len(names) > 40 {
		_ = os.Remove(names[0])
		names = names[1:]
	}
}

// tagCrops cuts slot i's name tag out of the frame the reading was taken
// from and out of the other sampled frames of the same screen, each with
// the regions as they sit in that frame (the headers move while the screen
// animates in). The reading's own frame comes first.
func tagCrops(img image.Image, samples []image.Image, layout config.Layout, i int, winner bool) []image.Image {
	out := []image.Image{}
	seen := false
	for _, f := range append([]image.Image{img}, samples...) {
		if f == nil || (seen && f == img) {
			continue
		}
		if f == img {
			seen = true
		}
		_, name, _ := SlotRegions(f, layout, i, winner)
		out = append(out, Crop(f, Rect(f.Bounds(), name)))
	}
	return out
}
