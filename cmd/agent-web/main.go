// Command agent-web runs Claude Code and Codex sessions behind a web UI.
//
//	agent-web --addr 127.0.0.1:8787
//
// Sessions are stored in SQLite and survive restarts. A CLI that has been
// idle for --idle-timeout is stopped and resumed by the next request.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/claude"
	"github.com/jclab-joseph/clicodebridge/codex"

	"github.com/jclab-joseph/agent-web/internal/manager"
	"github.com/jclab-joseph/agent-web/internal/server"
	"github.com/jclab-joseph/agent-web/internal/store"
	"github.com/jclab-joseph/agent-web/internal/version"
	"github.com/jclab-joseph/agent-web/internal/webui"
)

func defaultDataDir() string {
	if d := os.Getenv("AGENT_WEB_DATA_DIR"); d != "" {
		return d
	}
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "agent-web")
	}
	return ".agent-web"
}

func main() {
	addr := flag.String("addr", envOr("AGENT_WEB_ADDR", "127.0.0.1:8787"), "listen address")
	dataDir := flag.String("data-dir", defaultDataDir(), "directory for the SQLite database")
	token := flag.String("token", os.Getenv("AGENT_WEB_TOKEN"), "access token required by the web UI and API (env AGENT_WEB_TOKEN); required unless listening on loopback")
	idle := flag.Duration("idle-timeout", 5*time.Minute, "stop a CLI that has been idle this long (0 = never); the next request resumes it")
	claudeBin := flag.String("claude", "claude", "claude executable")
	codexBin := flag.String("codex", "codex", "codex executable")
	driftDir := flag.String("drift-dir", "", "save screens where a Claude recognizer disagrees with the CLI")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Get().String())
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	if err := run(log, *addr, *dataDir, *token, *idle, *claudeBin, *codexBin, *driftDir); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(log *slog.Logger, addr, dataDir, token string, idle time.Duration, claudeBin, codexBin, driftDir string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if token == "" && !isLoopback(ln.Addr()) {
		ln.Close()
		return fmt.Errorf("listening on %s without a token would let anyone on the network run commands as you; set --token or AGENT_WEB_TOKEN", addr)
	}

	dbPath := filepath.Join(dataDir, "agent-web.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer st.Close()

	claudeP := claude.New(claude.Config{Executable: claudeBin})
	defer claudeP.Close()
	ctx := context.Background()
	m, err := manager.New(ctx, manager.Config{
		Store: st, IdleTimeout: idle, DriftDir: driftDir, Logger: log,
		Providers: map[string]bridge.Provider{
			"claude": claudeP,
			"codex":  codex.New(codex.Config{Executable: codexBin}),
		},
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           server.New(m, server.Options{Token: token, Static: webui.FS()}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		log.Info("shutting down; stopping CLIs (sessions stay resumable)")
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Info("agent-web listening", "url", "http://"+ln.Addr().String(), "version", version.Get().Version, "db", dbPath, "idleTimeout", idle)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		m.Close()
		return err
	}
	<-done
	m.Close()
	return nil
}

func isLoopback(a net.Addr) bool {
	ta, ok := a.(*net.TCPAddr)
	return ok && ta.IP.IsLoopback()
}
