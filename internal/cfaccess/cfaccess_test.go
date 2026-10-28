package cfaccess

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

const (
	issuer = "https://myteam.cloudflareaccess.com"
	aud    = "4714c1358e65fe4b408ad6d432a5f878f08194bdb4752441fd56faefa9b2b6f2"
)

func TestTeamIssuer(t *testing.T) {
	for in, want := range map[string]string{
		"myteam":                               issuer,
		"myteam.cloudflareaccess.com":          issuer,
		"https://myteam.cloudflareaccess.com/": issuer,
	} {
		if got, err := TeamIssuer(in); err != nil || got != want {
			t.Errorf("TeamIssuer(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "http://myteam.cloudflareaccess.com"} {
		if _, err := TeamIssuer(bad); err == nil {
			t.Errorf("TeamIssuer(%q) accepted", bad)
		}
	}
}

func TestVerify(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	certs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	}))
	defer certs.Close()
	v := newVerifier(context.Background(), issuer, certs.URL, aud)

	sign := func(k *rsa.PrivateKey, c jwt.Claims, extra map[string]any) string {
		s, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: k}, (&jose.SignerOptions{}).WithHeader("kid", "k1"))
		if err != nil {
			t.Fatal(err)
		}
		tok, err := jwt.Signed(s).Claims(c).Claims(extra).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	now := time.Now()
	good := jwt.Claims{Issuer: issuer, Audience: jwt.Audience{aud}, Expiry: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now)}
	req := func(tok string, cookie bool) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
		if cookie {
			r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
		} else if tok != "" {
			r.Header.Set(HeaderName, tok)
		}
		return r
	}

	id, err := v.Verify(req(sign(key, good, map[string]any{"email": "me@example.com"}), false))
	if err != nil || id.Name() != "me@example.com" {
		t.Fatalf("valid token: %+v %v", id, err)
	}
	if id, err := v.Verify(req(sign(key, good, map[string]any{"common_name": "svc.access"}), true)); err != nil || id.Name() != "svc.access" {
		t.Fatalf("service token in the cookie: %+v %v", id, err)
	}

	wrongAud, wrongIss, expired := good, good, good
	wrongAud.Audience = jwt.Audience{"another-app"}
	wrongIss.Issuer = "https://evil.cloudflareaccess.com"
	expired.Expiry = jwt.NewNumericDate(now.Add(-time.Minute))
	for name, tok := range map[string]string{
		"no token":       "",
		"wrong audience": sign(key, wrongAud, nil),
		"wrong issuer":   sign(key, wrongIss, nil),
		"expired":        sign(key, expired, nil),
		"other key":      sign(other, good, nil),
		"not a jwt":      "garbage",
	} {
		if _, err := v.Verify(req(tok, false)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
