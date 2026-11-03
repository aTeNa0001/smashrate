package parse

import (
	"testing"

	"github.com/aTeNa0001/smashrate/internal/core"
)

func TestMatchSyntax(t *testing.T) {
	es, err := Match("Taro:マリオ > Hanako(link)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Character != "mario" || es[1].Character != "link" || es[0].Rank != 1 || es[1].Rank != 2 || es[0].Team == es[1].Team {
		t.Fatalf("%+v", es)
	}
	if core.DetectMode(es) != core.Mode1v1 {
		t.Fatal("expected 1v1")
	}

	es, err = Match("A+B > C+D", nil)
	if err != nil {
		t.Fatal(err)
	}
	if core.DetectMode(es) != core.ModeTeam || es[0].Team != es[1].Team || es[2].Team != es[3].Team || es[2].Rank != 2 {
		t.Fatalf("%+v", es)
	}

	es, err = Match("A > B = C > D", nil)
	if err != nil {
		t.Fatal(err)
	}
	if core.DetectMode(es) != core.ModeFFA || es[1].Rank != 2 || es[2].Rank != 2 || es[3].Rank != 3 || es[1].Team == es[2].Team {
		t.Fatalf("%+v", es)
	}
}

func TestMatchMentions(t *testing.T) {
	resolve := func(id string) (string, bool) {
		if id == "U1" {
			return "Taro", true
		}
		return "", false
	}
	es, err := Match("<@U1> > <@U2|hanako>", resolve)
	if err != nil {
		t.Fatal(err)
	}
	if es[0].Player != "Taro" || es[1].Player != "hanako" {
		t.Fatalf("%+v", es)
	}
	if _, err := Match("<@U3> > A", resolve); err == nil {
		t.Fatal("unlinked mention without label should fail")
	}
}

func TestMatchErrors(t *testing.T) {
	for _, s := range []string{"", "A >", "A > B:nosuchchar", "> A"} {
		if _, err := Match(s, nil); err == nil {
			t.Fatalf("%q should fail", s)
		}
	}
}

func TestPorts(t *testing.T) {
	m, err := Ports("P1=Taro p2:Hanako 3=Jiro")
	if err != nil {
		t.Fatal(err)
	}
	if m[1] != "Taro" || m[2] != "Hanako" || m[3] != "Jiro" {
		t.Fatalf("%v", m)
	}
	if _, err := Ports("Taro"); err == nil {
		t.Fatal("expected error")
	}
}
