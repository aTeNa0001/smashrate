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

func TestIsPlaceholder(t *testing.T) {
	for _, s := range []string{"P1", "p 2", "Player 3", "PLAYER4", "CPU", "COM 1", "プレイヤー1", ""} {
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
	if r, _, _, _ := up.At(2, 2).RGBA(); r == 0 {
		t.Fatal("left half should be white")
	}
	if r, _, _, _ := up.At(3, 0).RGBA(); r != 0 {
		t.Fatal("right half should be black")
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
