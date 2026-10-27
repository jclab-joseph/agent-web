// Package version reports the build version. Release builds set the
// variables with -ldflags "-X github.com/jclab-joseph/agent-web/internal/version.Version=v1.2.3 ...".
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	// Version is the git tag of the build, e.g. "v1.2.3".
	Version = ""
	// Commit is the git commit hash.
	Commit = ""
	// Date is the build time in RFC 3339.
	Date = ""
)

// BuildInfo describes the running binary.
type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	Date      string `json:"date,omitempty"`
	GoVersion string `json:"goVersion"`
	Platform  string `json:"platform"`
}

// Get returns the build info. Without ldflags (go run, go install) it falls
// back to what the Go toolchain recorded.
func Get() BuildInfo {
	i := BuildInfo{Version: Version, Commit: Commit, Date: Date, GoVersion: runtime.Version(), Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if i.Version == "" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			i.Version = bi.Main.Version
		}
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value
				}
			case "vcs.time":
				if i.Date == "" {
					i.Date = s.Value
				}
			}
		}
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	if len(i.Commit) > 12 {
		i.Commit = i.Commit[:12]
	}
	return i
}

// String is the --version output.
func (i BuildInfo) String() string {
	s := "agent-web " + i.Version
	if i.Commit != "" {
		s += " (" + i.Commit
		if i.Date != "" {
			s += ", " + i.Date
		}
		s += ")"
	}
	return fmt.Sprintf("%s %s %s", s, i.GoVersion, i.Platform)
}
