# ADR 0001: A minimal, read-only, bot-token Slack MCP server

- **Status:** Accepted
- **Date:** 2026-07-23
- **Deciders:** Maintainer

## Context

An MCP client — for example, an autonomous AI agent — needs to *read* Slack
context: channel history, thread replies, and basic channel metadata. It does
**not** need to write to Slack through this path.

We need an MCP server, installed as a stdio subprocess, that provides exactly
that read access and nothing more. The central risk is that an autonomous,
prompt-injectable client is on the other end of the tools. Anything the server
*can* do, the client can be tricked into doing. So the safest server is the one
that is incapable of doing anything dangerous.

Three broad options were considered:

1. **Use a community/general-purpose Slack MCP server.**
2. **Wrap the Slack SDK with a generic "call any method" tool.**
3. **Write a minimal server exposing only the three read operations we need.**

## Decision

We built option 3: a custom server exposing exactly three read-only tools
(`slack_channels_list`, `slack_conversations_history`,
`slack_conversations_replies`), backed by a Slack **bot** token and an explicit
channel-ID **allowlist**, over **stdio only**.

Key choices:

- **Bot token, not user/browser token.** Several popular community Slack MCP
  servers authenticate with a user's `xoxc`/`xoxd` browser session or an `xoxp`
  user token to gain broad, human-equivalent access. That is exactly the
  posture we want to avoid for an autonomous client: it inherits a person's full
  Slack identity and every channel they can see. A bot token with a handful of
  read scopes, invited only to specific channels, is far easier to reason about
  and revoke. The server refuses anything that is not `xoxb-`.

- **Closed tool surface, no dynamic proxy.** Option 2 (a generic Slack method
  proxy) is convenient but catastrophic here: it would let a manipulated agent
  call `chat.postMessage`, `conversations.kick`, `search.messages`, etc. We
  instead hardcode three read methods. A static regression test fails the build
  if a forbidden method or a fourth tool is ever introduced.

- **Allowlist over enumeration.** `slack_channels_list` never calls
  `conversations.list`; it fetches each explicitly allowlisted channel by ID.
  The agent cannot discover or read channels the operator did not intend.

- **Fail closed + sanitized errors.** Misconfiguration stops startup. Upstream
  errors collapse to stable codes so the token and Slack internals never leak
  to the MCP client.

- **Official MCP Go SDK.** We use `github.com/modelcontextprotocol/go-sdk`
  (v1.x), the official SDK. Its typed `AddTool[In, Out]` generates and validates
  JSON schemas from Go structs and supports tool annotations
  (`ReadOnlyHint`, `IdempotentHint`, `DestructiveHint`), which we set on all
  three tools. This keeps the protocol layer small and spec-compliant without a
  hand-rolled JSON-RPC implementation.

- **`slack-go/slack` for the Web API.** It is the de-facto Go Slack client,
  exposes context-aware `…Context` methods for cancellation, supports pointing
  at an alternate API URL (used to drive a fake server in tests), and returns
  typed rate-limit errors we can handle deliberately.

## Consequences

**Positive**

- The server is incapable of mutating Slack; prompt-injection cannot turn it
  into a posting/deleting tool.
- Small, auditable surface: three tools, one token type, an explicit allowlist.
- Fully unit- and integration-testable without live credentials (the Slack
  client is behind an interface and there is a fake HTTP server).

**Negative / trade-offs**

- We maintain our own small codebase instead of adopting an upstream project.
  Mitigated by how little there is (a few hundred lines) and by the test/security
  gates in CI.
- Adding a genuinely new read capability later (e.g. reading reactions) requires
  a code change and a new pinned tool — by design.
- The bot must be explicitly invited to every channel in the allowlist, which is
  slightly more operational work than a user token that sees everything. This is
  the point.

## Wildcard (member-scoped) mode

The allowlist accepts a single `*` in place of an ID list. It means every public
or private channel the bot is a member of, and nothing else. Empty stays a
startup error, and `*` cannot be mixed with IDs.

We added this because a curated ID list does not fit a bot whose channel set
changes often. The alternative people reach for is a token that can see the
whole workspace, which is the posture this project exists to avoid. Membership
is a control the operator already has: inviting the bot grants access, removing
it revokes access, and both are visible in Slack.

Two rules keep wildcard mode as tight as explicit mode:

- History and replies verify membership against Slack (`conversations.info`,
  `is_member`) before any content is read. Access ends the moment the bot leaves
  a channel, with no restart or config change.
- Listing uses `users.conversations`, which returns only the bot's member
  channels. We do not call `conversations.list`, so the server never sees the
  channels it is not in. DMs and MPIMs are excluded by request and filtered
  again as defense in depth.

The wider surface is the trade: wildcard mode can read any channel the bot joins
later, so the invite becomes the grant. Operators who want the allowlist itself
to be the audit record should stay with explicit IDs.

## Notes

Adding a channel to this MCP server's allowlist, or inviting the bot in wildcard
mode, only lets the client **read** that channel. This server cannot post
anywhere. If a client also writes to Slack through some other mechanism, where
it may post is configured there, separately from this read allowlist.
