package ocr

import (
	"image"
	"image/color"
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"た ろ う":         "たろう",
		"\n  Hanak0 \n": "Hanak0",
		"Ｔａｒｏ":          "Taro",
		"":              "",
		"   \n ":        "",
	} {
		if got := NormalizeName(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestMatchName(t *testing.T) {
	players := []string{"Hanako", "たろう", "ゲーマー太郎", "けん"}
	cases := map[string]string{
		"Hanak0":      "Hanako", // o/0 confusion is free
		"た ろ う":       "たろう",
		"ケ ー マ ー 太 郎": "ゲーマー太郎", // lost dakuten is free
		"けん":          "けん",
		"Hanaka":      "Hanako", // one real edit on a 6-char name
		"Jiro":        "",       // new player
		"けい":          "",       // 2-char names must match exactly
		"":            "",
	}
	for in, want := range cases {
		if got := MatchName(in, players); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestMatchCharacterAndRank(t *testing.T) {
	for in, want := range map[string]string{
		"DONKEY\nKONG":      "donkey_kong",
		"MARIO":             "mario",
		"BANJO &\nKAZOOIE":  "banjo_kazooie",
		"F0X":               "fox", // 0/o confusion
		"MR. GAME & WATCH":  "mr_game_and_watch",
		"P0KEMON TRAINER":   "pokemon_trainer",
		"ZERO SUIT SAMUS":   "zero_suit_samus",
		"DARK  SAMUS":       "dark_samus",
		"ROSALINA & LUMA":   "rosalina",
		"PONKEY | KONG":     "donkey_kong",
		"K00 PA":            "bowser",
		"MARJO":             "mario",
		"PACKUN\nド[0ER":     "piranha_plant",
		"PIKACHUj":          "pikachu",
		"IHOMURA'":          "pyra_mythra",
		"zELDA 、":           "zelda",
		"BAN」0\n& KAZOOJE":  "banjo_kazooie",
		"8A2」0\n& KAZOOIE":  "banjo_kazooie",
		"nothing like this": "",
		"":                  "",
	} {
		got, ok := MatchCharacter(in)
		if got != want || ok != (want != "") {
			t.Fatalf("%q: got %q/%v want %q", in, got, ok, want)
		}
	}
	for in, want := range map[string]int{"2": 2, "1st": 1, "３位": 3, "\n 4\n": 4, "x": 0, "": 0, "9": 0} {
		if got := ParseRank(in); got != want {
			t.Fatalf("rank %q: got %d want %d", in, got, want)
		}
	}
}

func TestIsPlaceholder(t *testing.T) {
	for _, s := range []string{"P1", "p 2", "Player 3", "PLAYER4", "CPU", "COM 1", "プレイヤー1", "コンピューター2", "コ ン ピ ュ ー タ ー 3", "コ ン に ユ ー タ ー 2", "ン ヒ ュ ー タ ー 4", "フ レ イ ヤ ー 1", "ン に ユ ー タ -", "心 ュ ー タ -", "1 ン に ユ ー タ ー 2", ""} {
		if !IsPlaceholder(s) {
			t.Fatalf("%q should be a placeholder", s)
		}
	}
	for _, s := range []string{"Paul", "たろう", "P1X", "cpuman", "Hanako"} {
		if IsPlaceholder(s) {
			t.Fatalf("%q should be a real name", s)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	if Levenshtein("kitten", "sitting") != 3 || Levenshtein("", "abc") != 3 || Levenshtein("abc", "abc") != 0 {
		t.Fatal("levenshtein")
	}
}

func TestUpscale(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.Set(0, 0, color.White)
	img.Set(1, 0, color.Black)
	up := Upscale(img, 3)
	if up.Rect.Dx() != 6 || up.Rect.Dy() != 3 {
		t.Fatalf("size %v", up.Rect)
	}
	if r, _, _, _ := up.At(0, 1).RGBA(); r < 0xf000 {
		t.Fatal("left edge should stay white")
	}
	if r, _, _, _ := up.At(5, 1).RGBA(); r > 0x0fff {
		t.Fatal("right edge should stay black")
	}
	// bilinear: the middle is a blend, not a hard step
	l, _, _, _ := up.At(2, 1).RGBA()
	r, _, _, _ := up.At(3, 1).RGBA()
	if !(l > r && l < 0xf000 && r > 0x0fff) {
		t.Fatalf("expected a smooth ramp across the middle, got %x %x", l, r)
	}
}

func TestGlyphSignature(t *testing.T) {
	// a bright vertical bar on dark: signature has its middle columns set
	// regardless of where the bar sits or how large the crop is
	draw := func(w, h, x0, x1 int) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				c := color.RGBA{20, 20, 20, 255}
				if x >= x0 && x < x1 && y >= 2 && y < h-2 {
					c = color.RGBA{250, 250, 250, 255}
				}
				img.Set(x, y, c)
			}
		}
		return img
	}
	a, ok1 := GlyphSignature(draw(40, 40, 10, 24))
	b, ok2 := GlyphSignature(draw(100, 60, 60, 81))
	if !ok1 || !ok2 {
		t.Fatal("expected glyphs")
	}
	if a != b {
		t.Fatalf("signature should not depend on position/size: %016x vs %016x", a, b)
	}
	if _, ok := GlyphSignature(draw(40, 40, 0, 0)); ok {
		t.Fatal("no bright ink should give no glyph")
	}
}

func TestNewEngines(t *testing.T) {
	if e, err := New(Config{Engine: "none"}); e != nil || err != nil {
		t.Fatal("none should disable")
	}
	if _, err := New(Config{Engine: "command", Command: "tesseract"}); err == nil {
		t.Fatal("command without {image} should fail")
	}
	if _, err := New(Config{Engine: "nosuch"}); err == nil {
		t.Fatal("unknown engine should fail")
	}
	e, err := New(Config{Engine: "command", Command: "echo {image}"})
	if err != nil || e.Name() != "command" {
		t.Fatalf("command engine: %v", err)
	}
}

// TestWindowsEngine renders a tag with the system font and reads it back with
// the built-in OCR. It only runs on Windows with powershell.exe available.
func TestWindowsEngine(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skip("powershell.exe not found")
	}
	if os.Getenv("SMASHRATE_OCR_TEST") == "" {
		t.Skip("set SMASHRATE_OCR_TEST=1 to run the live Windows OCR test")
	}
	e, err := New(Config{Engine: "windows"})
	if err != nil {
		t.Fatal(err)
	}
	// a blank image must not error and must return nothing
	blank := image.NewRGBA(image.Rect(0, 0, 60, 20))
	if txt, err := e.Recognize(blank); err != nil || NormalizeName(txt) != "" {
		t.Fatalf("blank: %q %v", txt, err)
	}
}
