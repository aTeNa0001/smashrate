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
	// StableFrames is the minimum number of frames a reading must have been
	// seen in while the results screen was visible. 1 records even a screen
	// that was skipped after a single frame.
	StableFrames int `toml:"stable_frames"`
	// SettleSeconds is how long readings are collected after the results
	// screen first appears before the most frequent one is recorded (the
	// panels animate in; the screen is recorded sooner if it disappears).
	SettleSeconds float64 `toml:"settle_seconds"`
	// IdleAfterSeconds: when the picture has not changed for this long (the
	// Switch is off or asleep), ffmpeg is stopped to save CPU and heat, and
	// the board is only probed every IdlePollSeconds. 0 disables idling.
	IdleAfterSeconds int `toml:"idle_after_seconds"`
	IdlePollSeconds  int `toml:"idle_poll_seconds"`
	// CooldownSeconds suppresses duplicate recordings of the same screen.
	CooldownSeconds int `toml:"cooldown_seconds"`
	// Hamming-distance thresholds (0-64) for template matching. The screen
	// is matched on two hashes: the first panel's stat labels (screen) and
	// the stat-row band across the frame (band); both must be within range.
	ScreenThreshold int `toml:"screen_threshold"`
	BandThreshold   int `toml:"band_threshold"`
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
	Screen  Region `toml:"screen"` // stat labels of the first panel: "this is the results screen"
	Band    Region `toml:"band"`   // stat rows across the whole frame: "with this many panels"
	Slots   []Slot `toml:"slots"`
	// WinnerShiftY: the 1st-place panel is drawn slightly enlarged, which
	// moves its caption and name tag down by this fraction of the frame.
	WinnerShiftY float64 `toml:"winner_shift_y"`
}

// CharRegion / NameRegion return a slot's regions, shifted for the winner's
// enlarged panel when winner is true.
func (l Layout) CharRegion(i int, winner bool) Region { return l.shifted(l.Slots[i].Char, winner) }
func (l Layout) NameRegion(i int, winner bool) Region { return l.shifted(l.Slots[i].Name, winner) }

func (l Layout) shifted(r Region, winner bool) Region {
	if winner {
		r.Y += l.WinnerShiftY
	}
	return r
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
			// Full 1080p: hashes do not care about resolution, but the name-tag
			// OCR misreads kana below ~30 px (360p renders them at 12 px).
			Width: 1920, Height: 1080, FPS: 6, StableFrames: 1, SettleSeconds: 1.5, CooldownSeconds: 90,
			IdleAfterSeconds: 300, IdlePollSeconds: 30,
			ScreenThreshold: 6, BandThreshold: 14, CharThreshold: 4, RankThreshold: 6, NameThreshold: 6,
			OCR:     ocr.Config{Engine: "auto", Language: "ja", Scale: 3, TimeoutSeconds: 15},
			Layouts: defaultLayouts(),
		},
	}
}

// panelSpec is where the player panels sit for one player count, measured
// on real 640x360 captures of every count from 2 to 8: a single row of
// panels whose left edge is x0 + i*step and whose width is w (fractions of
// the frame). With 2, 3 and 5 players the row is centred with gaps; with
// 4 and 6-8 it spans the full width. For up to 4 players the placement
// numeral sits at the panel's top right; from 5 players on it moves down
// onto the portrait.
type panelSpec struct {
	x0, step, w float64
	rankOnTop   bool
}

var panelSpecs = map[int]panelSpec{
	2: {0.133, 0.414, 0.31, true},
	3: {0.016, 0.327, 0.31, true},
	4: {0.0, 0.25, 0.25, true},
	5: {0.055, 0.184, 0.155, false},
	6: {0.0, 1.0 / 6, 1.0 / 6, false},
	7: {0.0, 1.0 / 7, 1.0 / 7, false},
	8: {0.0, 0.125, 0.125, false},
}

// defaultLayouts are geometries for Smash Ultimate's results screen (16:9).
// Each panel shows, from the top, the fighter name caption (English, up to
// two lines) left of the series icon, then the "1P"/"CP" badge with the
// player's name tag, then the portrait with the placement numeral, then the
// stat rows. The panels are slanted, so rows sit a little lower in panels
// further right; the regions are tall enough to cover every panel. Tune with
// `smashrate vision regions` against a screenshot when they do not fit.
func defaultLayouts() []Layout {
	var out []Layout
	for n := 2; n <= 8; n++ {
		ps := panelSpecs[n]
		var slots []Slot
		for i := 0; i < n; i++ {
			x, w := ps.x0+float64(i)*ps.step, ps.w
			s := Slot{Port: i + 1}
			if ps.rankOnTop {
				// wide panels: caption left of the series icon and numeral,
				// name tag on the same row as the "1P"/"CP" badge
				s.Char = Region{X: x + w*0.03, Y: 0.045, W: w * 0.55, H: 0.11}
				s.Name = Region{X: x + w*0.14, Y: 0.165, W: w * 0.53, H: 0.075}
				// the numeral is taller for the winner (it reaches from the
				// very top of the panel to below the badge row); a region
				// that clips it is not recognised as a digit at all
				s.Rank = Region{X: x + w*0.66, Y: 0, W: w * 0.34, H: 0.22}
			} else {
				// narrow panels: caption spans the panel (the icon sits behind
				// it), the badge is above the tag, the numeral is on the portrait
				s.Char = Region{X: x + w*0.02, Y: 0.045, W: w * 0.96, H: 0.11}
				s.Name = Region{X: x + w*0.02, Y: 0.16, W: w * 0.96, H: 0.065}
				s.Rank = Region{X: x + w*0.45, Y: 0.36, W: w * 0.55, H: 0.17}
			}
			slots = append(slots, s)
		}
		// The screen is recognised by the stat-row labels (全滅/撃墜/落下/自滅)
		// down the left edge of the first panel; they never change, the
		// numbers next to them do. The band of stat rows across the whole
		// frame then confirms the player count (its label columns repeat at
		// the panel pitch).
		screen := Region{X: ps.x0, Y: 0.56, W: ps.w * 0.3, H: 0.37}
		band := Region{X: 0, Y: 0.56, W: 1, H: 0.37}
		// Team battles use the same screen; teammates are told apart by
		// sharing a placement, so no separate layout is needed. A layout with
		// mode = "team" can still be configured if a build ever differs.
		out = append(out, Layout{Players: n, Screen: screen, Band: band, Slots: slots, WinnerShiftY: 0.03})
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
	// Decoding an array of tables into a pre-filled slice merges element by
	// element with the defaults (a user's 3rd layout would inherit fields of
	// the 3rd default). Layouts in the file replace the defaults wholesale.
	cfg.Vision.Layouts = nil
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
