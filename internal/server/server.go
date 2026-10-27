// Package server is the HTTP API (described by OpenAPI through Huma) and the
// embedded web UI.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	bridge "github.com/jclab-joseph/clicodebridge"

	"github.com/jclab-joseph/agent-web/internal/manager"
	"github.com/jclab-joseph/agent-web/internal/store"
	"github.com/jclab-joseph/agent-web/internal/version"
)

func init() {
	// Lists are always sent as arrays (never null), so the generated client
	// types need not allow null.
	huma.DefaultArrayNullable = false
}

const cookieName = "agent_web_session"

// Options configure the server.
type Options struct {
	// Token, when set, is required to use the API.
	Token string
	// Static serves the web UI; nil serves nothing besides the API.
	Static fs.FS
}

// Server is the HTTP handler.
type Server struct {
	m       *manager.Manager
	opts    Options
	api     huma.API
	handler http.Handler
}

// New builds the server. m may be nil when only the OpenAPI document is needed.
func New(m *manager.Manager, opts Options) *Server {
	s := &Server{m: m, opts: opts}
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("agent-web", version.Get().Version)
	cfg.Info.Description = "Run Claude Code and Codex sessions from the browser."
	cfg.CreateHooks = nil // no $schema fields or Link headers in responses
	cfg.OpenAPIPath = "/api/openapi"
	cfg.DocsPath = "/api/docs"
	cfg.SchemasPath = ""
	s.api = humago.New(mux, cfg)
	s.register()
	if opts.Static != nil {
		mux.Handle("GET /", spa(opts.Static))
	}
	s.handler = s.guard(mux)
	return s
}

// API returns the Huma API (for the OpenAPI document).
func (s *Server) API() huma.API { return s.api }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.handler.ServeHTTP(w, r) }

// --- access control ---------------------------------------------------------------

func tokenCookieValue(token string) string {
	sum := sha256.Sum256([]byte("agent-web:" + token))
	return hex.EncodeToString(sum[:])
}

func (s *Server) authenticated(r *http.Request) bool {
	if s.opts.Token == "" {
		return true
	}
	c, err := r.Cookie(cookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(tokenCookieValue(s.opts.Token))) == 1
}

// publicAPI are the API paths usable before logging in.
var publicAPI = map[string]bool{"/api/info": true, "/api/auth/login": true, "/api/auth/logout": true}

// guard applies the access rules. Sessions run with the user's permissions,
// so besides the token it blocks DNS rebinding (an unknown Host when no
// token protects a loopback server) and cross-site requests.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Token == "" && !loopbackHost(r.Host) {
			http.Error(w, "forbidden host; set a token to serve other hosts", http.StatusForbidden)
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
		if strings.HasPrefix(r.URL.Path, "/api/") && !publicAPI[r.URL.Path] && !s.authenticated(r) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"title":"Unauthorized","status":401,"detail":"log in with the access token"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// --- static UI ----------------------------------------------------------------------

// spa serves the built web UI, falling back to index.html for client routes.
func spa(static fs.FS) http.Handler {
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if strings.HasPrefix(p, "api/") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(static, p); err != nil {
			index, err := fs.ReadFile(static, "index.html")
			if err != nil {
				http.Error(w, "the web UI is not built into this binary (run `make frontend`)", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write(index)
			return
		}
		if strings.HasPrefix(p, "_nuxt/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// --- errors -------------------------------------------------------------------------

// apiError maps manager and library errors to HTTP errors.
func apiError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	case errors.Is(err, manager.ErrNotFound), errors.Is(err, store.ErrNotFound), errors.Is(err, bridge.ErrUnknownRequest):
		return huma.Error404NotFound(msg)
	case errors.Is(err, manager.ErrInvalid), errors.Is(err, bridge.ErrUnsupported):
		return huma.Error400BadRequest(msg)
	case errors.Is(err, manager.ErrNotRunning), errors.Is(err, bridge.ErrBusy):
		return huma.Error409Conflict(msg)
	case errors.Is(err, bridge.ErrExited):
		return huma.Error410Gone(msg)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, bridge.ErrScreenTimeout):
		return huma.Error504GatewayTimeout(msg)
	}
	return huma.Error500InternalServerError(msg)
}
