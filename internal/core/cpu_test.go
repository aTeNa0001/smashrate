package core

import "testing"

// A match with a CPU fighter is kept (who played what against which CPU)
// but moves nobody's rating, now or when ratings are recomputed, and the
// CPU pseudo-player is not a member.
func TestCPUMatchesAreKeptButNotRated(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	human := func() int {
		rows, _ := c.Leaderboard(ScopeOverall)
		for _, r := range rows {
			if r.Player.Name == "Taro" {
				return r.Display
			}
		}
		return 0
	}
	if _, err := c.RecordMatch(MatchInput{Entries: []EntryInput{{Player: "Taro", Team: 1, Rank: 1}, {Player: "Hanako", Team: 2, Rank: 2}}}); err != nil {
		t.Fatal(err)
	}
	before := human()
	m, err := c.RecordMatch(MatchInput{Entries: []EntryInput{
		{Player: "Taro", Team: 1, Rank: 2, Character: "marth"},
		{Player: "CPU", Team: 2, Rank: 1, Character: "yoshi"}, // typed as "CPU"
		{CPU: true, Team: 3, Rank: 3, Character: "kirby"},     // flagged
	}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Rated {
		t.Fatal("a match with a CPU must not be rated")
	}
	if len(m.Entries) != 3 || !m.Entries[1].CPU || !m.Entries[2].CPU || m.Entries[1].PlayerName != CPUName || m.Entries[2].Character != "kirby" {
		t.Fatalf("entries: %+v", m.Entries)
	}
	if human() != before {
		t.Fatalf("rating moved on a CPU match: %d -> %d", before, human())
	}
	if err := c.Recompute(); err != nil {
		t.Fatal(err)
	}
	if human() != before {
		t.Fatalf("recompute applied a CPU match: %d -> %d", before, human())
	}
	ps, _ := c.Players()
	for _, p := range ps {
		if p.Name == CPUName {
			t.Fatal("the CPU pseudo-player is listed as a member")
		}
	}
	if _, err := c.RecordMatch(MatchInput{Entries: []EntryInput{{CPU: true, Team: 1, Rank: 1}, {CPU: true, Team: 2, Rank: 2}}}); err == nil {
		t.Fatal("a match between CPUs only must be rejected")
	}
	ms, _ := c.Matches(10, 0)
	if len(ms) != 2 || ms[0].Rated || !ms[1].Rated {
		t.Fatalf("matches: %+v", ms)
	}
}

func TestSetEvidence(t *testing.T) {
	c, _ := Open(":memory:")
	defer c.Close()
	m, err := c.RecordMatch(MatchInput{Entries: []EntryInput{{Player: "A", Team: 1, Rank: 1}, {Player: "B", Team: 2, Rank: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Evidence != "" {
		t.Fatal("a fresh match has no evidence")
	}
	if err := c.SetEvidence(m.ID, "evidence/1.jpg"); err != nil {
		t.Fatal(err)
	}
	m2, _ := c.MatchByID(m.ID)
	if m2.Evidence != "evidence/1.jpg" {
		t.Fatalf("evidence = %q", m2.Evidence)
	}
	if err := c.SetEvidence(999, "x"); err == nil {
		t.Fatal("unknown match must be an error")
	}
}

func TestRemovePlayerOnlyWithoutMatches(t *testing.T) {
	c, _ := Open(":memory:")
	defer c.Close()
	if _, err := c.GetOrCreatePlayer("CP"); err != nil {
		t.Fatal(err)
	}
	_ = c.AddTemplate("name", "CP", 42)
	if err := c.RemovePlayer("CP"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PlayerByName("CP"); err == nil {
		t.Fatal("player still there")
	}
	if ts, _ := c.Templates("name"); len(ts) != 0 {
		t.Fatalf("its tag templates must go too: %+v", ts)
	}
	_, _ = c.RecordMatch(MatchInput{Entries: []EntryInput{{Player: "A", Team: 1, Rank: 1}, {Player: "B", Team: 2, Rank: 2}}})
	if err := c.RemovePlayer("A"); err == nil {
		t.Fatal("a player with matches must not be removed")
	}
}
