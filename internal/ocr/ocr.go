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

// Upscale enlarges img by an integer factor (nearest neighbour), which is
// what OCR engines want for small UI text.
func Upscale(img image.Image, factor int) *image.RGBA {
	if factor <= 1 {
		b := img.Bounds()
		out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				out.Set(x, y, img.At(b.Min.X+x, b.Min.Y+y))
			}
		}
		return out
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()*factor, b.Dy()*factor))
	for y := 0; y < out.Rect.Dy(); y++ {
		for x := 0; x < out.Rect.Dx(); x++ {
			out.Set(x, y, img.At(b.Min.X+x/factor, b.Min.Y+y/factor))
		}
	}
	return out
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
	path, err := writeTempPNG(Upscale(img, w.scale))
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
	path, err := writeTempPNG(Upscale(img, c.scale))
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
