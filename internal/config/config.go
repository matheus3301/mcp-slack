// Package config loads and validates the environment configuration for the
// mcp-slack server. It fails closed: any missing or malformed value produces
// an error and the process must not start.
//
// The Slack bot token is treated as a secret. It is never included in error
// messages, never logged, and never exposed through any exported field beyond
// the single accessor used to construct the Slack client.
package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/matheus3301/mcp-slack/internal/validate"
)

// Environment variable names.
const (
	// EnvBotToken is the NAME of the environment variable that holds the token,
	// not a credential itself. The value is only ever read from the environment,
	// never hardcoded or logged.
	EnvBotToken        = "SLACK_BOT_TOKEN" // #nosec G101 -- env var key, not a secret value
	EnvAllowedChannels = "SLACK_READ_ALLOWED_CHANNELS"
)

// botTokenPrefix is the only accepted Slack token type. Bot tokens are the
// least-privileged token that can read channel history with granular scopes.
const botTokenPrefix = "xoxb-"

// forbiddenTokenPrefixes are token types we explicitly refuse. Browser tokens
// (xoxc/xoxd) and user tokens (xoxp) carry a human's full session and must
// never be accepted by an autonomous server.
var forbiddenTokenPrefixes = []string{"xoxc-", "xoxd-", "xoxp-", "xoxa-", "xoxr-", "xoxs-"}

// Config is the validated runtime configuration.
type Config struct {
	botToken  string
	allowlist Allowlist
}

// BotToken returns the validated Slack bot token. Callers must not log it.
func (c *Config) BotToken() string { return c.botToken }

// Allowlist returns the immutable set of channel IDs the server may read.
func (c *Config) Allowlist() Allowlist { return c.allowlist }

// Load reads and validates configuration using the provided getenv function
// (os.Getenv in production, a fake in tests). It never returns the token in an
// error.
func Load(getenv func(string) string) (*Config, error) {
	token := strings.TrimSpace(getenv(EnvBotToken))
	if token == "" {
		return nil, fmt.Errorf("%s is required", EnvBotToken)
	}
	if err := validateToken(token); err != nil {
		return nil, err
	}

	allow, err := parseAllowlist(getenv(EnvAllowedChannels))
	if err != nil {
		return nil, err
	}

	return &Config{botToken: token, allowlist: allow}, nil
}

// validateToken enforces that only a Slack bot token is accepted. The token
// value is never echoed back in the error.
func validateToken(token string) error {
	lower := strings.ToLower(token)
	for _, p := range forbiddenTokenPrefixes {
		if strings.HasPrefix(lower, p) {
			return fmt.Errorf("%s must be a bot token (xoxb-); user, app, and browser tokens are refused", EnvBotToken)
		}
	}
	if !strings.HasPrefix(token, botTokenPrefix) {
		return fmt.Errorf("%s must be a Slack bot token beginning with %q", EnvBotToken, botTokenPrefix)
	}
	// A real xoxb token is well over this length; guard against obvious junk
	// without asserting Slack's exact internal format.
	if len(token) < len(botTokenPrefix)+10 {
		return fmt.Errorf("%s is too short to be a valid bot token", EnvBotToken)
	}
	return nil
}

// wildcardToken, alone, opts into wildcard mode: any public or private channel
// the bot is currently a member of. It must not be combined with explicit IDs.
const wildcardToken = "*"

// parseAllowlist parses the comma-separated channel-ID allowlist. An empty or
// missing value is a fatal error. A single "*" enables wildcard (member-scoped)
// mode. "*" mixed with IDs is invalid. Otherwise every entry must be a channel
// or private-group ID.
func parseAllowlist(raw string) (Allowlist, error) {
	if strings.TrimSpace(raw) == "" {
		return Allowlist{}, fmt.Errorf("%s is required and must be %q or a list of channel IDs", EnvAllowedChannels, wildcardToken)
	}

	tokens := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		t := strings.TrimSpace(part)
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	if len(tokens) == 0 {
		return Allowlist{}, errors.New(EnvAllowedChannels + " did not contain any channel IDs")
	}

	// Wildcard mode: exactly one token, and it must be the literal "*".
	for _, t := range tokens {
		if t == wildcardToken {
			if len(tokens) != 1 {
				return Allowlist{}, fmt.Errorf("%s: %q cannot be combined with channel IDs", EnvAllowedChannels, wildcardToken)
			}
			return wildcardAllowlist(), nil
		}
	}

	seen := make(map[string]struct{})
	ordered := make([]string, 0, len(tokens))
	for _, id := range tokens {
		if err := validate.ChannelID(id); err != nil {
			return Allowlist{}, fmt.Errorf("%s contains an invalid channel ID: %w", EnvAllowedChannels, err)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}
	return newAllowlist(ordered), nil
}
