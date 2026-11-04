// Package slackbot runs the Socket Mode slash-command bot. Socket Mode needs
// no public URL, which keeps the mini PC behind the home router.
package slackbot

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/aTeNa0001/smashrate/internal/balance"
	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/parse"
)

// Learner lets the bot label unknown vision crops; nil disables `learn`.
type Learner interface {
	Learn(cropID string, label string) error
	Pending() []string
}

// Bot is the Slack adapter.
type Bot struct {
	cfg     config.SlackConfig
	baseURL string
	core    *core.Core
	api     *slack.Client
	sm      *socketmode.Client
	learner Learner
}

// New creates a bot. It does not connect until Run.
func New(cfg config.SlackConfig, baseURL string, c *core.Core, learner Learner) *Bot {
	api := slack.New(cfg.BotToken, slack.OptionAppLevelToken(cfg.AppToken))
	return &Bot{cfg: cfg, baseURL: baseURL, core: c, api: api, sm: socketmode.New(api), learner: learner}
}

// Notify posts a message to the configured channel (used by the vision watcher).
func (b *Bot) Notify(text string) {
	if b.cfg.Channel == "" {
		return
	}
	if _, _, err := b.api.PostMessage(b.cfg.Channel, slack.MsgOptionText(text, false)); err != nil {
		log.Printf("slack notify: %v", err)
	}
}

// Run blocks until ctx is done.
func (b *Bot) Run(ctx context.Context) error {
	go func() {
		for evt := range b.sm.Events {
			switch evt.Type {
			case socketmode.EventTypeSlashCommand:
				cmd, ok := evt.Data.(slack.SlashCommand)
				if !ok {
					continue
				}
				text, inChannel := b.handle(cmd)
				payload := map[string]any{"text": text}
				if inChannel {
					payload["response_type"] = "in_channel"
				}
				b.sm.Ack(*evt.Request, payload)
			case socketmode.EventTypeConnected:
				log.Printf("slack: connected")
			case socketmode.EventTypeConnectionError:
				log.Printf("slack: connection error: %v", evt.Data)
			}
		}
	}()
	return b.sm.RunContext(ctx)
}

// Handle runs one command line; exported for tests and the CLI "chat" mode.
func (b *Bot) handle(cmd slack.SlashCommand) (string, bool) {
	return b.Handle(cmd.Text, cmd.UserID)
}

// Handle executes a command string on behalf of a Slack user.
func (b *Bot) Handle(text, userID string) (string, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return help(b.cfg.Command), false
	}
	sub := strings.ToLower(fields[0])
	rest := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	switch sub {
	case "help":
		return help(b.cfg.Command), false
	case "rank", "ranking", "lb":
		return b.ranking(rest), true
	case "stats", "me":
		return b.stats(rest, userID), true
	case "history", "log":
		return b.history(rest), false
	case "record", "r":
		return b.record(rest, userID), true
	case "balance", "team", "teams":
		return b.balance(rest), true
	case "ports", "port":
		return b.ports(rest), true
	case "link":
		return b.link(rest, userID), false
	case "fix":
		return b.fix(rest), true
	case "delete", "del":
		return b.del(rest), true
	case "learn":
		return b.learn(rest), false
	case "chars", "characters":
		return charList(), false
	}
	return "不明なコマンド: " + sub + "\n" + help(b.cfg.Command), false
}

func help(cmd string) string {
	return strings.Join([]string{
		"*" + cmd + " の使い方*",
		"`" + cmd + " rank` ランキング",
		"`" + cmd + " stats [名前]` 成績（省略時は自分）",
		"`" + cmd + " history [件数]` 直近の対戦",
		"`" + cmd + " record A:マリオ > B:リンク` 1on1 を記録（A の勝ち）",
		"`" + cmd + " record A > B > C > D` 乱闘の順位を記録、`A > B = C` は同率",
		"`" + cmd + " record A+B > C+D` チーム戦を記録",
		"`" + cmd + " balance 2 A B C D E F` チーム分けを提案（2〜4チーム、最大8人。末尾に uneven で人数差を許容）",
		"`" + cmd + " ports P1=A P2=B` 名前タグが未学習のときの応急処置（ポートの持ち主を仮設定）",
		"`" + cmd + " link 名前` 自分の Slack をプレイヤーに紐付け",
		"`" + cmd + " fix <試合ID> <名前> <キャラ>` キャラの誤認識を修正",
		"`" + cmd + " delete <試合ID>` 対戦を削除して再計算",
		"`" + cmd + " learn <画像ID> <キャラ|1st..4th|プレイヤー名>` 未知の画像（キャラ・順位・名前タグ）にラベルを付けて学習",
	}, "\n")
}

func (b *Bot) resolver() parse.Resolver {
	return func(id string) (string, bool) {
		p, err := b.core.PlayerBySlackID(id)
		if err != nil {
			return "", false
		}
		return p.Name, true
	}
}

func (b *Bot) ranking(arg string) string {
	scope := core.ScopeOverall
	switch strings.ToLower(arg) {
	case "1v1", "1on1":
		scope = core.ScopeMode(core.Mode1v1)
	case "ffa", "乱闘":
		scope = core.ScopeMode(core.ModeFFA)
	case "team", "チーム":
		scope = core.ScopeMode(core.ModeTeam)
	}
	lb, err := b.core.Leaderboard(scope)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(lb) == 0 {
		return "まだ対戦がありません。"
	}
	var sb strings.Builder
	sb.WriteString("*ランキング*")
	if scope != core.ScopeOverall {
		sb.WriteString(" (" + arg + ")")
	}
	sb.WriteString("\n")
	for i, r := range lb {
		if i >= 15 {
			break
		}
		fmt.Fprintf(&sb, "%2d. %-12s %4d  %d勝%d敗 (%.0f%%)\n", i+1, r.Player.Name, r.Display, r.Wins, r.Games-r.Wins, r.WinRate()*100)
	}
	sb.WriteString(b.baseURL + "/")
	return "```" + sb.String() + "```"
}

func (b *Bot) stats(arg, userID string) string {
	name := arg
	if name == "" {
		p, err := b.core.PlayerBySlackID(userID)
		if err != nil {
			return "名前を指定するか、`link 名前` で自分を紐付けてください。"
		}
		name = p.Name
	}
	if m := strings.TrimSpace(name); strings.HasPrefix(m, "<@") {
		if es, err := parse.Match(m, b.resolver()); err == nil && len(es) == 1 {
			name = es[0].Player
		}
	}
	st, err := b.core.Stats(name)
	if err != nil {
		return "プレイヤーが見つかりません: " + name
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "*%s*  レート %d", st.Player.Name, st.Overall.Display)
	if st.Rank > 0 {
		fmt.Fprintf(&sb, "（%d位）", st.Rank)
	}
	fmt.Fprintf(&sb, "  %d勝%d敗 勝率%.0f%%", st.Overall.Wins, st.Overall.Games-st.Overall.Wins, st.Overall.WinRate()*100)
	if st.CurrentStreak > 1 {
		fmt.Fprintf(&sb, "  %d連勝中", st.CurrentStreak)
	} else if st.CurrentStreak < -1 {
		fmt.Fprintf(&sb, "  %d連敗中", -st.CurrentStreak)
	}
	sb.WriteString("\n")
	for _, m := range st.Modes {
		fmt.Fprintf(&sb, "• %s: %d (%d勝/%d戦)\n", modeName(m.Mode), m.Display, m.Wins, m.Games)
	}
	for i, c := range st.Characters {
		if i >= 5 {
			break
		}
		fmt.Fprintf(&sb, "• %s: %d戦 勝率%.0f%% キャラ別%d\n", c.Name, c.Games, c.WinRate()*100, c.Display)
	}
	fmt.Fprintf(&sb, "%s/players/%s", b.baseURL, st.Player.Name)
	return sb.String()
}

func modeName(m string) string {
	switch m {
	case core.Mode1v1:
		return "1on1"
	case core.ModeFFA:
		return "乱闘"
	case core.ModeTeam:
		return "チーム"
	}
	return m
}

func (b *Bot) history(arg string) string {
	n, _ := strconv.Atoi(arg)
	if n <= 0 {
		n = 5
	}
	ms, err := b.core.Matches(n, 0)
	if err != nil {
		return "error: " + err.Error()
	}
	if len(ms) == 0 {
		return "まだ対戦がありません。"
	}
	var sb strings.Builder
	for _, m := range ms {
		sb.WriteString(formatMatch(m))
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatMatch renders one match as a single Slack line.
func formatMatch(m core.Match) string {
	var parts []string
	rank := 0
	var group []string
	flush := func() {
		if len(group) > 0 {
			parts = append(parts, strings.Join(group, " = "))
			group = nil
		}
	}
	// entries are ordered by team; group by rank then team
	byTeam := map[int][]string{}
	teamRank := map[int]int{}
	var teamOrder []int
	for _, e := range m.Entries {
		if _, ok := byTeam[e.Team]; !ok {
			teamOrder = append(teamOrder, e.Team)
		}
		s := e.PlayerName
		if e.Character != "" {
			s += ":" + e.CharacterName()
		}
		if e.DisplayAfter != 0 || e.DisplayBefore != 0 {
			s += fmt.Sprintf("(%+d)", e.DisplayAfter-e.DisplayBefore)
		}
		byTeam[e.Team] = append(byTeam[e.Team], s)
		teamRank[e.Team] = e.Rank
	}
	// sort teams by rank
	for i := 0; i < len(teamOrder); i++ {
		for j := i + 1; j < len(teamOrder); j++ {
			if teamRank[teamOrder[j]] < teamRank[teamOrder[i]] {
				teamOrder[i], teamOrder[j] = teamOrder[j], teamOrder[i]
			}
		}
	}
	for _, t := range teamOrder {
		if teamRank[t] != rank {
			flush()
			rank = teamRank[t]
		}
		group = append(group, strings.Join(byTeam[t], "+"))
	}
	flush()
	return fmt.Sprintf("#%d %s [%s] %s", m.ID, m.PlayedAt.Local().Format("01/02 15:04"), modeName(m.Mode), strings.Join(parts, " > "))
}

func (b *Bot) record(arg, userID string) string {
	es, err := parse.Match(arg, b.resolver())
	if err != nil {
		return "記録できません: " + err.Error()
	}
	m, err := b.core.RecordMatch(core.MatchInput{Source: "slack", Entries: es})
	if err != nil {
		return "記録できません: " + err.Error()
	}
	return "記録しました " + formatMatch(*m)
}

func (b *Bot) balance(arg string) string {
	fields := strings.Fields(arg)
	uneven := false
	var rest []string
	for _, f := range fields {
		switch strings.ToLower(f) {
		case "uneven", "不均等", "人数差":
			uneven = true
		default:
			rest = append(rest, f)
		}
	}
	fields = rest
	if len(fields) < 3 {
		return "使い方: balance <チーム数(2-4)> 名前1 名前2 ...（最大8人。末尾に uneven で人数差を許容）"
	}
	k, err := strconv.Atoi(fields[0])
	if err != nil {
		return "チーム数は数字で指定してください。"
	}
	var members []balance.Member
	for _, n := range fields[1:] {
		if es, err := parse.Match(n, b.resolver()); err == nil && len(es) == 1 {
			n = es[0].Player
		}
		p, err := b.core.GetOrCreatePlayer(n)
		if err != nil {
			return "error: " + err.Error()
		}
		members = append(members, balance.Member{Name: p.Name, Rating: b.core.PlayerRating(p.ID, core.ScopeOverall).Rating})
	}
	props, err := balance.Propose(members, k, balance.Options{TopN: 3, AllowUneven: uneven})
	if err != nil {
		return "error: " + err.Error()
	}
	var sb strings.Builder
	for i, p := range props {
		fmt.Fprintf(&sb, "*案%d*（差 %.1f）\n", i+1, p.Spread)
		for t, team := range p.Teams {
			var names []string
			for _, m := range team {
				names = append(names, fmt.Sprintf("%s(%d)", m.Name, m.Rating.Display()))
			}
			fmt.Fprintf(&sb, "  チーム%d %d人 %.0f%%: %s\n", t+1, len(team), p.WinProb[t]*100, strings.Join(names, ", "))
		}
	}
	return sb.String()
}

func (b *Bot) ports(arg string) string {
	if strings.TrimSpace(arg) == "" {
		ps, _ := b.core.Ports()
		if len(ps) == 0 {
			return "ポートは未設定です。`ports P1=名前 P2=名前`"
		}
		var parts []string
		for _, p := range ps {
			parts = append(parts, fmt.Sprintf("P%d=%s", p.Port, p.Player.Name))
		}
		return strings.Join(parts, " ")
	}
	m, err := parse.Ports(arg)
	if err != nil {
		return err.Error()
	}
	for port, name := range m {
		if es, err := parse.Match(name, b.resolver()); err == nil && len(es) == 1 {
			name = es[0].Player
		}
		if err := b.core.SetPort(port, name); err != nil {
			return "error: " + err.Error()
		}
	}
	return "設定しました: " + b.ports("")
}

func (b *Bot) link(arg, userID string) string {
	if strings.TrimSpace(arg) == "" {
		return "使い方: link <プレイヤー名>"
	}
	if err := b.core.SetSlackID(arg, userID); err != nil {
		return "error: " + err.Error()
	}
	return "紐付けました: " + arg
}

func (b *Bot) fix(arg string) string {
	f := strings.Fields(arg)
	if len(f) < 3 {
		return "使い方: fix <試合ID> <名前> <キャラ>"
	}
	id, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return "試合IDは数字です。"
	}
	if err := b.core.UpdateEntryCharacter(id, f[1], strings.Join(f[2:], " ")); err != nil {
		return "修正できません: " + err.Error()
	}
	m, _ := b.core.MatchByID(id)
	return "修正しました " + formatMatch(*m)
}

func (b *Bot) del(arg string) string {
	id, err := strconv.ParseInt(strings.TrimSpace(arg), 10, 64)
	if err != nil {
		return "使い方: delete <試合ID>"
	}
	if err := b.core.DeleteMatch(id); err != nil {
		return "削除できません: " + err.Error()
	}
	return fmt.Sprintf("#%d を削除し、レートを再計算しました。", id)
}

func (b *Bot) learn(arg string) string {
	if b.learner == nil {
		return "画像認識は無効です。"
	}
	f := strings.Fields(arg)
	if len(f) == 0 {
		p := b.learner.Pending()
		if len(p) == 0 {
			return "ラベル待ちの画像はありません。"
		}
		return "ラベル待ち: " + strings.Join(p, ", ") + "\n`learn <画像ID> <キャラ|1st..4th|プレイヤー名>`"
	}
	if len(f) < 2 {
		return "使い方: learn <画像ID> <ラベル>"
	}
	if err := b.learner.Learn(f[0], strings.Join(f[1:], " ")); err != nil {
		return "学習できません: " + err.Error()
	}
	return "学習しました: " + f[0] + " → " + strings.Join(f[1:], " ")
}

func charList() string {
	var names []string
	for _, c := range characters.All {
		names = append(names, c.NameJA)
	}
	return strings.Join(names, "、")
}
