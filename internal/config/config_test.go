package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLayoutsReplaceDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "smashrate.toml")
	err := os.WriteFile(path, []byte(`
[[vision.layouts]]
players = 2
screen = { x = 0.0, y = 0.5, w = 0.1, h = 0.4 }
slots = [ { port = 1, char = { x = 0, y = 0, w = 0.5, h = 0.2 }, name = { x = 0, y = 0.2, w = 0.5, h = 0.1 }, rank = { x = 0.5, y = 0, w = 0.5, h = 0.2 } } ]
[[vision.layouts]]
players = 3
screen = { x = 0.0, y = 0.5, w = 0.1, h = 0.4 }
slots = [ { port = 1, char = { x = 0, y = 0, w = 0.5, h = 0.2 }, name = { x = 0, y = 0.2, w = 0.5, h = 0.1 }, rank = { x = 0.5, y = 0, w = 0.5, h = 0.2 } } ]
[[vision.layouts]]
players = 5
screen = { x = 0.0, y = 0.5, w = 0.1, h = 0.4 }
slots = [ { port = 1, char = { x = 0, y = 0, w = 0.5, h = 0.2 }, name = { x = 0, y = 0.2, w = 0.5, h = 0.1 }, rank = { x = 0.5, y = 0, w = 0.5, h = 0.2 } } ]
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Vision.Layouts) != 3 {
		t.Fatalf("expected the file's 3 layouts only, got %d", len(cfg.Vision.Layouts))
	}
	for _, l := range cfg.Vision.Layouts {
		if l.Mode != "" {
			t.Fatalf("layout for %d players inherited mode %q from the defaults", l.Players, l.Mode)
		}
	}
	if _, ok := cfg.Vision.Layout(5, ""); !ok {
		t.Fatal("5-player layout from the file should be found")
	}
	// defaults still apply when the file has no layouts at all
	if err := os.WriteFile(path, []byte("[server]\naddr = \":1\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || len(cfg.Vision.Layouts) == 0 || cfg.Server.Addr != ":1" {
		t.Fatalf("defaults: %+v %v", cfg.Server, err)
	}
}
