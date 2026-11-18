package httpapi

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
)

func TestEvidenceIsServedFromTheDataDir(t *testing.T) {
	c, _ := core.Open(":memory:")
	defer c.Close()
	dir := t.TempDir()
	s, err := New(c, Options{DataDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := c.RecordMatch(core.MatchInput{Entries: []core.EntryInput{{Player: "A", Team: 1, Rank: 1}, {Player: "B", Team: 2, Rank: 2}}})
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	if rec := get("/matches/1/evidence.jpg"); rec.Code != 404 {
		t.Fatalf("no evidence yet: %d", rec.Code)
	}
	_ = os.MkdirAll(filepath.Join(dir, "evidence"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "evidence", "1.jpg"), []byte("jpeg-bytes"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "evidence", "1_thumb.jpg"), []byte("thumb"), 0o644)
	_ = c.SetEvidence(m.ID, "evidence/1.jpg")
	if rec := get("/matches/1/evidence.jpg"); rec.Code != 200 || rec.Body.String() != "jpeg-bytes" {
		t.Fatalf("full: %d %q", rec.Code, rec.Body.String())
	}
	if rec := get("/matches/1/evidence-thumb.jpg"); rec.Code != 200 || rec.Body.String() != "thumb" {
		t.Fatalf("thumb: %d %q", rec.Code, rec.Body.String())
	}
	// a path pointing outside the data dir is never served
	_ = c.SetEvidence(m.ID, "../secret.txt")
	if rec := get("/matches/1/evidence.jpg"); rec.Code != 404 {
		t.Fatalf("outside the data dir: %d", rec.Code)
	}
	if rec := get("/matches"); rec.Code != 200 {
		t.Fatalf("history page: %d", rec.Code)
	}
}
