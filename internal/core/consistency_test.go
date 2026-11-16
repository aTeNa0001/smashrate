package core

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// TestIncrementalRatingsMatchRecompute records a long random series of
// matches one by one and checks, after each, that the ratings kept
// incrementally equal a full replay. Editing or deleting any match replays
// everything, so the two paths must agree or ratings would jump on the
// first correction.
func TestIncrementalRatingsMatchRecompute(t *testing.T) {
	c, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	rng := rand.New(rand.NewSource(3))
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	chars := []string{"", "mario", "link", "samus"}
	for i := 0; i < 120; i++ {
		n := 2 + rng.Intn(7)
		perm := rng.Perm(len(names))[:n]
		k := 2 + rng.Intn(3)
		if rng.Intn(2) == 0 || k > n {
			k = n // everyone alone
		}
		rankOf := rng.Perm(k)
		var in MatchInput
		for j := 0; j < n; j++ {
			team := j%k + 1
			in.Entries = append(in.Entries, EntryInput{Player: names[perm[j]], Team: team, Rank: rankOf[team-1] + 1, Character: chars[rng.Intn(len(chars))]})
		}
		if _, err := c.RecordMatch(in); err != nil {
			t.Fatalf("match %d: %v (%+v)", i, err, in)
		}
		before := snapshotRatings(t, c)
		if err := c.Recompute(); err != nil {
			t.Fatal(err)
		}
		after := snapshotRatings(t, c)
		if before != after {
			bl, al := strings.Split(before, "\n"), strings.Split(after, "\n")
			var diff []string
			for j := range bl {
				if j >= len(al) || bl[j] != al[j] {
					diff = append(diff, "incremental: "+bl[j])
					if j < len(al) {
						diff = append(diff, "replay:      "+al[j])
					}
				}
			}
			m, _ := c.Matches(1, 0)
			t.Fatalf("after match %d (%s, played %s) the incremental ratings differ from a replay:\n%s", i, m[0].Mode, m[0].PlayedAt.Format(time.RFC3339Nano), strings.Join(diff, "\n"))
		}
	}
}

func snapshotRatings(t *testing.T, c *Core) string {
	t.Helper()
	out := ""
	for _, scope := range []string{ScopeOverall, ScopeMode(Mode1v1), ScopeMode(ModeFFA), ScopeMode(ModeTeam), ScopeChar("mario")} {
		lb, err := c.Leaderboard(scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range lb {
			out += fmt.Sprintf("%s %s %.9f %.9f %d/%d\n", scope, r.Player.Name, r.Rating.Mu, r.Rating.Sigma, r.Wins, r.Games)
		}
	}
	return out
}
