package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aTeNa0001/smashrate/internal/config"
)

const sessionCookie = "smashrate_session"

// auth implements the simple login: a password from the config, a signed
// session cookie, an optional API bearer token and optional trust of the
// Cloudflare Access header.
type auth struct {
	cfg    config.AuthConfig
	secret []byte
	limit  loginLimiter
}

func newAuth(cfg config.AuthConfig) *auth {
	sum := sha256.Sum256([]byte("smashrate-session:" + cfg.Password))
	return &auth{cfg: cfg, secret: sum[:], limit: loginLimiter{fails: map[string]*loginFails{}, now: time.Now}}
}

// A shared password invites guessing, so an address that gets it wrong
// loginMaxFails times within loginWindow is locked out for loginBlock.
const (
	loginMaxFails = 5
	loginWindow   = 10 * time.Minute
	loginBlock    = 10 * time.Minute
)

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*loginFails
	now   func() time.Time
}

type loginFails struct {
	n     int
	first time.Time // start of the counting window
	until time.Time // locked out until (zero = not locked)
}

// blocked reports whether addr is currently locked out.
func (l *loginLimiter) blocked(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[addr]
	return f != nil && l.now().Before(f.until)
}

// fail records a wrong password from addr and locks it out once it has
// failed too often. Stale entries are dropped so the map cannot grow forever.
func (l *loginLimiter) fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for a, f := range l.fails {
		if now.Sub(f.first) > loginWindow && now.After(f.until) {
			delete(l.fails, a)
		}
	}
	f := l.fails[addr]
	if f == nil || now.Sub(f.first) > loginWindow {
		f = &loginFails{first: now}
		l.fails[addr] = f
	}
	f.n++
	if f.n >= loginMaxFails {
		f.until = now.Add(loginBlock)
	}
}

// ok clears addr after a successful login.
func (l *loginLimiter) ok(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, addr)
}

// clientAddr is the visitor's address: the one Cloudflare saw when the
// request came through the tunnel, otherwise the TCP peer.
func clientAddr(r *http.Request) string {
	if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
		return ip
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (a *auth) enabled() bool { return a.cfg.Enabled() }

func (a *auth) token() string {
	m := hmac.New(sha256.New, a.secret)
	m.Write([]byte("session-v1"))
	return hex.EncodeToString(m.Sum(nil))
}

func (a *auth) checkPassword(p string) bool {
	return subtle.ConstantTimeCompare([]byte(p), []byte(a.cfg.Password)) == 1
}

// loggedIn reports whether the request carries valid credentials.
func (a *auth) loggedIn(r *http.Request) bool {
	if !a.enabled() {
		return true
	}
	if c, err := r.Cookie(sessionCookie); err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(a.token())) == 1 {
		return true
	}
	if a.cfg.APIToken != "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") &&
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(h, "Bearer ")), []byte(a.cfg.APIToken)) == 1 {
			return true
		}
	}
	if a.cfg.TrustCloudflareAccess && r.Header.Get("Cf-Access-Authenticated-User-Email") != "" {
		return true
	}
	return false
}

func (a *auth) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: a.token(), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r), MaxAge: int((90 * 24 * time.Hour).Seconds()),
	})
}

func clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// exempt paths never require a login.
func exempt(path string) bool {
	return path == "/login" || path == "/logout" || path == "/healthz"
}

// requireLogin is the gate applied in ServeHTTP. Writes always need a login;
// reads only when configured.
func (s *Server) requireLogin(w http.ResponseWriter, r *http.Request) bool {
	if !s.auth.enabled() || exempt(r.URL.Path) || s.auth.loggedIn(r) {
		return false
	}
	write := r.Method != http.MethodGet && r.Method != http.MethodHead
	// Team balancing is a query that happens to use POST (it carries a member list).
	if r.URL.Path == "/balance" || r.URL.Path == "/api/balance" {
		write = false
	}
	// Learning and player administration are always protected, even for reads.
	if r.URL.Path == "/learn" || r.URL.Path == "/players" {
		write = true
	}
	if !write && !s.auth.cfg.ReadRequiresLogin {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "login required (session cookie or Authorization: Bearer <api_token>)"})
		return true
	}
	next := r.URL.Path
	if !write {
		next = r.URL.RequestURI()
	}
	http.Redirect(w, r, "/login?next="+url.QueryEscape(next), http.StatusSeeOther)
	return true
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, r, "login", map[string]any{"Next": safeNext(r.URL.Query().Get("next")), "Error": r.URL.Query().Get("error")})
}

func (s *Server) loginSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	next := safeNext(r.FormValue("next"))
	addr := clientAddr(r)
	if s.auth.limit.blocked(addr) {
		http.Redirect(w, r, "/login?error=locked&next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	if !s.auth.enabled() || !s.auth.checkPassword(r.FormValue("password")) {
		s.auth.limit.fail(addr)
		time.Sleep(500 * time.Millisecond) // blunt brute-force slowdown
		http.Redirect(w, r, "/login?error=1&next="+url.QueryEscape(next), http.StatusSeeOther)
		return
	}
	s.auth.limit.ok(addr)
	s.auth.setSession(w, r)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearSession(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// safeNext only allows local paths as the post-login destination.
func safeNext(n string) string {
	if n == "" || !strings.HasPrefix(n, "/") || strings.HasPrefix(n, "//") {
		return "/"
	}
	return n
}
