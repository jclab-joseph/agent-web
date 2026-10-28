package manager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFindConversation(t *testing.T) {
	claudeDir, codexDir := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	t.Setenv("CODEX_HOME", codexDir)
	const id = "0f6c1d2e-1111-4222-8333-944455556666"
	writeFile(t, filepath.Join(claudeDir, "projects", "-work-app", id+".jsonl"),
		`{"type":"file-history-snapshot","snapshot":{}}`+"\n"+`{"type":"user","cwd":"/work/app","message":{}}`+"\n")
	writeFile(t, filepath.Join(codexDir, "sessions", "2026", "10", "28", "rollout-2026-10-28T01-02-03-"+id+".jsonl"),
		`{"type":"session_meta","payload":{"id":"`+id+`","cwd":"/work/codex"}}`+"\n")

	c, err := findConversation("claude", id)
	if err != nil || c.Cwd != "/work/app" {
		t.Fatalf("claude: %+v, %v", c, err)
	}
	c, err = findConversation("codex", id)
	if err != nil || c.Cwd != "/work/codex" {
		t.Fatalf("codex: %+v, %v", c, err)
	}
	for _, bad := range []string{"0f6c1d2e-0000-0000-0000-000000000000", "../../etc", "*", "short"} {
		if _, err := findConversation("claude", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: want ErrInvalid, got %v", bad, err)
		}
	}
}

func TestCreateResumesExistingConversation(t *testing.T) {
	claudeDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)
	work := t.TempDir()
	const id = "7a7b7c7d-1111-4222-8333-944455556666"
	writeFile(t, filepath.Join(claudeDir, "projects", "x", id+".jsonl"), `{"type":"user","cwd":"`+work+`"}`+"\n")

	p := &fakeProvider{}
	m := newTestManager(t, filepath.Join(t.TempDir(), "db.sqlite"), p, time.Hour)
	ctx := context.Background()

	// Another directory: Claude would not find the conversation there.
	_, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: t.TempDir(), ResumeID: id})
	if err == nil || !strings.Contains(err.Error(), "use that working directory") {
		t.Fatalf("wrong directory: %v", err)
	}

	// No directory: the conversation's own is used, and the CLI resumes it.
	s, err := m.Create(ctx, CreateOptions{Provider: "claude", ResumeID: id})
	if err != nil {
		t.Fatal(err)
	}
	sum := s.Summary()
	if sum.Cwd != work || sum.CLISessionID != id || !sum.Resumable {
		t.Fatalf("summary: %+v", sum)
	}
	if got := p.lastStart(); got.Resume != id || got.Cwd != work {
		t.Fatalf("start options: %+v", got)
	}
	if kinds := eventKinds(t, s); len(kinds) == 0 || kinds[0] != "lifecycle:resumed" {
		t.Fatalf("lifecycle: %v", kinds)
	}

	// The same conversation cannot be attached twice.
	if _, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: work, ResumeID: id}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate: %v", err)
	}
}
