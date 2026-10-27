package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/claude"

	"github.com/jclab-joseph/agent-web/internal/store"
)

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"photo.png":           "photo.png",
		"my photo (1).png":    "my_photo__1_.png",
		"../../etc/passwd":    "passwd",
		`C:\Users\me\a.txt`:   "a.txt",
		"스크린샷 2026-10-28.png": "스크린샷_2026-10-28.png",
		"$(rm -rf).sh":        "rm_-rf_.sh",
		"..":                  "file",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUploadsAndPrompt(t *testing.T) {
	p := &fakeProvider{}
	m := newTestManager(t, filepath.Join(t.TempDir(), "test.db"), p, 0)
	defer m.Close()
	ctx := context.Background()
	cwd := t.TempDir()
	s, err := m.Create(ctx, CreateOptions{Provider: "claude", Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}

	// The CLI gets the directory and, for Claude, the instructions.
	o := p.lastStart()
	dir := filepath.Join(cwd, ".scratch", "uploads")
	if !contains(o.Env, "AGENT_UPLOAD_DIR="+dir) {
		t.Fatalf("env = %v", o.Env)
	}
	if co := o.Provider.(*claude.Options); !strings.Contains(co.AppendSystemPrompt, "echo $AGENT_UPLOAD_DIR") || len(co.AddDirs) != 0 ||
		!contains(co.AllowedTools, "Bash(echo $AGENT_UPLOAD_DIR)") {
		t.Fatalf("claude options = %+v", co)
	}

	var names []string
	for range 3 {
		u, err := s.SaveUpload("shot.png", strings.NewReader("png"))
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, u.Name)
	}
	if strings.Join(names, ",") != "shot.png,shot-1.png,shot-2.png" {
		t.Fatalf("names = %v", names)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "shot-1.png")); string(b) != "png" {
		t.Fatalf("content = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal("no .gitignore in the upload directory")
	}

	prompt, err := s.promptWithFiles("look at these", []string{"shot.png", "shot-2.png"})
	if err != nil {
		t.Fatal(err)
	}
	want := "look at these\n\nAttached files:\n- $AGENT_UPLOAD_DIR/shot.png\n- $AGENT_UPLOAD_DIR/shot-2.png"
	if prompt != want {
		t.Fatalf("prompt = %q", prompt)
	}
	for _, bad := range []string{"../x", "missing.png", ".gitignore"} {
		if _, err := s.promptWithFiles("x", []string{bad}); err == nil {
			t.Errorf("file %q was accepted", bad)
		}
	}
	if _, err := s.promptWithFiles("  ", nil); err == nil {
		t.Error("empty prompt was accepted")
	}
}

func TestUploadDirOutsideWorkdir(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &fakeProvider{}
	up := filepath.Join(t.TempDir(), "uploads")
	m, err := New(context.Background(), Config{Store: st, UploadDir: up, Providers: map[string]bridge.Provider{"claude": p}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s, err := m.Create(context.Background(), CreateOptions{Provider: "claude", Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if co := p.lastStart().Provider.(*claude.Options); !contains(co.AddDirs, up) {
		t.Fatalf("add dirs = %v", co.AddDirs)
	}
	if _, err := s.SaveUpload("a.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(up, ".gitignore")); err == nil {
		t.Fatal("wrote a .gitignore outside the working directory")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestTitleSkipsAttachments(t *testing.T) {
	if got := titleFrom("look\n\nAttached files:\n- $AGENT_UPLOAD_DIR/a.png"); got != "look" {
		t.Errorf("title = %q", got)
	}
	if got := titleFrom("Attached files:\n- $AGENT_UPLOAD_DIR/a.png\n- $AGENT_UPLOAD_DIR/b.png"); got != "a.png" {
		t.Errorf("title = %q", got)
	}
}
