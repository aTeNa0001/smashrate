package main

import (
	"fmt"
	"image"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/slack-go/slack"

	"github.com/aTeNa0001/smashrate/internal/config"
	"github.com/aTeNa0001/smashrate/internal/core"
	"github.com/aTeNa0001/smashrate/internal/ocr"
	"github.com/aTeNa0001/smashrate/internal/vision"
)

type check struct {
	status string // OK | WARN | FAIL | SKIP
	name   string
	detail string
	hint   string
}

// doctor verifies that everything the configuration asks for is actually
// available on this machine and prints a checklist.
func doctor(cfgPath string, cfg config.Config, c *core.Core) error {
	var out []check
	add := func(status, name, detail, hint string) { out = append(out, check{status, name, detail, hint}) }

	// ---- config & storage
	if _, err := os.Stat(cfgPath); err != nil {
		add("WARN", "設定ファイル", cfgPath+" がない（既定値で動作中）", "smashrate.example.toml をコピーして smashrate.toml を作る")
	} else {
		add("OK", "設定ファイル", cfgPath, "")
	}
	probe := filepath.Join(cfg.Server.DataDir, ".write-test")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		add("FAIL", "データフォルダ", cfg.Server.DataDir+" に書き込めない: "+err.Error(), "フォルダの権限を確認")
	} else {
		os.Remove(probe)
		add("OK", "データフォルダ", cfg.Server.DataDir, "")
	}
	if players, err := c.Players(); err != nil {
		add("FAIL", "データベース", cfg.Server.Database+": "+err.Error(), "")
	} else {
		ms, _ := c.Matches(1, 0)
		add("OK", "データベース", fmt.Sprintf("%s（プレイヤー %d 人、試合 %s）", cfg.Server.Database, len(players), map[bool]string{true: "あり", false: "なし"}[len(ms) > 0]), "")
	}
	if free, err := diskFree(cfg.Server.DataDir); err == nil {
		st := "OK"
		if free < 2<<30 {
			st = "WARN"
		}
		add(st, "空き容量", fmt.Sprintf("%.1f GB", float64(free)/(1<<30)), "2 GB 未満ならバックアップ世代を減らす")
	}

	// ---- web
	host, port, _ := net.SplitHostPort(cfg.Server.Addr)
	if host == "" {
		host = "localhost"
	}
	url := fmt.Sprintf("http://%s:%s/healthz", host, port)
	if resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(url); err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		add("OK", "Web サーバー", url+" が応答（serve は起動済み）", "")
	} else if ln, err := net.Listen("tcp", cfg.Server.Addr); err == nil {
		ln.Close()
		add("WARN", "Web サーバー", cfg.Server.Addr+" は空いている（serve は未起動）", "smashrate serve か、タスク smashrate を起動")
	} else {
		add("FAIL", "Web サーバー", cfg.Server.Addr+" を別のプログラムが使用中", "addr を変えるか、そのプログラムを止める")
	}
	if cfg.Server.Auth.Enabled() {
		add("OK", "ログイン", "パスワード設定あり", "")
	} else {
		add("WARN", "ログイン", "パスワード未設定（LAN 内なら誰でも記録・削除できる）", "外に公開する前に [server.auth] password を設定")
	}
	if cfg.Server.Auth.TrustCloudflareAccess && !strings.HasPrefix(cfg.Server.Addr, "127.0.0.1") && !strings.HasPrefix(cfg.Server.Addr, "localhost") {
		add("FAIL", "Cloudflare Access 信頼", "trust_cloudflare_access = true なのに addr が "+cfg.Server.Addr, "addr = \"127.0.0.1:8080\" にしてトンネル以外から届かないようにする")
	}

	// ---- Windows specifics
	if runtime.GOOS == "windows" {
		if secs, ok := sleepTimeoutAC(); ok {
			if secs == 0 {
				add("OK", "スリープ", "電源接続時はスリープしない", "")
			} else {
				add("FAIL", "スリープ", fmt.Sprintf("%d 分でスリープする設定（常駐が止まる）", secs/60), "powercfg /change standby-timeout-ac 0")
			}
		}
		switch sacState() {
		case "0":
			add("OK", "Smart App Control", "オフ", "")
		case "1":
			add("FAIL", "Smart App Control", "オン（署名のない smashrate.exe が止められる）", "Windows セキュリティ → アプリとブラウザー コントロール → Smart App Control → オフ")
		case "2":
			add("WARN", "Smart App Control", "評価モード（後でオンに転ぶと exe が止められる）", "今のうちにオフにする")
		default:
			add("SKIP", "Smart App Control", "状態を取得できず", "")
		}
	}

	// ---- capture
	if !cfg.Vision.Enabled {
		add("SKIP", "キャプチャ", "vision.enabled = false", "自動記録を使うなら true にして input を設定")
	} else {
		if path, err := exec.LookPath(cfg.Vision.FFmpeg); err != nil {
			add("FAIL", "ffmpeg", cfg.Vision.FFmpeg+" が見つからない", "winget install Gyan.FFmpeg のあと新しいターミナルで再実行")
		} else {
			ver := "?"
			if b, err := exec.Command(path, "-version").Output(); err == nil {
				ver = strings.Fields(string(b))[2]
			}
			add("OK", "ffmpeg", path+" ("+ver+")", "")
			devs := vision.ListDevices(cfg.Vision)
			want := strings.TrimPrefix(cfg.Vision.Input, "video=")
			if cfg.Vision.InputFormat == "dshow" {
				names := dshowVideoDevices(devs)
				switch {
				case len(names) == 0:
					add("FAIL", "キャプチャデバイス", "ffmpeg から映像デバイスが見えない", "キャプチャボードを USB 3.0 に挿し直す。smashrate vision devices で確認")
				case contains(names, want):
					add("OK", "キャプチャデバイス", want, "")
				default:
					add("FAIL", "キャプチャデバイス", fmt.Sprintf("%q は見つからない。見えているのは: %s", want, strings.Join(names, " / ")), "smashrate.toml の input = \"video=<上の名前>\" に直す")
				}
			} else {
				add("SKIP", "キャプチャデバイス", "input_format "+cfg.Vision.InputFormat+" の一覧確認は未対応", "smashrate vision devices で確認")
			}
		}
		// A DirectShow device can be open in one process only. If serve is
		// running it owns the device, so ask it for its latest frame instead
		// of opening the device a second time.
		snapURL := fmt.Sprintf("http://%s:%s/api/vision/snapshot", host, port)
		if resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(snapURL); err == nil {
			resp.Body.Close()
			switch resp.StatusCode {
			case 200:
				add("OK", "フレーム取得", "常駐プロセスが映像を受信中", "")
			case 503:
				add("FAIL", "フレーム取得", "常駐プロセスは起動しているが映像がまだ来ない", "Switch の電源とキャプチャボードの入力、USB 3.0 の挿し口を確認。タスクを再起動して再確認")
			default:
				add("WARN", "フレーム取得", "常駐プロセスの設定では vision が無効（設定変更後に再起動していない）", "schtasks /End /TN smashrate; schtasks /Run /TN smashrate")
			}
		} else if src, err := vision.OpenFFmpeg(cfg.Vision); err == nil {
			done := make(chan error, 1)
			go func() { _, err := src.Next(); done <- err }()
			select {
			case err := <-done:
				if err == nil {
					add("OK", "フレーム取得", fmt.Sprintf("%dx%d のフレームを受信", cfg.Vision.Width, cfg.Vision.Height), "")
				} else {
					add("FAIL", "フレーム取得", err.Error(), "EOF なら別のプログラム（OBS など）がデバイスを使用中の可能性。それ以外はデバイス名・input_args（解像度）を見直す")
				}
			case <-time.After(8 * time.Second):
				add("FAIL", "フレーム取得", "8 秒待っても映像が来ない", "Switch の電源とキャプチャボードの入力を確認")
			}
			src.Close()
		}
		det, _ := vision.NewDetector(cfg.Vision, c)
		if det != nil {
			n := det.TemplateCount(vision.KindScreen)
			if n == 0 {
				add("WARN", "リザルト画面の学習", "未学習（自動記録はまだ動かない）", "リザルト画面のスクショで smashrate vision teach を実行")
			} else {
				add("OK", "リザルト画面の学習", fmt.Sprintf("画面 %d、キャラ %d、順位 %d、名前 %d テンプレート", n, det.TemplateCount(vision.KindChar), det.TemplateCount(vision.KindRank), det.TemplateCount(vision.KindName)), "")
			}
		}
		// OCR
		if eng, err := ocr.New(cfg.Vision.OCR); err != nil {
			add("FAIL", "名前タグ OCR", err.Error(), "")
		} else if eng == nil {
			add("WARN", "名前タグ OCR", "無効（新しい名前は手動の learn が必要）", "[vision.ocr] engine = \"auto\"")
		} else if _, err := eng.Recognize(image.NewRGBA(image.Rect(0, 0, 40, 16))); err != nil {
			add("FAIL", "名前タグ OCR", eng.Name()+": "+err.Error(), "Windows の言語設定に日本語を追加（OCR の言語パックが入る）")
		} else {
			add("OK", "名前タグ OCR", eng.Name()+" が動作", "")
		}
	}

	// ---- outbound internet (office networks: proxy / TLS inspection)
	for _, probe := range []struct{ name, url, hint string }{
		{"外向き HTTPS (Cloudflare)", "https://www.cloudflare.com/cdn-cgi/trace", "cloudflared が外に出られない。プロキシ必須なら HTTPS_PROXY を設定、証明書エラーなら社内の SSL 検査が原因"},
		{"外向き HTTPS (Slack)", "https://slack.com/api/api.test", "Slack Socket Mode が外に出られない。上と同じ対処"},
	} {
		resp, err := (&http.Client{Timeout: 6 * time.Second}).Get(probe.url)
		switch {
		case err != nil:
			add("FAIL", probe.name, err.Error(), probe.hint)
		case resp.StatusCode == 407:
			resp.Body.Close()
			add("FAIL", probe.name, "407 Proxy Authentication Required", "プロキシ認証が必要。HTTPS_PROXY=http://user:pass@proxy:port を設定")
		case resp.StatusCode >= 400:
			resp.Body.Close()
			add("WARN", probe.name, fmt.Sprintf("HTTP %d", resp.StatusCode), probe.hint)
		default:
			resp.Body.Close()
			add("OK", probe.name, "HTTP "+resp.Status, "")
		}
	}

	// ---- slack
	if !cfg.Slack.Enabled {
		add("SKIP", "Slack", "slack.enabled = false", "")
	} else {
		switch {
		case !strings.HasPrefix(cfg.Slack.BotToken, "xoxb-"):
			add("FAIL", "Slack", "bot_token が xoxb- で始まっていない", "Bot User OAuth Token を設定")
		case !strings.HasPrefix(cfg.Slack.AppToken, "xapp-"):
			add("FAIL", "Slack", "app_token が xapp- で始まっていない", "App-Level Token（connections:write）を設定")
		default:
			api := slack.New(cfg.Slack.BotToken)
			if r, err := api.AuthTest(); err != nil {
				add("FAIL", "Slack", "認証失敗: "+err.Error(), "トークンを再発行してワークスペースに再インストール")
			} else {
				add("OK", "Slack", fmt.Sprintf("ワークスペース %s、ボット %s", r.Team, r.User), "")
			}
		}
	}

	// ---- cloudflared (optional)
	if path, err := exec.LookPath("cloudflared"); err == nil {
		add("OK", "cloudflared", path, "")
	} else {
		add("SKIP", "cloudflared", "未導入（外部公開しないなら不要）", "winget install Cloudflare.cloudflared")
	}

	// ---- print
	fails := 0
	for _, ck := range out {
		mark := map[string]string{"OK": "[OK]  ", "WARN": "[WARN]", "FAIL": "[FAIL]", "SKIP": "[--]  "}[ck.status]
		fmt.Printf("%s %-16s %s\n", mark, ck.name, ck.detail)
		if ck.hint != "" && ck.status != "OK" {
			fmt.Printf("       → %s\n", ck.hint)
		}
		if ck.status == "FAIL" {
			fails++
		}
	}
	if fails > 0 {
		return fmt.Errorf("%d 件の FAIL があります", fails)
	}
	fmt.Println("問題なし")
	return nil
}

var dshowVideoLine = regexp.MustCompile(`"([^"]+)"\s*\(video\)`)

func dshowVideoDevices(listing string) []string {
	var names []string
	for _, m := range dshowVideoLine.FindAllStringSubmatch(listing, -1) {
		names = append(names, m[1])
	}
	return names
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// sleepTimeoutAC returns the plugged-in sleep timeout in seconds (0 = never).
func sleepTimeoutAC() (int, bool) {
	out, err := exec.Command("powercfg", "/query", "SCHEME_CURRENT", "SUB_SLEEP", "STANDBYIDLE").Output()
	if err != nil {
		return 0, false
	}
	// the AC line comes first in every locale: "... AC ...: 0x00000000"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "AC") && strings.Contains(line, "0x") {
			hex := line[strings.LastIndex(line, "0x")+2:]
			var v int
			if _, err := fmt.Sscanf(strings.TrimSpace(hex), "%x", &v); err == nil {
				return v, true
			}
		}
	}
	return 0, false
}

// sacState reads Smart App Control's state: "0" off, "1" on, "2" evaluation.
func sacState() string {
	out, err := exec.Command("reg", "query", `HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy`, "/v", "VerifiedAndReputablePolicyState").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	for i, tok := range f {
		if tok == "VerifiedAndReputablePolicyState" && i+2 < len(f) {
			switch strings.ToLower(f[i+2]) {
			case "0x0":
				return "0"
			case "0x1":
				return "1"
			case "0x2":
				return "2"
			}
		}
	}
	return ""
}
