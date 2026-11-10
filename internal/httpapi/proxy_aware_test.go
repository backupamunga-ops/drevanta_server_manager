package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

var tlsConnStateStub tls.ConnectionState

func TestRequestIsHTTPS(t *testing.T) {
	cases := []struct {
		name string
		tls  bool
		xffp string
		want bool
	}{
		{"plain tunnel request", false, "", false},
		{"proxy https", false, "https", true},
		{"proxy http downgrade attempt", false, "http", false},
		{"direct TLS", true, "", true},
	}
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/auth/login", nil)
		if tc.tls {
			r.TLS = &tlsConnStateStub
		}
		if tc.xffp != "" {
			r.Header.Set("X-Forwarded-Proto", tc.xffp)
		}
		if got := requestIsHTTPS(r); got != tc.want {
			t.Errorf("%s: requestIsHTTPS = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSessionCookieSecureFlag(t *testing.T) {
	// Tunnel path: no Secure flag (browser on http://127.0.0.1 must
	// keep receiving the cookie).
	rec := httptest.NewRecorder()
	setSessionCookie(rec, httptest.NewRequest(http.MethodPost, "http://127.0.0.1/x", nil), "tok")
	if got := rec.Header().Get("Set-Cookie"); !contains(got, "HttpOnly") || contains(got, "Secure") {
		t.Fatalf("tunnel cookie = %q, want HttpOnly without Secure", got)
	}

	// Proxied HTTPS path: Secure must be present.
	rec2 := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/x", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	setSessionCookie(rec2, req, "tok")
	if got := rec2.Header().Get("Set-Cookie"); !contains(got, "Secure") {
		t.Fatalf("proxied cookie = %q, want Secure", got)
	}
}

func TestClientKeyBehindProxy(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/v1/auth/login", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct {
		name   string
		remote string
		xff    string
		want   string
	}{
		{"proxy forwards client ip", "127.0.0.1:5555", "203.0.113.7", "login:203.0.113.7"},
		{"proxy forwards ip:port form", "127.0.0.1:5555", "203.0.113.7:443", "login:203.0.113.7"},
		{"tunnel: raw loopback", "127.0.0.1:5555", "", "login:127.0.0.1"},
		{"non-loopback ignores xff", "198.51.100.9:1234", "203.0.113.7", "login:198.51.100.9"},
		{"proxy garbage xff falls back", "127.0.0.1:5555", "not an ip", "login:127.0.0.1"},
	}
	for _, tc := range cases {
		if got := clientKey(mk(tc.remote, tc.xff), "login"); got != tc.want {
			t.Errorf("%s: clientKey = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
