package ocr

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/aTeNa0001/smashrate/internal/characters"
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

var placeholderTag = regexp.MustCompile(`(?i)^(p|player|cpu|com)[ -]?[1-8]?$`)

// placeholderWords are the game's Japanese default tags; OCR tends to
// misread a kana or two (コンにユーター), so they are matched fuzzily.
var placeholderWords = []string{"プレイヤー", "コンピューター"}

// IsPlaceholder reports whether an OCR'd tag is the game's default label
// (P1, プレイヤー1, CPU, コンピューター2, ...) rather than a person's name.
// Humans without a chosen name tag and CPU fighters both show such labels;
// neither may be registered as a player.
func IsPlaceholder(text string) bool {
	n := NormalizeName(text)
	if n == "" || placeholderTag.MatchString(n) {
		return true
	}
	// strip a trailing player number
	core := strings.TrimRight(n, "0123456789０-９")
	if core == "" {
		return true
	}
	for _, w := range placeholderWords {
		d := Levenshtein(core, w)
		if d <= 2 || (len([]rune(core)) >= 5 && d <= 3) {
			return true
		}
	}
	return false
}

// NormalizeJoined is NormalizeName over every line joined together, for
// labels that wrap onto two lines (e.g. the fighter name "DONKEY KONG").
func NormalizeJoined(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		b.WriteString(NormalizeName(line))
	}
	return b.String()
}

// MatchCharacter resolves OCR'd fighter-name text (the English caption on
// the results screen) to a character key, tolerating small misreads.
func MatchCharacter(text string) (string, bool) {
	joined := NormalizeJoined(text)
	if joined == "" {
		return "", false
	}
	if ch, ok := characters.Resolve(joined); ok {
		return ch.Key, true
	}
	// fighter captions are long and come from a closed list, so allow a
	// quarter of the characters to be misread
	best, bestDist := "", 1<<30
	for _, c := range characters.All {
		d := Levenshtein(joined, NormalizeName(c.NameEN))
		if d < bestDist {
			best, bestDist = c.Key, d
		}
	}
	n := len([]rune(joined))
	allowed := n / 4
	if allowed < 1 {
		allowed = 1
	}
	if best != "" && bestDist <= allowed {
		return best, true
	}
	return "", false
}

var rankDigit = regexp.MustCompile(`[1-8１-８]`)

// ParseRank extracts a placement 1-8 from OCR'd badge text ("2", "1st", "３位").
func ParseRank(text string) int {
	m := rankDigit.FindString(text)
	if m == "" {
		return 0
	}
	r := []rune(m)[0]
	if r >= '１' && r <= '８' {
		return int(r-'１') + 1
	}
	return int(r - '0')
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
	// small kana read as large (ュ vs ユ)
	if big, ok := smallKana[r]; ok {
		return big
	}
	return r
}

var smallKana = func() map[rune]rune {
	m := map[rune]rune{}
	small, big := []rune("ァィゥェォャュョッヮぁぃぅぇぉゃゅょっゎ"), []rune("アイウエオヤユヨツワあいうえおやゆよつわ")
	for i := range small {
		m[small[i]] = big[i]
	}
	return m
}()

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
