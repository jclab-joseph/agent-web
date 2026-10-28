package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/jclab-joseph/agent-web/internal/manager"
	"github.com/jclab-joseph/agent-web/internal/model"
	"github.com/jclab-joseph/agent-web/internal/version"
)

// actionTimeout bounds an action. Resuming the CLI and driving its TUI can
// take a while.
const actionTimeout = 2 * time.Minute

func actionCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, actionTimeout)
}

// --- types ----------------------------------------------------------------------------

// ServerInfo describes the server.
type ServerInfo struct {
	Version            version.BuildInfo `json:"version"`
	AuthRequired       bool              `json:"authRequired" doc:"An access token is required (log in with login)"`
	CloudflareAccess   bool              `json:"cloudflareAccess" doc:"Requests must come through Cloudflare Access"`
	Authenticated      bool              `json:"authenticated"`
	Missing            string            `json:"missing,omitempty" enum:"cloudflare,token" doc:"The authentication that is missing, if any"`
	User               string            `json:"user,omitempty" doc:"The Cloudflare Access user"`
	IdleTimeoutSeconds int               `json:"idleTimeoutSeconds" doc:"Idle CLIs are stopped after this long; 0 = never"`
	Home               string            `json:"home" doc:"The server user's home directory (empty before authenticating)"`
}

// Choice is one selectable value.
type Choice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ProviderInfo describes a CLI.
type ProviderInfo struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Version     string   `json:"version,omitempty"`
	Models      []Choice `json:"models"`
	Efforts     []Choice `json:"efforts"`
	Modes       []Choice `json:"modes"`
}

// Workdir is a recently used working directory.
type Workdir struct {
	Path       string    `json:"path"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	UseCount   int       `json:"useCount"`
	Exists     bool      `json:"exists"`
}

// DirEntry is a directory.
type DirEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DirListing lists subdirectories.
type DirListing struct {
	Dir     string     `json:"dir" doc:"The directory listed"`
	Parent  string     `json:"parent,omitempty"`
	Exists  bool       `json:"exists" doc:"The requested path is an existing directory"`
	Entries []DirEntry `json:"entries"`
}

type SessionPath struct {
	ID string `path:"id" doc:"Session id"`
}

type sessionOutput struct{ Body model.Session }

type emptyBody struct{}

// --- registration -----------------------------------------------------------------------

func (s *Server) register() {
	api := s.api
	tagsSys := []string{"system"}
	tagsSess := []string{"sessions"}

	huma.Register(api, huma.Operation{OperationID: "getInfo", Method: http.MethodGet, Path: "/api/info", Tags: tagsSys,
		Summary: "Server version and how to authenticate", Description: "Public: the UI calls it before logging in."},
		func(ctx context.Context, in *struct{}) (*struct{ Body ServerInfo }, error) {
			a := authFrom(ctx)
			info := ServerInfo{Version: version.Get(), AuthRequired: s.opts.Token != "", CloudflareAccess: s.opts.Cloudflare != nil,
				Authenticated: a.ok(), Missing: a.Missing, User: a.User}
			if a.ok() {
				info.Home, _ = os.UserHomeDir()
				if s.m != nil {
					info.IdleTimeoutSeconds = int(s.m.IdleTimeout() / time.Second)
				}
			}
			return &struct{ Body ServerInfo }{Body: info}, nil
		})

	type cookieOutput struct {
		SetCookie http.Cookie `header:"Set-Cookie"`
	}
	huma.Register(api, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/api/auth/login", Tags: tagsSys,
		Summary: "Log in with the access token", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				Token string `json:"token"`
			}
		}) (*cookieOutput, error) {
			if s.opts.Token == "" {
				return &cookieOutput{}, nil
			}
			if subtle.ConstantTimeCompare([]byte(in.Body.Token), []byte(s.opts.Token)) != 1 {
				time.Sleep(500 * time.Millisecond)
				return nil, huma.Error401Unauthorized("wrong token")
			}
			return &cookieOutput{SetCookie: http.Cookie{Name: cookieName, Value: tokenCookieValue(s.opts.Token), Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 90 * 24 * 3600}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/api/auth/logout", Tags: tagsSys,
		Summary: "Log out", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct{}) (*cookieOutput, error) {
			return &cookieOutput{SetCookie: http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
				SameSite: http.SameSiteStrictMode}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "listProviders", Method: http.MethodGet, Path: "/api/providers", Tags: tagsSys,
		Summary: "Available CLIs with their models, efforts and modes"},
		func(ctx context.Context, in *struct{}) (*struct{ Body []ProviderInfo }, error) {
			out := &struct{ Body []ProviderInfo }{Body: []ProviderInfo{}}
			ps := s.m.Providers()
			names := make([]string, 0, len(ps))
			for n := range ps {
				names = append(names, n)
			}
			sort.Strings(names) // claude, codex
			for _, n := range names {
				info, err := ps[n].Info(ctx)
				if err != nil {
					continue // not installed
				}
				pi := ProviderInfo{Name: info.Name, DisplayName: info.DisplayName, Version: info.Version,
					Models: []Choice{}, Efforts: []Choice{}, Modes: []Choice{}}
				for _, c := range info.Models {
					pi.Models = append(pi.Models, Choice{ID: c.ID, Name: c.Name, Description: c.Description})
				}
				for _, c := range info.Efforts {
					pi.Efforts = append(pi.Efforts, Choice{ID: c.ID, Name: c.Name, Description: c.Description})
				}
				for _, c := range info.Modes {
					pi.Modes = append(pi.Modes, Choice{ID: c.ID, Name: c.Name, Description: c.Description})
				}
				out.Body = append(out.Body, pi)
			}
			return out, nil
		})

	// --- working directories ---

	huma.Register(api, huma.Operation{OperationID: "listWorkdirs", Method: http.MethodGet, Path: "/api/workdirs", Tags: tagsSys,
		Summary: "Recently used working directories"},
		func(ctx context.Context, in *struct{}) (*struct{ Body []Workdir }, error) {
			ws, err := s.m.Store().ListWorkdirs(ctx, 50)
			if err != nil {
				return nil, apiError(err)
			}
			out := &struct{ Body []Workdir }{Body: []Workdir{}}
			for _, w := range ws {
				fi, err := os.Stat(w.Path)
				out.Body = append(out.Body, Workdir{Path: w.Path, LastUsedAt: w.LastUsedAt, UseCount: w.UseCount, Exists: err == nil && fi.IsDir()})
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "deleteWorkdir", Method: http.MethodDelete, Path: "/api/workdirs", Tags: tagsSys,
		Summary: "Forget a recently used working directory", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Path string `query:"path" required:"true"`
		}) (*struct{}, error) {
			return nil, apiError(s.m.Store().DeleteWorkdir(ctx, in.Path))
		})

	huma.Register(api, huma.Operation{OperationID: "listDirs", Method: http.MethodGet, Path: "/api/fs/dirs", Tags: tagsSys,
		Summary: "List subdirectories, for picking a working directory",
		Description: "If path is a directory, lists its subdirectories. Otherwise lists the subdirectories of its parent " +
			"whose names start with its last element (autocompletion)."},
		func(ctx context.Context, in *struct {
			Path   string `query:"path" doc:"Absolute path or ~/...; empty means the home directory"`
			Hidden bool   `query:"hidden" doc:"Include hidden directories"`
		}) (*struct{ Body DirListing }, error) {
			l, err := listDirs(in.Path, in.Hidden)
			if err != nil {
				return nil, apiError(err)
			}
			return &struct{ Body DirListing }{Body: l}, nil
		})

	// --- sessions ---

	huma.Register(api, huma.Operation{OperationID: "listSessions", Method: http.MethodGet, Path: "/api/sessions", Tags: tagsSess,
		Summary: "All sessions, most recently active first"},
		func(ctx context.Context, in *struct{}) (*struct{ Body []model.Session }, error) {
			return &struct{ Body []model.Session }{Body: s.m.List()}, nil
		})

	type createBody struct {
		Provider  string              `json:"provider" enum:"claude,codex"`
		Cwd       string              `json:"cwd,omitempty" doc:"Working directory (absolute, or ~/...). May be empty with resumeId: the conversation's own directory is used."`
		CreateDir bool                `json:"createDir,omitempty" doc:"Create the directory if it does not exist"`
		Title     string              `json:"title,omitempty"`
		Model     string              `json:"model,omitempty"`
		Effort    string              `json:"effort,omitempty"`
		Mode      string              `json:"mode,omitempty"`
		Options   model.LaunchOptions `json:"options,omitempty"`
		ResumeID  string              `json:"resumeId,omitempty" maxLength:"128" doc:"Attach to an existing CLI conversation (Claude or Codex session id); the CLI starts with --resume"`
	}
	huma.Register(api, huma.Operation{OperationID: "createSession", Method: http.MethodPost, Path: "/api/sessions", Tags: tagsSess,
		Summary: "Create a session and start its CLI", DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *struct{ Body createBody }) (*sessionOutput, error) {
			ctx, cancel := actionCtx(ctx)
			defer cancel()
			b := in.Body
			sess, err := s.m.Create(ctx, manager.CreateOptions{Provider: b.Provider, Cwd: b.Cwd, CreateDir: b.CreateDir, Title: b.Title,
				Model: b.Model, Effort: b.Effort, Mode: b.Mode, Options: b.Options, ResumeID: b.ResumeID})
			if err != nil {
				return nil, apiError(err)
			}
			return &sessionOutput{Body: sess.Summary()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "getSession", Method: http.MethodGet, Path: "/api/sessions/{id}", Tags: tagsSess,
		Summary: "One session"},
		func(ctx context.Context, in *SessionPath) (*sessionOutput, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			return &sessionOutput{Body: sess.Summary()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "updateSession", Method: http.MethodPatch, Path: "/api/sessions/{id}", Tags: tagsSess,
		Summary: "Rename a session"},
		func(ctx context.Context, in *struct {
			SessionPath
			Body struct {
				Title string `json:"title" maxLength:"200"`
			}
		}) (*sessionOutput, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			sess.Rename(in.Body.Title)
			return &sessionOutput{Body: sess.Summary()}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "deleteSession", Method: http.MethodDelete, Path: "/api/sessions/{id}", Tags: tagsSess,
		Summary: "Stop the CLI and delete the session and its history", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *SessionPath) (*struct{}, error) {
			return nil, apiError(s.m.Delete(ctx, in.ID))
		})

	sessionActionNoBody(s, "startSession", "start", "Start or resume the CLI", func(ctx context.Context, sess *manager.Session, _ emptyBody) error {
		return sess.Start(ctx)
	})
	sessionActionNoBody(s, "stopSession", "stop", "Stop the CLI (the session stays resumable)", func(ctx context.Context, sess *manager.Session, _ emptyBody) error {
		sess.Stop()
		return nil
	})
	sessionActionNoBody(s, "interruptSession", "interrupt", "Interrupt the running turn", func(ctx context.Context, sess *manager.Session, _ emptyBody) error {
		return sess.Interrupt(ctx)
	})

	type sendBody struct {
		Text  string   `json:"text"`
		Files []string `json:"files,omitempty" doc:"Names of uploaded files (from uploadFiles) to attach; the prompt lists them as $AGENT_UPLOAD_DIR/<name>"`
	}
	sessionAction(s, "sendPrompt", "send", "Send a prompt (resumes the CLI if it was stopped)", func(ctx context.Context, sess *manager.Session, b sendBody) error {
		return sess.SendWithFiles(ctx, b.Text, b.Files)
	})

	type commandBody struct {
		Command string `json:"command" pattern:"^/" doc:"A slash command such as /compact"`
	}
	sessionAction(s, "runCommand", "command", "Run a slash command (resumes the CLI if it was stopped)", func(ctx context.Context, sess *manager.Session, b commandBody) error {
		return sess.Command(ctx, b.Command)
	})

	type respondBody struct {
		RequestID string         `json:"requestId"`
		Response  model.Response `json:"response"`
	}
	sessionAction(s, "respondRequest", "respond", "Answer a permission prompt or question", func(ctx context.Context, sess *manager.Session, b respondBody) error {
		return sess.Respond(ctx, b.RequestID, b.Response)
	})

	type settingsBody struct {
		Model  *string `json:"model,omitempty"`
		Effort *string `json:"effort,omitempty"`
		Mode   *string `json:"mode,omitempty"`
	}
	sessionAction(s, "changeSettings", "settings", "Change model, effort or mode (stored for the next start when the CLI is stopped)",
		func(ctx context.Context, sess *manager.Session, b settingsBody) error {
			return sess.SetSettings(ctx, manager.SettingsChange{Model: b.Model, Effort: b.Effort, Mode: b.Mode})
		})

	huma.Register(api, huma.Operation{OperationID: "getHistory", Method: http.MethodGet, Path: "/api/sessions/{id}/history", Tags: tagsSess,
		Summary: "Earlier events of a session"},
		func(ctx context.Context, in *struct {
			SessionPath
			Before int64 `query:"before" doc:"Return events with a smaller seq; 0 = the latest"`
			Limit  int   `query:"limit" default:"200" minimum:"1" maximum:"2000"`
		}) (*struct{ Body []model.Event }, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			evs, err := sess.History(ctx, in.Before, in.Limit)
			if err != nil {
				return nil, apiError(err)
			}
			return &struct{ Body []model.Event }{Body: evs}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "terminalInput", Method: http.MethodPost, Path: "/api/sessions/{id}/terminal", Tags: tagsSess,
		Summary: "Type into the CLI's terminal", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			SessionPath
			Body struct {
				Data string `json:"data"`
			}
		}) (*struct{}, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			return nil, apiError(sess.TerminalInput(in.Body.Data))
		})

	s.registerFile()
	s.registerUploads()
	s.registerStreams()
}

// sessionAction registers POST /api/sessions/{id}/<path> returning the
// session summary.
func sessionAction[B any](s *Server, opID, path, summary string, f func(context.Context, *manager.Session, B) error) {
	huma.Register(s.api, huma.Operation{OperationID: opID, Method: http.MethodPost, Path: "/api/sessions/{id}/" + path,
		Tags: []string{"sessions"}, Summary: summary},
		func(ctx context.Context, in *struct {
			SessionPath
			Body B
		}) (*sessionOutput, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			ctx, cancel := actionCtx(ctx)
			defer cancel()
			if err := f(ctx, sess, in.Body); err != nil {
				return nil, apiError(err)
			}
			return &sessionOutput{Body: sess.Summary()}, nil
		})
}

// sessionActionNoBody is sessionAction for actions without a request body.
func sessionActionNoBody(s *Server, opID, path, summary string, f func(context.Context, *manager.Session, emptyBody) error) {
	huma.Register(s.api, huma.Operation{OperationID: opID, Method: http.MethodPost, Path: "/api/sessions/{id}/" + path,
		Tags: []string{"sessions"}, Summary: summary},
		func(ctx context.Context, in *SessionPath) (*sessionOutput, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			ctx, cancel := actionCtx(ctx)
			defer cancel()
			if err := f(ctx, sess, emptyBody{}); err != nil {
				return nil, apiError(err)
			}
			return &sessionOutput{Body: sess.Summary()}, nil
		})
}

// registerFile serves attachments. Range requests work, so videos can seek.
func (s *Server) registerFile() {
	huma.Register(s.api, huma.Operation{OperationID: "getFile", Method: http.MethodGet, Path: "/api/sessions/{id}/files/{fileId}",
		Tags: []string{"sessions"}, Summary: "An image or file from the session's events",
		Responses: map[string]*huma.Response{"200": {Description: "The file", Content: map[string]*huma.MediaType{
			"application/octet-stream": {Schema: &huma.Schema{Type: huma.TypeString, Format: "binary"}}}}}},
		func(ctx context.Context, in *struct {
			SessionPath
			FileID   string `path:"fileId"`
			Download bool   `query:"download" doc:"Serve as an attachment instead of inline"`
		}) (*huma.StreamResponse, error) {
			sess, err := s.m.Get(in.ID)
			if err != nil {
				return nil, apiError(err)
			}
			a, err := sess.Attachment(ctx, in.FileID)
			if err != nil {
				return nil, apiError(err)
			}
			var content io.ReadSeeker
			modTime := a.CreatedAt
			var closer io.Closer
			if a.Data != nil {
				content = bytes.NewReader(a.Data)
			} else {
				if a.Path == "" {
					return nil, huma.Error410Gone("the file has no content")
				}
				f, err := os.Open(a.Path)
				if err != nil {
					return nil, huma.Error410Gone("the file is no longer available")
				}
				fi, err := f.Stat()
				if err != nil || !fi.Mode().IsRegular() {
					f.Close()
					return nil, huma.Error410Gone("not a regular file")
				}
				content, modTime, closer = f, fi.ModTime(), f
			}
			disposition := "inline"
			if in.Download {
				disposition = "attachment"
			}
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				if closer != nil {
					defer closer.Close()
				}
				r, w := humago.Unwrap(hctx)
				w.Header().Set("Content-Type", a.MediaType)
				w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Name}))
				w.Header().Set("X-Content-Type-Options", "nosniff")
				// The agent chose these files: an HTML or SVG one must not run
				// script with this origin, which controls the sessions.
				w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
				http.ServeContent(w, r, a.Name, modTime, content)
			}}, nil
		})
}

// listDirs implements listDirs.
func listDirs(path string, hidden bool) (DirListing, error) {
	if strings.TrimSpace(path) == "" {
		path = "~"
	}
	p, err := manager.NormalizeDir(path)
	if err != nil {
		return DirListing{}, err
	}
	dir, prefix := p, ""
	exists := false
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		exists = true
	} else {
		dir, prefix = filepath.Dir(p), filepath.Base(p)
	}
	out := DirListing{Dir: dir, Exists: exists, Entries: []DirEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		out.Parent = parent
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return out, nil // nothing to complete
	}
	for _, e := range ents {
		name := e.Name()
		if !hidden && strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if prefix != "" && !strings.HasPrefix(strings.ToLower(name), strings.ToLower(prefix)) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(full); err == nil && fi.IsDir() {
				isDir = true
			}
		}
		if !isDir {
			continue
		}
		out.Entries = append(out.Entries, DirEntry{Name: name, Path: full})
		if len(out.Entries) >= 500 {
			break
		}
	}
	return out, nil
}
