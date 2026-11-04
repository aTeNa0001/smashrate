package balance

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/rating"
)

func members(mus ...float64) []Member {
	out := make([]Member, len(mus))
	for i, mu := range mus {
		out[i] = Member{Name: fmt.Sprintf("p%d", i), Rating: rating.Rating{Mu: mu, Sigma: 4}}
	}
	return out
}

func TestProposeTwoTeamsPerfectSplit(t *testing.T) {
	// 40+10 == 30+20
	props, err := Propose(members(40, 30, 20, 10), 2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(props) == 0 {
		t.Fatal("no proposals")
	}
	if props[0].Spread != 0 {
		t.Fatalf("best split should be perfectly balanced, got spread %.2f: %+v", props[0].Spread, props[0])
	}
	if len(props[0].Teams[0]) != 2 || len(props[0].Teams[1]) != 2 {
		t.Fatalf("expected 2v2, got %d v %d", len(props[0].Teams[0]), len(props[0].Teams[1]))
	}
	if props[0].WinProb[0] < 0.49 || props[0].WinProb[0] > 0.51 {
		t.Fatalf("balanced teams should be ~50%%: %v", props[0].WinProb)
	}
}

func TestProposeUnevenCount(t *testing.T) {
	props, err := Propose(members(30, 28, 26, 24, 22), 2, Options{TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range props {
		a, b := len(p.Teams[0]), len(p.Teams[1])
		if a+b != 5 || (a != 3 && a != 2) {
			t.Fatalf("sizes should be 3/2, got %d/%d", a, b)
		}
	}
	// proposals are sorted best-first and distinct
	for i := 1; i < len(props); i++ {
		if props[i].Spread < props[i-1].Spread {
			t.Fatal("proposals not sorted")
		}
	}
}

func TestProposeThreeTeams(t *testing.T) {
	props, err := Propose(members(30, 30, 30, 20, 20, 20), 3, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if props[0].Spread != 0 {
		t.Fatalf("expected perfect 3-way split, got %.2f", props[0].Spread)
	}
}

func TestProposeEightIntoFour(t *testing.T) {
	// 8 players, 4 teams of 2: pairs (15+22),(16+21),(17+20),(18+19) all sum 37
	props, err := Propose(members(15, 16, 17, 18, 19, 20, 21, 22), 4, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(props) == 0 || props[0].Spread != 0 {
		t.Fatalf("expected a perfect 4-way split, got %+v", props)
	}
}

func TestHillClimbFindsCloseSplit(t *testing.T) {
	// exercise the heuristic path directly
	rng := rand.New(rand.NewSource(1))
	m := members(15, 16, 17, 18, 19, 20, 21, 22)
	assign := randomAssignment(rng, 8, 2, teamSizes(8, 2))
	hillClimb(m, assign, 2, rng, false)
	if s := spread(m, assign, 2); s > 2 {
		t.Fatalf("hill climb spread %.1f", s)
	}
	assign = randomAssignment(rng, 8, 3, teamSizes(8, 3))
	hillClimb(m, assign, 3, rng, true)
	count := make([]int, 3)
	for _, t := range assign {
		count[t]++
	}
	for _, c := range count {
		if c == 0 {
			t.Fatal("uneven hill climb emptied a team")
		}
	}
}

func TestProposeUnevenSizes(t *testing.T) {
	// two very strong players vs five weak ones: with uneven sizes allowed the
	// best split is 2 vs 5 (sum 80 vs 75), which equal sizing cannot express.
	m := members(40, 40, 15, 15, 15, 15, 15)
	props, err := Propose(m, 2, Options{AllowUneven: true})
	if err != nil {
		t.Fatal(err)
	}
	best := props[0]
	if len(best.Teams[0])+len(best.Teams[1]) != 7 || best.Spread > 5.01 {
		t.Fatalf("uneven best: sizes %d/%d spread %.1f", len(best.Teams[0]), len(best.Teams[1]), best.Spread)
	}
	if len(best.Teams[0]) == len(best.Teams[1])+1 || len(best.Teams[1]) == len(best.Teams[0])+1 {
		t.Fatalf("expected a genuinely uneven split, got %d/%d", len(best.Teams[0]), len(best.Teams[1]))
	}
	even, err := Propose(m, 2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if even[0].Spread <= best.Spread {
		t.Fatalf("equal-size split should be worse here: %.1f vs %.1f", even[0].Spread, best.Spread)
	}

	// 7 players into 3 teams, 8 into 4: every team non-empty, within limits
	for _, tc := range []struct{ n, k int }{{7, 3}, {8, 4}, {5, 4}} {
		mus := make([]float64, tc.n)
		for i := range mus {
			mus[i] = 20 + float64(i)
		}
		ps, err := Propose(members(mus...), tc.k, Options{AllowUneven: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			if len(p.Teams) != tc.k {
				t.Fatalf("%d/%d: %d teams", tc.n, tc.k, len(p.Teams))
			}
			for _, team := range p.Teams {
				if len(team) == 0 {
					t.Fatalf("%d/%d: empty team in %+v", tc.n, tc.k, p)
				}
			}
		}
	}
}

func TestProposeLimits(t *testing.T) {
	nine := make([]float64, 9)
	if _, err := Propose(members(nine...), 2, Options{}); err == nil {
		t.Fatal("9 members should be rejected")
	}
	if _, err := Propose(members(1, 2, 3, 4, 5), 5, Options{}); err == nil {
		t.Fatal("5 teams should be rejected")
	}
}

func TestProposeErrors(t *testing.T) {
	if _, err := Propose(members(1, 2), 1, Options{}); err == nil {
		t.Fatal("expected error for k<2")
	}
	if _, err := Propose(members(1, 2), 3, Options{}); err == nil {
		t.Fatal("expected error for k>n")
	}
}
