package ocr

import (
	"testing"
	"unicode/utf8"
)

// FuzzNormalizeName: OCR output is arbitrary text; cleaning it must never
// panic, must return valid UTF-8, and must be idempotent (cleaning a
// cleaned name changes nothing), otherwise a player could be created twice.
func FuzzNormalizeName(f *testing.F) {
	for _, s := range []string{"ぎんぴか", " Ｔａｒｏ ", "コンピューター3", "プレイヤー1", "名前\n", "\xff\xfe", "ゆけとおしをまたの〃、", "a　b", "１Ｐ", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := NormalizeName(s)
		if !utf8.ValidString(n) {
			t.Fatalf("invalid utf-8 from %q: %q", s, n)
		}
		if again := NormalizeName(n); again != n {
			t.Fatalf("not idempotent: %q -> %q -> %q", s, n, again)
		}
		j := NormalizeJoined(s)
		if !utf8.ValidString(j) {
			t.Fatalf("invalid utf-8 from NormalizeJoined %q: %q", s, j)
		}
		MatchCharacter(s)
	})
}
