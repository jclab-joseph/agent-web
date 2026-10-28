package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/jclab-joseph/agent-web/internal/cfaccess"
)

// Sessions run commands with the user's permissions, so every request goes
// through three checks:
//
//  1. Host: unless --allowed-any-host, the Host must be a loopback name or
//     one of --allowed-hosts. Without --allowed-hosts, any Host is accepted
//     once authentication (token or Cloudflare Access) is configured. This
//     blocks DNS rebinding against an unauthenticated loopback server.
//  2. Origin: state-changing requests from another origin are rejected.
//  3. Authentication: every configured method must pass for the API.

func tokenCookieValue(token string) string {
	sum := sha256.Sum256([]byte("agent-web:" + token))
	return hex.EncodeToString(sum[:])
}

// authResult is the outcome of the authentication checks.
type authResult struct {
	// Missing is the first method that failed: "cloudflare", "token" or "".
	Missing string
	// User is the Cloudflare Access identity, if any.
	User string
	// Err explains a Cloudflare failure.
	Err error
}

func (a authResult) ok() bool { return a.Missing == "" }

func (s *Server) authConfigured() bool { return s.opts.Token != "" || s.opts.Cloudflare != nil }

func (s *Server) authenticate(r *http.Request) authResult {
	var res authResult
	if s.opts.Cloudflare != nil {
		id, err := s.opts.Cloudflare.Verify(r)
		if err != nil {
			return authResult{Missing: "cloudflare", Err: err}
		}
		res.User = id.Name()
	}
	if s.opts.Token != "" {
		c, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(tokenCookieValue(s.opts.Token))) != 1 {
			res.Missing = "token"
		}
	}
	return res
}

// hostAllowed applies the Host rule.
func (s *Server) hostAllowed(hostport string) bool {
	if s.opts.AllowAnyHost || loopbackHost(hostport) {
		return true
	}
	if len(s.opts.AllowedHosts) == 0 {
		return s.authConfigured()
	}
	host := hostOnly(hostport)
	for _, pat := range s.opts.AllowedHosts {
		if MatchHost(pat, host) {
			return true
		}
	}
	return false
}

// MatchHost reports whether host matches pattern: the same name (case and
// a trailing dot ignored, any port), or for "*.example.com" any subdomain
// of example.com.
func MatchHost(pattern, host string) bool {
	norm := func(h string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(h)), ".") }
	pattern, host = norm(hostOnly(pattern)), norm(host)
	if pattern == "" || host == "" {
		return false
	}
	if rest, ok := strings.CutPrefix(pattern, "*."); ok {
		return strings.HasSuffix(host, "."+rest)
	}
	return host == pattern
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostport, "[]")
}

func loopbackHost(hostport string) bool {
	host := hostOnly(hostport)
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// publicAPI are the API paths usable before authenticating.
var publicAPI = map[string]bool{"/api/info": true, "/api/auth/login": true, "/api/auth/logout": true}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			msg := "forbidden host " + hostOnly(r.Host) + ": allow it with --allowed-hosts"
			if !s.authConfigured() {
				msg += ", or configure authentication (--token or --cloudflare-aud)"
			}
			http.Error(w, msg, http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "cross-origin request rejected", http.StatusForbidden)
					return
				}
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			a := s.authenticate(r)
			r = r.WithContext(context.WithValue(r.Context(), authKey{}, a))
			if !a.ok() && !publicAPI[r.URL.Path] {
				detail := "log in with the access token"
				if a.Missing == "cloudflare" {
					detail = "sign in through Cloudflare Access"
					if a.Err != nil && !errors.Is(a.Err, cfaccess.ErrNoToken) {
						detail += " (" + a.Err.Error() + ")"
					}
				}
				writeProblem(w, http.StatusUnauthorized, "Unauthorized", detail)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type authKey struct{}

// authFrom returns the authentication result guard stored for the request.
func authFrom(ctx context.Context) authResult {
	a, _ := ctx.Value(authKey{}).(authResult)
	return a
}

func writeProblem(w http.ResponseWriter, status int, title, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	b, _ := json.Marshal(map[string]any{"title": title, "status": status, "detail": detail})
	_, _ = w.Write(b)
}
