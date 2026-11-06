// Package ocr reads the player name tag when it is not yet known.
//
// The tag is tried against learned hashes first (see package vision); OCR
// only runs the first time a new tag appears. The recognised name is then
// stored as a hash template, so each tag is OCR'd once. Engines:
//
//   - windows: the OCR built into Windows 10/11 (Windows.Media.Ocr) through a
//     small PowerShell script. No installation, Japanese supported.
//   - command: any external program given as a template, e.g.
//     "tesseract {image} - -l jpn" or a RapidOCR wrapper script.
//   - none: disabled; unknown tags are stashed for manual labelling.
package ocr

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"image/png"
)

// Config selects and tunes the engine.
type Config struct {
	// Engine: "auto" (windows on Windows, none elsewhere), "windows", "command", "none".
	Engine string `toml:"engine"`
	// Command template for engine=command; {image} is replaced by a PNG path.
	Command string `toml:"command"`
	// Language tag for the Windows engine (e.g. "ja", "en").
	Language string `toml:"language"`
	// Scale enlarges the crop before recognition (small tags read better at 3-4x).
	Scale int `toml:"scale"`
	// TimeoutSeconds bounds one recognition.
	TimeoutSeconds int `toml:"timeout_seconds"`
}

// Engine recognises text in an image.
type Engine interface {
	// Recognize returns the recognised lines joined by newlines ("" if nothing).
	Recognize(img image.Image) (string, error)
	Name() string
}

// New builds the configured engine; (nil, nil) when disabled.
func New(cfg Config) (Engine, error) {
	eng := strings.ToLower(strings.TrimSpace(cfg.Engine))
	if eng == "" || eng == "auto" {
		if runtime.GOOS == "windows" {
			eng = "windows"
		} else {
			eng = "none"
		}
	}
	if cfg.Scale <= 0 {
		cfg.Scale = 3
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 15
	}
	switch eng {
	case "none", "off", "false":
		return nil, nil
	case "windows":
		if runtime.GOOS != "windows" {
			return nil, errors.New("ocr: engine \"windows\" only works on Windows")
		}
		lang := cfg.Language
		if lang == "" {
			lang = "ja"
		}
		return &windowsEngine{lang: lang, scale: cfg.Scale, timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}, nil
	case "command":
		if !strings.Contains(cfg.Command, "{image}") {
			return nil, errors.New("ocr: command must contain {image}")
		}
		return &commandEngine{tmpl: cfg.Command, scale: cfg.Scale, timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}, nil
	}
	return nil, fmt.Errorf("ocr: unknown engine %q", cfg.Engine)
}

// ---------- shared helpers ----------

// prepare enlarges small crops so that text is at least ~50 px tall; crops
// that are already large (pre-processed by the caller) are passed through.
func prepare(img image.Image, scale int) image.Image {
	if img.Bounds().Dy() >= 50 || scale <= 1 {
		return img
	}
	return Upscale(img, scale)
}

// Variants returns the images to try, in order, when reading a label:
// the smooth 4x enlargement (best for katakana name tags) and the isolated
// bright ink on white (best for the bold Latin fighter captions).
func Variants(crop image.Image) []image.Image {
	up := Upscale(crop, 4)
	out := []image.Image{up}
	if iso, ok := IsolateBright(up, 180, 12); ok {
		out = append(out, iso)
	}
	return out
}

// Upscale enlarges img by an integer factor with bilinear interpolation.
// OCR engines read small UI text far better when it is enlarged smoothly
// (nearest-neighbour blocks confuse them).
func Upscale(img image.Image, factor int) *image.RGBA {
	b := img.Bounds()
	if factor <= 1 {
		out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				out.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return out
	}
	// source as RGBA for fast access
	src := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			src.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	sw, sh := b.Dx(), b.Dy()
	out := image.NewRGBA(image.Rect(0, 0, sw*factor, sh*factor))
	px := func(x, y int) (float64, float64, float64) {
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if x >= sw {
			x = sw - 1
		}
		if y >= sh {
			y = sh - 1
		}
		i := src.PixOffset(x, y)
		return float64(src.Pix[i]), float64(src.Pix[i+1]), float64(src.Pix[i+2])
	}
	for y := 0; y < sh*factor; y++ {
		fy := (float64(y)+0.5)/float64(factor) - 0.5
		y0 := int(fy)
		if fy < 0 {
			y0 = -1
		}
		ty := fy - float64(y0)
		for x := 0; x < sw*factor; x++ {
			fx := (float64(x)+0.5)/float64(factor) - 0.5
			x0 := int(fx)
			if fx < 0 {
				x0 = -1
			}
			tx := fx - float64(x0)
			r00, g00, b00 := px(x0, y0)
			r10, g10, b10 := px(x0+1, y0)
			r01, g01, b01 := px(x0, y0+1)
			r11, g11, b11 := px(x0+1, y0+1)
			lerp := func(a, b, t float64) float64 { return a + (b-a)*t }
			r := lerp(lerp(r00, r10, tx), lerp(r01, r11, tx), ty)
			g := lerp(lerp(g00, g10, tx), lerp(g01, g11, tx), ty)
			bl := lerp(lerp(b00, b10, tx), lerp(b01, b11, tx), ty)
			i := out.PixOffset(x, y)
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = uint8(r+0.5), uint8(g+0.5), uint8(bl+0.5), 255
		}
	}
	return out
}

// Binarize turns a crop into black text on a white background. Game UI
// text is light on dark with gradients and outlines, which OCR engines
// read poorly; a hard threshold (above thr = text) followed by inversion
// gives them the printed-page look they are trained on.
func Binarize(img image.Image, thr uint8) *image.Gray {
	b := img.Bounds()
	out := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			l := (299*(r>>8) + 587*(g>>8) + 114*(bl>>8)) / 1000
			v := uint8(255)
			if uint8(l) >= thr {
				v = 0
			}
			out.Pix[out.PixOffset(x, y)] = v
		}
	}
	return out
}

// IsolateBright binarizes img (bright pixels = ink), crops to the ink's
// bounding box with a margin, and returns black ink on white. Used for the
// placement numeral: the bright digit is kept while the dark outline,
// laurels and the semi-transparent series icon behind it are dropped.
// ok is false when nothing bright was found.
func IsolateBright(img image.Image, thr uint8, margin int) (*image.Gray, bool) {
	return isolate(Binarize(img, thr), margin)
}

// isNumeralInk keeps the silver / gold placement numeral and drops the
// green laurels of 1st place and the dark red series icon behind it.
func isNumeralInk(r, g, b uint8) bool {
	l := (299*int(r) + 587*int(g) + 114*int(b)) / 1000
	if l < 120 {
		return false
	}
	green := int(g) > int(r)*115/100 && int(g) > int(b)*3/2
	return !green
}

// isolateNumeral binarizes with the numeral colour rule, then crops to the ink.
func isolateNumeral(img image.Image) (*image.Gray, bool) {
	b := img.Bounds()
	bw := image.NewGray(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			v := uint8(255)
			if isNumeralInk(uint8(r>>8), uint8(g>>8), uint8(bl>>8)) {
				v = 0
			}
			bw.Pix[bw.PixOffset(x, y)] = v
		}
	}
	return isolate(bw, 0)
}

func isolate(bw *image.Gray, margin int) (*image.Gray, bool) {
	w, h := bw.Rect.Dx(), bw.Rect.Dy()
	minX, minY, maxX, maxY := w, h, -1, -1
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if bw.Pix[bw.PixOffset(x, y)] == 0 {
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if maxX < 0 || maxX-minX < 2 || maxY-minY < 4 {
		return nil, false
	}
	out := image.NewGray(image.Rect(0, 0, maxX-minX+1+2*margin, maxY-minY+1+2*margin))
	for i := range out.Pix {
		out.Pix[i] = 255
	}
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			out.Pix[out.PixOffset(x-minX+margin, y-minY+margin)] = bw.Pix[bw.PixOffset(x, y)]
		}
	}
	return out, true
}

// GlyphSignature reduces the bright glyph in img (a placement numeral) to a
// 64-bit shape code: the isolated ink is scaled onto an 8x8 grid and each
// cell is 1 when more than half of it is ink. It does not depend on where
// in the crop the glyph sits, its size, or the column's background, so one
// learned signature per digit covers every column. ok is false when no
// glyph was found.
func GlyphSignature(img image.Image) (uint64, bool) {
	iso, ok := isolateNumeral(img)
	if !ok {
		return 0, false
	}
	w, h := iso.Rect.Dx(), iso.Rect.Dy()
	var sig uint64
	for gy := 0; gy < 8; gy++ {
		for gx := 0; gx < 8; gx++ {
			x0, x1 := gx*w/8, (gx+1)*w/8
			y0, y1 := gy*h/8, (gy+1)*h/8
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			ink, n := 0, 0
			for y := y0; y < y1 && y < h; y++ {
				for x := x0; x < x1 && x < w; x++ {
					if iso.Pix[iso.PixOffset(x, y)] == 0 {
						ink++
					}
					n++
				}
			}
			sig <<= 1
			if n > 0 && ink*2 > n {
				sig |= 1
			}
		}
	}
	return sig, true
}

func writeTempPNG(img image.Image) (string, error) {
	f, err := os.CreateTemp("", "smashrate-ocr-*.png")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func runCapture(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return "", fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// ---------- windows ----------

//go:embed winocr.ps1
var winScript []byte

type windowsEngine struct {
	lang    string
	scale   int
	timeout time.Duration
	once    sync.Once
	script  string
	initErr error
}

func (w *windowsEngine) Name() string { return "windows" }

func (w *windowsEngine) Recognize(img image.Image) (string, error) {
	w.once.Do(func() {
		dir := filepath.Join(os.TempDir(), "smashrate")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			w.initErr = err
			return
		}
		w.script = filepath.Join(dir, "winocr.ps1")
		w.initErr = os.WriteFile(w.script, winScript, 0o644)
	})
	if w.initErr != nil {
		return "", w.initErr
	}
	path, err := writeTempPNG(prepare(img, w.scale))
	if err != nil {
		return "", err
	}
	defer os.Remove(path)
	ctx, cancel := context.WithTimeout(context.Background(), w.timeout)
	defer cancel()
	// powershell.exe (5.1) has the WinRT projection; pwsh 7 does not.
	return runCapture(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass",
		"-File", w.script, "-Path", path, "-Lang", w.lang)
}

// ---------- external command ----------

type commandEngine struct {
	tmpl    string
	scale   int
	timeout time.Duration
}

func (c *commandEngine) Name() string { return "command" }

func (c *commandEngine) Recognize(img image.Image) (string, error) {
	path, err := writeTempPNG(prepare(img, c.scale))
	if err != nil {
		return "", err
	}
	defer os.Remove(path)
	line := strings.ReplaceAll(c.tmpl, "{image}", path)
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "", errors.New("ocr: empty command")
	}
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	return runCapture(ctx, parts[0], parts[1:]...)
}
