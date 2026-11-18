package ocr

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/aTeNa0001/smashrate/internal/characters"
)

// badgeLine matches the port badge ("1P", "2P", "CP") that can sit on the
// line above the name when the crop is tall.
var badgeLine = regexp.MustCompile(`(?i)^[1-8]?[pc]p?$`)

// NormalizeName cleans OCR output into a player name: the first non-empty
// line that is not just the port badge, whitespace removed (the Windows
// engine spaces out Japanese characters), full-width ASCII folded to
// half-width.
func NormalizeName(text string) string {
	first := ""
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
		s := b.String()
		if s == "" {
			continue
		}
		if badgeLine.MatchString(s) {
			if first == "" {
				first = s
			}
			continue
		}
		return s
	}
	return first
}

var placeholderTag = regexp.MustCompile(`(?i)^(p|player|cpu|cp|com)[ -]?[1-8]?$`)

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
	// (a cutset lists characters, not ranges: every full-width digit is spelled out)
	core := strings.TrimRight(n, "0123456789０１２３４５６７８９-ー・")
	if core == "" {
		return true
	}
	// fragments that only the default tags contain (narrow panels cut the
	// first kana off: ンピューター, 心ュータ, レイヤー)
	for _, frag := range []string{"ピュータ", "ピューター", "ュータ", "ユータ", "ユーター", "コンピ", "レイヤ", "プレイ"} {
		if strings.Contains(core, frag) && len([]rune(core)) <= 9 {
			return true
		}
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
	// captions are all letters: digits are misread letters (F0X, 5ORA)
	joined = digitsAsLetters.Replace(joined)
	if ch, ok := characters.Resolve(joined); ok {
		return ch.Key, true
	}
	// Fighter captions come from a closed list (English names plus the
	// Japanese market's spellings kept as aliases), so a third of the
	// characters may be misread and the nearest name still wins.
	// A fuzzy match needs a reading of four or more characters, at most a
	// third of them wrong, and no other fighter as close: a garbled
	// two-letter fragment ("◎R", "AC" off the narrow panels) used to be
	// matched to whichever short name was nearest.
	n := len([]rune(joined))
	if n < 4 {
		return "", false
	}
	best, bestDist, second := "", 1<<30, 1<<30
	for _, c := range characters.All {
		cands := append([]string{c.NameEN}, c.Aliases...)
		d := 1 << 30
		for _, cand := range cands {
			if isASCII(cand) {
				d = min(d, Levenshtein(joined, NormalizeName(cand)))
			}
		}
		switch {
		case d < bestDist:
			second = bestDist
			best, bestDist = c.Key, d
		case d < second:
			second = d
		}
	}
	allowed := max(1, n/3)
	if best != "" && bestDist <= allowed && second > bestDist {
		return best, true
	}
	return "", false
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
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
	folded := FoldKana(text)
	for _, c := range candidates {
		cn := NormalizeName(c)
		// the small name tags lose dakuten and turn つ into っ: compare the
		// folded forms too
		d := min(Levenshtein(text, cn), Levenshtein(folded, FoldKana(cn)))
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

// PlausibleName reports whether an OCR reading of a name tag is worth
// registering as a new player: at least two characters, all of them
// letters, digits, kana or kanji (the console only allows those, plus a
// few symbols we never see). Fragments such as "ア.ン" (a tag read through
// the edge of the badge) are not names.
func PlausibleName(s string) bool {
	rs := []rune(s)
	if len(rs) < 2 {
		return false
	}
	for _, r := range rs {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == 'ー', r == '・', r == '～':
		default:
			return false
		}
	}
	return true
}

var kanaFolder = func() *strings.Replacer {
	voiced := "がぎぐげござじずぜぞだぢづでどばびぶべぼぱぴぷぺぽガギグゲゴザジズゼゾダヂヅデドバビブベボパピプペポヴ"
	plain := "かきくけこさしすせそたちつてとはひふへほはひふへほカキクケコサシスセソタチツテトハヒフヘホハヒフヘホウ"
	small := "ぁぃぅぇぉっゃゅょゎァィゥェォッャュョヮ"
	large := "あいうえおつやゆよわアイウエオツヤユヨワ"
	var pairs []string
	vr, pr := []rune(voiced), []rune(plain)
	for i := range vr {
		pairs = append(pairs, string(vr[i]), string(pr[i]))
	}
	sr, lr := []rune(small), []rune(large)
	for i := range sr {
		pairs = append(pairs, string(sr[i]), string(lr[i]))
	}
	return strings.NewReplacer(pairs...)
}()

// FoldKana removes what OCR most often loses on a small name tag: dakuten
// and handakuten (ぎ→き, ぴ→ひ) and the small kana (っ→つ).
func FoldKana(s string) string { return kanaFolder.Replace(s) }

var digitsAsLetters = strings.NewReplacer("0", "O", "1", "I", "2", "Z", "5", "S", "6", "G", "8", "B")
