package vision

import "testing"

func TestFillMissingRankInfersTheLeftoverPlacement(t *testing.T) {
	slots := func(ranks ...int) *Reading {
		r := &Reading{Players: len(ranks)}
		for i, n := range ranks {
			r.Slots = append(r.Slots, SlotReading{Port: i + 1, Rank: n, RankHash: 1})
		}
		return r
	}
	cases := []struct {
		in   []int
		want []int
	}{
		{[]int{0, 2}, []int{1, 2}},             // 1v1: the unread gold "1"
		{[]int{1, 0}, []int{1, 2}},             // 1v1: the loser's numeral unread
		{[]int{2, 0, 3, 1}, []int{2, 4, 3, 1}}, // 4 players, one missing
		{[]int{0, 0}, []int{0, 0}},             // two unknowns: nothing to infer
		{[]int{1, 1, 0}, []int{1, 1, 0}},       // teams share placements: do not guess
		{[]int{1, 2}, []int{1, 2}},             // nothing missing
		{[]int{3, 0}, []int{3, 0}},             // a misread beyond the player count
	}
	for _, c := range cases {
		r := slots(c.in...)
		fillMissingRank(r)
		for i, want := range c.want {
			if r.Slots[i].Rank != want {
				t.Errorf("%v: slot %d = %d, want %d", c.in, i, r.Slots[i].Rank, want)
			}
			if inferred := c.in[i] == 0 && want != 0; r.Slots[i].Inferred != inferred {
				t.Errorf("%v: slot %d inferred=%v", c.in, i, r.Slots[i].Inferred)
			}
		}
	}
}
