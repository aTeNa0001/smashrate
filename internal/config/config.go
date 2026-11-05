// Package config loads smashrate.toml.
package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"

	"github.com/aTeNa0001/smashrate/internal/ocr"
)

// Config is the whole configuration file.
type Config struct {
	Server ServerConfig `toml:"server"`
	Slack  SlackConfig  `toml:"slack"`
	Vision VisionConfig `toml:"vision"`
}

// ServerConfig covers storage and HTTP.
type ServerConfig struct {
	Addr     string `toml:"addr"`
	Database string `toml:"database"`
	DataDir  string `toml:"data_dir"`
	// BaseURL is used in Slack messages to link to the web UI.
	BaseURL string     `toml:"base_url"`
	Auth    AuthConfig `toml:"auth"`
}

// AuthConfig is the web UI's simple login. With an empty password every
// request is allowed (LAN-only use). With a password, writes (recording,
// deleting, editing) require a login; reads too if ReadRequiresLogin.
type AuthConfig struct {
	Password string `toml:"password"`
	// APIToken lets scripts call write endpoints with "Authorization: Bearer <token>".
	APIToken string `toml:"api_token"`
	// ReadRequiresLogin also protects every page and GET endpoint.
	ReadRequiresLogin bool `toml:"read_requires_login"`
	// TrustCloudflareAccess treats a request carrying the
	// Cf-Access-Authenticated-User-Email header (set by Cloudflare Access after
	// the user signed in) as logged in. Only enable when port 8080 is not
	// reachable except through the tunnel, because the header can be forged
	// by anyone who can reach the port directly.
	TrustCloudflareAccess bool `toml:"trust_cloudflare_access"`
}

// Enabled reports whether any login is configured.
func (a AuthConfig) Enabled() bool { return a.Password != "" }

// SlackConfig enables the Socket Mode bot.
type SlackConfig struct {
	Enabled  bool   `toml:"enabled"`
	BotToken string `toml:"bot_token"`
	AppToken string `toml:"app_token"`
	Channel  string `toml:"channel"`
	Command  string `toml:"command"`
}

// VisionConfig configures the capture-board watcher.
type VisionConfig struct {
	Enabled bool `toml:"enabled"`
	// FFmpeg is the ffmpeg binary.
	FFmpeg string `toml:"ffmpeg"`
	// InputFormat is the ffmpeg demuxer: dshow (Windows), v4l2 (Linux), avfoundation (macOS).
	InputFormat string `toml:"input_format"`
	// Input is the device string, e.g. "video=USB Video" (dshow) or "/dev/video0" (v4l2).
	Input string `toml:"input"`
	// Extra ffmpeg input options, e.g. ["-video_size", "1920x1080"].
	InputArgs []string `toml:"input_args"`
	// Analysis resolution. Frames are scaled to this before hashing.
	Width  int     `toml:"width"`
	Height int     `toml:"height"`
	FPS    float64 `toml:"fps"`
	// StableFrames is how many consecutive identical readings are required.
	StableFrames int `toml:"stable_frames"`
	// CooldownSeconds suppresses duplicate recordings of the same screen.
	CooldownSeconds int `toml:"cooldown_seconds"`
	// Hamming-distance thresholds (0-64) for template matching.
	ScreenThreshold int `toml:"screen_threshold"`
	CharThreshold   int `toml:"char_threshold"`
	RankThreshold   int `toml:"rank_threshold"`
	NameThreshold   int `toml:"name_threshold"`
	// OCR reads the name tag of players whose tag has not been seen before.
	OCR ocr.Config `toml:"ocr"`
	// Layouts describe where things are on the results screen, by player count.
	Layouts []Layout `toml:"layouts"`
}

// Region is a rectangle in fractions of the frame (0..1).
type Region struct {
	X float64 `toml:"x"`
	Y float64 `toml:"y"`
	W float64 `toml:"w"`
	H float64 `toml:"h"`
}

// Slot is one player's column on the results screen.
type Slot struct {
	Port int    `toml:"port"`
	Char Region `toml:"char"`
	Rank Region `toml:"rank"`
	// Name is the player's name tag. It identifies the human directly, so
	// no port assignment is needed once each member's tag has been learned.
	Name Region `toml:"name"`
}

// Layout is the results-screen geometry for one player count and format.
// Team battles use a different results screen (players grouped by team), so
// they get their own layout with Mode = "team"; Mode "" covers 1on1 and
// free-for-all, whose screens share one geometry.
type Layout struct {
	Players int    `toml:"players"`
	Mode    string `toml:"mode"`   // "" (1on1 / free-for-all) or "team"
	Screen  Region `toml:"screen"` // region that identifies "this is the results screen"
	Slots   []Slot `toml:"slots"`
}

// ScreenLabel is the template label that identifies this layout's screen.
func (l Layout) ScreenLabel() string {
	if l.Mode == "team" {
		return fmt.Sprintf("results:%d:team", l.Players)
	}
	return fmt.Sprintf("results:%d", l.Players)
}

// Default returns a configuration that works out of the box for the web UI.
func Default() Config {
	return Config{
		Server: ServerConfig{Addr: ":8080", Database: "data/smashrate.db", DataDir: "data", BaseURL: "http://localhost:8080"},
		Slack:  SlackConfig{Command: "/smash"},
		Vision: VisionConfig{
			FFmpeg: "ffmpeg", InputFormat: "dshow", Input: "video=USB Video",
			Width: 640, Height: 360, FPS: 4, StableFrames: 2, CooldownSeconds: 90,
			ScreenThreshold: 10, CharThreshold: 12, RankThreshold: 10, NameThreshold: 10,
			OCR:     ocr.Config{Engine: "auto", Language: "ja", Scale: 3, TimeoutSeconds: 15},
			Layouts: defaultLayouts(),
		},
	}
}

// defaultLayouts are geometries for Smash Ultimate's results screen (16:9),
// measured on a real 4-player capture: each column shows, from the top, the
// fighter name caption (English, up to two lines) with the placement number
// at the column's top right, then the port badge and the player's name tag,
// then the portrait and the stat rows. One column per player for 2-4
// players, two rows of up to four columns for 5-8 players. Tune with
// `smashrate vision regions` against a screenshot when they do not fit.
func defaultLayouts() []Layout {
	// column i of cols within a band spanning [y0, y0+h) of the frame
	cell := func(port, i, cols int, y0, h float64) Slot {
		w := 1.0 / float64(cols)
		x := float64(i) * w
		return Slot{
			Port: port,
			// "DONKEY KONG" caption, top left of the column
			Char: Region{X: x + w*0.03, Y: y0 + h*0.02, W: w * 0.68, H: h * 0.13},
			// placement numeral (with laurels for 1st), top right
			Rank: Region{X: x + w*0.78, Y: y0 + h*0.005, W: w * 0.22, H: h * 0.15},
			// name tag to the right of the "1P"/"CP" badge
			Name: Region{X: x + w*0.30, Y: y0 + h*0.15, W: w * 0.68, H: h * 0.065},
		}
	}
	var out []Layout
	for n := 2; n <= 8; n++ {
		var slots []Slot
		if n <= 4 {
			for i := 0; i < n; i++ {
				slots = append(slots, cell(i+1, i, n, 0, 0.86))
			}
		} else {
			top := (n + 1) / 2
			for i := 0; i < top; i++ {
				slots = append(slots, cell(i+1, i, top, 0, 0.43))
			}
			for i := 0; i < n-top; i++ {
				slots = append(slots, cell(top+i+1, i, n-top, 0.43, 0.43))
			}
		}
		// The screen is recognised by the stat-row labels (全滅/撃墜/落下/自滅)
		// down the left edge, which never change; the numbers next to them do.
		screen := Region{X: 0.0, Y: 0.56, W: 0.09, H: 0.37}
		out = append(out, Layout{Players: n, Screen: screen, Slots: slots})
		// Team battles (3+ players) show a differently arranged results screen.
		// The same geometry is only a placeholder until tuned on a real screenshot.
		if n >= 3 {
			teamSlots := make([]Slot, len(slots))
			copy(teamSlots, slots)
			out = append(out, Layout{Players: n, Mode: "team", Screen: screen, Slots: teamSlots})
		}
	}
	return out
}

// Load reads path; a missing file yields Default().
func Load(path string) (Config, error) {
	cfg := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if _, err := toml.Decode(string(b), &cfg); err != nil {
		return cfg, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cfg.Vision.Layouts) == 0 {
		cfg.Vision.Layouts = defaultLayouts()
	}
	return cfg, nil
}

// Layout returns the layout for a player count and format ("" or "team").
func (v VisionConfig) Layout(players int, mode string) (Layout, bool) {
	if mode != "team" {
		mode = ""
	}
	for _, l := range v.Layouts {
		if l.Players == players && normMode(l.Mode) == mode {
			return l, true
		}
	}
	return Layout{}, false
}

func normMode(m string) string {
	if m == "team" {
		return "team"
	}
	return ""
}
