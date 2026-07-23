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

// parseAllowlist parses the comma-separated channel-ID allowlist. It requires
// at least one entry and rejects anything that is not a channel/group ID.
func parseAllowlist(raw string) (Allowlist, error) {
	if strings.TrimSpace(raw) == "" {
		return Allowlist{}, fmt.Errorf("%s is required and must list at least one channel ID", EnvAllowedChannels)
	}

	seen := make(map[string]struct{})
	ordered := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		id := strings.TrimSpace(part)
		if id == "" {
			continue
		}
		if err := validate.ChannelID(id); err != nil {
			return Allowlist{}, fmt.Errorf("%s contains an invalid channel ID: %w", EnvAllowedChannels, err)
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ordered = append(ordered, id)
	}

	if len(ordered) == 0 {
		return Allowlist{}, errors.New(EnvAllowedChannels + " did not contain any valid channel IDs")
	}
	return newAllowlist(ordered), nil
}
