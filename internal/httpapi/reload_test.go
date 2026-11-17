package httpapi

import (
	"image"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/vision"
)

// countingVision is a Vision that only counts template reloads.
type countingVision struct{ reloads int }

func (v *countingVision) LastFrame() image.Image             { return nil }
func (v *countingVision) Status() vision.Status              { return vision.Status{} }
func (v *countingVision) Events() []vision.Event             { return nil }
func (v *countingVision) PendingCrops() []vision.PendingCrop { return nil }
func (v *countingVision) CropPath(string) (string, bool)     { return "", false }
func (v *countingVision) Learn(string, string) error         { return nil }
func (v *countingVision) Discard(string)                     {}
func (v *countingVision) TeachScreen(int, string) error      { return nil }
func (v *countingVision) Reload() error                      { v.reloads++; return nil }

// Renaming or merging a player relabels their name-tag templates in the
// database; the running detector must pick that up, or the next results
// screen recreates the misread name.
func TestRenameAndMergeReloadTheDetector(t *testing.T) {
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cv := &countingVision{}
	s, err := New(c, Options{Vision: cv})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = c.RecordMatch(core.MatchInput{Entries: []core.EntryInput{{Player: "Kinbika", Team: 1, Rank: 1}, {Player: "B", Team: 2, Rank: 2}}})
	_, _ = c.RecordMatch(core.MatchInput{Entries: []core.EntryInput{{Player: "Ginpika", Team: 1, Rank: 1}, {Player: "B", Team: 2, Rank: 2}}})
	post := func(form url.Values) string {
		req := httptest.NewRequest("POST", "/players", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, req)
		return rec.Header().Get("Location")
	}
	if loc := post(url.Values{"action": {"rename"}, "from": {"Kinbika"}, "to": {"Kinpika"}}); !strings.Contains(loc, "done=") {
		t.Fatalf("rename: %s", loc)
	}
	if cv.reloads != 1 {
		t.Fatalf("rename should reload the detector once, got %d", cv.reloads)
	}
	if loc := post(url.Values{"action": {"merge"}, "from": {"Kinpika"}, "to": {"Ginpika"}}); !strings.Contains(loc, "done=") {
		t.Fatalf("merge: %s", loc)
	}
	if cv.reloads != 2 {
		t.Fatalf("merge should reload the detector, got %d", cv.reloads)
	}
	if loc := post(url.Values{"action": {"rename"}, "from": {"nobody"}, "to": {"x"}}); !strings.Contains(loc, "error=") {
		t.Fatalf("bad rename: %s", loc)
	}
	if cv.reloads != 2 {
		t.Fatal("a failed rename must not reload")
	}
}
