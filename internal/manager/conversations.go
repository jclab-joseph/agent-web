package manager

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// conversationIDRe bounds what a resume id may contain; it is used in a glob.
var conversationIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{7,127}$`)

// conversation is a CLI conversation found on disk by its id.
type conversation struct {
	Path string
	// Cwd is the working directory the conversation was recorded in; empty
	// if the file does not say.
	Cwd string
}

// claudeConfigDir and codexHome follow the providers' own defaults, which
// agent-web does not override.
func claudeConfigDir() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func codexHome() string {
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

// findConversation locates the transcript (Claude) or rollout (Codex) of an
// existing conversation, so a session can be attached to it with --resume.
func findConversation(provider, id string) (conversation, error) {
	if !conversationIDRe.MatchString(id) {
		return conversation{}, fmt.Errorf("%w: %q is not a session id", ErrInvalid, id)
	}
	var pattern string
	switch provider {
	case "claude":
		pattern = filepath.Join(claudeConfigDir(), "projects", "*", id+".jsonl")
	case "codex":
		pattern = filepath.Join(codexHome(), "sessions", "*", "*", "*", "rollout-*-"+id+".jsonl")
	default:
		return conversation{}, fmt.Errorf("%w: unknown provider %q", ErrInvalid, provider)
	}
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		return conversation{}, fmt.Errorf("%w: no %s conversation with id %s on this machine", ErrInvalid, provider, id)
	}
	c := conversation{Path: matches[len(matches)-1]}
	c.Cwd = recordedCwd(c.Path)
	return c, nil
}

// recordedCwd reads the working directory from the first records of a
// transcript: Claude puts "cwd" on its message records, Codex in the
// session_meta payload.
func recordedCwd(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 64<<10)
	for range 200 {
		line, err := r.ReadBytes('\n')
		var rec struct {
			Cwd     string `json:"cwd"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &rec) == nil {
			if rec.Cwd != "" {
				return rec.Cwd
			}
			if rec.Payload.Cwd != "" {
				return rec.Payload.Cwd
			}
		}
		if err != nil {
			return ""
		}
	}
	return ""
}
