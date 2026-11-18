package vision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
)

// The corpus under testdata/corpus holds real capture frames with what
// they show. Every frame that ever went wrong live is added here, so a
// change to the geometry or the hashes is checked against all of them:
// each frame must be recognised, every placement read, and a player's tag
// must be identified from a template learned on a different frame (and a
// different layout) of the corpus.
type corpusFrame struct {
	File    string `json:"file"`
	Players int    `json:"players"`
	Slots   []struct {
		Port int    `json:"port"`
		Name string `json:"name"`
		CPU  bool   `json:"cpu"`
		Char string `json:"char"`
		Rank int    `json:"rank"`
	} `json:"slots"`
}

func loadCorpus(t *testing.T) []corpusFrame {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "corpus", "corpus.json"))
	if err != nil {
		t.Skip("no corpus:", err)
	}
	var frames []corpusFrame
	if err := json.Unmarshal(raw, &frames); err != nil {
		t.Fatal(err)
	}
	return frames
}

func TestCorpusFramesAreReadCorrectly(t *testing.T) {
	frames := loadCorpus(t)
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cfg := config.Default().Vision // the real geometry, winner shift included
	// Teach the screen, captions and numerals from every frame, but each
	// name tag from the first frame that shows it only: the other frames
	// must identify it from that one template.
	taughtName := map[string]bool{}
	for _, f := range frames {
		img, err := OpenFiles(filepath.Join("testdata", "corpus", f.File)).Next()
		if err != nil {
			t.Fatal(err)
		}
		var chars, ranks, names []string
		for _, s := range f.Slots {
			chars = append(chars, s.Char)
			ranks = append(ranks, itoa(s.Rank))
			if s.Name != "" && !taughtName[s.Name] {
				names = append(names, s.Name)
				taughtName[s.Name] = true
			} else {
				names = append(names, "")
			}
		}
		if err := Teach(c, cfg, img, f.Players, "", chars, ranks, names); err != nil {
			t.Fatalf("%s: teach: %v", f.File, err)
		}
	}
	det, err := NewDetector(cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		img, _ := OpenFiles(filepath.Join("testdata", "corpus", f.File)).Next()
		r, ok := det.Detect(img)
		if !ok {
			t.Errorf("%s: not recognised as a results screen", f.File)
			continue
		}
		if r.Players != f.Players {
			t.Errorf("%s: read as %d players, want %d", f.File, r.Players, f.Players)
			continue
		}
		for i, s := range f.Slots {
			got := r.Slots[i]
			if got.Rank != s.Rank {
				t.Errorf("%s P%d: rank %d, want %d (dist %d)", f.File, s.Port, got.Rank, s.Rank, got.RankDist)
			}
			if got.Char != s.Char {
				t.Errorf("%s P%d: char %q, want %q (dist %d)", f.File, s.Port, got.Char, s.Char, got.CharDist)
			}
			if s.Name != "" && got.Name != s.Name {
				t.Errorf("%s P%d: name %q, want %q (dist %d)", f.File, s.Port, got.Name, s.Name, got.NameDist)
			}
			if s.CPU && got.Name != "" {
				t.Errorf("%s P%d: a CPU tag was identified as %q", f.File, s.Port, got.Name)
			}
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return string(rune('0' + n))
}
