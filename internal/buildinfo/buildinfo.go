// Package buildinfo formats the version and build metadata stamped into the
// binary at link time. It keeps the string layout in one tested place so the
// entrypoint stays thin.
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Info carries the values a release build injects via -ldflags. Any field may
// be empty for a plain `go build` or `go install`, in which case String falls
// back to whatever the Go toolchain recorded in the embedded build info.
type Info struct {
	Version string // e.g. "v0.1.0", or "dev" for an unstamped build
	Commit  string // full or short git SHA
	Date    string // RFC3339 build timestamp
}

// resolve fills empty fields from the embedded build info when possible. It is
// separated from String so tests can drive the formatting deterministically
// without depending on how the test binary was built.
func (i Info) resolve() Info {
	if i.Version == "" {
		i.Version = "dev"
	}
	if i.Commit != "" && i.Date != "" {
		return i
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return i
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
	return i
}

// String renders a single human-readable line, for example:
//
//	mcp-slack v0.1.0 (commit abc1234, built 2026-01-02T15:04:05Z, go1.26.5)
//
// Missing pieces are omitted rather than shown as empty parentheses.
func (i Info) String() string {
	r := i.resolve()

	var b strings.Builder
	b.WriteString("mcp-slack ")
	b.WriteString(r.Version)

	var parts []string
	if r.Commit != "" {
		parts = append(parts, "commit "+shortCommit(r.Commit))
	}
	if r.Date != "" {
		parts = append(parts, "built "+r.Date)
	}
	parts = append(parts, runtime.Version())

	b.WriteString(" (")
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString(")")
	return b.String()
}

// shortCommit trims a git SHA to 12 characters, leaving shorter values intact.
func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
