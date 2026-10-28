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
	"strconv"
	"strings"
	"syscall"
	"time"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/claude"
	"github.com/jclab-joseph/clicodebridge/codex"

	"github.com/jclab-joseph/agent-web/internal/cfaccess"
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
	uploadDir := flag.String("upload-dir", envOr("AGENT_WEB_UPLOAD_DIR", manager.DefaultUploadDir), "where uploaded files are saved, passed to the CLI as $AGENT_UPLOAD_DIR (${WORKDIR} = the session's working directory)")
	driftDir := flag.String("drift-dir", "", "save screens where a Claude recognizer disagrees with the CLI")
	allowedHosts := flag.String("allowed-hosts", os.Getenv("AGENT_WEB_ALLOWED_HOSTS"), "comma-separated Host names to accept besides localhost, e.g. agent.example.com,*.example.net (env AGENT_WEB_ALLOWED_HOSTS)")
	allowAnyHost := flag.Bool("allowed-any-host", envBool("AGENT_WEB_ALLOWED_ANY_HOST"), "accept any Host header (env AGENT_WEB_ALLOWED_ANY_HOST)")
	cfAUD := flag.String("cloudflare-aud", os.Getenv("AGENT_WEB_CLOUDFLARE_AUD"), "require Cloudflare Access: the application's AUD tag (env AGENT_WEB_CLOUDFLARE_AUD)")
	cfTeam := flag.String("cloudflare-team", os.Getenv("AGENT_WEB_CLOUDFLARE_TEAM"), "Cloudflare Access team domain, e.g. myteam or myteam.cloudflareaccess.com; required with --cloudflare-aud (env AGENT_WEB_CLOUDFLARE_TEAM)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.Get().String())
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(log)
	access, err := accessOptions(*token, *allowedHosts, *allowAnyHost, *cfAUD, *cfTeam)
	if err != nil {
		log.Error(err.Error())
		os.Exit(2)
	}
	if err := run(log, *addr, *dataDir, access, *idle, *claudeBin, *codexBin, *driftDir, *uploadDir); err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func envBool(k string) bool {
	v, _ := strconv.ParseBool(os.Getenv(k))
	return v
}

// accessOptions builds the server's access rules from the flags.
func accessOptions(token, allowedHosts string, anyHost bool, cfAUD, cfTeam string) (server.Options, error) {
	o := server.Options{Token: token, AllowAnyHost: anyHost}
	for _, h := range strings.Split(allowedHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			o.AllowedHosts = append(o.AllowedHosts, h)
		}
	}
	switch {
	case cfAUD != "" && cfTeam == "":
		return o, errors.New("--cloudflare-aud needs --cloudflare-team (the team domain that signs the tokens)")
	case cfAUD == "" && cfTeam != "":
		return o, errors.New("--cloudflare-team needs --cloudflare-aud")
	case cfAUD != "":
		v, err := cfaccess.New(cfTeam, cfAUD)
		if err != nil {
			return o, err
		}
		o.Cloudflare = v
	}
	return o, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func run(log *slog.Logger, addr, dataDir string, access server.Options, idle time.Duration, claudeBin, codexBin, driftDir, uploadDir string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if access.Token == "" && access.Cloudflare == nil && !isLoopback(ln.Addr()) {
		ln.Close()
		return fmt.Errorf("listening on %s without authentication would let anyone on the network run commands as you; set --token or --cloudflare-aud", addr)
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
		Store: st, IdleTimeout: idle, DriftDir: driftDir, UploadDir: uploadDir, Logger: log,
		Providers: map[string]bridge.Provider{
			"claude": claudeP,
			"codex":  codex.New(codex.Config{Executable: codexBin}),
		},
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Handler:           server.New(m, withStatic(access)),
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
	log.Info("access", "token", access.Token != "", "cloudflareAccess", access.Cloudflare != nil,
		"allowedHosts", strings.Join(access.AllowedHosts, ","), "allowedAnyHost", access.AllowAnyHost)
	log.Info("agent-web listening", "url", "http://"+ln.Addr().String(), "version", version.Get().Version, "db", dbPath, "idleTimeout", idle)
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		m.Close()
		return err
	}
	<-done
	m.Close()
	return nil
}

func withStatic(o server.Options) server.Options {
	o.Static = webui.FS()
	return o
}

func isLoopback(a net.Addr) bool {
	ta, ok := a.(*net.TCPAddr)
	return ok && ta.IP.IsLoopback()
}
