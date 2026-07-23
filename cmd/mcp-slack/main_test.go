package main

import (
	"strings"
	"testing"
)

// TestRun_FailsClosedWithoutToken verifies the process refuses to start (and
// returns before ever opening the stdio transport) when configuration is
// invalid. It must not leak the token or block.
func TestRun_FailsClosedWithoutToken(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "")
	t.Setenv("SLACK_READ_ALLOWED_CHANNELS", "C01234567")

	err := run()
	if err == nil {
		t.Fatal("run() must return an error when the bot token is missing")
	}
	if !strings.Contains(err.Error(), "startup") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRun_FailsClosedWithoutAllowlist(t *testing.T) {
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-1234567890-abcdefghijklmnop")
	t.Setenv("SLACK_READ_ALLOWED_CHANNELS", "")

	if err := run(); err == nil {
		t.Fatal("run() must return an error when the allowlist is empty")
	}
}

func TestVersionRequested(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"--version"}, {"-version"}, {"version"}, {"-v"},
		{"--foo", "version"},
	} {
		if !versionRequested(args) {
			t.Errorf("versionRequested(%v) = false, want true", args)
		}
	}
	for _, args := range [][]string{
		{}, {"serve"}, {"--help"}, {"-h"}, {"C01234567"},
	} {
		if versionRequested(args) {
			t.Errorf("versionRequested(%v) = true, want false", args)
		}
	}
}

func TestBuildInfo_UsesLdflagVars(t *testing.T) {
	t.Parallel()
	// The banner must reflect whatever the linker stamped into the package
	// vars, defaulting version to a non-empty value.
	got := buildInfo().String()
	if !strings.HasPrefix(got, "mcp-slack ") {
		t.Errorf("version banner malformed: %q", got)
	}
}
