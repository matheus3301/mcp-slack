# ADR 0002: Slack writes require explicit capability and destination grants

- Status: Accepted
- Supersedes: [ADR 0001](0001-minimal-read-only-slack-mcp-server.md)

The original server has three read tools and no way to deliver a result back to
Slack. Clients need root posts, replies, scheduled messages and optional DM and
collaboration features. The same client may read substantially more channels
than it is allowed to write to.

Register typed tools only for explicitly selected capabilities. The `read`
group offers channel metadata, history and thread replies. Use the Slack bot identity,
a separate explicit channel write allowlist, Slack membership checks and resource
validation on every applicable path. Never let a read wildcard imply write
access. Direct messages are a separate grant by participant set, not a special
case of a channel allowlist. Client annotations and tool filters supplement, but
do not replace, those checks.

A post, schedule, or upload has a client operation key and a local receipt.
Avoid automatic repetition if the upstream result is uncertain. The journal
contains no message or file bodies. File endpoints use Slack-owned destinations
and reject resources or paths outside the configured scope.

This permits deployments to expose exactly the tools they need.
It increases the number of APIs and policy paths to test.
It does not add an arbitrary Slack API proxy, user impersonation, workspace
administration, or unrestricted search.
