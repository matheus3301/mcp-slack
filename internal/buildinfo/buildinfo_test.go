package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestString_FullMetadata(t *testing.T) {
	t.Parallel()
	got := Info{
		Version: "v0.1.0",
		Commit:  "abcdef1234567890abcdef",
		Date:    "2026-01-02T15:04:05Z",
	}.String()

	// Commit is shortened to 12 chars.
	want := "mcp-slack v0.1.0 (commit abcdef123456, built 2026-01-02T15:04:05Z, " + runtime.Version() + ")"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestString_DefaultsVersionToDev(t *testing.T) {
	t.Parallel()
	got := Info{Commit: "abc123", Date: "2026-01-02T15:04:05Z"}.String()
	if !strings.HasPrefix(got, "mcp-slack dev ") {
		t.Errorf("empty version should render as dev, got %q", got)
	}
}

func TestString_OmitsMissingPieces(t *testing.T) {
	t.Parallel()
	// With no commit/date and no VCS info available in a normal test build,
	// the string must still be well formed and always carry the Go version.
	got := Info{Version: "v1.2.3", Commit: "deadbeefcafe00", Date: ""}.String()
	if strings.Contains(got, "built ") {
		t.Errorf("no date should mean no 'built' segment: %q", got)
	}
	if !strings.Contains(got, "commit deadbeefcafe") {
		t.Errorf("commit should be present and shortened: %q", got)
	}
	if !strings.Contains(got, runtime.Version()) {
		t.Errorf("go version must always be present: %q", got)
	}
	if strings.Contains(got, "()") || strings.Contains(got, ", )") {
		t.Errorf("must not emit empty segments: %q", got)
	}
}

func TestShortCommit(t *testing.T) {
	t.Parallel()
	if got := shortCommit("abcdef1234567890"); got != "abcdef123456" {
		t.Errorf("shortCommit long = %q", got)
	}
	if got := shortCommit("abc123"); got != "abc123" {
		t.Errorf("shortCommit short = %q", got)
	}
	if got := shortCommit(""); got != "" {
		t.Errorf("shortCommit empty = %q", got)
	}
}
