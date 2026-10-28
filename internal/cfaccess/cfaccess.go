// Package cfaccess verifies Cloudflare Access application tokens.
//
// Cloudflare Access sends a signed JWT with every request it lets through:
// the Cf-Access-Jwt-Assertion header (and the CF_Authorization cookie). A
// token is accepted when it is signed by one of the team's keys
// (https://<team>.cloudflareaccess.com/cdn-cgi/access/certs), was issued by
// that team, is unexpired, and names the application's AUD tag.
// See https://developers.cloudflare.com/cloudflare-one/identity/authorization-cookie/validating-json/
package cfaccess

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// HeaderName is the request header that carries the token.
const HeaderName = "Cf-Access-Jwt-Assertion"

// CookieName is the cookie that carries the token in the browser.
const CookieName = "CF_Authorization"

// ErrNoToken means the request carries no Access token: it did not come
// through Cloudflare Access.
var ErrNoToken = errors.New("no Cloudflare Access token")

// Identity is who Access authenticated.
type Identity struct {
	// Email is the user's email; empty for a service token.
	Email string
	// CommonName is the service token's client id; empty for a user.
	CommonName string
}

// Name is the email or the service token's name.
func (i Identity) Name() string {
	if i.Email != "" {
		return i.Email
	}
	return i.CommonName
}

// Verifier checks tokens for one Access application.
type Verifier struct {
	v *oidc.IDTokenVerifier
}

// TeamIssuer turns a team name ("myteam"), team domain
// ("myteam.cloudflareaccess.com") or URL into the issuer URL.
func TeamIssuer(team string) (string, error) {
	team = strings.TrimSpace(team)
	if team == "" {
		return "", errors.New("the Cloudflare Access team domain is empty")
	}
	if !strings.Contains(team, "://") {
		team = "https://" + team
	}
	u, err := url.Parse(team)
	if err != nil || u.Host == "" || u.Scheme != "https" {
		return "", fmt.Errorf("invalid Cloudflare Access team domain %q", team)
	}
	host := u.Host
	if !strings.Contains(host, ".") {
		host += ".cloudflareaccess.com"
	}
	return "https://" + host, nil
}

// New returns a verifier for the application with this AUD tag in the team.
func New(team, aud string) (*Verifier, error) {
	issuer, err := TeamIssuer(team)
	if err != nil {
		return nil, err
	}
	aud = strings.TrimSpace(aud)
	if aud == "" {
		return nil, errors.New("the Cloudflare Access AUD tag is empty")
	}
	return newVerifier(context.Background(), issuer, issuer+"/cdn-cgi/access/certs", aud), nil
}

// NewWithKeys is New with an explicit issuer and key set URL (for tests).
func NewWithKeys(issuer, certsURL, aud string) *Verifier {
	return newVerifier(context.Background(), issuer, certsURL, aud)
}

func newVerifier(ctx context.Context, issuer, certsURL, aud string) *Verifier {
	keys := oidc.NewRemoteKeySet(ctx, certsURL)
	return &Verifier{v: oidc.NewVerifier(issuer, keys, &oidc.Config{ClientID: aud, SupportedSigningAlgs: []string{oidc.RS256}})}
}

// Token returns the Access token of a request.
func Token(r *http.Request) string {
	if t := r.Header.Get(HeaderName); t != "" {
		return t
	}
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	return ""
}

// Verify checks a request's token.
func (v *Verifier) Verify(r *http.Request) (Identity, error) {
	raw := Token(r)
	if raw == "" {
		return Identity{}, ErrNoToken
	}
	tok, err := v.v.Verify(r.Context(), raw)
	if err != nil {
		return Identity{}, fmt.Errorf("invalid Cloudflare Access token: %w", err)
	}
	var claims struct {
		Email      string `json:"email"`
		CommonName string `json:"common_name"`
	}
	if err := tok.Claims(&claims); err != nil {
		return Identity{}, err
	}
	return Identity{Email: claims.Email, CommonName: claims.CommonName}, nil
}
