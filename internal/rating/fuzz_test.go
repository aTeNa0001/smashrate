package rating

import (
	"math"
	"testing"
)

// FuzzRate builds line-ups from random bytes and checks the rating update
// never panics or produces NaN/Inf, keeps sigma positive, and moves the
// winner of a 1on1 between equals up and the loser down.
func FuzzRate(f *testing.F) {
	f.Add([]byte{2, 1, 1, 1, 2})
	f.Add([]byte{3, 2, 2, 1, 1, 1, 2, 3})
	f.Add([]byte{4, 1, 1, 1, 1, 1, 1, 2, 2})
	f.Add([]byte{8, 1, 1, 1, 1, 1, 1, 1, 1, 1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 1 {
			return
		}
		nTeams := int(data[0]%8) + 1
		data = data[1:]
		var teams []Team
		var ranks []int
		for i := 0; i < nTeams; i++ {
			size := 1
			if len(data) > 0 {
				size = int(data[0]%4) + 1
				data = data[1:]
			}
			var tm Team
			for j := 0; j < size; j++ {
				r := NewRating()
				if len(data) > 1 {
					// spread mu over roughly -50..+50 and sigma over 0.1..8
					r.Mu = DefaultMu + float64(int(data[0])-128)/2.5
					r.Sigma = 0.1 + float64(data[1])/32
					data = data[2:]
				}
				tm = append(tm, r)
			}
			teams = append(teams, tm)
			rank := 1
			if len(data) > 0 {
				rank = int(data[0]%uint8(nTeams)) + 1
				data = data[1:]
			}
			ranks = append(ranks, rank)
		}
		out, err := Rate(teams, ranks)
		if err != nil {
			if nTeams >= 2 {
				t.Fatalf("valid input rejected: %v (teams=%v ranks=%v)", err, teams, ranks)
			}
			return
		}
		if len(out) != len(teams) {
			t.Fatalf("got %d teams, want %d", len(out), len(teams))
		}
		for i, tm := range out {
			if len(tm) != len(teams[i]) {
				t.Fatalf("team %d changed size", i)
			}
			for _, r := range tm {
				if math.IsNaN(r.Mu) || math.IsInf(r.Mu, 0) || math.IsNaN(r.Sigma) || !(r.Sigma > 0) {
					t.Fatalf("broken rating %+v from teams=%v ranks=%v", r, teams, ranks)
				}
				if r.Sigma > DefaultSigma*1.01 {
					t.Fatalf("sigma grew to %v (prior %v)", r.Sigma, DefaultSigma)
				}
			}
		}
		// 1on1 between equals: winner up, loser down, by the same amount
		if nTeams == 2 && len(teams[0]) == 1 && len(teams[1]) == 1 && ranks[0] != ranks[1] {
			a, b := NewRating(), NewRating()
			res, _ := Rate([]Team{{a}, {b}}, ranks)
			w, l := res[0][0], res[1][0]
			if ranks[0] > ranks[1] {
				w, l = l, w
			}
			if !(w.Mu > DefaultMu) || !(l.Mu < DefaultMu) || math.Abs((w.Mu-DefaultMu)-(DefaultMu-l.Mu)) > 1e-9 {
				t.Fatalf("1on1 between equals: winner %v loser %v", w, l)
			}
		}
	})
}

// TestRatingIsDeterministicAndBounded pins properties the leaderboard
// relies on: the same match always gives the same update, a win never
// lowers the winner's displayed rating, and a long streak stays finite.
func TestRatingIsDeterministicAndBounded(t *testing.T) {
	a, b := NewRating(), NewRating()
	r1, _ := Rate([]Team{{a}, {b}}, []int{1, 2})
	r2, _ := Rate([]Team{{a}, {b}}, []int{1, 2})
	if r1[0][0] != r2[0][0] || r1[1][0] != r2[1][0] {
		t.Fatalf("not deterministic: %v vs %v", r1, r2)
	}
	for i := 0; i < 10000; i++ {
		res, err := Rate([]Team{{a}, {b}}, []int{1, 2})
		if err != nil {
			t.Fatal(err)
		}
		if res[0][0].Display() < a.Display() {
			t.Fatalf("game %d: winner's display fell from %d to %d", i, a.Display(), res[0][0].Display())
		}
		a, b = res[0][0], res[1][0]
	}
	if math.IsInf(a.Mu, 0) || math.IsNaN(a.Mu) || a.Sigma <= 0 || b.Sigma <= 0 {
		t.Fatalf("after 10000 wins: %v / %v", a, b)
	}
}
