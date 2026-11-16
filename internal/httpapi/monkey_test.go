package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
)

// TestMonkey throws a long random sequence of valid, invalid and garbage
// requests at the whole web surface and checks that the server never
// returns a 5xx, never panics, and that the data stays consistent: the
// leaderboard is sorted, games/wins add up to the stored matches, ratings
// are finite, and a full recompute reproduces exactly the same numbers.
//
// The sequence is seeded, so a failure prints the seed to replay it:
//
//	SMASHRATE_MONKEY_SEED=12345 SMASHRATE_MONKEY_OPS=2000 go test ./internal/httpapi -run Monkey
func TestMonkey(t *testing.T) {
	seed := int64(1)
	if s := os.Getenv("SMASHRATE_MONKEY_SEED"); s != "" {
		seed, _ = strconv.ParseInt(s, 10, 64)
	}
	ops := 400
	if s := os.Getenv("SMASHRATE_MONKEY_OPS"); s != "" {
		ops, _ = strconv.Atoi(s)
	}
	rng := rand.New(rand.NewSource(seed))
	s, c := newTestServer(t)
	m := &monkey{t: t, s: s, c: c, rng: rng, seed: seed}

	names := []string{"Taro", "Hanako", "ぎんぴか", "ゆげ", "Ａｂｃ", "x", "Åsa", "名無し", "Kirby ", "zeta"}
	chars := []string{"", "mario", "マリオ", "link", "min_min", "samus"}

	every := 50
	if s := os.Getenv("SMASHRATE_MONKEY_CHECK_EVERY"); s != "" {
		every, _ = strconv.Atoi(s)
	}
	for i := 0; i < ops; i++ {
		op := rng.Intn(14)
		m.op = fmt.Sprintf("op %d (case %d)", i, op)
		switch op {
		case 0, 1, 2, 3: // a valid match through the JSON API
			m.post("/api/matches", "application/json", m.validMatchJSON(names, chars), 201)
		case 4: // a valid match through the HTML form
			m.postForm("/record", m.validMatchForm(names, chars), http.StatusSeeOther)
		case 5: // an invalid match: duplicate player, no winner, one player, bad JSON
			m.post("/api/matches", "application/json", m.invalidMatchJSON(names), 400)
		case 6: // random garbage bodies must be rejected, not crash
			body := make([]byte, rng.Intn(64))
			rng.Read(body)
			m.post("/api/matches", "application/json", string(body), 400)
			m.postForm("/record", url.Values{"p1_name": {string(body)}}, 0)
		case 7: // edit an existing match (sometimes to an invalid one)
			if id := m.someMatchID(); id != 0 {
				if rng.Intn(3) == 0 {
					m.put(fmt.Sprintf("/api/matches/%d", id), m.invalidMatchJSON(names), 400)
				} else {
					m.put(fmt.Sprintf("/api/matches/%d", id), m.validMatchJSON(names, chars), 200)
				}
			}
			m.put(fmt.Sprintf("/api/matches/%d", rng.Int63n(1<<40)), m.validMatchJSON(names, chars), 0)
		case 8: // delete something, existing or not
			if id := m.someMatchID(); id != 0 && rng.Intn(2) == 0 {
				m.del(fmt.Sprintf("/api/matches/%d", id), 200)
			} else {
				m.postForm(fmt.Sprintf("/matches/%d/delete", rng.Int63n(1<<40)), nil, 0)
			}
		case 9: // rename / merge players
			from, to := names[rng.Intn(len(names))], names[rng.Intn(len(names))]
			action := "rename"
			if rng.Intn(2) == 0 {
				action = "merge"
			}
			m.postForm("/players", url.Values{"action": {action}, "from": {from}, "to": {to}}, 0)
			// keep the pool usable: rename back what may have moved
			m.postForm("/players", url.Values{"action": {"rename"}, "from": {to}, "to": {from}}, 0)
		case 10: // team balancing
			k := 2 + rng.Intn(3)
			var members []string
			for _, n := range names {
				if rng.Intn(2) == 0 {
					members = append(members, n)
				}
			}
			body, _ := json.Marshal(map[string]any{"members": members, "teams": k, "allow_uneven": rng.Intn(2) == 0})
			m.post("/api/balance", "application/json", string(body), 0)
			v := url.Values{"teams": {strconv.Itoa(k)}, "members": members, "extra": {"a,b c"}}
			m.postForm("/balance", v, 0)
		default: // read everything
			for _, p := range []string{"/", "/matches", "/matches?page=1", "/matches?page=-3", "/record", "/balance", "/status", "/learn", "/players",
				"/api/leaderboard", "/api/leaderboard?scope=mode:ffa", "/api/leaderboard?scope=char:mario", "/api/matches", "/api/characters", "/api/status",
				"/players/" + url.PathEscape(names[rng.Intn(len(names))]), "/api/players/" + url.PathEscape(names[rng.Intn(len(names))]),
				"/players/nobody-" + strconv.Itoa(rng.Int()), "/matches/0/edit", "/no-such-page", "/api/vision/snapshot"} {
				m.get(p)
			}
			if id := m.someMatchID(); id != 0 {
				m.get(fmt.Sprintf("/matches/%d/edit", id))
			}
		}
		if every > 0 && i%every == every-1 {
			m.checkInvariants()
		}
	}
	m.checkInvariants()
}

type monkey struct {
	t    *testing.T
	s    *Server
	c    *core.Core
	rng  *rand.Rand
	seed int64
	n    int
	op   string // the operation in progress, for failure messages
}

func (m *monkey) fail(format string, args ...any) {
	m.t.Helper()
	m.t.Fatalf("seed %d, request #%d, %s: %s", m.seed, m.n, m.op, fmt.Sprintf(format, args...))
}

// do sends a request and fails on a 5xx or, when want != 0, on any other status.
func (m *monkey) do(req *http.Request, want int) *httptest.ResponseRecorder {
	m.t.Helper()
	m.n++
	rec := httptest.NewRecorder()
	func() {
		defer func() {
			if r := recover(); r != nil {
				m.fail("%s %s panicked: %v", req.Method, req.URL, r)
			}
		}()
		m.s.ServeHTTP(rec, req)
	}()
	if rec.Code >= 500 {
		m.fail("%s %s -> %d: %s", req.Method, req.URL, rec.Code, rec.Body.String())
	}
	if want != 0 && rec.Code != want {
		m.fail("%s %s -> %d, want %d: %s", req.Method, req.URL, rec.Code, want, rec.Body.String())
	}
	return rec
}

func (m *monkey) get(path string) *httptest.ResponseRecorder {
	return m.do(httptest.NewRequest("GET", path, nil), 0)
}

func (m *monkey) post(path, ctype, body string, want int) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	return m.do(req, want)
}

func (m *monkey) put(path, body string, want int) *httptest.ResponseRecorder {
	req := httptest.NewRequest("PUT", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return m.do(req, want)
}

func (m *monkey) del(path string, want int) *httptest.ResponseRecorder {
	return m.do(httptest.NewRequest("DELETE", path, nil), want)
}

func (m *monkey) postForm(path string, v url.Values, want int) *httptest.ResponseRecorder {
	return m.post(path, "application/x-www-form-urlencoded", v.Encode(), want)
}

func (m *monkey) someMatchID() int64 {
	ms, _ := m.c.Matches(20, 0)
	if len(ms) == 0 {
		return 0
	}
	return ms[m.rng.Intn(len(ms))].ID
}

// randomEntries builds a valid line-up: 2-8 distinct players, 1on1,
// free-for-all or teams, with ties allowed.
func (m *monkey) randomEntries(names, chars []string) []map[string]any {
	n := 2 + m.rng.Intn(7)
	perm := m.rng.Perm(len(names))[:n]
	var teams []int
	switch m.rng.Intn(3) {
	case 0: // everyone alone
		for i := 0; i < n; i++ {
			teams = append(teams, i+1)
		}
	default: // 2-4 teams, players spread over them, every team non-empty
		k := 2 + m.rng.Intn(3)
		if k > n {
			k = n
		}
		for i := 0; i < n; i++ {
			teams = append(teams, i%k+1)
		}
		m.rng.Shuffle(n, func(i, j int) { teams[i], teams[j] = teams[j], teams[i] })
	}
	// ranks per team: a permutation with optional ties, team 1 always has a rank-1
	maxTeam := 0
	for _, t := range teams {
		if t > maxTeam {
			maxTeam = t
		}
	}
	rankOf := make([]int, maxTeam+1)
	order := m.rng.Perm(maxTeam)
	r := 1
	for i, t := range order {
		rankOf[t+1] = r
		if i == maxTeam-1 || m.rng.Intn(3) != 0 {
			r++
		}
	}
	var out []map[string]any
	for i := 0; i < n; i++ {
		e := map[string]any{"player": names[perm[i]], "team": teams[i], "rank": rankOf[teams[i]]}
		if ch := chars[m.rng.Intn(len(chars))]; ch != "" {
			e["character"] = ch
		}
		if m.rng.Intn(2) == 0 {
			e["port"] = 1 + m.rng.Intn(4)
		}
		if m.rng.Intn(3) == 0 {
			e["stocks"] = m.rng.Intn(4)
		}
		out = append(out, e)
	}
	return out
}

func (m *monkey) validMatchJSON(names, chars []string) string {
	b, _ := json.Marshal(map[string]any{"entries": m.randomEntries(names, chars), "note": strings.Repeat("ん", m.rng.Intn(20))})
	return string(b)
}

func (m *monkey) validMatchForm(names, chars []string) url.Values {
	v := url.Values{}
	for i, e := range m.randomEntries(names, chars) {
		p := fmt.Sprintf("p%d_", i+1)
		v.Set(p+"name", e["player"].(string))
		v.Set(p+"team", strconv.Itoa(e["team"].(int)))
		v.Set(p+"rank", strconv.Itoa(e["rank"].(int)))
		if ch, ok := e["character"]; ok {
			v.Set(p+"char", ch.(string))
		}
	}
	return v
}

func (m *monkey) invalidMatchJSON(names []string) string {
	a, b := names[0], names[1]
	bad := []string{
		fmt.Sprintf(`{"entries":[{"player":%q,"team":1,"rank":1},{"player":%q,"team":2,"rank":2}]}`, a, a), // duplicate
		fmt.Sprintf(`{"entries":[{"player":%q,"team":1,"rank":2},{"player":%q,"team":2,"rank":3}]}`, a, b), // no winner
		fmt.Sprintf(`{"entries":[{"player":%q,"team":1,"rank":1}]}`, a),                                    // alone
		fmt.Sprintf(`{"entries":[{"player":%q,"team":1,"rank":1},{"player":%q,"team":1,"rank":2}]}`, a, b), // one team, two ranks
		fmt.Sprintf(`{"entries":[{"player":%q,"team":0,"rank":1},{"player":%q,"team":2,"rank":2}]}`, a, b), // team 0
		fmt.Sprintf(`{"entries":[{"player":"","team":1,"rank":1},{"player":%q,"team":2,"rank":2}]}`, b),    // empty name
		`{"entries":[]}`, `{}`, `[]`, `null`, `{"entries":[{"player":"a","team":1,"rank":1},{"player":"b","team":2,"rank":2}],"played_at":"yesterday"}`,
		`{"entries":"nope"}`, `{"entries":[1,2,3]}`, `{"entries":[{"player":"a","team":1,"rank":1},{"player":"b","team":2,"rank":2}]`,
	}
	return bad[m.rng.Intn(len(bad))]
}

// checkInvariants compares what the API reports with what the store holds.
func (m *monkey) checkInvariants() {
	m.t.Helper()
	type row struct {
		Name   string  `json:"name"`
		Rating int     `json:"rating"`
		Mu     float64 `json:"mu"`
		Sigma  float64 `json:"sigma"`
		Games  int     `json:"games"`
		Wins   int     `json:"wins"`
	}
	var lb []row
	if err := json.Unmarshal(m.get("/api/leaderboard").Body.Bytes(), &lb); err != nil {
		m.fail("leaderboard json: %v", err)
	}
	games, wins := map[string]int{}, map[string]int{}
	seen := map[string]bool{}
	for i, r := range lb {
		if seen[r.Name] {
			m.fail("player %q listed twice", r.Name)
		}
		seen[r.Name] = true
		if i > 0 && lb[i-1].Rating < r.Rating {
			m.fail("leaderboard not sorted: %q %d above %q %d", lb[i-1].Name, lb[i-1].Rating, r.Name, r.Rating)
		}
		if math.IsNaN(r.Mu) || math.IsInf(r.Mu, 0) || !(r.Sigma > 0) || r.Sigma > 10 {
			m.fail("player %q has a broken rating mu=%v sigma=%v", r.Name, r.Mu, r.Sigma)
		}
		if r.Wins > r.Games || r.Games < 0 {
			m.fail("player %q: %d wins of %d games", r.Name, r.Wins, r.Games)
		}
		games[r.Name], wins[r.Name] = r.Games, r.Wins
	}
	// every stored entry is counted exactly once
	wantGames, wantWins := map[string]int{}, map[string]int{}
	for off := 0; ; off += 100 {
		ms, err := m.c.Matches(100, off)
		if err != nil {
			m.fail("matches: %v", err)
		}
		for _, mt := range ms {
			for _, e := range mt.Entries {
				wantGames[e.PlayerName]++
				if e.Rank == 1 {
					wantWins[e.PlayerName]++
				}
			}
		}
		if len(ms) < 100 {
			break
		}
	}
	for n, g := range wantGames {
		if games[n] != g || wins[n] != wantWins[n] {
			m.fail("player %q: leaderboard says %d games / %d wins, matches say %d / %d", n, games[n], wins[n], g, wantWins[n])
		}
	}
	for n, g := range games {
		if g != 0 && wantGames[n] != g {
			m.fail("player %q has %d games but no matches", n, g)
		}
	}
	// recomputing from scratch must give the same ratings
	if err := m.c.Recompute(); err != nil {
		m.fail("recompute: %v", err)
	}
	var again []row
	if err := json.Unmarshal(m.get("/api/leaderboard").Body.Bytes(), &again); err != nil {
		m.fail("leaderboard json after recompute: %v", err)
	}
	if !bytes.Equal(mustJSON(lb), mustJSON(again)) {
		m.fail("recompute changed the leaderboard:\n%s\n%s", mustJSON(lb), mustJSON(again))
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
