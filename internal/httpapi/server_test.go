package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
)

func newTestServer(t *testing.T) (*Server, *core.Core) {
	t.Helper()
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return s, c
}

func TestAPIRecordAndPages(t *testing.T) {
	s, _ := newTestServer(t)
	body := `{"entries":[{"player":"Taro","character":"マリオ","team":1,"rank":1},{"player":"Hanako","character":"link","team":2,"rank":2}]}`
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/matches", strings.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("record: %d %s", rec.Code, rec.Body)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/leaderboard", nil))
	var lb []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &lb); err != nil || len(lb) != 2 || lb[0]["name"] != "Taro" {
		t.Fatalf("leaderboard: %s", rec.Body)
	}

	for _, path := range []string{"/", "/players/Taro", "/matches", "/record", "/balance", "/ports", "/api/players/Hanako", "/api/characters"} {
		rec = httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		if strings.HasPrefix(path, "/players/") && !strings.Contains(rec.Body.String(), "マリオ") {
			t.Fatalf("player page should list the character: %s", rec.Body)
		}
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/players/nobody", nil))
	if rec.Code != 404 {
		t.Fatalf("missing player: %d", rec.Code)
	}
}

func TestAPIBalanceAndPorts(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/balance", strings.NewReader(`{"members":["A","B","C","D"],"teams":2}`)))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "WinProb") {
		t.Fatalf("balance: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("PUT", "/api/ports/1", strings.NewReader(`{"player":"A"}`)))
	if rec.Code != 200 {
		t.Fatalf("set port: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/api/ports", nil))
	if !strings.Contains(rec.Body.String(), `"1":"A"`) {
		t.Fatalf("ports: %s", rec.Body)
	}
}

func TestFormRecord(t *testing.T) {
	s, c := newTestServer(t)
	form := "p1_name=A&p1_char=&p1_team=1&p1_rank=1&p2_name=B&p2_team=2&p2_rank=2&p3_name=C&p3_team=3&p3_rank=3"
	req := httptest.NewRequest("POST", "/record", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("form record: %d %s", rec.Code, rec.Body)
	}
	ms, _ := c.Matches(1, 0)
	if len(ms) != 1 || ms[0].Mode != core.ModeFFA {
		t.Fatalf("matches: %+v", ms)
	}
}
