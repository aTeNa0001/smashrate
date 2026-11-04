// Package balance proposes evenly matched team splits for a set of players.
package balance

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/aTeNa0001/smashrate/internal/rating"
)

// Member is a player to be placed on a team.
type Member struct {
	Name   string
	Rating rating.Rating
}

// Proposal is one candidate split.
type Proposal struct {
	Teams    [][]Member
	Strength []float64 // sum of mu per team
	WinProb  []float64 // predicted probability each team wins
	Spread   float64   // max strength - min strength (lower is better)
}

// Options tunes the search.
type Options struct {
	// AllowUneven permits team sizes that differ by more than one.
	// By default every team has n/k or n/k+1 members.
	AllowUneven bool
	// TopN is the number of proposals to return (default 3).
	TopN int
	// Seed makes the heuristic search reproducible when > 0.
	Seed int64
}

const bruteForceLimit = 2_000_000

// Smash Bros. Ultimate limits: up to 8 fighters, up to 4 teams.
const (
	MaxMembers = 8
	MaxTeams   = 4
)

// Propose returns the most balanced ways to split members into k teams,
// best first. For small inputs every split is enumerated; larger ones use
// repeated randomized hill climbing.
//
// With opt.AllowUneven, team sizes may differ freely (e.g. 7 players as
// 4/2/1): strength is the sum of mu, so a smaller team is only proposed when
// its members are strong enough to make up for the missing player.
func Propose(members []Member, k int, opt Options) ([]Proposal, error) {
	n := len(members)
	if k < 2 {
		return nil, errors.New("balance: need at least two teams")
	}
	if k > MaxTeams {
		return nil, fmt.Errorf("balance: at most %d teams", MaxTeams)
	}
	if n > MaxMembers {
		return nil, fmt.Errorf("balance: at most %d members", MaxMembers)
	}
	if n < k {
		return nil, errors.New("balance: more teams than members")
	}
	if opt.TopN <= 0 {
		opt.TopN = 3
	}
	sizes := teamSizes(n, k)

	var results []Proposal
	seen := map[string]bool{}
	add := func(assign []int) {
		key := canonicalKey(assign, k)
		if seen[key] {
			return
		}
		seen[key] = true
		results = append(results, build(members, assign, k))
	}

	if math.Pow(float64(k), float64(n)) <= bruteForceLimit {
		enumerate(n, k, sizes, !opt.AllowUneven, add)
	} else {
		rng := rand.New(rand.NewSource(opt.Seed))
		if opt.Seed == 0 {
			rng = rand.New(rand.NewSource(rand.Int63()))
		}
		for restart := 0; restart < 200; restart++ {
			assign := randomAssignment(rng, n, k, sizes)
			hillClimb(members, assign, k, rng, opt.AllowUneven)
			add(assign)
		}
	}

	sort.SliceStable(results, func(i, j int) bool { return results[i].Spread < results[j].Spread })
	if len(results) > opt.TopN {
		results = results[:opt.TopN]
	}
	return results, nil
}

func teamSizes(n, k int) []int {
	sizes := make([]int, k)
	for i := range sizes {
		sizes[i] = n / k
	}
	for i := 0; i < n%k; i++ {
		sizes[i]++
	}
	return sizes
}

func enumerate(n, k int, sizes []int, equal bool, visit func([]int)) {
	assign := make([]int, n)
	count := make([]int, k)
	var rec func(i int)
	rec = func(i int) {
		if i == n {
			if equal {
				if !sameMultiset(count, sizes) {
					return
				}
			} else {
				for _, c := range count {
					if c == 0 {
						return
					}
				}
			}
			visit(append([]int(nil), assign...))
			return
		}
		for t := 0; t < k; t++ {
			if equal && count[t] >= maxInts(sizes) {
				continue
			}
			// symmetry breaking: player 0 always on team 0, team t+1 only after team t used
			if t > 0 && count[t-1] == 0 {
				break
			}
			assign[i] = t
			count[t]++
			rec(i + 1)
			count[t]--
		}
	}
	rec(0)
}

func sameMultiset(a, b []int) bool {
	x := append([]int(nil), a...)
	y := append([]int(nil), b...)
	sort.Ints(x)
	sort.Ints(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func maxInts(v []int) int {
	m := v[0]
	for _, x := range v {
		if x > m {
			m = x
		}
	}
	return m
}

func randomAssignment(rng *rand.Rand, n, k int, sizes []int) []int {
	pool := make([]int, 0, n)
	for t, s := range sizes {
		for i := 0; i < s; i++ {
			pool = append(pool, t)
		}
	}
	rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	return pool
}

func spread(members []Member, assign []int, k int) float64 {
	str := make([]float64, k)
	for i, t := range assign {
		str[t] += members[i].Rating.Mu
	}
	lo, hi := str[0], str[0]
	for _, s := range str {
		lo = math.Min(lo, s)
		hi = math.Max(hi, s)
	}
	return hi - lo
}

func hillClimb(members []Member, assign []int, k int, rng *rand.Rand, uneven bool) {
	n := len(assign)
	cur := spread(members, assign, k)
	count := make([]int, k)
	for _, t := range assign {
		count[t]++
	}
	for iter := 0; iter < 400; iter++ {
		i := rng.Intn(n)
		if uneven && rng.Intn(2) == 0 {
			// move one player to another team, keeping every team non-empty
			from, to := assign[i], rng.Intn(k)
			if from == to || count[from] == 1 {
				continue
			}
			assign[i] = to
			if s := spread(members, assign, k); s < cur {
				cur = s
				count[from]--
				count[to]++
			} else {
				assign[i] = from
			}
			continue
		}
		j := rng.Intn(n)
		if assign[i] == assign[j] {
			continue
		}
		assign[i], assign[j] = assign[j], assign[i]
		if s := spread(members, assign, k); s < cur {
			cur = s
		} else {
			assign[i], assign[j] = assign[j], assign[i]
		}
	}
}

// canonicalKey relabels teams in order of first appearance so that
// permutations of team numbers compare equal.
func canonicalKey(assign []int, k int) string {
	relabel := make([]int, k)
	for i := range relabel {
		relabel[i] = -1
	}
	next := 0
	b := make([]byte, len(assign))
	for i, t := range assign {
		if relabel[t] < 0 {
			relabel[t] = next
			next++
		}
		b[i] = byte('a' + relabel[t])
	}
	return string(b)
}

func build(members []Member, assign []int, k int) Proposal {
	p := Proposal{Teams: make([][]Member, k), Strength: make([]float64, k)}
	rteams := make([]rating.Team, k)
	for i, t := range assign {
		p.Teams[t] = append(p.Teams[t], members[i])
		p.Strength[t] += members[i].Rating.Mu
		rteams[t] = append(rteams[t], members[i].Rating)
	}
	// drop empty teams (only possible when EqualSizes is false and k > n)
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range p.Strength {
		lo = math.Min(lo, s)
		hi = math.Max(hi, s)
	}
	p.Spread = hi - lo
	p.WinProb = rating.PredictWin(rteams)
	// present teams strongest first for readability
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return p.Strength[idx[a]] > p.Strength[idx[b]] })
	q := Proposal{Spread: p.Spread}
	for _, i := range idx {
		q.Teams = append(q.Teams, p.Teams[i])
		q.Strength = append(q.Strength, p.Strength[i])
		q.WinProb = append(q.WinProb, p.WinProb[i])
	}
	return q
}
