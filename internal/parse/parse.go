// Package parse turns the compact chat/CLI match syntax into entries.
//
//	A > B                 A beat B (1on1)
//	A:mario > B:link      with characters
//	A > B > C > D         free-for-all placement
//	A+B > C+D             team battle
//	A > B = C             B and C tied for second
//
// Names may be Slack mentions (<@U123|name>) and characters may be given in
// Japanese, English or common abbreviations.
package parse

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aTeNa0001/smashrate/internal/characters"
	"github.com/aTeNa0001/smashrate/internal/core"
)

var mention = regexp.MustCompile(`<@([A-Z0-9]+)(?:\|([^>]*))?>`)

// Resolver maps a Slack user id to a player name; may be nil.
type Resolver func(slackID string) (string, bool)

// Match parses the placement syntax.
func Match(text string, resolve Resolver) ([]core.EntryInput, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("empty match")
	}
	// Mentions contain '>' so they must be substituted before splitting.
	var mentionErr error
	text = mention.ReplaceAllStringFunc(text, func(m string) string {
		sub := mention.FindStringSubmatch(m)
		if resolve != nil {
			if n, ok := resolve(sub[1]); ok {
				return n
			}
		}
		if sub[2] != "" {
			return sub[2]
		}
		mentionErr = fmt.Errorf("slack user %s is not linked to a player (use `link <name>`)", sub[1])
		return m
	})
	if mentionErr != nil {
		return nil, mentionErr
	}
	var entries []core.EntryInput
	team := 0
	for rank, group := range strings.Split(text, ">") {
		group = strings.TrimSpace(group)
		if group == "" {
			return nil, errors.New("empty placement group")
		}
		for _, teamText := range strings.Split(group, "=") {
			teamText = strings.TrimSpace(teamText)
			if teamText == "" {
				return nil, errors.New("empty team")
			}
			team++
			for _, member := range strings.Split(teamText, "+") {
				member = strings.TrimSpace(member)
				if member == "" {
					return nil, errors.New("empty player")
				}
				e, err := parseMember(member)
				if err != nil {
					return nil, err
				}
				e.Team = team
				e.Rank = rank + 1
				entries = append(entries, e)
			}
		}
	}
	return entries, nil
}

func parseMember(s string) (core.EntryInput, error) {
	name, char := s, ""
	// allow "name:char", "name(char)", "name/char"
	if i := strings.IndexAny(s, ":(/"); i >= 0 {
		name = strings.TrimSpace(s[:i])
		char = strings.Trim(strings.TrimSpace(s[i+1:]), "() ")
	}
	if name == "" {
		return core.EntryInput{}, errors.New("empty player name")
	}
	e := core.EntryInput{Player: name}
	if char != "" {
		ch, ok := characters.Resolve(char)
		if !ok {
			return core.EntryInput{}, fmt.Errorf("unknown character %q for %s", char, name)
		}
		e.Character = ch.Key
	}
	return e, nil
}

// Ports parses "P1=name P2=name" (also "1=name", "p3:name").
func Ports(text string) (map[int]string, error) {
	out := map[int]string{}
	for _, tok := range strings.Fields(text) {
		tok = strings.TrimLeft(tok, "pP")
		sep := strings.IndexAny(tok, "=:")
		if sep < 1 {
			return nil, fmt.Errorf("expected P1=name, got %q", tok)
		}
		var port int
		if _, err := fmt.Sscanf(tok[:sep], "%d", &port); err != nil {
			return nil, fmt.Errorf("bad port in %q", tok)
		}
		out[port] = strings.TrimSpace(tok[sep+1:])
	}
	if len(out) == 0 {
		return nil, errors.New("no port assignments given")
	}
	return out, nil
}
