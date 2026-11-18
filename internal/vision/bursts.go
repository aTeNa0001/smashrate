package vision

import (
	"fmt"
	"image"
	"image/jpeg"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Every appearance of the results screen is written out frame by frame
// under data/bursts/<time>/ (JPEG, with a log of what each frame read and
// what was finally recorded), so a session can be replayed offline with
// `smashrate vision replay` and every screen verified against what was
// recorded without anyone having to stop on the screen or report a miss.

const (
	burstKeepDirs   = 40  // newest appearances kept
	burstDenseUntil = 60  // every frame is saved up to here ...
	burstEvery      = 5   // ... then every n-th
	burstMaxFrames  = 150 // and no more than this many
)

// burstDir is where this appearance's frames go; "" when bursts are not kept.
func (w *Watcher) burstDir(b *burst) string {
	if w.dataDir == "" {
		return ""
	}
	if b.dir == "" {
		b.dir = filepath.Join(w.dataDir, "bursts", b.start.Format("20060102-150405.000"))
		if err := os.MkdirAll(b.dir, 0o755); err != nil {
			log.Printf("vision: bursts: %v", err)
			b.dir = "-"
		}
		w.pruneBursts()
	}
	if b.dir == "-" {
		return ""
	}
	return b.dir
}

// saveBurstFrame stores frame number n of the appearance and what it read.
func (w *Watcher) saveBurstFrame(b *burst, n int, img image.Image, r *Reading) {
	dir := w.burstDir(b)
	if dir == "" {
		return
	}
	saved := b.saved
	if n > burstDenseUntil && n%burstEvery != 0 || saved >= burstMaxFrames {
		return
	}
	name := fmt.Sprintf("%03d.jpg", n)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return
	}
	err = jpeg.Encode(f, toRGBA(img), &jpeg.Options{Quality: 85})
	f.Close()
	if err != nil {
		return
	}
	b.saved++
	w.burstLog(b, fmt.Sprintf("%s %s", name, r.Key()))
}

// burstLog appends a line to the appearance's log.
func (w *Watcher) burstLog(b *burst, line string) {
	dir := w.burstDir(b)
	if dir == "" {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "burst.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	// wall clock, not w.now(): tests drive the watcher's clock per call
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05.000"), strings.ReplaceAll(line, "\n", " / "))
	f.Close()
}

// pruneBursts keeps only the newest burstKeepDirs appearances.
func (w *Watcher) pruneBursts() {
	dirs, err := filepath.Glob(filepath.Join(w.dataDir, "bursts", "*"))
	if err != nil {
		return
	}
	sort.Strings(dirs)
	for len(dirs) > burstKeepDirs {
		_ = os.RemoveAll(dirs[0])
		dirs = dirs[1:]
	}
}

// BurstFrames lists the frames saved under one appearance's directory in
// order, for replay.
func BurstFrames(dir string) ([]string, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.jpg"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no frames under %s", dir)
	}
	return names, nil
}

// ReplayResult is what replaying an appearance would do.
type ReplayResult struct {
	Frames   int
	Readings map[string]int // reading key -> frames
	Best     *Reading       // the reading that would be recorded
	Start    time.Time
}

// Replay runs the detector over an appearance's saved frames and returns
// the vote the watcher would have taken, without recording anything.
func Replay(det *Detector, dir string) (*ReplayResult, error) {
	names, err := BurstFrames(dir)
	if err != nil {
		return nil, err
	}
	res := &ReplayResult{Readings: map[string]int{}}
	var best *Reading
	bestN := 0
	for _, name := range names {
		img, err := OpenFiles(name).Next()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		res.Frames++
		r, ok := det.Detect(img)
		if !ok {
			res.Readings["(not a results screen)"]++
			continue
		}
		k := r.Key()
		res.Readings[k]++
		if res.Readings[k] >= bestN { // the latest on a tie, as the watcher does
			best, bestN = r, res.Readings[k]
		}
	}
	res.Best = best
	return res, nil
}
