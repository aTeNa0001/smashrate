// Package e2e builds the real smashrate binary, starts `smashrate serve`
// with a login-protected config on a free port and drives it the way a
// member on a phone (HTML forms) and a script (JSON API, CLI) would.
package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

type app struct {
	t      *testing.T
	bin    string
	cfg    string
	base   string
	client *http.Client
	cmd    *exec.Cmd
}

func start(t *testing.T) *app {
	t.Helper()
	if testing.Short() {
		t.Skip("e2e skipped in -short mode")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "smashrate")
	if os.PathSeparator == '\\' {
		bin += ".exe"
	}
	build := exec.Command("go", "build", "-o", bin, "../cmd/smashrate")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	cfg := filepath.Join(dir, "smashrate.toml")
	toml := fmt.Sprintf(`[server]
addr = %q
database = %q
data_dir = %q
base_url = "http://%s"
[server.auth]
password = "pw"
read_requires_login = true
[slack]
enabled = false
[vision]
enabled = false
`, addr, filepath.ToSlash(filepath.Join(dir, "e2e.db")), filepath.ToSlash(filepath.Join(dir, "data")), addr)
	if err := os.WriteFile(cfg, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-config", cfg, "serve")
	cmd.Dir = dir
	logf, _ := os.Create(filepath.Join(dir, "serve.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		logf.Close()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(dir, "serve.log"))
			t.Logf("serve.log:\n%s", b)
		}
	})
	jar, _ := cookiejar.New(nil)
	a := &app{t: t, bin: bin, cfg: cfg, base: "http://" + addr, cmd: cmd,
		client: &http.Client{Jar: jar, Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if resp, err := http.Get(a.base + "/healthz"); err == nil && resp.StatusCode == 200 {
			resp.Body.Close()
			return a
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not come up")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type resp struct {
	code int
	loc  string
	body string
}

func (a *app) do(method, path, ctype, body string) resp {
	a.t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, a.base+path, r)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := a.client.Do(req)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return resp{res.StatusCode, res.Header.Get("Location"), string(b)}
}

func (a *app) get(path string) resp { return a.do("GET", path, "", "") }
func (a *app) form(path string, v url.Values) resp {
	return a.do("POST", path, "application/x-www-form-urlencoded", v.Encode())
}
func (a *app) jsonReq(method, path string, v any) resp {
	b, _ := json.Marshal(v)
	return a.do(method, path, "application/json", string(b))
}

func (a *app) cli(args ...string) string {
	a.t.Helper()
	cmd := exec.Command(a.bin, append([]string{"-config", a.cfg}, args...)...)
	cmd.Dir = filepath.Dir(a.cfg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		a.t.Fatalf("cli %v: %v\n%s", args, err, out)
	}
	return string(out)
}

type lbRow struct {
	Name   string `json:"name"`
	Rating int    `json:"rating"`
	Games  int    `json:"games"`
	Wins   int    `json:"wins"`
}

func (a *app) leaderboard() []lbRow {
	a.t.Helper()
	r := a.get("/api/leaderboard")
	if r.code != 200 {
		a.t.Fatalf("leaderboard: %d %s", r.code, r.body)
	}
	var lb []lbRow
	if err := json.Unmarshal([]byte(r.body), &lb); err != nil {
		a.t.Fatal(err)
	}
	return lb
}

func (a *app) matchCount() int {
	a.t.Helper()
	var ms []json.RawMessage
	if err := json.Unmarshal([]byte(a.get("/api/matches").body), &ms); err != nil {
		a.t.Fatal(err)
	}
	return len(ms)
}

func TestMemberJourney(t *testing.T) {
	a := start(t)

	// 1. nothing is readable before login; the login page itself is
	if r := a.get("/"); r.code != http.StatusSeeOther || !strings.HasPrefix(r.loc, "/login") {
		t.Fatalf("index before login: %d %s", r.code, r.loc)
	}
	if r := a.get("/api/leaderboard"); r.code != 401 {
		t.Fatalf("api before login: %d", r.code)
	}
	if r := a.get("/login"); r.code != 200 || !strings.Contains(r.body, "ログイン") {
		t.Fatalf("login page: %d", r.code)
	}

	// 2. wrong password is refused, right one opens everything
	if r := a.form("/login", url.Values{"password": {"nope"}, "next": {"/"}}); !strings.Contains(r.loc, "error=1") {
		t.Fatalf("wrong password: %d %s", r.code, r.loc)
	}
	if r := a.form("/login", url.Values{"password": {"pw"}, "next": {"/record"}}); r.loc != "/record" {
		t.Fatalf("login: %d %s %s", r.code, r.loc, r.body)
	}
	if r := a.get("/"); r.code != 200 || !strings.Contains(r.body, "ランキング") {
		t.Fatalf("index after login: %d", r.code)
	}

	// 3. record matches the way the form does: Taro beats Hanako twice,
	//    then a 4-player free-for-all, then a 2v2 through the JSON API
	for i := 0; i < 2; i++ {
		r := a.form("/record", url.Values{
			"p1_name": {"Taro"}, "p1_char": {"マリオ"}, "p1_team": {"1"}, "p1_rank": {"1"},
			"p2_name": {"Hanako"}, "p2_char": {"link"}, "p2_team": {"2"}, "p2_rank": {"2"},
		})
		if r.code != http.StatusSeeOther || strings.Contains(r.loc, "error") {
			t.Fatalf("record form: %d %s %s", r.code, r.loc, r.body)
		}
	}
	r := a.form("/record", url.Values{
		"p1_name": {"Taro"}, "p1_team": {"1"}, "p1_rank": {"2"},
		"p2_name": {"Hanako"}, "p2_team": {"2"}, "p2_rank": {"1"},
		"p3_name": {"ぎんぴか"}, "p3_team": {"3"}, "p3_rank": {"3"},
		"p4_name": {"Jiro"}, "p4_team": {"4"}, "p4_rank": {"4"},
	})
	if r.code != http.StatusSeeOther || strings.Contains(r.loc, "error") {
		t.Fatalf("ffa form: %d %s", r.code, r.loc)
	}
	teamMatch := map[string]any{"entries": []map[string]any{
		{"player": "Taro", "team": 1, "rank": 1}, {"player": "Jiro", "team": 1, "rank": 1},
		{"player": "Hanako", "team": 2, "rank": 2}, {"player": "ぎんぴか", "team": 2, "rank": 2},
	}}
	r = a.jsonReq("POST", "/api/matches", teamMatch)
	if r.code != 201 {
		t.Fatalf("api record: %d %s", r.code, r.body)
	}
	var created struct {
		ID   int64  `json:"id"`
		Mode string `json:"mode"`
	}
	json.Unmarshal([]byte(r.body), &created)
	if created.Mode != "team" {
		t.Fatalf("2v2 recorded as %q: %s", created.Mode, r.body)
	}

	// 4. the ranking page and the API agree, and Taro (3 wins of 4) leads
	lb := a.leaderboard()
	if len(lb) != 4 || lb[0].Name != "Taro" || lb[0].Games != 4 || lb[0].Wins != 3 {
		t.Fatalf("leaderboard: %+v", lb)
	}
	index := a.get("/").body
	pos := func(name string) int { return strings.Index(index, name) }
	for i := 1; i < len(lb); i++ {
		if pos(lb[i-1].Name) < 0 || pos(lb[i-1].Name) > pos(lb[i].Name) {
			t.Fatalf("ranking page order differs from the API: %+v", lb)
		}
	}
	if r := a.get("/players/" + url.PathEscape("ぎんぴか")); r.code != 200 || !strings.Contains(r.body, "ぎんぴか") {
		t.Fatalf("player page: %d", r.code)
	}
	if r := a.get("/matches"); r.code != 200 || !strings.Contains(r.body, "Hanako") {
		t.Fatalf("matches page: %d", r.code)
	}

	// 5. the CLI records too (same database, server running)
	if out := a.cli("record", "Jiro > Taro"); !strings.Contains(out, "Jiro") {
		t.Fatalf("cli record: %s", out)
	}
	if n := a.matchCount(); n != 5 {
		t.Fatalf("matches after cli record: %d", n)
	}
	if out := a.cli("rank"); !strings.Contains(out, "Taro") {
		t.Fatalf("cli rank: %s", out)
	}

	// 6. editing a match re-rates: give the 2v2 to the other team
	before := a.leaderboard()
	flipped := map[string]any{"entries": []map[string]any{
		{"player": "Taro", "team": 1, "rank": 2}, {"player": "Jiro", "team": 1, "rank": 2},
		{"player": "Hanako", "team": 2, "rank": 1}, {"player": "ぎんぴか", "team": 2, "rank": 1},
	}}
	if r := a.jsonReq("PUT", fmt.Sprintf("/api/matches/%d", created.ID), flipped); r.code != 200 {
		t.Fatalf("edit: %d %s", r.code, r.body)
	}
	after := a.leaderboard()
	find := func(lb []lbRow, n string) lbRow {
		for _, r := range lb {
			if r.Name == n {
				return r
			}
		}
		return lbRow{}
	}
	if find(after, "Hanako").Rating <= find(before, "Hanako").Rating || find(after, "Taro").Rating >= find(before, "Taro").Rating {
		t.Fatalf("edit did not re-rate: before %+v after %+v", before, after)
	}
	if r := a.get(fmt.Sprintf("/matches/%d/edit", created.ID)); r.code != 200 {
		t.Fatalf("edit page: %d", r.code)
	}

	// 7. deleting through the form removes it and restores the ratings
	if r := a.form(fmt.Sprintf("/matches/%d/delete", created.ID), nil); r.code != http.StatusSeeOther {
		t.Fatalf("delete: %d %s", r.code, r.body)
	}
	if n := a.matchCount(); n != 4 {
		t.Fatalf("matches after delete: %d", n)
	}
	if r := a.get(fmt.Sprintf("/matches/%d/edit", created.ID)); r.code != 404 {
		t.Fatalf("deleted match still editable: %d", r.code)
	}

	// 8. team balancing proposes two teams from the members given
	r = a.form("/balance", url.Values{"teams": {"2"}, "members": {"Taro", "Hanako", "Jiro", "ぎんぴか"}})
	if r.code != 200 || !strings.Contains(r.body, "Taro") {
		t.Fatalf("balance: %d", r.code)
	}
	r = a.jsonReq("POST", "/api/balance", map[string]any{"members": []string{"Taro", "Hanako", "Jiro"}, "teams": 2, "allow_uneven": true})
	if r.code != 200 {
		t.Fatalf("api balance: %d %s", r.code, r.body)
	}

	// 9. renaming a player follows through to every page
	if r := a.form("/players", url.Values{"action": {"rename"}, "from": {"Jiro"}, "to": {"次郎"}}); r.code != http.StatusSeeOther || strings.Contains(r.loc, "error") {
		t.Fatalf("rename: %d %s", r.code, r.loc)
	}
	if r := a.get("/players/" + url.PathEscape("次郎")); r.code != 200 {
		t.Fatalf("renamed player page: %d", r.code)
	}
	if find(a.leaderboard(), "Jiro").Name != "" {
		t.Fatal("old name still on the leaderboard")
	}

	// 10. status pages work without a capture board
	if r := a.get("/status"); r.code != 200 || !strings.Contains(r.body, "キャプチャ") {
		t.Fatalf("status: %d", r.code)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(a.get("/api/status").body), &st); err != nil || st["version"] == nil {
		t.Fatalf("api status: %v %s", err, a.get("/api/status").body)
	}

	// 11. logging out closes the door again
	if r := a.form("/logout", nil); r.code != http.StatusSeeOther {
		t.Fatalf("logout: %d", r.code)
	}
	if r := a.get("/api/leaderboard"); r.code != 401 {
		t.Fatalf("api after logout: %d", r.code)
	}

	// 12. guessing the password is rate limited
	for i := 0; i < 5; i++ {
		a.form("/login", url.Values{"password": {"guess" + strconv.Itoa(i)}})
	}
	if r := a.form("/login", url.Values{"password": {"pw"}}); !strings.Contains(r.loc, "error=locked") {
		t.Fatalf("no lockout after 5 wrong passwords: %d %s", r.code, r.loc)
	}
}

// TestNoServerErrorsOnBrokenInput: whatever a phone or script sends to any
// route, the answer is a 4xx, never a 5xx (which would mean a panic or an
// unhandled error).
func TestNoServerErrorsOnBrokenInput(t *testing.T) {
	a := start(t)
	a.form("/login", url.Values{"password": {"pw"}})
	routes := regexp.MustCompile(`\{[^}]*\}`)
	for _, m := range []string{"GET", "POST", "PUT", "DELETE"} {
		for _, p := range []string{"/", "/players/{name}", "/matches", "/record", "/matches/{id}/delete", "/matches/{id}/edit", "/balance",
			"/api/leaderboard", "/api/players/{name}", "/api/matches", "/api/matches/{id}", "/api/balance", "/api/characters",
			"/api/vision/snapshot", "/status", "/api/status", "/learn", "/learn/screen", "/api/vision/pending/{id}", "/players"} {
			for _, bad := range []string{"", "{", "[]", `{"entries":[{"player":"a"}]}`, "p1_name=%ZZ&teams=x", strings.Repeat("あ", 5000), "\x00\xff"} {
				path := routes.ReplaceAllString(p, url.PathEscape(bad))
				for _, ctype := range []string{"application/json", "application/x-www-form-urlencoded"} {
					if r := a.do(m, path, ctype, bad); r.code >= 500 {
						t.Fatalf("%s %s with %q (%s) -> %d %s", m, path, bad, ctype, r.code, r.body)
					}
				}
			}
		}
	}
}
