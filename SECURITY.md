# Security Policy

`mcp-slack` is a deliberately small, read-only MCP server. It is designed to be
given to an MCP client — including an autonomous, potentially prompt-injectable
agent — while keeping the blast radius as small as possible. This document
describes the security model and how to report issues.

## Reporting a vulnerability

Please **do not** open a public issue for security problems. Instead, use
GitHub's private vulnerability reporting on this repository
(**Security → Report a vulnerability**), or contact the maintainer directly.
Include reproduction steps and the affected version/commit.

## Design guarantees

The server is built so that the following hold by construction, not by policy:

1. **Read-only surface.** Exactly three tools are exposed, all of which map to
   Slack *read* methods (`conversations.info`, `conversations.history`,
   `conversations.replies`). There is no tool — and no code path — that posts,
   edits, deletes, reacts, marks as read, uploads, searches, manages
   usergroups, or calls an arbitrary Slack method. A regression test
   (`internal/mcpserver/forbidden_test.go`) statically fails the build if a
   forbidden method or a fourth tool ever appears.

2. **Bot token only.** The token is read once from `SLACK_BOT_TOKEN` and must
   begin with `xoxb-`. User tokens (`xoxp-`), browser/session tokens
   (`xoxc-`/`xoxd-`), and app-level tokens (`xoxa-`) are rejected at startup.

3. **Channel allowlist.** Every read is gated by an explicit, comma-separated
   allowlist of channel IDs in `SLACK_READ_ALLOWED_CHANNELS`. Only `C…` and
   `G…` IDs are accepted; direct messages (`D…`) are rejected by format, and a
   defense-in-depth check refuses any channel Slack reports as an IM/MPIM. The
   workspace is never enumerated: channel metadata is fetched one ID at a time.

4. **Fail closed.** A missing/invalid token or an empty/invalid allowlist is a
   fatal startup error. The process never starts in a degraded, "allow all"
   mode.

5. **No secret leakage.** The token is never logged and never included in any
   error returned to the MCP client. Slack and transport errors are collapsed
   to a fixed set of stable error codes (e.g. `CHANNEL_NOT_FOUND`,
   `PERMISSION_DENIED`, `RATE_LIMITED`); raw HTTP bodies, headers, URLs, and
   stack traces are never surfaced.

6. **No persistence.** Message bodies are streamed through to the caller and are
   never cached, persisted, or logged.

7. **Bounded work.** Page sizes are clamped to `1..100`. Rate-limit (HTTP 429)
   responses are retried a small, fixed number of times with a hard cap on the
   honored `Retry-After`; the server never sleeps unboundedly. All Slack calls
   respect context cancellation/timeouts.

8. **Stdio only.** The server speaks MCP over stdin/stdout. It opens no network
   listener of its own.

## Threat model (summary)

| Threat | Mitigation |
| --- | --- |
| Compromised/over-scoped token | Bot token only; grant only `channels:read`, `groups:read`, `channels:history`, `groups:history` |
| Agent asked to read a sensitive channel | Allowlist gate; channel must be added explicitly and the bot must be invited |
| Prompt-injection tricking the agent into writing to Slack | No write tool exists in this server |
| Data exfiltration via error strings | Errors sanitized to fixed codes; no token or raw payload in errors |
| Runaway pagination / DoS on Slack | Page size clamp, bounded retries, context timeouts |
| Supply-chain drift | Dependencies pinned in `go.mod`/`go.sum`; CI runs `govulncheck` and `gosec`; GitHub Actions pinned to commit SHAs |

## Known residual findings

- **govulncheck:** `GO-2026-5024` (`golang.org/x/sys` integer overflow in
  `NewNTUnicodeString`) appears as a *required-module* vulnerability but is
  **not called** — it is Windows-only code and this server targets Linux/macOS.
  It will clear on the next `golang.org/x/sys` bump.
- **gosec G101** on the `SLACK_BOT_TOKEN` constant is a false positive (the
  constant is the environment-variable *name*, not a secret) and is annotated
  with a justified `#nosec G101`.

## Supported versions

This project tracks `main`. Security fixes are applied to `main` and released as
new tags.
