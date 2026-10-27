package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	bridge "github.com/jclab-joseph/clicodebridge"
	"github.com/jclab-joseph/clicodebridge/claude"

	"github.com/jclab-joseph/agent-web/internal/model"
)

// UploadEnv is the environment variable that tells the CLI where uploaded
// files are. Prompts refer to uploads as $AGENT_UPLOAD_DIR/<name>.
const UploadEnv = "AGENT_UPLOAD_DIR"

// DefaultUploadDir is the default Config.UploadDir.
const DefaultUploadDir = "${WORKDIR}/.scratch/uploads"

// uploadSystemPrompt is appended to Claude's system prompt. The Read tool
// takes a literal path, so the model has to resolve the variable first.
const uploadSystemPrompt = `Files the user attaches in agent-web are saved in the directory named by the ` + UploadEnv + ` environment variable, and prompts refer to them as $` + UploadEnv + `/<file name>. ` +
	`The Read tool (and any other tool that takes a file path) does not expand environment variables: ` +
	`first run ` + "`echo $" + UploadEnv + "`" + ` with the Bash tool to get the absolute directory, ` +
	`then Read <that directory>/<file name>.`

// uploadEchoRule pre-approves the command the system prompt asks for.
const uploadEchoRule = "Bash(echo $" + UploadEnv + ")"

// attachedHeader starts the list of attached files in a prompt. The web UI
// recognizes it to show the files.
const attachedHeader = "Attached files:"

// UploadDir is the session's upload directory (absolute).
func (s *Session) UploadDir() string {
	s.mu.Lock()
	cwd := s.rec.Cwd
	s.mu.Unlock()
	return s.m.uploadDir(cwd)
}

func (m *Manager) uploadDir(cwd string) string {
	tmpl := m.cfg.UploadDir
	if tmpl == "" {
		tmpl = DefaultUploadDir
	}
	dir := os.Expand(tmpl, func(k string) string {
		if k == "WORKDIR" {
			return cwd
		}
		return os.Getenv(k)
	})
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	return filepath.Clean(dir)
}

// uploadLaunch adds what the CLI needs to find uploads: the environment
// variable, and for Claude the instructions and access to the directory
// when it lies outside the working directory.
func (m *Manager) uploadLaunch(so *bridge.SessionOptions, cwd string) {
	dir := m.uploadDir(cwd)
	so.Env = append(so.Env, UploadEnv+"="+dir)
	if co, ok := so.Provider.(*claude.Options); ok {
		if co.AppendSystemPrompt != "" {
			co.AppendSystemPrompt += "\n\n"
		}
		co.AppendSystemPrompt += uploadSystemPrompt
		// Resolving the directory is harmless; do not ask for it every time.
		co.AllowedTools = append(co.AllowedTools, uploadEchoRule)
		if rel, err := filepath.Rel(cwd, dir); err != nil || strings.HasPrefix(rel, "..") {
			if err := os.MkdirAll(dir, 0o755); err == nil {
				co.AddDirs = append(co.AddDirs, dir)
			}
		}
	}
}

// sanitizeName keeps a file name usable in a prompt and a shell: no path
// separators, whitespace or shell metacharacters. Letters of any script stay.
func sanitizeName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r), unicode.IsSpace(r), strings.ContainsRune("/$`'\"\\;&|<>*?()[]{}!#~%^=,:", r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		out = "file"
	}
	if len(out) > 180 { // keep room for "-n" within the 255-byte limit
		ext := filepath.Ext(out)
		if len(ext) > 20 {
			ext = ""
		}
		out = out[:180-len(ext)] + ext
	}
	return out
}

// validUploadName reports whether name is a plain file name inside the
// upload directory.
func validUploadName(name string) bool {
	return name != "" && name == filepath.Base(name) && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

// SaveUpload stores a file in the upload directory as <name>.<ext>, or
// <name>-<n>.<ext> when that exists.
func (s *Session) SaveUpload(name string, r io.Reader) (model.Upload, error) {
	dir := s.UploadDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return model.Upload{}, err
	}
	s.ignoreUploads(dir)
	clean := sanitizeName(name)
	ext := filepath.Ext(clean)
	stem := strings.TrimSuffix(clean, ext)
	var f *os.File
	var final string
	for n := 0; ; n++ {
		final = clean
		if n > 0 {
			final = fmt.Sprintf("%s-%d%s", stem, n, ext)
		}
		var err error
		f, err = os.OpenFile(filepath.Join(dir, final), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || n > 10000 {
			return model.Upload{}, err
		}
	}
	size, err := io.Copy(f, r)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return model.Upload{}, err
	}
	s.touch()
	return uploadInfo(dir, final, size), nil
}

func uploadInfo(dir, name string, size int64) model.Upload {
	mt := bridge.MediaTypeOf(name)
	return model.Upload{Name: name, Ref: "$" + UploadEnv + "/" + name, Path: filepath.Join(dir, name), Size: size,
		MediaType: mt, Kind: string(bridge.Attachment{MediaType: mt}.Kind())}
}

// ignoreUploads keeps uploads out of git when the directory is inside the
// working directory.
func (s *Session) ignoreUploads(dir string) {
	s.mu.Lock()
	cwd := s.rec.Cwd
	s.mu.Unlock()
	if rel, err := filepath.Rel(cwd, dir); err != nil || strings.HasPrefix(rel, "..") {
		return
	}
	p := filepath.Join(dir, ".gitignore")
	if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
		_ = os.WriteFile(p, []byte("# Files uploaded through agent-web\n*\n"), 0o644)
	}
}

// Upload returns an uploaded file.
func (s *Session) Upload(name string) (model.Upload, error) {
	if !validUploadName(name) || name == ".gitignore" {
		return model.Upload{}, ErrNotFound
	}
	dir := s.UploadDir()
	fi, err := os.Stat(filepath.Join(dir, name))
	if err != nil || !fi.Mode().IsRegular() {
		return model.Upload{}, ErrNotFound
	}
	return uploadInfo(dir, name, fi.Size()), nil
}

// promptWithFiles appends the attached files to a prompt.
func (s *Session) promptWithFiles(text string, files []string) (string, error) {
	text = strings.TrimSpace(text)
	if len(files) == 0 {
		if text == "" {
			return "", fmt.Errorf("%w: the prompt is empty", ErrInvalid)
		}
		return text, nil
	}
	var b strings.Builder
	if text != "" {
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	b.WriteString(attachedHeader)
	for _, name := range files {
		u, err := s.Upload(name)
		if err != nil {
			return "", fmt.Errorf("%w: no uploaded file %q", ErrInvalid, name)
		}
		b.WriteString("\n- ")
		b.WriteString(u.Ref)
	}
	return b.String(), nil
}

// SendWithFiles submits a prompt with uploaded files attached.
func (s *Session) SendWithFiles(ctx context.Context, text string, files []string) error {
	prompt, err := s.promptWithFiles(text, files)
	if err != nil {
		return err
	}
	return s.Send(ctx, prompt)
}
