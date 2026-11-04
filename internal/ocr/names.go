package ocr

import (
	"regexp"
	"strings"
	"unicode"
)

// NormalizeName cleans OCR output into a player name: first non-empty line,
// whitespace removed (the Windows engine spaces out Japanese characters),
// full-width ASCII folded to half-width.
func NormalizeName(text string) string {
	for _, line := range strings.Split(text, "\n") {
		var b strings.Builder
		for _, r := range line {
			if unicode.IsSpace(r) {
				continue
			}
			// full-width ASCII (U+FF01..U+FF5E) -> ASCII
			if r >= 0xFF01 && r <= 0xFF5E {
				r = r - 0xFF01 + 0x21
			}
			b.WriteRune(r)
		}
		if s := b.String(); s != "" {
			return s
		}
	}
	return ""
}

var placeholderTag = regexp.MustCompile(`(?i)^(p|player|プレイヤー|cpu|com)[ -]?[1-8]?$`)

// IsPlaceholder reports whether an OCR'd tag is the game's default label
// (P1, Player 2, CPU, ...) rather than a person's name. This happens on a
// console where no name tag was chosen; such a player must not be registered.
func IsPlaceholder(text string) bool {
	n := NormalizeName(text)
	return n == "" || placeholderTag.MatchString(n)
}

// Levenshtein is the edit distance between two strings in runes.
func Levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if foldRune(ra[i-1]) == foldRune(rb[j-1]) {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

// foldRune treats common OCR confusions as equal.
func foldRune(r rune) rune {
	r = unicode.ToLower(r)
	switch r {
	case '0':
		return 'o'
	case '1', 'l', '|':
		return 'i'
	case 'ー', '一', '-', '―', '－':
		return '-'
	}
	// strip dakuten/handakuten differences (ゲ vs ケ) by mapping to the base
	if base, ok := dakutenBase[r]; ok {
		return base
	}
	return r
}

var dakutenBase = func() map[rune]rune {
	m := map[rune]rune{}
	pairs := []struct{ voiced, base string }{
		{"がぎぐげござじずぜぞだぢづでどばびぶべぼ", "かきくけこさしすせそたちつてとはひふへほ"},
		{"ぱぴぷぺぽ", "はひふへほ"},
		{"ガギグゲゴザジズゼゾダヂヅデドバビブベボ", "カキクケコサシスセソタチツテトハヒフヘホ"},
		{"パピプペポ", "ハヒフヘホ"},
		{"ヴ", "ウ"},
	}
	for _, p := range pairs {
		v, b := []rune(p.voiced), []rune(p.base)
		for i := range v {
			m[v[i]] = b[i]
		}
	}
	return m
}()

// MatchName picks the existing name that the OCR text most likely denotes.
// It tolerates one edit for short names and two for longer ones, counting
// common OCR confusions (0/o, dakuten) as free. Returns "" when no candidate
// is close enough, meaning the text is a new player.
func MatchName(text string, candidates []string) string {
	text = NormalizeName(text)
	if text == "" {
		return ""
	}
	best, bestDist := "", 1<<30
	for _, c := range candidates {
		d := Levenshtein(text, NormalizeName(c))
		if d < bestDist {
			best, bestDist = c, d
		}
	}
	n := len([]rune(text))
	allowed := 0
	switch {
	case n >= 6:
		allowed = 2
	case n >= 3:
		allowed = 1
	}
	if best != "" && bestDist <= allowed {
		return best
	}
	return ""
}
