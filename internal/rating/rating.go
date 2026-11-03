// Package rating implements the Plackett-Luce rating model (the default model
// of OpenSkill, a TrueSkill-compatible Bayesian skill rating system).
//
// It supports an arbitrary number of teams of arbitrary sizes and partial
// ties, which covers every Smash Bros. local format we care about:
// 1v1, free-for-all (every player is their own team) and team battles.
package rating

import (
	"errors"
	"math"
)

const (
	// DefaultMu is the prior mean skill of a new player.
	DefaultMu = 25.0
	// DefaultSigma is the prior standard deviation of a new player.
	DefaultSigma = DefaultMu / 3.0
	// Beta is the skill-class width (how much "luck" a single game has).
	Beta = DefaultSigma / 2.0
	// Kappa prevents sigma from collapsing to zero.
	Kappa = 0.0001
	// Z is the number of standard deviations used for the conservative ordinal.
	Z = 3.0

	// DisplayBase / DisplayScale map the ordinal onto a familiar "Elo-looking"
	// number. A brand-new player shows as DisplayBase.
	DisplayBase  = 1500.0
	DisplayScale = 25.0
)

// Rating is one player's belief state.
type Rating struct {
	Mu    float64
	Sigma float64
}

// NewRating returns the prior for an unseen player.
func NewRating() Rating { return Rating{Mu: DefaultMu, Sigma: DefaultSigma} }

// Ordinal is a conservative skill estimate (mu - Z*sigma). It is 0 for a new
// player and grows as the system becomes confident the player is strong.
func (r Rating) Ordinal() float64 { return r.Mu - Z*r.Sigma }

// Display converts the ordinal into the number shown to humans.
func (r Rating) Display() int { return int(math.Round(DisplayBase + DisplayScale*r.Ordinal())) }

// Team is a group of ratings that played together.
type Team []Rating

type teamStat struct {
	mu      float64
	sigmaSq float64
	rank    int
}

func summarize(teams []Team, ranks []int) []teamStat {
	out := make([]teamStat, len(teams))
	for i, t := range teams {
		var mu, s2 float64
		for _, r := range t {
			mu += r.Mu
			s2 += r.Sigma * r.Sigma
		}
		out[i] = teamStat{mu: mu, sigmaSq: s2, rank: ranks[i]}
	}
	return out
}

// Rate returns the updated ratings for each team given the final placement.
//
// ranks[i] is the placement of teams[i]: 1 for the winner, 2 for second, ...
// Equal ranks denote a tie. Lower is better. The input is not mutated.
func Rate(teams []Team, ranks []int) ([]Team, error) {
	if len(teams) < 2 {
		return nil, errors.New("rating: need at least two teams")
	}
	if len(teams) != len(ranks) {
		return nil, errors.New("rating: teams and ranks length mismatch")
	}
	for i, t := range teams {
		if len(t) == 0 {
			return nil, errors.New("rating: empty team")
		}
		if ranks[i] < 1 {
			return nil, errors.New("rating: ranks must be >= 1")
		}
	}

	ts := summarize(teams, ranks)
	n := len(ts)

	var c float64
	for _, t := range ts {
		c += t.sigmaSq + Beta*Beta
	}
	c = math.Sqrt(c)

	// sumQ[q] = sum over teams i that placed equal or worse than q of exp(mu_i / c)
	sumQ := make([]float64, n)
	for i := range ts {
		e := math.Exp(ts[i].mu / c)
		for q := range ts {
			if ts[i].rank >= ts[q].rank {
				sumQ[q] += e
			}
		}
	}
	// a[i] = number of teams sharing team i's rank (tie multiplicity)
	a := make([]float64, n)
	for i := range ts {
		for q := range ts {
			if ts[i].rank == ts[q].rank {
				a[i]++
			}
		}
	}

	out := make([]Team, n)
	for i, ti := range ts {
		var omega, delta float64
		iMuOverC := math.Exp(ti.mu / c)
		for q, tq := range ts {
			if tq.rank > ti.rank {
				continue // q placed worse than i: no term
			}
			p := iMuOverC / sumQ[q]
			delta += p * (1 - p) / a[q]
			if q == i {
				omega += (1 - p) / a[q]
			} else {
				omega -= p / a[q]
			}
		}
		omega *= ti.sigmaSq / c
		delta *= ti.sigmaSq / (c * c)
		gamma := math.Sqrt(ti.sigmaSq) / c
		delta *= gamma

		out[i] = make(Team, len(teams[i]))
		for j, r := range teams[i] {
			s2 := r.Sigma * r.Sigma
			nr := Rating{
				Mu:    r.Mu + (s2/ti.sigmaSq)*omega,
				Sigma: r.Sigma * math.Sqrt(math.Max(1-(s2/ti.sigmaSq)*delta, Kappa)),
			}
			out[i][j] = nr
		}
	}
	return out, nil
}

// PredictWin returns, for each team, the probability that it finishes first.
// Probabilities sum to 1.
func PredictWin(teams []Team) []float64 {
	n := len(teams)
	if n == 1 {
		return []float64{1}
	}
	ranks := make([]int, n)
	for i := range ranks {
		ranks[i] = 1
	}
	ts := summarize(teams, ranks)
	denom := float64(n*(n-1)) / 2
	out := make([]float64, n)
	for i := range ts {
		var p float64
		for q := range ts {
			if q == i {
				continue
			}
			p += normCDF((ts[i].mu - ts[q].mu) / math.Sqrt(float64(n)*Beta*Beta+ts[i].sigmaSq+ts[q].sigmaSq))
		}
		out[i] = p / denom
	}
	return out
}

func normCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
