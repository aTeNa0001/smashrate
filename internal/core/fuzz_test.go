package core

import (
	"strings"
	"testing"
)

// FuzzRecordMatch records line-ups built from random bytes (names with
// arbitrary unicode, random teams/ranks/characters) and checks the store
// never panics and stays consistent: accepted matches can be read back
// with the same entries, and Recompute agrees with the incremental ratings.
func FuzzRecordMatch(f *testing.F) {
	f.Add("Taro\x00Hanako", []byte{1, 2, 1, 2})
	f.Add("a\x00b\x00c\x00d", []byte{1, 1, 2, 2, 1, 1, 2, 2})
	f.Add("ぎんぴか\x00ゆげ\x00x", []byte{1, 2, 3, 1, 1, 2})
	f.Fuzz(func(t *testing.T, names string, data []byte) {
		c, err := Open(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var in MatchInput
		i := 0
		for _, n := range splitNames(names) {
			e := EntryInput{Player: n}
			if i+1 < len(data) {
				e.Team, e.Rank = int(data[i]%5), int(data[i+1]%5)
				i += 2
			}
			if i < len(data) && data[i]%3 == 0 {
				e.Character = []string{"mario", "マリオ", "nope", ""}[int(data[i]/3)%4]
				i++
			}
			in.Entries = append(in.Entries, e)
		}
		m, err := c.RecordMatch(in)
		if err != nil {
			if m != nil {
				t.Fatalf("error and match: %v %+v", err, m)
			}
			if ms, _ := c.Matches(1, 0); len(ms) != 0 {
				t.Fatalf("rejected match was stored: %+v", ms)
			}
			return
		}
		got, err := c.MatchByID(m.ID)
		if err != nil {
			t.Fatalf("stored match not readable: %v", err)
		}
		if len(got.Entries) != len(in.Entries) {
			t.Fatalf("stored %d entries, sent %d", len(got.Entries), len(in.Entries))
		}
		// entries come back ordered by placement, so match them by name
		sent := map[string]EntryInput{}
		for _, e := range in.Entries {
			sent[strings.ToLower(strings.TrimSpace(e.Player))] = e
		}
		for _, e := range got.Entries {
			want, ok := sent[strings.ToLower(e.PlayerName)]
			if !ok || e.Team != want.Team || e.Rank != want.Rank {
				t.Fatalf("entry %q changed: sent %+v got %+v", e.PlayerName, want, e)
			}
		}
		before, _ := c.Leaderboard(ScopeOverall)
		if err := c.Recompute(); err != nil {
			t.Fatalf("recompute: %v", err)
		}
		after, _ := c.Leaderboard(ScopeOverall)
		if len(before) != len(after) {
			t.Fatalf("recompute changed the player count")
		}
		for k := range before {
			if before[k].Player.Name != after[k].Player.Name || before[k].Display != after[k].Display {
				t.Fatalf("recompute disagrees: %+v vs %+v", before[k], after[k])
			}
		}
	})
}

func splitNames(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == 0 {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	return append(out, cur)
}
