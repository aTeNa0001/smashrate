// Command smashrate is the single binary that runs everything: the web UI,
// the JSON API, the Slack bot and the capture-board watcher.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/httpapi"
	"github.com/aTeNa0001/smashrate/internal/ocr"
	"github.com/aTeNa0001/smashrate/internal/parse"
	"github.com/aTeNa0001/smashrate/internal/slackbot"
	"github.com/aTeNa0001/smashrate/internal/vision"
)

// version is set by the release build (-ldflags "-X main.version=v1.2.3").
var version = "dev"

const usage = `smashrate - Smash Bros. local rating system

Usage:
  smashrate [-config smashrate.toml] <command> [args]

Commands:
  version                     print the build version
  doctor                      check that everything the config needs is available (ffmpeg, device, OCR, Slack, ...)
  serve                       run web UI / API (+ Slack bot and capture watcher if enabled)
  record "A:mario > B:link"   record a match (same syntax as Slack)
  rank [1v1|ffa|team]         print the leaderboard
  stats <name>                print a player's statistics
  history [n]                 print recent matches
  balance <teams> <names...> [uneven]
                              propose balanced teams (2-4 teams, up to 8 players; "uneven" allows unequal sizes)
  delete <match-id>           delete a match and recompute
  fix <match-id> <name> <char>  correct a character
  rename <old> <new>          fix a misread player name
  merge <duplicate> <keep>    merge a duplicate player into another and recompute
  recompute                   rebuild every rating from the match history
  vision devices              list capture devices seen by ffmpeg
  vision snapshot [-out f]    grab one frame from the capture board to a PNG
  vision regions -image f [-players N] [-mode team]
                              write crops of every layout region (for tuning smashrate.toml)
  vision teach -image f -players N [-mode team]
                              learn what the results screen looks like (once per player count / format);
                              characters, placements and name tags are then learned automatically via OCR.
                              Optional: -chars a,b -ranks 1,2 -names Taro,Hanako to pre-teach them.
  vision test -image f        run the detector on a screenshot and print what it sees
  vision ocr -image f         run the name-tag OCR engine on an image and print the text
  vision watch                run only the capture watcher (no web server)
`

func main() {
	log.SetFlags(log.Ltime)
	cfgPath := flag.String("config", "smashrate.toml", "config file")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flag.Parse()
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(2)
	}
	if flag.Arg(0) == "version" {
		fmt.Println("smashrate", version)
		return
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(cfg.Server.DataDir, 0o755); err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Server.Database), 0o755); err != nil {
		fatal(err)
	}
	c, err := core.Open(cfg.Server.Database)
	if err != nil {
		fatal(err)
	}
	defer c.Close()

	args := flag.Args()[1:]
	switch flag.Arg(0) {
	case "serve":
		err = serve(cfg, c)
	case "doctor":
		err = doctor(*cfgPath, cfg, c)
	case "record":
		err = record(c, strings.Join(args, " "))
	case "rank":
		err = rank(c, strings.Join(args, " "))
	case "stats":
		err = stats(c, strings.Join(args, " "))
	case "history":
		err = history(c, args)
	case "balance":
		err = balanceCmd(c, args)
	case "delete":
		err = deleteCmd(c, args)
	case "fix":
		err = fixCmd(c, args)
	case "rename":
		if len(args) != 2 {
			err = errors.New("usage: rename <old> <new>")
		} else {
			err = c.RenamePlayer(args[0], args[1])
		}
	case "merge":
		if len(args) != 2 {
			err = errors.New("usage: merge <duplicate> <keep>")
		} else {
			err = c.MergePlayers(args[0], args[1])
		}
	case "recompute":
		err = c.Recompute()
		if err == nil {
			fmt.Println("recomputed")
		}
	case "vision":
		err = visionCmd(cfg, c, args)
	default:
		flag.Usage()
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func serve(cfg config.Config, c *core.Core) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var watcher *vision.Watcher
	var bot *slackbot.Bot
	opts := httpapi.Options{Auth: cfg.Server.Auth}
	if cfg.Vision.Enabled {
		opts.Frame = func() image.Image {
			if watcher == nil {
				return nil
			}
			return watcher.LastFrame()
		}
	}
	srv, err := httpapi.New(c, opts)
	if err != nil {
		return err
	}
	if cfg.Server.Auth.Enabled() {
		log.Printf("web login enabled (read_requires_login=%v, trust_cloudflare_access=%v)", cfg.Server.Auth.ReadRequiresLogin, cfg.Server.Auth.TrustCloudflareAccess)
	} else {
		log.Printf("web login disabled: anyone who can reach %s can record and delete", cfg.Server.Addr)
	}

	if cfg.Slack.Enabled {
		if cfg.Slack.BotToken == "" || cfg.Slack.AppToken == "" {
			return errors.New("slack.enabled but bot_token/app_token missing")
		}
	}
	if cfg.Vision.Enabled {
		watcher, err = vision.NewWatcher(cfg.Vision, c, cfg.Server.DataDir, func(s string) {
			log.Println("vision:", s)
			if bot != nil {
				bot.Notify(s)
			}
		})
		if err != nil {
			return err
		}
	}
	if cfg.Slack.Enabled {
		var learner slackbot.Learner
		if watcher != nil {
			learner = watcher
		}
		bot = slackbot.New(cfg.Slack, cfg.Server.BaseURL, c, learner)
		go func() {
			if err := bot.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("slack: %v", err)
			}
		}()
	}
	if watcher != nil {
		go func() {
			for ctx.Err() == nil {
				src, err := vision.OpenFFmpeg(cfg.Vision)
				if err != nil {
					log.Printf("vision: %v (retrying in 10s)", err)
				} else {
					err = watcher.Run(ctx, src)
					src.Close()
					if ctx.Err() == nil {
						log.Printf("vision: source ended: %v (restarting in 10s)", err)
					}
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Second):
				}
			}
		}()
	}

	hs := &http.Server{Addr: cfg.Server.Addr, Handler: srv}
	go func() {
		<-ctx.Done()
		hs.Close()
	}()
	log.Printf("web UI on http://localhost%s", cfg.Server.Addr)
	if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func record(c *core.Core, text string) error {
	es, err := parse.Match(text, nil)
	if err != nil {
		return err
	}
	m, err := c.RecordMatch(core.MatchInput{Source: "cli", Entries: es})
	if err != nil {
		return err
	}
	fmt.Printf("recorded #%d [%s]\n", m.ID, m.Mode)
	for _, e := range m.Entries {
		fmt.Printf("  %d位 %-12s %-10s %4d → %4d\n", e.Rank, e.PlayerName, e.CharacterName(), e.DisplayBefore, e.DisplayAfter)
	}
	return nil
}

func rank(c *core.Core, scopeArg string) error {
	scope := core.ScopeOverall
	switch strings.ToLower(scopeArg) {
	case "1v1":
		scope = core.ScopeMode(core.Mode1v1)
	case "ffa":
		scope = core.ScopeMode(core.ModeFFA)
	case "team":
		scope = core.ScopeMode(core.ModeTeam)
	}
	lb, err := c.Leaderboard(scope)
	if err != nil {
		return err
	}
	for i, r := range lb {
		fmt.Printf("%2d. %-14s %4d  %3d戦 %3d勝 %3.0f%%  (μ=%.2f σ=%.2f)\n", i+1, r.Player.Name, r.Display, r.Games, r.Wins, r.WinRate()*100, r.Rating.Mu, r.Rating.Sigma)
	}
	return nil
}

func stats(c *core.Core, name string) error {
	st, err := c.Stats(name)
	if err != nil {
		return err
	}
	fmt.Printf("%s  rating %d (rank %d)  %d勝%d敗  streak %d (best %d)  avg place %.2f\n",
		st.Player.Name, st.Overall.Display, st.Rank, st.Overall.Wins, st.Overall.Games-st.Overall.Wins, st.CurrentStreak, st.BestStreak, st.AvgPlacement)
	for _, m := range st.Modes {
		fmt.Printf("  %-5s %4d  %d/%d\n", m.Mode, m.Display, m.Wins, m.Games)
	}
	for _, ch := range st.Characters {
		fmt.Printf("  %-14s %4d  %d/%d (%.0f%%)\n", ch.Name, ch.Display, ch.Wins, ch.Games, ch.WinRate()*100)
	}
	for _, h := range st.HeadToHead {
		fmt.Printf("  vs %-12s %d/%d\n", h.Opponent.Name, h.Wins, h.Games)
	}
	return nil
}

func history(c *core.Core, args []string) error {
	n := 10
	if len(args) > 0 {
		n, _ = strconv.Atoi(args[0])
	}
	ms, err := c.Matches(n, 0)
	if err != nil {
		return err
	}
	for _, m := range ms {
		fmt.Printf("#%d %s [%s]", m.ID, m.PlayedAt.Local().Format("2006-01-02 15:04"), m.Mode)
		for _, e := range m.Entries {
			fmt.Printf("  %d:%s", e.Rank, e.PlayerName)
			if e.Character != "" {
				fmt.Printf("(%s)", e.CharacterName())
			}
		}
		fmt.Println()
	}
	return nil
}

func balanceCmd(c *core.Core, args []string) error {
	uneven := false
	var rest []string
	for _, a := range args {
		if a == "uneven" || a == "-uneven" || a == "--uneven" {
			uneven = true
		} else {
			rest = append(rest, a)
		}
	}
	args = rest
	if len(args) < 3 {
		return errors.New("usage: balance <teams 2-4> <name> <name> ... [uneven]")
	}
	k, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	srv, err := httpapi.New(c, httpapi.Options{})
	if err != nil {
		return err
	}
	props, err := srv.Balance(args[1:], k, uneven)
	if err != nil {
		return err
	}
	for i, p := range props {
		fmt.Printf("proposal %d (spread %.1f)\n", i+1, p.Spread)
		for t, team := range p.Teams {
			var names []string
			for _, m := range team {
				names = append(names, fmt.Sprintf("%s(%d)", m.Name, m.Rating.Display()))
			}
			fmt.Printf("  team %d (%d)  %3.0f%%  %s\n", t+1, len(team), p.WinProb[t]*100, strings.Join(names, ", "))
		}
	}
	return nil
}

func deleteCmd(c *core.Core, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: delete <match-id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return err
	}
	return c.DeleteMatch(id)
}

func fixCmd(c *core.Core, args []string) error {
	if len(args) < 3 {
		return errors.New("usage: fix <match-id> <name> <character>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return err
	}
	return c.UpdateEntryCharacter(id, args[1], strings.Join(args[2:], " "))
}

func visionCmd(cfg config.Config, c *core.Core, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: vision <devices|snapshot|regions|teach|test|watch>")
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("vision "+sub, flag.ExitOnError)
	imagePath := fs.String("image", "", "screenshot file (PNG/JPEG)")
	out := fs.String("out", "", "output file or directory")
	players := fs.Int("players", 2, "number of players on the results screen")
	mode := fs.String("mode", "", `"" for 1on1 / free-for-all, "team" for the team-battle results screen`)
	chars := fs.String("chars", "", "comma-separated characters per column (P1,P2,...)")
	ranks := fs.String("ranks", "", "comma-separated placements per column, e.g. 1,2")
	names := fs.String("names", "", "comma-separated player names per column (the name tags)")
	scale := fs.Int("scale", 0, "regions: also write up_*.png crops enlarged by this factor (what the OCR engine sees)")
	fs.Parse(rest)

	switch sub {
	case "devices":
		fmt.Print(vision.ListDevices(cfg.Vision))
		return nil
	case "snapshot":
		path := *out
		if path == "" {
			path = filepath.Join(cfg.Server.DataDir, "snapshot.png")
		}
		// Prefer the running server's current frame: the device stays open, so
		// there is no re-handshake (capture boards show their logo for a second
		// or two after being opened).
		host, port, _ := net.SplitHostPort(cfg.Server.Addr)
		if host == "" {
			host = "localhost"
		}
		if resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(fmt.Sprintf("http://%s:%s/api/vision/snapshot", host, port)); err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == 200 {
				f, err := os.Create(path)
				if err != nil {
					return err
				}
				_, err = io.Copy(f, resp.Body)
				f.Close()
				if err != nil {
					return err
				}
				fmt.Println("saved", path, "(from the running watcher)")
				return nil
			}
		}
		src, err := vision.OpenFFmpeg(cfg.Vision)
		if err != nil {
			return err
		}
		defer src.Close()
		// skip the first seconds: the board's logo / the signal handshake
		var img image.Image
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) || img == nil {
			img, err = src.Next()
			if err != nil {
				return err
			}
		}
		if err := vision.SavePNG(path, img); err != nil {
			return err
		}
		fmt.Println("saved", path)
		return nil
	case "regions":
		if *imagePath == "" {
			return errors.New("-image is required")
		}
		img, err := vision.OpenFiles(*imagePath).Next()
		if err != nil {
			return err
		}
		dir := *out
		if dir == "" {
			dir = filepath.Join(cfg.Server.DataDir, "regions")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		layout, ok := cfg.Vision.Layout(*players, *mode)
		if !ok {
			return fmt.Errorf("no layout for %d players (mode %q)", *players, *mode)
		}
		b := img.Bounds()
		save := func(name string, r config.Region) error {
			crop := vision.Crop(img, vision.Rect(b, r))
			if *scale > 1 {
				up := ocr.Upscale(crop, *scale)
				if err := vision.SavePNG(filepath.Join(dir, "up_"+name+".png"), up); err != nil {
					return err
				}
				for _, thr := range []uint8{140, 180} {
					if err := vision.SavePNG(filepath.Join(dir, fmt.Sprintf("bw%d_%s.png", thr, name)), ocr.Binarize(up, thr)); err != nil {
						return err
					}
					if iso, ok := ocr.IsolateBright(up, thr, 12); ok {
						if err := vision.SavePNG(filepath.Join(dir, fmt.Sprintf("iso%d_%s.png", thr, name)), iso); err != nil {
							return err
						}
					}
				}
			}
			return vision.SavePNG(filepath.Join(dir, name+".png"), crop)
		}
		if err := save("screen", layout.Screen); err != nil {
			return err
		}
		for _, s := range layout.Slots {
			if err := save(fmt.Sprintf("p%d_char", s.Port), s.Char); err != nil {
				return err
			}
			if err := save(fmt.Sprintf("p%d_rank", s.Port), s.Rank); err != nil {
				return err
			}
			if err := save(fmt.Sprintf("p%d_name", s.Port), s.Name); err != nil {
				return err
			}
		}
		fmt.Println("wrote crops to", dir)
		return nil
	case "teach":
		if *imagePath == "" {
			return errors.New("-image is required")
		}
		img, err := vision.OpenFiles(*imagePath).Next()
		if err != nil {
			return err
		}
		if err := vision.Teach(c, cfg.Vision, img, *players, *mode, splitCSV(*chars), splitCSV(*ranks), splitCSV(*names)); err != nil {
			return err
		}
		fmt.Println("learned templates from", *imagePath)
		return nil
	case "test":
		if *imagePath == "" {
			return errors.New("-image is required")
		}
		det, err := vision.NewDetector(cfg.Vision, c)
		if err != nil {
			return err
		}
		img, err := vision.OpenFiles(*imagePath).Next()
		if err != nil {
			return err
		}
		r, ok := det.Detect(img)
		if !ok {
			fmt.Println("not a results screen (no screen template within threshold)")
			return nil
		}
		kind := "1on1 / free-for-all"
		if r.Team {
			kind = "team battle"
		}
		fmt.Printf("results screen, %d players (%s)\n", r.Players, kind)
		for _, s := range r.Slots {
			fmt.Printf("  P%d  name=%-12q (dist %2d)  char=%-16q (dist %2d)  rank=%d (dist %2d, glyph %016x)\n", s.Port, s.Name, s.NameDist, s.Char, s.CharDist, s.Rank, s.RankDist, s.RankHash)
		}
		return nil
	case "ocr":
		if *imagePath == "" {
			return errors.New("-image is required")
		}
		eng, err := ocr.New(cfg.Vision.OCR)
		if err != nil {
			return err
		}
		if eng == nil {
			return errors.New("ocr is disabled in config (vision.ocr.engine)")
		}
		img, err := vision.OpenFiles(*imagePath).Next()
		if err != nil {
			return err
		}
		text, err := eng.Recognize(img)
		if err != nil {
			return err
		}
		fmt.Printf("engine: %s\nraw: %q\nname: %q\n", eng.Name(), text, ocr.NormalizeName(text))
		return nil
	case "watch":
		w, err := vision.NewWatcher(cfg.Vision, c, cfg.Server.DataDir, nil)
		if err != nil {
			return err
		}
		src, err := vision.OpenFFmpeg(cfg.Vision)
		if err != nil {
			return err
		}
		defer src.Close()
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return w.Run(ctx, src)
	}
	return fmt.Errorf("unknown vision command %q", sub)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
