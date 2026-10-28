package server

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/jclab-joseph/agent-web/internal/cfaccess"
)

func TestMatchHost(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		{"agent.example.com", "agent.example.com", true},
		{"agent.example.com", "AGENT.example.com.", true},
		{"agent.example.com:8787", "agent.example.com", true},
		{"agent.example.com", "evil.com", false},
		{"*.example.com", "a.b.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "badexample.com", false},
		{"", "x", false},
	}
	for _, c := range cases {
		if got := MatchHost(c.pattern, c.host); got != c.want {
			t.Errorf("MatchHost(%q, %q) = %v", c.pattern, c.host, got)
		}
	}
}

// do sends GET /api/info (public, so only the Host rule decides) and
// returns the status.
func do(t *testing.T, s *Server, host string, mod func(*http.Request)) int {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/info", nil)
	r.Host = host
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w.Code
}

func TestHostRules(t *testing.T) {
	cases := []struct {
		name string
		opts Options
		host string
		want int
	}{
		{"no auth, loopback", Options{}, "127.0.0.1:8787", 200},
		{"no auth, localhost", Options{}, "localhost:8787", 200},
		{"no auth, other host", Options{}, "agent.example.com", 403},
		{"no auth, allowed host", Options{AllowedHosts: []string{"agent.example.com"}}, "agent.example.com", 200},
		{"no auth, allowed wildcard", Options{AllowedHosts: []string{"*.example.com"}}, "agent.example.com", 200},
		{"no auth, not in the list", Options{AllowedHosts: []string{"agent.example.com"}}, "evil.com", 403},
		{"no auth, loopback with a list", Options{AllowedHosts: []string{"agent.example.com"}}, "127.0.0.1:8787", 200},
		{"any host", Options{AllowAnyHost: true}, "evil.com", 200},
		{"token, any host", Options{Token: "t"}, "agent.example.com", 200},
		{"token and a list", Options{Token: "t", AllowedHosts: []string{"agent.example.com"}}, "evil.com", 403},
	}
	for _, c := range cases {
		if got := do(t, New(nil, c.opts), c.host, nil); got != c.want {
			t.Errorf("%s: status %d, want %d", c.name, got, c.want)
		}
	}
}

func TestCloudflareAccess(t *testing.T) {
	const issuer, aud = "https://myteam.cloudflareaccess.com", "app-aud"
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	certs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer certs.Close()
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "k"))
	tok, _ := jwt.Signed(signer).Claims(jwt.Claims{Issuer: issuer, Audience: jwt.Audience{aud},
		Expiry: jwt.NewNumericDate(time.Now().Add(time.Hour))}).Claims(map[string]any{"email": "me@example.com"}).Serialize()
	withJWT := func(r *http.Request) { r.Header.Set(cfaccess.HeaderName, tok) }

	s := New(nil, Options{Cloudflare: cfaccess.NewWithKeys(issuer, certs.URL, aud)})
	info := func(mod func(*http.Request)) ServerInfo {
		r := httptest.NewRequest(http.MethodGet, "http://agent.example.com/api/info", nil)
		if mod != nil {
			mod(r)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		var out ServerInfo
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return out
	}

	// Any host is fine once Access authenticates.
	got := info(withJWT)
	if !got.Authenticated || got.User != "me@example.com" || !got.CloudflareAccess || got.Home == "" {
		t.Fatalf("with a token: %+v", got)
	}
	got = info(nil)
	if got.Authenticated || got.Missing != "cloudflare" || got.Home != "" {
		t.Fatalf("without a token: %+v", got)
	}

	api := func(mod func(*http.Request)) (int, string) {
		r := httptest.NewRequest(http.MethodGet, "http://agent.example.com/api/workdirs", nil)
		if mod != nil {
			mod(r)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := api(nil); code != 401 || !strings.Contains(body, "Cloudflare Access") {
		t.Fatalf("API without a token: %d %s", code, body)
	}
	if code, _ := api(func(r *http.Request) { r.Header.Set(cfaccess.HeaderName, tok+"x") }); code != 401 {
		t.Fatalf("API with a bad token: %d", code)
	}
	// Both methods configured: both are needed.
	s = New(nil, Options{Token: "secret", Cloudflare: cfaccess.NewWithKeys(issuer, certs.URL, aud)})
	if got := info(withJWT); got.Authenticated || got.Missing != "token" {
		t.Fatalf("token missing: %+v", got)
	}
	if got := info(func(r *http.Request) {
		withJWT(r)
		r.AddCookie(&http.Cookie{Name: cookieName, Value: tokenCookieValue("secret")})
	}); !got.Authenticated {
		t.Fatalf("both: %+v", got)
	}
}
