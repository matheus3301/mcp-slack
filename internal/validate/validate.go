// Package validate holds the input validators shared by configuration parsing
// and the MCP tool handlers. Every validator is total, allocation-light, and
// safe for concurrent use; each returns a stable, sanitized error suitable for
// returning to an MCP client (it never echoes secrets or unbounded input).
package validate

import (
	"fmt"
	"regexp"
)

// Bounds for message page sizes. Slack itself caps most read methods at 1000,
// but a conservative ceiling keeps responses small and predictable.
const (
	MinLimit     = 1
	MaxLimit     = 100
	DefaultLimit = 20
)

// maxCursorLen bounds an opaque Slack pagination cursor. Real cursors are a
// couple hundred base64 characters; this leaves generous headroom while
// rejecting absurd input.
const maxCursorLen = 1024

var (
	// channelIDRe matches public-channel (C) and private-group (G) IDs. Slack
	// IDs are uppercase alphanumeric. DM (D) and other prefixes are rejected by
	// construction, so no direct-message or user conversation can be addressed.
	channelIDRe = regexp.MustCompile(`^[CG][A-Z0-9]{6,20}$`)

	// timestampRe matches a Slack message timestamp such as "1699999999.123456".
	timestampRe = regexp.MustCompile(`^\d{10}\.\d{6}$`)

	// cursorRe matches the base64/url-safe character set Slack uses for
	// response_metadata.next_cursor.
	cursorRe = regexp.MustCompile(`^[A-Za-z0-9+/=_-]+$`)
)

// ChannelID validates a Slack channel or private-group ID. Only C... and G...
// IDs are accepted; direct messages (D...) and multi-party DMs addressed by
// other schemes are refused.
func ChannelID(id string) error {
	if id == "" {
		return fmt.Errorf("channel ID is required")
	}
	if !channelIDRe.MatchString(id) {
		return fmt.Errorf("channel ID must be a C... or G... Slack ID")
	}
	return nil
}

// Timestamp validates an optional Slack message timestamp. An empty string is
// treated as "not provided" and is valid; callers decide whether it is
// required (see Thread timestamps, which are validated with RequiredTimestamp).
func Timestamp(ts string) error {
	if ts == "" {
		return nil
	}
	if !timestampRe.MatchString(ts) {
		return fmt.Errorf("timestamp must be in Slack ts format, e.g. 1699999999.123456")
	}
	return nil
}

// RequiredTimestamp validates a Slack timestamp that must be present.
func RequiredTimestamp(ts string) error {
	if ts == "" {
		return fmt.Errorf("timestamp is required")
	}
	return Timestamp(ts)
}

// Cursor validates an optional pagination cursor.
func Cursor(cursor string) error {
	if cursor == "" {
		return nil
	}
	if len(cursor) > maxCursorLen {
		return fmt.Errorf("cursor is too long")
	}
	if !cursorRe.MatchString(cursor) {
		return fmt.Errorf("cursor contains invalid characters")
	}
	return nil
}

// Limit normalizes a requested page size. Zero (unset) yields DefaultLimit; any
// value outside [MinLimit, MaxLimit] is rejected rather than silently clamped,
// so callers get deterministic, predictable behavior.
func Limit(requested int) (int, error) {
	if requested == 0 {
		return DefaultLimit, nil
	}
	if requested < MinLimit || requested > MaxLimit {
		return 0, fmt.Errorf("limit must be between %d and %d", MinLimit, MaxLimit)
	}
	return requested, nil
}
