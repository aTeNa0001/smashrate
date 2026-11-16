package parse

import (
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
)

// FuzzMatch feeds arbitrary text to the placement parser: it must never
// panic, and whatever it accepts must be a well-formed line-up (two or
// more entries with names, teams and ranks starting at 1, a winner).
func FuzzMatch(f *testing.F) {
	for _, s := range []string{"A > B", "A:mario > B:link", "A > B > C > D", "A+B > C+D", "A > B = C", "<@U1|taro> > <@U2>",
		"", ">", "A", "A >", "> B", "A > A", "A+A > B", "A = B", "A>B>C=D>E+F", "太郎:マリオ > 花子:リンク", "a:b:c > d", "A + > B", "A >> B"} {
		f.Add(s)
	}
	resolve := func(id string) (string, bool) { return "user-" + id, id != "NOPE" }
	f.Fuzz(func(t *testing.T, text string) {
		entries, err := Match(text, resolve)
		if err != nil {
			if entries != nil {
				t.Fatalf("error with entries: %q -> %v, %v", text, entries, err)
			}
			return
		}
		if len(entries) == 0 {
			t.Fatalf("accepted %q with no entries", text)
		}
		if len(entries) == 1 {
			// a lone name is valid: the Slack bot uses it for `stats <name>`
			if strings.TrimSpace(entries[0].Player) == "" {
				t.Fatalf("accepted %q as an empty name", text)
			}
			return
		}
		winner := false
		for _, e := range entries {
			if strings.TrimSpace(e.Player) == "" || e.Team < 1 || e.Rank < 1 {
				t.Fatalf("accepted %q with a malformed entry %+v", text, e)
			}
			if e.Rank == 1 {
				winner = true
			}
		}
		if !winner {
			t.Fatalf("accepted %q without a winner: %+v", text, entries)
		}
		if core.DetectMode(entries) == "" {
			t.Fatalf("no mode for %q", text)
		}
	})
}
