# Security policy

Use GitHub's [private vulnerability reporting](https://github.com/matheus3301/mcp-slack/security/advisories/new)
for security issues. Include the affected version, minimal reproduction and
expected behavior. Do not include Slack tokens or workspace message content.

## Boundaries

Treat an MCP client as potentially exposed to prompt injection. Authorization
is checked at the server, independently of model instructions and client trust.

| Risk | Server boundary |
| --- | --- |
| Token grants a human's Slack access | Accept a bot token only. Reject user and browser tokens. |
| A readable channel becomes a write target | Write access requires a separate explicit channel allowlist. There is no write wildcard. |
| Channel IDs route to DMs or groups | Verify the conversation type and membership with Slack. DMs require separate capability and participant grants. |
| Editing another author's message or reusing a foreign resource | Verify the destination and ownership or channel association before mutating it. |
| Retrying an uncertain post, schedule or upload | Persist an operation key and receipt metadata; reconcile ambiguous outcomes rather than repeating them. |
| Malicious file paths or URLs | Bound file size, reject caller-provided fetch URLs, and resolve upload paths within the approved artifact root. |
| Leaking Slack credentials or content | Sanitize upstream errors; keep tokens and message/file bodies out of logs and receipt storage. |
| Calling arbitrary Slack methods | Register a fixed typed catalog over stdio. No generic method proxy or network listener. |

The app still has the Slack scopes granted at installation. Restrict those scopes
and invite the bot only where needed. A client using the same bot token by some
other path is outside this server's policy.

## Operating the server

Use the legacy read-only configuration unless a deployment needs additional
capabilities. Keep the receipt directory private and persistent if writes need to
survive restarts. Do not share the process environment or diagnostic dumps with
untrusted clients. Keep write tools behind client approvals appropriate to your
workspace; annotations are descriptive rather than an authorization boundary.

Run `make check` before publishing a version and verify its checksum and
attestation before installing. Report vulnerabilities on the latest supported
release or the default branch.
