package rating

import (
	"math"
	"testing"
)

func approx(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s: got %.6f want %.6f", name, got, want)
	}
}

// Reference values computed with the openskill Python package (PlackettLuce,
// default parameters): model.rate([[r],[r]], ranks=[1,2]).
func TestRate1v1MatchesOpenSkill(t *testing.T) {
	a, b := NewRating(), NewRating()
	out, err := Rate([]Team{{a}, {b}}, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	approx(t, "winner mu", out[0][0].Mu, 27.6352, 1e-3)
	approx(t, "winner sigma", out[0][0].Sigma, 8.0659, 1e-3)
	approx(t, "loser mu", out[1][0].Mu, 22.3648, 1e-3)
	approx(t, "loser sigma", out[1][0].Sigma, 8.0659, 1e-3)
}

func TestRateFFAOrdering(t *testing.T) {
	teams := []Team{{NewRating()}, {NewRating()}, {NewRating()}, {NewRating()}}
	out, err := Rate(teams, []int{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < 4; i++ {
		if out[i-1][0].Mu <= out[i][0].Mu {
			t.Fatalf("place %d should have higher mu than place %d", i, i+1)
		}
	}
	// zero-sum-ish: total mu is preserved for symmetric priors
	var sum float64
	for _, tm := range out {
		sum += tm[0].Mu
	}
	approx(t, "total mu", sum, 100, 1e-6)
}

func TestRateTeamsAndTies(t *testing.T) {
	strong := Rating{Mu: 35, Sigma: 4}
	out, err := Rate([]Team{{strong, strong}, {NewRating(), NewRating()}}, []int{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	// A tie between a favoured team and an even team pulls the favoured team down.
	if out[0][0].Mu >= strong.Mu {
		t.Fatalf("strong player should lose mu on a tie against weaker team: %v", out[0][0])
	}
	// Sigma never grows and never hits zero.
	for _, tm := range out {
		for _, r := range tm {
			if r.Sigma <= 0 || r.Sigma > DefaultSigma {
				t.Fatalf("sigma out of range: %v", r)
			}
		}
	}
}

func TestPredictWin(t *testing.T) {
	even := PredictWin([]Team{{NewRating()}, {NewRating()}})
	approx(t, "even", even[0], 0.5, 1e-9)
	p := PredictWin([]Team{{Rating{Mu: 35, Sigma: 3}}, {Rating{Mu: 20, Sigma: 3}}})
	if p[0] < 0.9 {
		t.Fatalf("strong player should be a heavy favourite: %v", p)
	}
	approx(t, "sum", p[0]+p[1], 1, 1e-9)
}

func TestDisplay(t *testing.T) {
	if NewRating().Display() != 1500 {
		t.Fatalf("new player should display 1500, got %d", NewRating().Display())
	}
}

func TestRateErrors(t *testing.T) {
	if _, err := Rate([]Team{{NewRating()}}, []int{1}); err == nil {
		t.Fatal("expected error for single team")
	}
	if _, err := Rate([]Team{{NewRating()}, {}}, []int{1, 2}); err == nil {
		t.Fatal("expected error for empty team")
	}
}
