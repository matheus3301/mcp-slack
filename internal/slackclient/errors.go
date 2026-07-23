package slackclient

import (
	"context"
	"errors"
	"strings"

	"github.com/slack-go/slack"
)

// Stable, client-facing error codes. These are the ONLY error identifiers that
// leave this package. Raw HTTP bodies, headers, URLs, token fragments, and
// stack traces are never surfaced to the MCP client.
const (
	CodeAuthFailed       = "AUTH_FAILED"
	CodePermissionDenied = "PERMISSION_DENIED"
	CodeChannelNotFound  = "CHANNEL_NOT_FOUND"
	CodeNotInChannel     = "NOT_IN_CHANNEL"
	CodeThreadNotFound   = "THREAD_NOT_FOUND"
	CodeRateLimited      = "RATE_LIMITED"
	CodeTimeout          = "TIMEOUT"
	CodeInvalidRequest   = "INVALID_REQUEST"
	CodeUpstreamError    = "UPSTREAM_ERROR"
)

// Error is a sanitized, stable error returned by the Slack client and the MCP
// tools. Its string form is a code plus a fixed human-readable message; it
// never contains request-specific or secret data.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// NewError builds a sanitized Error with the given code and message.
func NewError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// messages maps each code to a fixed, safe description.
var messages = map[string]string{
	CodeAuthFailed:       "Slack authentication failed",
	CodePermissionDenied: "the bot token lacks permission for this operation",
	CodeChannelNotFound:  "the requested channel does not exist or is not accessible",
	CodeNotInChannel:     "the bot is not a member of the requested channel",
	CodeThreadNotFound:   "the requested thread was not found",
	CodeRateLimited:      "Slack rate limit exceeded; retry later",
	CodeTimeout:          "the request timed out",
	CodeInvalidRequest:   "the request was invalid",
	CodeUpstreamError:    "the Slack API returned an unexpected error",
}

// coded builds an Error using the canonical message for a code.
func coded(code string) *Error {
	return &Error{Code: code, Message: messages[code]}
}

// knownSlackErrors maps documented Slack API error strings to stable codes.
// Anything not listed collapses to UPSTREAM_ERROR so we never leak novel or
// unexpected Slack strings verbatim.
var knownSlackErrors = map[string]string{
	"invalid_auth":           CodeAuthFailed,
	"not_authed":             CodeAuthFailed,
	"account_inactive":       CodeAuthFailed,
	"token_revoked":          CodeAuthFailed,
	"token_expired":          CodeAuthFailed,
	"no_permission":          CodePermissionDenied,
	"missing_scope":          CodePermissionDenied,
	"not_allowed_token_type": CodePermissionDenied,
	"ekm_access_denied":      CodePermissionDenied,
	"channel_not_found":      CodeChannelNotFound,
	"not_in_channel":         CodeNotInChannel,
	"is_archived":            CodeNotInChannel,
	"thread_not_found":       CodeThreadNotFound,
	"message_not_found":      CodeThreadNotFound,
	"ratelimited":            CodeRateLimited,
	"rate_limited":           CodeRateLimited,
	"invalid_arguments":      CodeInvalidRequest,
	"invalid_argument_name":  CodeInvalidRequest,
	"invalid_cursor":         CodeInvalidRequest,
	"invalid_limit":          CodeInvalidRequest,
	"invalid_ts_latest":      CodeInvalidRequest,
	"invalid_ts_oldest":      CodeInvalidRequest,
}

// Sanitize converts any error returned by the Slack SDK or transport into a
// stable *Error. It is the single choke point through which upstream failures
// are allowed to reach an MCP client.
func Sanitize(err error) error {
	if err == nil {
		return nil
	}

	// Already sanitized.
	var e *Error
	if errors.As(err, &e) {
		return e
	}

	// Context cancellation / deadline.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return coded(CodeTimeout)
	}

	// Rate limiting surfaced by the SDK as a typed error.
	var rl *slack.RateLimitedError
	if errors.As(err, &rl) {
		return coded(CodeRateLimited)
	}

	// Slack API-level error responses carry a documented error string.
	if code, ok := slackErrorCode(err); ok {
		if mapped, known := knownSlackErrors[code]; known {
			return coded(mapped)
		}
		// A structured Slack error we don't specifically map: safe to report
		// generically without leaking transport details.
		return coded(CodeUpstreamError)
	}

	// Everything else (network, TLS, JSON decode, etc.) is opaque by design.
	return coded(CodeUpstreamError)
}

// slackErrorCode extracts the Slack error string from a structured Slack error
// response, if present. It only trusts the typed SlackErrorResponse so that
// arbitrary transport error text is never treated as a Slack code.
func slackErrorCode(err error) (string, bool) {
	var serr slack.SlackErrorResponse
	if errors.As(err, &serr) {
		return strings.TrimSpace(serr.Err), true
	}
	return "", false
}
