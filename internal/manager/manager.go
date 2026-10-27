// Package manager owns the sessions: it persists them, runs their CLI
// processes on demand, records their events, and stops a CLI that has been
// idle for too long. A stopped session is resumed (--resume) by the next
// action that needs the CLI.
package manager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/claude"
	"github.com/jclab-joseph/clicodebridge/codex"

	"github.com/jclab-joseph/agent-web/internal/model"
	"github.com/jclab-joseph/agent-web/internal/store"
)

var (
	// ErrNotFound means there is no session with that id.
	ErrNotFound = errors.New("session not found")
	// ErrNotRunning means the action needs a running CLI and does not start one.
	ErrNotRunning = errors.New("the CLI is not running")
	// ErrInvalid wraps a rejected argument.
	ErrInvalid = errors.New("invalid request")
)

// Config configures a Manager.
type Config struct {
	Store     *store.Store
	Providers map[string]bridge.Provider
	// IdleTimeout stops a CLI that has been idle this long. 0 disables it.
	IdleTimeout time.Duration
	// UploadDir is where uploaded files are saved, given to the CLI as
	// $AGENT_UPLOAD_DIR. ${WORKDIR} is the session's working directory.
	// Default DefaultUploadDir.
	UploadDir string
	// DriftDir is passed to the Claude provider (recognizer drift fixtures).
	DriftDir string
	Logger   *slog.Logger
}

// Manager holds every session.
type Manager struct {
	cfg  Config
	log  *slog.Logger
	stop chan struct{}
	wg   sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*Session
}

// New loads the stored sessions (none is started) and starts the idle watchdog.
func New(ctx context.Context, cfg Config) (*Manager, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	m := &Manager{cfg: cfg, log: cfg.Logger, stop: make(chan struct{}), sessions: map[string]*Session{}}
	recs, err := cfg.Store.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	for _, rec := range recs {
		s, err := m.newSession(ctx, rec)
		if err != nil {
			return nil, err
		}
		m.sessions[rec.ID] = s
	}
	if cfg.IdleTimeout > 0 {
		m.wg.Add(1)
		go m.watchdog()
	}
	return m, nil
}

// Providers returns the provider names in display order.
func (m *Manager) Providers() map[string]bridge.Provider { return m.cfg.Providers }

// IdleTimeout is the configured idle timeout.
func (m *Manager) IdleTimeout() time.Duration { return m.cfg.IdleTimeout }

// Store returns the underlying store.
func (m *Manager) Store() *store.Store { return m.cfg.Store }

// Close stops every running CLI. The sessions stay resumable.
func (m *Manager) Close() {
	close(m.stop)
	m.wg.Wait()
	var wg sync.WaitGroup
	for _, s := range m.all() {
		wg.Add(1)
		go func() { defer wg.Done(); s.stopCLI(model.LifecycleShutdown) }()
	}
	wg.Wait()
}

func (m *Manager) all() []*Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s)
	}
	return out
}

// Get returns a session.
func (m *Manager) Get(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[id]; s != nil {
		return s, nil
	}
	return nil, ErrNotFound
}

// List returns all session summaries, most recently active first.
func (m *Manager) List() []model.Session {
	all := m.all()
	out := make([]model.Session, 0, len(all))
	for _, s := range all {
		out = append(out, s.Summary())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	return out
}

// CreateOptions describe a new session.
type CreateOptions struct {
	Provider  string
	Cwd       string
	CreateDir bool
	Title     string
	Model     string
	Effort    string
	Mode      string
	Options   model.LaunchOptions
}

// NormalizeDir expands ~ and makes path absolute and clean.
func NormalizeDir(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("%w: the working directory is empty", ErrInvalid)
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: the working directory must be an absolute path", ErrInvalid)
	}
	return filepath.Clean(path), nil
}

// Create stores a new session and starts its CLI. If the CLI cannot be
// started the session is discarded.
func (m *Manager) Create(ctx context.Context, o CreateOptions) (*Session, error) {
	if _, ok := m.cfg.Providers[o.Provider]; !ok {
		return nil, fmt.Errorf("%w: unknown provider %q", ErrInvalid, o.Provider)
	}
	cwd, err := NormalizeDir(o.Cwd)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(cwd)
	switch {
	case errors.Is(err, os.ErrNotExist) && o.CreateDir:
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			return nil, fmt.Errorf("%w: create %s: %v", ErrInvalid, cwd, err)
		}
	case err != nil:
		return nil, fmt.Errorf("%w: working directory %s: %v", ErrInvalid, cwd, err)
	case !fi.IsDir():
		return nil, fmt.Errorf("%w: %s is not a directory", ErrInvalid, cwd)
	}
	opts, _ := json.Marshal(o.Options)
	now := time.Now()
	rec := store.Session{
		ID: newID(), Provider: o.Provider, Cwd: cwd, Title: strings.TrimSpace(o.Title),
		Model: o.Model, Effort: o.Effort, Mode: o.Mode, Options: string(opts),
		CreatedAt: now, UpdatedAt: now, LastActiveAt: now,
	}
	if err := m.cfg.Store.CreateSession(ctx, rec); err != nil {
		return nil, err
	}
	s, err := m.newSession(ctx, rec)
	if err != nil {
		return nil, err
	}
	if _, err := s.ensureRunning(ctx); err != nil {
		_ = m.cfg.Store.DeleteSession(context.WithoutCancel(ctx), rec.ID)
		return nil, err
	}
	if err := m.cfg.Store.TouchWorkdir(ctx, cwd, now); err != nil {
		m.log.Warn("record working directory", "err", err)
	}
	m.mu.Lock()
	m.sessions[rec.ID] = s
	m.mu.Unlock()
	return s, nil
}

// Delete stops the CLI and removes the session and its history. The CLI's
// own transcript is left alone.
func (m *Manager) Delete(ctx context.Context, id string) error {
	s, err := m.Get(id)
	if err != nil {
		return err
	}
	s.stopCLI(model.LifecycleStopped)
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
	s.markDeleted()
	return m.cfg.Store.DeleteSession(ctx, id)
}

// watchdog stops CLIs that have been idle for IdleTimeout.
func (m *Manager) watchdog() {
	defer m.wg.Done()
	tick := min(max(m.cfg.IdleTimeout/10, time.Second), 15*time.Second)
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
		}
		now := time.Now()
		for _, s := range m.all() {
			if s.idleExpired(now) {
				m.log.Info("stopping idle CLI", "session", s.id, "idle", m.cfg.IdleTimeout)
				go s.stopCLI(model.LifecycleSuspended)
			}
		}
	}
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// providerOptions builds the provider-specific launch options.
func (m *Manager) providerOptions(provider string, o model.LaunchOptions) any {
	tri := func(v string) *bool {
		switch v {
		case "on":
			return claude.Bool(true)
		case "off":
			return claude.Bool(false)
		}
		return nil
	}
	switch provider {
	case "claude":
		co := &claude.Options{
			TrustWorkspace: o.TrustWorkspace, RemoteControl: tri(o.RemoteControl), RemoteControlName: o.RemoteControlName,
			Chrome: tri(o.Chrome), ScreenReader: o.ScreenReader, DriftDir: m.cfg.DriftDir,
		}
		if o.Bridge == "off" {
			co.Bridge = claude.BridgeOff
		}
		if o.Interaction == "terminal" {
			co.Interaction = claude.InteractionTerminal
		}
		return co
	case "codex":
		return &codex.Options{TrustWorkspace: o.TrustWorkspace, Search: o.Search}
	}
	return nil
}
