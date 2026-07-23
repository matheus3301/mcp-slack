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
