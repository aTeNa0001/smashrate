package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLoginLimiterLocksAnAddressAfterRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	l := loginLimiter{fails: map[string]*loginFails{}, now: func() time.Time { return now }}

	for i := 0; i < loginMaxFails-1; i++ {
		l.fail("1.2.3.4")
	}
	if l.blocked("1.2.3.4") {
		t.Fatalf("blocked after %d failures", loginMaxFails-1)
	}
	l.fail("1.2.3.4")
	if !l.blocked("1.2.3.4") {
		t.Fatalf("not blocked after %d failures", loginMaxFails)
	}
	if l.blocked("5.6.7.8") {
		t.Fatal("another address is blocked")
	}

	// the lock expires on its own
	now = now.Add(loginBlock + time.Second)
	if l.blocked("1.2.3.4") {
		t.Fatal("still blocked after the lockout")
	}

	// failures older than the window do not count towards a new lock
	for i := 0; i < loginMaxFails-1; i++ {
		l.fail("1.2.3.4")
	}
	now = now.Add(loginWindow + time.Second)
	l.fail("1.2.3.4")
	if l.blocked("1.2.3.4") {
		t.Fatal("old failures counted towards the lock")
	}

	// a successful login clears the record
	l.ok("1.2.3.4")
	if l.fails["1.2.3.4"] != nil {
		t.Fatal("record kept after ok")
	}
}

func TestClientAddrPrefersCloudflareHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	if got := clientAddr(r); got != "127.0.0.1" {
		t.Fatalf("peer: %q", got)
	}
	r.Header.Set("CF-Connecting-IP", "203.0.113.9")
	if got := clientAddr(r); got != "203.0.113.9" {
		t.Fatalf("cloudflare header: %q", got)
	}
}
