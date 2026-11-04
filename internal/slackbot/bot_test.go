package slackbot

import (
	"strings"
	"testing"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
)

func newBot(t *testing.T) (*Bot, *core.Core) {
	t.Helper()
	c, err := core.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	cfg := config.Default().Slack
	cfg.BotToken, cfg.AppToken = "xoxb-test", "xapp-test"
	return New(cfg, "http://example.test", c, nil), c
}

func TestRecordRankStatsHistory(t *testing.T) {
	b, c := newBot(t)
	out, inChannel := b.Handle("record 太郎:マリオ > 花子:リンク", "U1")
	if !inChannel || !strings.Contains(out, "記録しました") || !strings.Contains(out, "太郎:マリオ(+") {
		t.Fatalf("record: %q", out)
	}
	out, _ = b.Handle("record A+B > C+D", "U1")
	if !strings.Contains(out, "[チーム]") {
		t.Fatalf("team record: %q", out)
	}
	out, _ = b.Handle("rank", "U1")
	if !strings.Contains(out, "1. 太郎") || !strings.Contains(out, "http://example.test/") {
		t.Fatalf("rank: %q", out)
	}
	out, _ = b.Handle("rank team", "U1")
	if strings.Contains(out, "太郎") || !strings.Contains(out, "A") {
		t.Fatalf("rank team should only list team-battle players: %q", out)
	}
	out, _ = b.Handle("stats 太郎", "U1")
	if !strings.Contains(out, "レート") || !strings.Contains(out, "マリオ") || !strings.Contains(out, "1on1") {
		t.Fatalf("stats: %q", out)
	}
	out, _ = b.Handle("history 1", "U1")
	if !strings.Contains(out, "#2") || strings.Contains(out, "#1 ") {
		t.Fatalf("history: %q", out)
	}
	ms, _ := c.Matches(10, 0)
	if len(ms) != 2 {
		t.Fatalf("matches = %d", len(ms))
	}
}

func TestLinkAndMentions(t *testing.T) {
	b, _ := newBot(t)
	out, _ := b.Handle("stats", "U9")
	if !strings.Contains(out, "link") {
		t.Fatalf("unlinked stats should explain link: %q", out)
	}
	if out, _ = b.Handle("link 太郎", "U9"); !strings.Contains(out, "紐付けました") {
		t.Fatalf("link: %q", out)
	}
	if out, _ = b.Handle("record <@U9> > 花子", "U9"); !strings.Contains(out, "太郎") {
		t.Fatalf("mention record: %q", out)
	}
	if out, _ = b.Handle("stats", "U9"); !strings.Contains(out, "*太郎*") {
		t.Fatalf("own stats: %q", out)
	}
}

func TestRenameMerge(t *testing.T) {
	b, c := newBot(t)
	b.Handle("record Hanak0 > Taro", "U1")
	b.Handle("record Hanako > Taro", "U1")
	if out, _ := b.Handle("rename Hanak0 Hanako", "U1"); !strings.Contains(out, "変更できません") {
		t.Fatalf("rename onto existing: %q", out)
	}
	if out, _ := b.Handle("rename Hanak0 Hana", "U1"); !strings.Contains(out, "変更しました") {
		t.Fatalf("rename: %q", out)
	}
	if out, _ := b.Handle("merge Hana Hanako", "U1"); !strings.Contains(out, "統合") {
		t.Fatalf("merge: %q", out)
	}
	h, _ := c.PlayerByName("Hanako")
	if r := c.PlayerRating(h.ID, core.ScopeOverall); r.Games != 2 {
		t.Fatalf("merged games = %d", r.Games)
	}
}

func TestBalanceFixDeleteHelp(t *testing.T) {
	b, c := newBot(t)
	b.Handle("record 太郎:マリオ > 花子:リンク", "U1")
	out, _ := b.Handle("balance 2 太郎 花子 けん みゆ りょう さくら uneven", "U1")
	if !strings.Contains(out, "案1") || !strings.Contains(out, "チーム1") || !strings.Contains(out, "人") {
		t.Fatalf("balance: %q", out)
	}
	if out, _ = b.Handle("balance 5 a b c d e", "U1"); !strings.Contains(out, "error") {
		t.Fatalf("5 teams: %q", out)
	}
	if out, _ = b.Handle("fix 1 太郎 ルイージ", "U1"); !strings.Contains(out, "ルイージ") {
		t.Fatalf("fix: %q", out)
	}
	if out, _ = b.Handle("delete 1", "U1"); !strings.Contains(out, "再計算") {
		t.Fatalf("delete: %q", out)
	}
	if ms, _ := c.Matches(10, 0); len(ms) != 0 {
		t.Fatal("match should be deleted")
	}
	if out, _ = b.Handle("", "U1"); !strings.Contains(out, "使い方") {
		t.Fatalf("help: %q", out)
	}
	if out, _ = b.Handle("nosuch", "U1"); !strings.Contains(out, "不明なコマンド") {
		t.Fatalf("unknown: %q", out)
	}
	if out, _ = b.Handle("learn x y", "U1"); !strings.Contains(out, "無効") {
		t.Fatalf("learn without vision: %q", out)
	}
}
