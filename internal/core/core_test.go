package core

import (
	"testing"
	"time"
)

func openTest(t *testing.T) *Core {
	t.Helper()
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func e(player, char string, team, rank int) EntryInput {
	return EntryInput{Player: player, Character: char, Team: team, Rank: rank}
}

func TestRecord1v1UpdatesAllScopes(t *testing.T) {
	c := openTest(t)
	m, err := c.RecordMatch(MatchInput{Source: "test", Entries: []EntryInput{e("Taro", "マリオ", 1, 1), e("Hanako", "link", 2, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != Mode1v1 {
		t.Fatalf("mode = %s", m.Mode)
	}
	taro, _ := c.PlayerByName("taro") // case-insensitive
	hanako, _ := c.PlayerByName("Hanako")

	if r := c.PlayerRating(taro.ID, ScopeOverall); r.Display <= 1500 || r.Games != 1 || r.Wins != 1 {
		t.Fatalf("winner overall: %+v", r)
	}
	if r := c.PlayerRating(hanako.ID, ScopeOverall); r.Display >= 1500 || r.Wins != 0 {
		t.Fatalf("loser overall: %+v", r)
	}
	if r := c.PlayerRating(taro.ID, ScopeMode(Mode1v1)); r.Games != 1 {
		t.Fatalf("mode scope not updated: %+v", r)
	}
	if r := c.PlayerRating(taro.ID, ScopeChar("mario")); r.Games != 1 {
		t.Fatalf("char scope not updated: %+v", r)
	}
	if r := c.PlayerRating(taro.ID, ScopeMode(ModeFFA)); r.Games != 0 {
		t.Fatalf("ffa scope should be untouched: %+v", r)
	}
	if m.Entries[0].DisplayAfter <= m.Entries[0].DisplayBefore {
		t.Fatalf("entry rating movement not filled: %+v", m.Entries[0])
	}
}

func TestFFAAndTeamModes(t *testing.T) {
	c := openTest(t)
	m, err := c.RecordMatch(MatchInput{Entries: []EntryInput{e("A", "", 1, 1), e("B", "", 2, 2), e("C", "", 3, 3), e("D", "", 4, 4)}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != ModeFFA {
		t.Fatalf("mode = %s", m.Mode)
	}
	m, err = c.RecordMatch(MatchInput{Entries: []EntryInput{e("A", "", 1, 1), e("B", "", 1, 1), e("C", "", 2, 2), e("D", "", 2, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != ModeTeam {
		t.Fatalf("mode = %s", m.Mode)
	}
	if w := m.Winners(); len(w) != 2 {
		t.Fatalf("winners = %v", w)
	}
	lb, err := c.Leaderboard(ScopeOverall)
	if err != nil {
		t.Fatal(err)
	}
	if len(lb) != 4 || lb[0].Player.Name != "A" || lb[3].Player.Name != "D" {
		t.Fatalf("leaderboard order: %+v", lb)
	}
}

func TestValidation(t *testing.T) {
	c := openTest(t)
	cases := [][]EntryInput{
		{e("A", "", 1, 1)},                         // one player
		{e("A", "", 1, 1), e("A", "", 2, 2)},       // duplicate
		{e("A", "", 1, 2), e("B", "", 2, 2)},       // no winner
		{e("A", "", 1, 1), e("B", "", 1, 2)},       // inconsistent team rank
		{e("A", "", 1, 1), e("B", "", 1, 1)},       // single team
		{e("A", "nosuch", 1, 1), e("B", "", 2, 2)}, // unknown character
		{e("A", "", 1, 1), e("B", "", 2, 2), e("C", "", 3, 3), e("D", "", 4, 4), e("E", "", 5, 5), e("F", "", 6, 6), e("G", "", 7, 7), e("H", "", 8, 8), e("I", "", 9, 9)}, // 9 players
		{e("A", "", 1, 1), e("B", "", 1, 1), e("C", "", 2, 2), e("D", "", 3, 3), e("E", "", 4, 4), e("F", "", 5, 5)},                                                       // 5 teams in team battle
	}
	for i, in := range cases {
		if _, err := c.RecordMatch(MatchInput{Entries: in}); err == nil {
			t.Fatalf("case %d should fail", i)
		}
	}
}

func TestRecomputeIsDeterministicAndDeleteReverts(t *testing.T) {
	c := openTest(t)
	base := time.Date(2026, 11, 1, 20, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		w, l := "A", "B"
		if i%3 == 2 {
			w, l = "B", "A"
		}
		if _, err := c.RecordMatch(MatchInput{PlayedAt: base.Add(time.Duration(i) * time.Minute), Entries: []EntryInput{e(w, "fox", 1, 1), e(l, "falco", 2, 2)}}); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := c.PlayerByName("A")
	before := c.PlayerRating(a.ID, ScopeOverall)
	if err := c.Recompute(); err != nil {
		t.Fatal(err)
	}
	after := c.PlayerRating(a.ID, ScopeOverall)
	if before != after {
		t.Fatalf("recompute changed ratings: %+v vs %+v", before, after)
	}

	// deleting the last match must leave exactly the state after 4 matches
	ms, _ := c.Matches(1, 0)
	if err := c.DeleteMatch(ms[0].ID); err != nil {
		t.Fatal(err)
	}
	if r := c.PlayerRating(a.ID, ScopeOverall); r.Games != 4 {
		t.Fatalf("games after delete = %d", r.Games)
	}
	st, err := c.Stats("A")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.History) != 5 { // prior + 4 matches
		t.Fatalf("history len = %d", len(st.History))
	}
	if len(st.Characters) != 2 {
		t.Fatalf("characters = %+v", st.Characters)
	}
	if len(st.HeadToHead) != 1 || st.HeadToHead[0].Games != 4 {
		t.Fatalf("h2h = %+v", st.HeadToHead)
	}
	if st.Rank != 1 || st.BestStreak < 2 {
		t.Fatalf("rank/streak: %+v", st)
	}
}

func TestUpdateEntryCharacter(t *testing.T) {
	c := openTest(t)
	m, err := c.RecordMatch(MatchInput{Entries: []EntryInput{e("A", "mario", 1, 1), e("B", "", 2, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.UpdateEntryCharacter(m.ID, "A", "ルイージ"); err != nil {
		t.Fatal(err)
	}
	a, _ := c.PlayerByName("A")
	if r := c.PlayerRating(a.ID, ScopeChar("mario")); r.Games != 0 {
		t.Fatalf("old char scope should be gone after recompute: %+v", r)
	}
	if r := c.PlayerRating(a.ID, ScopeChar("luigi")); r.Games != 1 {
		t.Fatalf("new char scope missing: %+v", r)
	}
}

func TestUpdateMatch(t *testing.T) {
	c := openTest(t)
	m, err := c.RecordMatch(MatchInput{Entries: []EntryInput{e("A", "mario", 1, 1), e("B", "link", 2, 2)}})
	if err != nil {
		t.Fatal(err)
	}
	// swap the result and add a third player: becomes a 3-player FFA won by B
	if err := c.UpdateMatch(m.ID, MatchInput{Note: "fixed", Entries: []EntryInput{e("A", "mario", 1, 2), e("B", "link", 2, 1), e("C", "", 3, 3)}}); err != nil {
		t.Fatal(err)
	}
	got, _ := c.MatchByID(m.ID)
	if got.Mode != ModeFFA || got.Note != "fixed" || len(got.Entries) != 3 || got.Winners()[0] != "B" {
		t.Fatalf("%+v", got)
	}
	a, _ := c.PlayerByName("A")
	b, _ := c.PlayerByName("B")
	if c.PlayerRating(a.ID, ScopeOverall).Display >= c.PlayerRating(b.ID, ScopeOverall).Display {
		t.Fatal("ratings should follow the corrected result")
	}
	if c.PlayerRating(a.ID, ScopeMode(Mode1v1)).Games != 0 {
		t.Fatal("old 1v1 scope should be gone after recompute")
	}
	if err := c.UpdateMatch(999, MatchInput{Entries: []EntryInput{e("A", "", 1, 1), e("B", "", 2, 2)}}); err == nil {
		t.Fatal("missing match should fail")
	}
}

func TestPortsAndTemplates(t *testing.T) {
	c := openTest(t)
	if err := c.SetPort(1, "A"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPort(2, "B"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetPort(2, "none"); err != nil {
		t.Fatal(err)
	}
	ps, _ := c.Ports()
	if len(ps) != 1 || ps[0].Player.Name != "A" {
		t.Fatalf("ports = %+v", ps)
	}
	if err := c.AddTemplate("char", "mario", 0xFFFFFFFFFFFFFFFF); err != nil {
		t.Fatal(err)
	}
	ts, _ := c.Templates("char")
	if len(ts) != 1 || ts[0].Hash != 0xFFFFFFFFFFFFFFFF {
		t.Fatalf("templates = %+v", ts)
	}
}
