package config

import (
	"strings"
	"testing"
)

// fakeEnv returns a getenv function backed by a map.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

const goodToken = "xoxb-1234567890-abcdefghijklmnop"

func TestLoad_Success(t *testing.T) {
	t.Parallel()
	cfg, err := Load(fakeEnv(map[string]string{
		EnvBotToken:        goodToken,
		EnvAllowedChannels: "C01234567, G0ABCDEFG ,C01234567", // trailing dup + whitespace
	}))
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.BotToken() != goodToken {
		t.Errorf("token not preserved")
	}
	if cfg.Allowlist().Len() != 2 {
		t.Errorf("allowlist len = %d, want 2 (dedup)", cfg.Allowlist().Len())
	}
	if !cfg.Allowlist().Allowed("C01234567") || !cfg.Allowlist().Allowed("G0ABCDEFG") {
		t.Errorf("expected allowlisted channels missing")
	}
	if cfg.Allowlist().Allowed("C99999999") {
		t.Errorf("unexpected channel allowed")
	}
}

func TestLoad_TokenRejections(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"missing":      "",
		"user_token":   "xoxp-1234567890-abcdefghij",
		"browser_xoxc": "xoxc-1234567890-abcdefghij",
		"browser_xoxd": "xoxd-1234567890-abcdefghij",
		"app_token":    "xoxa-1234567890-abcdefghij",
		"wrong_prefix": "slack-1234567890-abcdefghij",
		"too_short":    "xoxb-1",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(fakeEnv(map[string]string{
				EnvBotToken:        tok,
				EnvAllowedChannels: "C01234567",
			}))
			if err == nil {
				t.Fatalf("expected error for %s token", name)
			}
			if strings.Contains(err.Error(), tok) && tok != "" {
				t.Errorf("error leaked token value: %q", err.Error())
			}
		})
	}
}

func TestLoad_AllowlistRejections(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty":       "",
		"whitespace":  "   ",
		"only_commas": ",, ,",
		"dm_id":       "D01234567",
		"user_id":     "U01234567",
		"name":        "general",
		"mixed_bad":   "C01234567,not-an-id",
	}
	for name, allow := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(fakeEnv(map[string]string{
				EnvBotToken:        goodToken,
				EnvAllowedChannels: allow,
			}))
			if err == nil {
				t.Fatalf("expected error for allowlist %q", allow)
			}
		})
	}
}

func TestAllowlist_Immutable(t *testing.T) {
	t.Parallel()
	a := newAllowlist([]string{"C01234567", "G0ABCDEFG"})
	ids := a.IDs()
	ids[0] = "MUTATED"
	if a.Allowed("MUTATED") || !a.Allowed("C01234567") {
		t.Error("IDs() returned a slice that aliases internal state")
	}
}

func TestAllowlist_ZeroValue(t *testing.T) {
	t.Parallel()
	var a Allowlist
	if a.Allowed("C01234567") {
		t.Error("zero-value allowlist must deny everything")
	}
	if a.Len() != 0 {
		t.Error("zero-value allowlist len must be 0")
	}
}
