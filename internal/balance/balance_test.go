package balance

import (
	"fmt"
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

func TestProposeLargeUsesHeuristic(t *testing.T) {
	mus := make([]float64, 20)
	for i := range mus {
		mus[i] = 15 + float64(i)
	}
	props, err := Propose(members(mus...), 4, Options{Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(props) == 0 || props[0].Spread > 4 {
		t.Fatalf("heuristic should find a close split, got %+v", props)
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
