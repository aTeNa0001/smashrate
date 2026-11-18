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
	b := &burst{readings: map[string]*burstEntry{}}
	var best *burstEntry
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
		e := b.readings[k]
		if e == nil {
			e = &burstEntry{reading: r}
			b.readings[k] = e
			b.order = append(b.order, k)
		}
		e.count++
		e.last = time.Now()
		if best == nil || e.count >= best.count { // the latest on a tie, as the watcher does
			best = e
		}
	}
	if best != nil {
		res.Best = mergeReadings(b, best.reading)
	}
	return res, nil
}

// The ring buffer keeps one frame a second of whatever the board shows,
// the newest ringKeep of them, under data/ring. A results screen that the
// detector does not recognise at all (a layout never seen at this
// resolution) leaves no burst, but it is in here for the next two and a
// half minutes, so it can be fetched and the layout taught from it
// without anyone having to bring the screen up again.
const (
	ringEvery = time.Second
	ringKeep  = 900 // 15 minutes
)

func (w *Watcher) ring(img image.Image, now time.Time) {
	if w.dataDir == "" || now.Sub(w.lastRing) < ringEvery {
		return
	}
	w.lastRing = now
	dir := filepath.Join(w.dataDir, "ring")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.Create(filepath.Join(dir, now.Format("20060102-150405")+".jpg"))
	if err != nil {
		return
	}
	_ = jpeg.Encode(f, toRGBA(img), &jpeg.Options{Quality: 80})
	f.Close()
	w.ringN++
	if w.ringN%20 == 0 {
		names, _ := filepath.Glob(filepath.Join(dir, "*.jpg"))
		sort.Strings(names)
		for len(names) > ringKeep {
			_ = os.Remove(names[0])
			names = names[1:]
		}
	}
}

// keepEvidence stores the results screen a match was read from under
// data/evidence/<match id>.jpg, with a quarter-size thumbnail for the
// history page, and points the match at it. These are never pruned: they
// are the match's proof.
func (w *Watcher) keepEvidence(matchID int64, img image.Image) {
	if w.dataDir == "" {
		return
	}
	dir := filepath.Join(w.dataDir, "evidence")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("vision: evidence: %v", err)
		return
	}
	name := fmt.Sprintf("%d.jpg", matchID)
	full := toRGBA(img).(*image.RGBA)
	if err := writeJPEG(filepath.Join(dir, name), full, 85); err != nil {
		log.Printf("vision: evidence: %v", err)
		return
	}
	if err := writeJPEG(filepath.Join(dir, fmt.Sprintf("%d_thumb.jpg", matchID)), Shrink(full, 4), 80); err != nil {
		log.Printf("vision: evidence thumbnail: %v", err)
	}
	if err := w.core.SetEvidence(matchID, filepath.ToSlash(filepath.Join("evidence", name))); err != nil {
		log.Printf("vision: evidence: %v", err)
	}
}

func writeJPEG(path string, img image.Image, quality int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
}

// Shrink box-filters img down by an integer factor.
func Shrink(img *image.RGBA, factor int) *image.RGBA {
	if factor < 1 {
		factor = 1
	}
	b := img.Bounds()
	w, h := b.Dx()/factor, b.Dy()/factor
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	n := factor * factor
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, bl int
			for dy := 0; dy < factor; dy++ {
				i := img.PixOffset(b.Min.X+x*factor, b.Min.Y+y*factor+dy)
				for dx := 0; dx < factor; dx++ {
					r += int(img.Pix[i])
					g += int(img.Pix[i+1])
					bl += int(img.Pix[i+2])
					i += 4
				}
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}
	return out
}
