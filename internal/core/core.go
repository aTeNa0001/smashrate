// Package core owns the SQLite database and every rule of the system:
// recording matches, applying ratings, recomputing history and answering
// statistics queries. The HTTP, Slack, CLI and vision layers are thin
// adapters over this package.
package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/rating"
	_ "modernc.org/sqlite"
)

// Modes.
const (
	Mode1v1  = "1v1"
	ModeFFA  = "ffa"
	ModeTeam = "team"
)

// Rating scopes.
const (
	ScopeOverall = "overall"
)

// ScopeMode returns the scope for a per-format sub-rating.
func ScopeMode(mode string) string { return "mode:" + mode }

// ScopeChar returns the scope for a per-character sub-rating.
func ScopeChar(key string) string { return "char:" + key }

// Core is the application handle. It is safe for concurrent use.
type Core struct {
	db *sql.DB
}

// Open opens (and migrates) the SQLite database at path. Use ":memory:" for tests.
func Open(path string) (*Core, error) {
	dsn := path
	if path != ":memory:" {
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc sqlite + :memory: must stay on one connection or each conn gets its own DB
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Core{db: db}, nil
}

// Close releases the database.
func (c *Core) Close() error { return c.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// ---------- players ----------

// Player is a human.
type Player struct {
	ID        int64
	Name      string
	SlackID   string
	CreatedAt time.Time
}

// GetOrCreatePlayer returns the player with that name, creating it if needed.
func (c *Core) GetOrCreatePlayer(name string) (Player, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Player{}, errors.New("player name is empty")
	}
	if p, err := c.PlayerByName(name); err == nil {
		return p, nil
	}
	res, err := c.db.Exec(`INSERT INTO players(name, created_at) VALUES(?, ?)`, name, now())
	if err != nil {
		return Player{}, err
	}
	id, _ := res.LastInsertId()
	return Player{ID: id, Name: name, CreatedAt: time.Now()}, nil
}

// PlayerByName finds a player (case-insensitive).
func (c *Core) PlayerByName(name string) (Player, error) {
	var p Player
	var created string
	err := c.db.QueryRow(`SELECT id, name, slack_id, created_at FROM players WHERE name = ? COLLATE NOCASE`, strings.TrimSpace(name)).
		Scan(&p.ID, &p.Name, &p.SlackID, &created)
	if err != nil {
		return Player{}, fmt.Errorf("player %q: %w", name, err)
	}
	p.CreatedAt = parseTime(created)
	return p, nil
}

// PlayerBySlackID finds a player by Slack user id.
func (c *Core) PlayerBySlackID(slackID string) (Player, error) {
	var p Player
	var created string
	err := c.db.QueryRow(`SELECT id, name, slack_id, created_at FROM players WHERE slack_id = ?`, slackID).
		Scan(&p.ID, &p.Name, &p.SlackID, &created)
	if err != nil {
		return Player{}, err
	}
	p.CreatedAt = parseTime(created)
	return p, nil
}

// SetSlackID links a player to a Slack user.
func (c *Core) SetSlackID(name, slackID string) error {
	p, err := c.GetOrCreatePlayer(name)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`UPDATE players SET slack_id = ? WHERE id = ?`, slackID, p.ID)
	return err
}

// Players lists every player alphabetically.
func (c *Core) Players() ([]Player, error) {
	rows, err := c.db.Query(`SELECT id, name, slack_id, created_at FROM players ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Player
	for rows.Next() {
		var p Player
		var created string
		if err := rows.Scan(&p.ID, &p.Name, &p.SlackID, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = parseTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---------- matches ----------

// EntryInput describes one participant of a match to be recorded.
type EntryInput struct {
	Player    string // player name (created on first sight)
	Character string // free text, resolved via characters.Resolve; may be empty
	Team      int    // team number (1-based). For 1v1 / FFA give every player a distinct team.
	Rank      int    // placement of this player's team: 1 = winner. Ties share a rank.
	Port      int    // controller port 1-4, 0 if unknown
	Stocks    *int   // stocks remaining, optional
}

// MatchInput is a match to record.
type MatchInput struct {
	PlayedAt time.Time // zero = now
	Source   string
	Note     string
	Entries  []EntryInput
}

// Entry is a stored participant row.
type Entry struct {
	ID         int64
	MatchID    int64
	PlayerID   int64
	PlayerName string
	Character  string // key, may be empty
	Team       int
	Rank       int
	Port       int
	Stocks     *int
	// Rating movement in the overall scope, filled by MatchByID / Matches.
	DisplayBefore int
	DisplayAfter  int
}

// CharacterName returns the Japanese display name of the entry's character.
func (e Entry) CharacterName() string {
	if ch, ok := characters.ByKey(e.Character); ok {
		return ch.NameJA
	}
	return e.Character
}

// Match is a stored match.
type Match struct {
	ID       int64
	PlayedAt time.Time
	Mode     string
	Source   string
	Note     string
	Entries  []Entry
}

// Winners returns the names of players on the rank-1 team.
func (m Match) Winners() []string {
	var out []string
	for _, e := range m.Entries {
		if e.Rank == 1 {
			out = append(out, e.PlayerName)
		}
	}
	return out
}

// DetectMode derives the format from team structure.
func DetectMode(entries []EntryInput) string {
	teams := map[int]int{}
	for _, e := range entries {
		teams[e.Team]++
	}
	solo := true
	for _, n := range teams {
		if n > 1 {
			solo = false
		}
	}
	switch {
	case solo && len(teams) == 2:
		return Mode1v1
	case solo:
		return ModeFFA
	default:
		return ModeTeam
	}
}

func validate(in MatchInput) error {
	if len(in.Entries) < 2 {
		return errors.New("a match needs at least two players")
	}
	seen := map[string]bool{}
	teamRank := map[int]int{}
	for _, e := range in.Entries {
		name := strings.ToLower(strings.TrimSpace(e.Player))
		if name == "" {
			return errors.New("empty player name")
		}
		if seen[name] {
			return fmt.Errorf("player %q appears twice", e.Player)
		}
		seen[name] = true
		if e.Team < 1 {
			return fmt.Errorf("player %q: team must be >= 1", e.Player)
		}
		if e.Rank < 1 {
			return fmt.Errorf("player %q: rank must be >= 1", e.Player)
		}
		if r, ok := teamRank[e.Team]; ok && r != e.Rank {
			return fmt.Errorf("team %d has inconsistent ranks", e.Team)
		}
		teamRank[e.Team] = e.Rank
	}
	if len(teamRank) < 2 {
		return errors.New("a match needs at least two teams")
	}
	hasWinner := false
	for _, r := range teamRank {
		if r == 1 {
			hasWinner = true
		}
	}
	if !hasWinner {
		return errors.New("no team has rank 1")
	}
	return nil
}

// RecordMatch stores a match and applies every rating update.
func (c *Core) RecordMatch(in MatchInput) (*Match, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	if in.PlayedAt.IsZero() {
		in.PlayedAt = time.Now()
	}
	mode := DetectMode(in.Entries)

	tx, err := c.db.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO matches(played_at, mode, source, note, created_at) VALUES(?,?,?,?,?)`,
		in.PlayedAt.UTC().Format(time.RFC3339), mode, in.Source, in.Note, now())
	if err != nil {
		return nil, err
	}
	matchID, _ := res.LastInsertId()

	for _, e := range in.Entries {
		p, err := getOrCreatePlayerTx(tx, e.Player)
		if err != nil {
			return nil, err
		}
		charKey := ""
		if strings.TrimSpace(e.Character) != "" {
			ch, ok := characters.Resolve(e.Character)
			if !ok {
				return nil, fmt.Errorf("unknown character %q", e.Character)
			}
			charKey = ch.Key
		}
		if _, err := tx.Exec(`INSERT INTO entries(match_id, player_id, character, team, rank, port, stocks) VALUES(?,?,?,?,?,?,?)`,
			matchID, p.ID, charKey, e.Team, e.Rank, e.Port, e.Stocks); err != nil {
			return nil, err
		}
	}
	if err := applyRatings(tx, matchID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return c.MatchByID(matchID)
}

func getOrCreatePlayerTx(tx *sql.Tx, name string) (Player, error) {
	name = strings.TrimSpace(name)
	var p Player
	err := tx.QueryRow(`SELECT id, name FROM players WHERE name = ? COLLATE NOCASE`, name).Scan(&p.ID, &p.Name)
	if err == nil {
		return p, nil
	}
	res, err := tx.Exec(`INSERT INTO players(name, created_at) VALUES(?, ?)`, name, now())
	if err != nil {
		return Player{}, err
	}
	p.ID, _ = res.LastInsertId()
	p.Name = name
	return p, nil
}

type storedEntry struct {
	playerID int64
	charKey  string
	team     int
	rank     int
}

// applyRatings updates every scope for the given match inside tx. It assumes
// the match is the latest one (used by both RecordMatch and Recompute).
func applyRatings(tx *sql.Tx, matchID int64) error {
	var mode string
	if err := tx.QueryRow(`SELECT mode FROM matches WHERE id = ?`, matchID).Scan(&mode); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT player_id, character, team, rank FROM entries WHERE match_id = ? ORDER BY id`, matchID)
	if err != nil {
		return err
	}
	var entries []storedEntry
	for rows.Next() {
		var e storedEntry
		if err := rows.Scan(&e.playerID, &e.charKey, &e.team, &e.rank); err != nil {
			rows.Close()
			return err
		}
		entries = append(entries, e)
	}
	rows.Close()

	// scopes: overall and mode use the same rating for everybody; char scope
	// uses each player's own character rating (opponents likewise).
	scopeFor := func(e storedEntry, kind string) string {
		switch kind {
		case "overall":
			return ScopeOverall
		case "mode":
			return ScopeMode(mode)
		default:
			if e.charKey == "" {
				return ""
			}
			return ScopeChar(e.charKey)
		}
	}
	for _, kind := range []string{"overall", "mode", "char"} {
		if err := applyScope(tx, matchID, entries, func(e storedEntry) string { return scopeFor(e, kind) }); err != nil {
			return err
		}
	}
	return nil
}

func applyScope(tx *sql.Tx, matchID int64, entries []storedEntry, scopeOf func(storedEntry) string) error {
	// group by team
	teamIdx := map[int]int{}
	var teams []rating.Team
	var ranks []int
	var members [][]int // entry indexes per team
	for i, e := range entries {
		if _, ok := teamIdx[e.team]; !ok {
			teamIdx[e.team] = len(teams)
			teams = append(teams, nil)
			ranks = append(ranks, e.rank)
			members = append(members, nil)
		}
		t := teamIdx[e.team]
		scope := scopeOf(e)
		r := rating.NewRating()
		if scope != "" {
			r = loadState(tx, scope, e.playerID)
		}
		teams[t] = append(teams[t], r)
		members[t] = append(members[t], i)
	}
	updated, err := rating.Rate(teams, ranks)
	if err != nil {
		return err
	}
	for t := range teams {
		for j, idx := range members[t] {
			e := entries[idx]
			scope := scopeOf(e)
			if scope == "" {
				continue
			}
			before, after := teams[t][j], updated[t][j]
			win := 0
			if e.rank == 1 {
				win = 1
			}
			if _, err := tx.Exec(`INSERT INTO rating_states(scope, player_id, mu, sigma, games, wins) VALUES(?,?,?,?,1,?)
				ON CONFLICT(scope, player_id) DO UPDATE SET mu = excluded.mu, sigma = excluded.sigma, games = games + 1, wins = wins + excluded.wins`,
				scope, e.playerID, after.Mu, after.Sigma, win); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO rating_history(match_id, player_id, scope, mu_before, sigma_before, mu_after, sigma_after) VALUES(?,?,?,?,?,?,?)`,
				matchID, e.playerID, scope, before.Mu, before.Sigma, after.Mu, after.Sigma); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadState(tx *sql.Tx, scope string, playerID int64) rating.Rating {
	r := rating.NewRating()
	_ = tx.QueryRow(`SELECT mu, sigma FROM rating_states WHERE scope = ? AND player_id = ?`, scope, playerID).Scan(&r.Mu, &r.Sigma)
	return r
}

// Recompute replays every non-deleted match in chronological order and
// rebuilds all rating states and history from scratch.
func (c *Core) Recompute() error {
	tx, err := c.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM rating_states`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM rating_history`); err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT id FROM matches WHERE deleted = 0 ORDER BY played_at, id`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := applyRatings(tx, id); err != nil {
			return fmt.Errorf("match %d: %w", id, err)
		}
	}
	return tx.Commit()
}

// DeleteMatch soft-deletes a match and recomputes every rating.
func (c *Core) DeleteMatch(id int64) error {
	res, err := c.db.Exec(`UPDATE matches SET deleted = 1 WHERE id = ? AND deleted = 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("match %d not found", id)
	}
	return c.Recompute()
}

// UpdateEntryCharacter corrects the character of one participant and recomputes.
func (c *Core) UpdateEntryCharacter(matchID int64, player string, character string) error {
	p, err := c.PlayerByName(player)
	if err != nil {
		return err
	}
	key := ""
	if strings.TrimSpace(character) != "" {
		ch, ok := characters.Resolve(character)
		if !ok {
			return fmt.Errorf("unknown character %q", character)
		}
		key = ch.Key
	}
	res, err := c.db.Exec(`UPDATE entries SET character = ? WHERE match_id = ? AND player_id = ?`, key, matchID, p.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("player %s not in match %d", player, matchID)
	}
	return c.Recompute()
}

// MatchByID loads one match with entries.
func (c *Core) MatchByID(id int64) (*Match, error) {
	ms, err := c.loadMatches(`WHERE m.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("match %d not found", id)
	}
	return &ms[0], nil
}

// Matches lists recent matches, newest first.
func (c *Core) Matches(limit, offset int) ([]Match, error) {
	if limit <= 0 {
		limit = 50
	}
	return c.loadMatches(`WHERE m.deleted = 0 ORDER BY m.played_at DESC, m.id DESC LIMIT ? OFFSET ?`, limit, offset)
}

// MatchesForPlayer lists a player's matches, newest first.
func (c *Core) MatchesForPlayer(playerID int64, limit int) ([]Match, error) {
	if limit <= 0 {
		limit = 20
	}
	return c.loadMatches(`WHERE m.deleted = 0 AND m.id IN (SELECT match_id FROM entries WHERE player_id = ?) ORDER BY m.played_at DESC, m.id DESC LIMIT ?`, playerID, limit)
}

func (c *Core) loadMatches(where string, args ...any) ([]Match, error) {
	rows, err := c.db.Query(`SELECT m.id, m.played_at, m.mode, m.source, m.note FROM matches m `+where, args...)
	if err != nil {
		return nil, err
	}
	var out []Match
	for rows.Next() {
		var m Match
		var played string
		if err := rows.Scan(&m.ID, &played, &m.Mode, &m.Source, &m.Note); err != nil {
			rows.Close()
			return nil, err
		}
		m.PlayedAt = parseTime(played)
		out = append(out, m)
	}
	rows.Close()
	for i := range out {
		es, err := c.entriesFor(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Entries = es
	}
	return out, nil
}

func (c *Core) entriesFor(matchID int64) ([]Entry, error) {
	rows, err := c.db.Query(`
		SELECT e.id, e.match_id, e.player_id, p.name, e.character, e.team, e.rank, e.port, e.stocks,
		       COALESCE(h.mu_before, 0), COALESCE(h.sigma_before, 0), COALESCE(h.mu_after, 0), COALESCE(h.sigma_after, 0)
		FROM entries e
		JOIN players p ON p.id = e.player_id
		LEFT JOIN rating_history h ON h.match_id = e.match_id AND h.player_id = e.player_id AND h.scope = ?
		WHERE e.match_id = ? ORDER BY e.team, e.id`, ScopeOverall, matchID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var stocks sql.NullInt64
		var mb, sb, ma, sa float64
		if err := rows.Scan(&e.ID, &e.MatchID, &e.PlayerID, &e.PlayerName, &e.Character, &e.Team, &e.Rank, &e.Port, &stocks, &mb, &sb, &ma, &sa); err != nil {
			return nil, err
		}
		if stocks.Valid {
			v := int(stocks.Int64)
			e.Stocks = &v
		}
		if ma != 0 || sa != 0 {
			e.DisplayBefore = rating.Rating{Mu: mb, Sigma: sb}.Display()
			e.DisplayAfter = rating.Rating{Mu: ma, Sigma: sa}.Display()
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------- ratings & stats ----------

// RatingRow is a player's standing in one scope.
type RatingRow struct {
	Player  Player
	Rating  rating.Rating
	Display int
	Games   int
	Wins    int
}

// WinRate in [0,1].
func (r RatingRow) WinRate() float64 {
	if r.Games == 0 {
		return 0
	}
	return float64(r.Wins) / float64(r.Games)
}

// Leaderboard returns players ranked by display rating in a scope.
func (c *Core) Leaderboard(scope string) ([]RatingRow, error) {
	rows, err := c.db.Query(`SELECT p.id, p.name, p.slack_id, s.mu, s.sigma, s.games, s.wins
		FROM rating_states s JOIN players p ON p.id = s.player_id WHERE s.scope = ?`, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RatingRow
	for rows.Next() {
		var r RatingRow
		if err := rows.Scan(&r.Player.ID, &r.Player.Name, &r.Player.SlackID, &r.Rating.Mu, &r.Rating.Sigma, &r.Games, &r.Wins); err != nil {
			return nil, err
		}
		r.Display = r.Rating.Display()
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Display != out[j].Display {
			return out[i].Display > out[j].Display
		}
		return out[i].Player.Name < out[j].Player.Name
	})
	return out, rows.Err()
}

// PlayerRating returns the rating of one player in one scope (prior if none).
func (c *Core) PlayerRating(playerID int64, scope string) RatingRow {
	r := RatingRow{Rating: rating.NewRating()}
	_ = c.db.QueryRow(`SELECT mu, sigma, games, wins FROM rating_states WHERE scope = ? AND player_id = ?`, scope, playerID).
		Scan(&r.Rating.Mu, &r.Rating.Sigma, &r.Games, &r.Wins)
	r.Display = r.Rating.Display()
	return r
}

// CharacterStat is a player's record with one character.
type CharacterStat struct {
	Key     string
	Name    string
	Games   int
	Wins    int
	Display int // per-character sub-rating
}

// WinRate in [0,1].
func (s CharacterStat) WinRate() float64 {
	if s.Games == 0 {
		return 0
	}
	return float64(s.Wins) / float64(s.Games)
}

// HeadToHead is a 1v1 record against one opponent.
type HeadToHead struct {
	Opponent Player
	Games    int
	Wins     int
}

// WinRate in [0,1].
func (h HeadToHead) WinRate() float64 {
	if h.Games == 0 {
		return 0
	}
	return float64(h.Wins) / float64(h.Games)
}

// HistoryPoint is one rating observation for charts.
type HistoryPoint struct {
	MatchID  int64
	PlayedAt time.Time
	Display  int
}

// ModeStat is a player's record in one format.
type ModeStat struct {
	Mode  string
	Games int
	Wins  int
	// Display is the format-specific sub-rating.
	Display int
}

// WinRate in [0,1].
func (m ModeStat) WinRate() float64 {
	if m.Games == 0 {
		return 0
	}
	return float64(m.Wins) / float64(m.Games)
}

// PlayerStats is everything the player page shows.
type PlayerStats struct {
	Player        Player
	Overall       RatingRow
	Rank          int // position on the overall leaderboard, 1-based (0 if unrated)
	Modes         []ModeStat
	Characters    []CharacterStat
	HeadToHead    []HeadToHead
	Recent        []Match
	History       []HistoryPoint
	CurrentStreak int // positive = wins in a row, negative = losses in a row
	BestStreak    int
	AvgPlacement  float64 // mean rank across all matches
}

// Stats assembles a player's statistics.
func (c *Core) Stats(name string) (*PlayerStats, error) {
	p, err := c.PlayerByName(name)
	if err != nil {
		return nil, err
	}
	st := &PlayerStats{Player: p}
	st.Overall = c.PlayerRating(p.ID, ScopeOverall)
	st.Overall.Player = p

	lb, err := c.Leaderboard(ScopeOverall)
	if err != nil {
		return nil, err
	}
	for i, r := range lb {
		if r.Player.ID == p.ID {
			st.Rank = i + 1
		}
	}

	for _, mode := range []string{Mode1v1, ModeFFA, ModeTeam} {
		r := c.PlayerRating(p.ID, ScopeMode(mode))
		if r.Games > 0 {
			st.Modes = append(st.Modes, ModeStat{Mode: mode, Games: r.Games, Wins: r.Wins, Display: r.Display})
		}
	}

	// characters
	rows, err := c.db.Query(`SELECT e.character, COUNT(*), SUM(e.rank = 1)
		FROM entries e JOIN matches m ON m.id = e.match_id
		WHERE e.player_id = ? AND m.deleted = 0 AND e.character != '' GROUP BY e.character ORDER BY COUNT(*) DESC`, p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var cs CharacterStat
		if err := rows.Scan(&cs.Key, &cs.Games, &cs.Wins); err != nil {
			rows.Close()
			return nil, err
		}
		if ch, ok := characters.ByKey(cs.Key); ok {
			cs.Name = ch.NameJA
		} else {
			cs.Name = cs.Key
		}
		st.Characters = append(st.Characters, cs)
	}
	rows.Close()
	for i := range st.Characters {
		st.Characters[i].Display = c.PlayerRating(p.ID, ScopeChar(st.Characters[i].Key)).Display
	}

	// head to head (1v1 only)
	rows, err = c.db.Query(`SELECT o.player_id, op.name, COUNT(*), SUM(e.rank = 1)
		FROM entries e
		JOIN matches m ON m.id = e.match_id
		JOIN entries o ON o.match_id = e.match_id AND o.player_id != e.player_id
		JOIN players op ON op.id = o.player_id
		WHERE e.player_id = ? AND m.deleted = 0 AND m.mode = '1v1'
		GROUP BY o.player_id ORDER BY COUNT(*) DESC`, p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h HeadToHead
		if err := rows.Scan(&h.Opponent.ID, &h.Opponent.Name, &h.Games, &h.Wins); err != nil {
			rows.Close()
			return nil, err
		}
		st.HeadToHead = append(st.HeadToHead, h)
	}
	rows.Close()

	// history (overall), oldest first
	rows, err = c.db.Query(`SELECT h.match_id, m.played_at, h.mu_after, h.sigma_after
		FROM rating_history h JOIN matches m ON m.id = h.match_id
		WHERE h.player_id = ? AND h.scope = ? ORDER BY m.played_at, m.id`, p.ID, ScopeOverall)
	if err != nil {
		return nil, err
	}
	st.History = append(st.History, HistoryPoint{Display: rating.NewRating().Display()})
	for rows.Next() {
		var hp HistoryPoint
		var played string
		var mu, sigma float64
		if err := rows.Scan(&hp.MatchID, &played, &mu, &sigma); err != nil {
			rows.Close()
			return nil, err
		}
		hp.PlayedAt = parseTime(played)
		hp.Display = rating.Rating{Mu: mu, Sigma: sigma}.Display()
		st.History = append(st.History, hp)
	}
	rows.Close()

	// streaks and average placement, chronological
	rows, err = c.db.Query(`SELECT e.rank FROM entries e JOIN matches m ON m.id = e.match_id
		WHERE e.player_id = ? AND m.deleted = 0 ORDER BY m.played_at, m.id`, p.ID)
	if err != nil {
		return nil, err
	}
	var ranksSeq []int
	for rows.Next() {
		var r int
		if err := rows.Scan(&r); err != nil {
			rows.Close()
			return nil, err
		}
		ranksSeq = append(ranksSeq, r)
	}
	rows.Close()
	cur, best, sum := 0, 0, 0
	for _, r := range ranksSeq {
		sum += r
		if r == 1 {
			if cur < 0 {
				cur = 0
			}
			cur++
			if cur > best {
				best = cur
			}
		} else {
			if cur > 0 {
				cur = 0
			}
			cur--
		}
	}
	st.CurrentStreak, st.BestStreak = cur, best
	if len(ranksSeq) > 0 {
		st.AvgPlacement = float64(sum) / float64(len(ranksSeq))
	}

	st.Recent, err = c.MatchesForPlayer(p.ID, 10)
	if err != nil {
		return nil, err
	}
	return st, nil
}

// CharacterUsage is roster-wide usage, for the overview page.
type CharacterUsage struct {
	Key   string
	Name  string
	Games int
	Wins  int
}

// CharacterUsage aggregates character picks across all players.
func (c *Core) CharacterUsage() ([]CharacterUsage, error) {
	rows, err := c.db.Query(`SELECT e.character, COUNT(*), SUM(e.rank = 1) FROM entries e JOIN matches m ON m.id = e.match_id
		WHERE m.deleted = 0 AND e.character != '' GROUP BY e.character ORDER BY COUNT(*) DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CharacterUsage
	for rows.Next() {
		var u CharacterUsage
		if err := rows.Scan(&u.Key, &u.Games, &u.Wins); err != nil {
			return nil, err
		}
		if ch, ok := characters.ByKey(u.Key); ok {
			u.Name = ch.NameJA
		} else {
			u.Name = u.Key
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ---------- ports (vision) ----------

// PortAssignment maps a controller port to a player.
type PortAssignment struct {
	Port      int
	Player    Player
	UpdatedAt time.Time
}

// SetPort records who is holding a controller port.
func (c *Core) SetPort(port int, playerName string) error {
	if port < 1 || port > 8 {
		return fmt.Errorf("port %d out of range", port)
	}
	if strings.TrimSpace(playerName) == "" || strings.EqualFold(playerName, "none") {
		_, err := c.db.Exec(`DELETE FROM ports WHERE port = ?`, port)
		return err
	}
	p, err := c.GetOrCreatePlayer(playerName)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`INSERT INTO ports(port, player_id, updated_at) VALUES(?,?,?)
		ON CONFLICT(port) DO UPDATE SET player_id = excluded.player_id, updated_at = excluded.updated_at`, port, p.ID, now())
	return err
}

// Ports returns the current port assignments.
func (c *Core) Ports() ([]PortAssignment, error) {
	rows, err := c.db.Query(`SELECT t.port, p.id, p.name, t.updated_at FROM ports t JOIN players p ON p.id = t.player_id ORDER BY t.port`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PortAssignment
	for rows.Next() {
		var a PortAssignment
		var upd string
		if err := rows.Scan(&a.Port, &a.Player.ID, &a.Player.Name, &upd); err != nil {
			return nil, err
		}
		a.UpdatedAt = parseTime(upd)
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---------- vision templates ----------

// Template is a learned perceptual hash.
type Template struct {
	ID    int64
	Kind  string
	Label string
	Hash  uint64
}

// AddTemplate stores a learned hash.
func (c *Core) AddTemplate(kind, label string, hash uint64) error {
	_, err := c.db.Exec(`INSERT INTO vision_templates(kind, label, hash, created_at) VALUES(?,?,?,?)`, kind, label, int64(hash), now())
	return err
}

// Templates lists learned hashes of one kind.
func (c *Core) Templates(kind string) ([]Template, error) {
	rows, err := c.db.Query(`SELECT id, kind, label, hash FROM vision_templates WHERE kind = ? ORDER BY id`, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		var t Template
		var h int64
		if err := rows.Scan(&t.ID, &t.Kind, &t.Label, &h); err != nil {
			return nil, err
		}
		t.Hash = uint64(h)
		out = append(out, t)
	}
	return out, rows.Err()
}
