// Package httpapi serves the web UI and the JSON API.
package httpapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aTeNa0001/smashrate/internal/balance"
	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
)

//go:embed templates/*.html
var templateFS embed.FS

// Server is the HTTP layer.
type Server struct {
	core *core.Core
	tpl  *template.Template
	mux  *http.ServeMux
	auth *auth
}

// Options configures the server.
type Options struct {
	Auth config.AuthConfig
}

// New builds the handler.
func New(c *core.Core, opt Options) (*Server, error) {
	funcs := template.FuncMap{
		"pct": func(f float64) string { return fmt.Sprintf("%.0f%%", f*100) },
		"f1":  func(f float64) string { return fmt.Sprintf("%.1f", f) },
		"date": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("01/02 15:04")
		},
		"modeName":  modeName,
		"hasPrefix": strings.HasPrefix,
		"inc":       func(i int) int { return i + 1 },
		"mul":       func(a, b int) int { return a * b },
		"div": func(a, b int) float64 {
			if b == 0 {
				return 0
			}
			return float64(a) / float64(b)
		},
		"delta": func(e core.Entry) string {
			d := e.DisplayAfter - e.DisplayBefore
			if e.DisplayAfter == 0 && e.DisplayBefore == 0 {
				return ""
			}
			if d >= 0 {
				return fmt.Sprintf("+%d", d)
			}
			return strconv.Itoa(d)
		},
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"json": func(v any) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	}
	tpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s := &Server{core: c, tpl: tpl, mux: http.NewServeMux(), auth: newAuth(opt.Auth)}
	s.routes()
	return s, nil
}

func modeName(m string) string {
	switch m {
	case core.Mode1v1:
		return "1on1"
	case core.ModeFFA:
		return "乱闘"
	case core.ModeTeam:
		return "チーム"
	}
	return m
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.requireLogin(w, r) {
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /login", s.loginForm)
	s.mux.HandleFunc("POST /login", s.loginSubmit)
	s.mux.HandleFunc("POST /logout", s.logout)
	s.mux.HandleFunc("GET /{$}", s.index)
	s.mux.HandleFunc("GET /players/{name}", s.player)
	s.mux.HandleFunc("GET /matches", s.matches)
	s.mux.HandleFunc("GET /record", s.recordForm)
	s.mux.HandleFunc("POST /record", s.recordSubmit)
	s.mux.HandleFunc("POST /matches/{id}/delete", s.deleteMatch)
	s.mux.HandleFunc("GET /balance", s.balanceForm)
	s.mux.HandleFunc("POST /balance", s.balanceSubmit)
	s.mux.HandleFunc("GET /ports", s.ports)
	s.mux.HandleFunc("POST /ports", s.portsSubmit)

	s.mux.HandleFunc("GET /api/leaderboard", s.apiLeaderboard)
	s.mux.HandleFunc("GET /api/players/{name}", s.apiPlayer)
	s.mux.HandleFunc("GET /api/matches", s.apiMatches)
	s.mux.HandleFunc("POST /api/matches", s.apiRecord)
	s.mux.HandleFunc("DELETE /api/matches/{id}", s.apiDelete)
	s.mux.HandleFunc("POST /api/balance", s.apiBalance)
	s.mux.HandleFunc("GET /api/ports", s.apiPorts)
	s.mux.HandleFunc("PUT /api/ports/{port}", s.apiSetPort)
	s.mux.HandleFunc("GET /api/characters", s.apiCharacters)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["Page"] = name
	data["AuthEnabled"] = s.auth.enabled()
	data["LoggedIn"] = s.auth.enabled() && s.auth.loggedIn(r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name+".html", data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// ---------- pages ----------

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	lb, err := s.core.Leaderboard(core.ScopeOverall)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	recent, _ := s.core.Matches(10, 0)
	usage, _ := s.core.CharacterUsage()
	s.render(w, r, "index", map[string]any{"Leaderboard": lb, "Recent": recent, "Usage": usage})
}

func (s *Server) player(w http.ResponseWriter, r *http.Request) {
	st, err := s.core.Stats(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "player", map[string]any{"S": st})
}

func (s *Server) matches(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 0 {
		page = 0
	}
	ms, err := s.core.Matches(50, page*50)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.render(w, r, "matches", map[string]any{"Matches": ms, "Page": page, "Next": page + 1, "Prev": page - 1})
}

func (s *Server) recordForm(w http.ResponseWriter, r *http.Request) {
	players, _ := s.core.Players()
	s.render(w, r, "record", map[string]any{"Players": players, "Characters": characters.All, "Error": r.URL.Query().Get("error")})
}

func (s *Server) recordSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var in core.MatchInput
	in.Source = "web"
	in.Note = r.FormValue("note")
	for i := 1; i <= 8; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("p%d_name", i)))
		if name == "" {
			continue
		}
		team, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("p%d_team", i)))
		rank, _ := strconv.Atoi(r.FormValue(fmt.Sprintf("p%d_rank", i)))
		in.Entries = append(in.Entries, core.EntryInput{
			Player: name, Character: r.FormValue(fmt.Sprintf("p%d_char", i)), Team: team, Rank: rank,
		})
	}
	m, err := s.core.RecordMatch(in)
	if err != nil {
		http.Redirect(w, r, "/record?error="+err.Error(), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/matches#m%d", m.ID), http.StatusSeeOther)
}

func (s *Server) deleteMatch(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.core.DeleteMatch(id); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	http.Redirect(w, r, "/matches", http.StatusSeeOther)
}

func (s *Server) balanceForm(w http.ResponseWriter, r *http.Request) {
	players, _ := s.core.Players()
	s.render(w, r, "balance", map[string]any{"Players": players, "Teams": 2})
}

func (s *Server) balanceSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	k, _ := strconv.Atoi(r.FormValue("teams"))
	names := r.Form["members"]
	for _, extra := range strings.FieldsFunc(r.FormValue("extra"), func(c rune) bool { return c == ',' || c == '\n' || c == ' ' || c == '　' }) {
		names = append(names, extra)
	}
	props, err := s.Balance(names, k)
	players, _ := s.core.Players()
	data := map[string]any{"Players": players, "Teams": k, "Selected": names, "Proposals": props}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, r, "balance", data)
}

// Balance resolves names to ratings and proposes team splits.
func (s *Server) Balance(names []string, k int) ([]balance.Proposal, error) {
	var members []balance.Member
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" || seen[strings.ToLower(n)] {
			continue
		}
		seen[strings.ToLower(n)] = true
		p, err := s.core.GetOrCreatePlayer(n)
		if err != nil {
			return nil, err
		}
		members = append(members, balance.Member{Name: p.Name, Rating: s.core.PlayerRating(p.ID, core.ScopeOverall).Rating})
	}
	return balance.Propose(members, k, balance.Options{TopN: 3})
}

func (s *Server) ports(w http.ResponseWriter, r *http.Request) {
	ps, _ := s.core.Ports()
	players, _ := s.core.Players()
	byPort := map[int]string{}
	for _, p := range ps {
		byPort[p.Port] = p.Player.Name
	}
	s.render(w, r, "ports", map[string]any{"ByPort": byPort, "Players": players})
}

func (s *Server) portsSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	for port := 1; port <= 4; port++ {
		if err := s.core.SetPort(port, r.FormValue(fmt.Sprintf("port%d", port))); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
	}
	http.Redirect(w, r, "/ports", http.StatusSeeOther)
}

// ---------- JSON API ----------

type apiEntry struct {
	Player    string `json:"player"`
	Character string `json:"character,omitempty"`
	Team      int    `json:"team"`
	Rank      int    `json:"rank"`
	Port      int    `json:"port,omitempty"`
	Stocks    *int   `json:"stocks,omitempty"`
}

type apiMatchIn struct {
	PlayedAt string     `json:"played_at,omitempty"`
	Source   string     `json:"source,omitempty"`
	Note     string     `json:"note,omitempty"`
	Entries  []apiEntry `json:"entries"`
}

func (s *Server) apiLeaderboard(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = core.ScopeOverall
	}
	lb, err := s.core.Leaderboard(scope)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	type row struct {
		Name    string  `json:"name"`
		Display int     `json:"rating"`
		Mu      float64 `json:"mu"`
		Sigma   float64 `json:"sigma"`
		Games   int     `json:"games"`
		Wins    int     `json:"wins"`
		WinRate float64 `json:"win_rate"`
	}
	out := make([]row, 0, len(lb))
	for _, x := range lb {
		out = append(out, row{x.Player.Name, x.Display, x.Rating.Mu, x.Rating.Sigma, x.Games, x.Wins, x.WinRate()})
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiPlayer(w http.ResponseWriter, r *http.Request) {
	st, err := s.core.Stats(r.PathValue("name"))
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"name": st.Player.Name, "rating": st.Overall.Display, "mu": st.Overall.Rating.Mu, "sigma": st.Overall.Rating.Sigma,
		"rank": st.Rank, "games": st.Overall.Games, "wins": st.Overall.Wins, "win_rate": st.Overall.WinRate(),
		"modes": st.Modes, "characters": st.Characters, "head_to_head": st.HeadToHead,
		"current_streak": st.CurrentStreak, "best_streak": st.BestStreak, "avg_placement": st.AvgPlacement,
		"history": st.History,
	})
}

func (s *Server) apiMatches(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	ms, err := s.core.Matches(limit, offset)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, ms)
}

func (s *Server) apiRecord(w http.ResponseWriter, r *http.Request) {
	var in apiMatchIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, err)
		return
	}
	mi := core.MatchInput{Source: in.Source, Note: in.Note}
	if mi.Source == "" {
		mi.Source = "api"
	}
	if in.PlayedAt != "" {
		t, err := time.Parse(time.RFC3339, in.PlayedAt)
		if err != nil {
			writeErr(w, 400, fmt.Errorf("played_at: %w", err))
			return
		}
		mi.PlayedAt = t
	}
	for _, e := range in.Entries {
		mi.Entries = append(mi.Entries, core.EntryInput{Player: e.Player, Character: e.Character, Team: e.Team, Rank: e.Rank, Port: e.Port, Stocks: e.Stocks})
	}
	m, err := s.core.RecordMatch(mi)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 201, m)
}

func (s *Server) apiDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := s.core.DeleteMatch(id); err != nil {
		writeErr(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiBalance(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Members []string `json:"members"`
		Teams   int      `json:"teams"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, err)
		return
	}
	props, err := s.Balance(in.Members, in.Teams)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, props)
}

func (s *Server) apiPorts(w http.ResponseWriter, r *http.Request) {
	ps, err := s.core.Ports()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := map[string]string{}
	for _, p := range ps {
		out[strconv.Itoa(p.Port)] = p.Player.Name
	}
	writeJSON(w, 200, out)
}

func (s *Server) apiSetPort(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(r.PathValue("port"))
	var in struct {
		Player string `json:"player"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.core.SetPort(port, in.Player); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) apiCharacters(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, characters.All)
}
