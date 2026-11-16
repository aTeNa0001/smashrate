package vision

import (
	"context"
	"errors"
	"image"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aTeNa0001/smashrate/internal/core"
)

// setupBurst teaches one 1v1 screen and returns a watcher with a fake clock
// that advances `step` per processed frame.
func setupBurst(t *testing.T, step time.Duration) (*core.Core, *Watcher, *image.RGBA, *[]string) {
	t.Helper()
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := testVisionCfg()
	cfg.CooldownSeconds = 60
	cfg.SettleSeconds = 1.5
	f1 := frame(cfg, 2, []int64{11, 12}, []int64{21, 22}, 41, 42)
	if err := Teach(c, cfg, f1, 2, "", []string{"mario", "link"}, []string{"1", "2"}, []string{"Taro", "Hanako"}); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	w, err := NewWatcher(cfg, c, t.TempDir(), func(s string) { msgs = append(msgs, s) })
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	w.now = func() time.Time { clock = clock.Add(step); return clock }
	return c, w, f1, &msgs
}

func noise() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 640, 360))
	paint(img, img.Bounds(), 4242)
	return img
}

func TestBurstRecordsAScreenShownForOneFrame(t *testing.T) {
	c, w, f1, msgs := setupBurst(t, 250*time.Millisecond)
	w.Process(noise())
	w.Process(f1) // visible for a single frame
	if ms, _ := c.Matches(10, 0); len(ms) != 0 {
		t.Fatal("nothing should be recorded while the screen may still be animating")
	}
	w.Process(noise()) // gone: record now
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 || ms[0].Entries[0].PlayerName != "Taro" {
		t.Fatalf("expected the single-frame screen to be recorded: %+v msgs=%v", ms, *msgs)
	}
	st := w.Status()
	if st.LastScreenFrames != 1 || st.LastScreenMs <= 0 {
		t.Fatalf("status should report how long the screen was visible: %+v", st)
	}
	// the same screen still being shown (or shown again within the cooldown)
	// is not recorded twice
	w.Process(f1)
	w.Process(f1)
	w.Process(noise())
	if ms, _ := c.Matches(10, 0); len(ms) != 1 {
		t.Fatalf("cooldown should suppress the repeat, got %d", len(ms))
	}
}

func TestBurstPicksTheSettledReadingAndSettles(t *testing.T) {
	c, w, f1, _ := setupBurst(t, 250*time.Millisecond)
	// while the panels animate in, the numerals read differently for a
	// frame, then the settled screen is shown for several frames
	animating := frame(w.cfg, 2, []int64{11, 12}, []int64{22, 21}, 41, 42) // ranks swapped
	w.Process(animating)
	w.Process(f1)
	w.Process(f1)
	if ms, _ := c.Matches(10, 0); len(ms) != 0 {
		t.Fatal("too early")
	}
	w.Process(f1) // 1.5 s since the burst started: settle and record
	w.Process(f1)
	w.Process(f1)
	w.Process(f1)
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("expected 1 match after settling, got %d", len(ms))
	}
	if ms[0].Entries[0].PlayerName != "Taro" || ms[0].Entries[0].Rank != 1 {
		t.Fatalf("the most frequent reading should win: %+v", ms[0].Entries)
	}
}

// A screen that stays up long after settling is recorded exactly once,
// even if its reading changes meanwhile (a name tag learned while it is
// up changes the reading's key), and the status page reports the whole
// time it was visible.
func TestBurstRecordsALongScreenOnce(t *testing.T) {
	c, w, f1, _ := setupBurst(t, 250*time.Millisecond)
	other := frame(w.cfg, 2, []int64{11, 12}, []int64{22, 21}, 41, 42) // a different reading
	for i := 0; i < 8; i++ {                                           // 2 s: settles and records after 1.5 s
		w.Process(f1)
	}
	for i := 0; i < 8; i++ { // the reading changes while the screen is still up
		w.Process(other)
	}
	w.Process(noise()) // gone
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("one appearance of the screen must give one match, got %d", len(ms))
	}
	if st := w.Status(); st.LastScreenFrames != 16 || st.LastScreenMs < 3500 {
		t.Fatalf("status should cover the whole appearance: %+v", st)
	}
}

func TestBurstHonoursStableFrames(t *testing.T) {
	c, w, f1, msgs := setupBurst(t, 250*time.Millisecond)
	w.cfg.StableFrames = 2
	w.Process(f1)
	w.Process(noise())
	if ms, _ := c.Matches(10, 0); len(ms) != 0 {
		t.Fatal("one frame is below stable_frames = 2")
	}
	if len(*msgs) != 1 || !strings.Contains((*msgs)[0], "stable_frames") {
		t.Fatalf("expected an explanatory note, got %v", *msgs)
	}
}

// seqSource replays frames; each Next advances the fake clock through now().
type seqSource struct {
	frames []image.Image
	i      int
	closed bool
	mu     *sync.Mutex
}

func (s *seqSource) Next() (image.Image, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.i >= len(s.frames) {
		return nil, io.EOF
	}
	img := s.frames[s.i]
	s.i++
	return img, nil
}

func (s *seqSource) Close() error { s.closed = true; return nil }

func TestRunLoopIdlesAndResumes(t *testing.T) {
	c, w, f1, _ := setupBurst(t, time.Second)
	w.cfg.IdleAfterSeconds = 3
	w.cfg.IdlePollSeconds = 1
	static := noise()
	var mu sync.Mutex
	opens := 0
	var sources []*seqSource
	open := func() (FrameSource, error) {
		mu.Lock()
		defer mu.Unlock()
		opens++
		var fr []image.Image
		switch opens {
		case 1: // live, then the picture freezes long enough to idle
			fr = []image.Image{static, static, static, static, static, static}
		case 2: // probe: still the same picture
			fr = []image.Image{static, static, static, static, static, static}
		default: // probe: the Switch is back on, a results screen appears
			fr = []image.Image{f1, f1, static, static, static, static, static, static, static}
		}
		s := &seqSource{frames: fr, mu: &mu}
		sources = append(sources, s)
		return s, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { w.RunLoop(ctx, open); close(done) }()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ms, _ := c.Matches(10, 0); len(ms) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	ms, _ := c.Matches(10, 0)
	if len(ms) != 1 {
		t.Fatalf("the screen shown after resuming should be recorded, got %d matches", len(ms))
	}
	mu.Lock()
	n := opens
	mu.Unlock()
	if n < 3 {
		t.Fatalf("expected the board to be probed at least twice after idling, opens=%d", n)
	}
	if !sources[0].closed || !sources[1].closed {
		t.Fatal("idling must close ffmpeg")
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("ctx")
	}
}
