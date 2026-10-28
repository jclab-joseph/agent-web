// Package server is the HTTP API (described by OpenAPI through Huma) and the
// embedded web UI.
package server

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	bridge "github.com/jclab-joseph/clicodebridge"

	"github.com/jclab-joseph/agent-web/internal/cfaccess"
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
	// Cloudflare, when set, requires a valid Cloudflare Access token.
	Cloudflare *cfaccess.Verifier
	// AllowedHosts are Host names (exact, or "*.example.com") accepted
	// besides loopback names. When set, other hosts are rejected even with
	// authentication configured.
	AllowedHosts []string
	// AllowAnyHost turns the Host check off.
	AllowAnyHost bool
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
