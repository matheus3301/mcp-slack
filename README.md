# mcp-slack

A read-only [Model Context Protocol](https://modelcontextprotocol.io) (MCP)
server for Slack. It gives an MCP client three things and nothing else: channel
metadata, channel history, and thread replies. It speaks MCP over stdio,
authenticates with a Slack bot token, and reads only the channels you name in an
allowlist.

The point is a surface small enough to hand to an autonomous, prompt-injectable
client without losing sleep. The server cannot post, edit, delete, react, mark
as read, upload or download files, search, manage usergroups, open DMs, or call
an arbitrary Slack method. Those paths do not exist in the code, and a test
fails the build if anyone adds one.

- Module: `github.com/matheus3301/mcp-slack`
- Entrypoint: `./cmd/mcp-slack`
- Go: 1.26.x (see [`go.mod`](go.mod) and [`.mise.toml`](.mise.toml))
- SDKs: [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) (official MCP Go SDK) and [`github.com/slack-go/slack`](https://github.com/slack-go/slack)

## Contents

- [Quick start](#quick-start)
- [Install](#install)
- [The three tools](#the-three-tools)
- [Architecture](#architecture)
- [Threat model](#threat-model)
- [Slack app: scopes and bot membership](#slack-app-scopes-and-bot-membership)
- [Environment variables](#environment-variables)
- [Running locally](#running-locally)
- [Tool schemas and examples](#tool-schemas-and-examples)
- [Error codes](#error-codes)
- [Local development](#local-development)
- [Security checks](#security-checks)
- [Using it from an MCP client](#using-it-from-an-mcp-client)

## Quick start

The fastest path is to grab the prebuilt binary for your platform, then point an
MCP client at it. This needs the [GitHub CLI](https://cli.github.com), which
detects your platform and downloads the latest release.

```bash
# 1. Download the binary for this machine and unpack it.
os=$(uname -s | tr '[:upper:]' '[:lower:]')            # linux or darwin
arch=$(uname -m); case "$arch" in x86_64) arch=amd64;; aarch64|arm64) arch=arm64;; esac
gh release download --repo matheus3301/mcp-slack --pattern "mcp-slack_*_${os}_${arch}.tar.gz"
tar -xzf mcp-slack_*_"${os}"_"${arch}".tar.gz

# 2. Move it onto your PATH and confirm it runs.
sudo install mcp-slack /usr/local/bin/mcp-slack
mcp-slack --version
```

Windows users download `mcp-slack_<version>_windows_amd64.zip` from the
[releases page](https://github.com/matheus3301/mcp-slack/releases) and unzip it.
No CLI needed. To verify the download and its provenance first, see
[Install](#install).

Create a Slack app, give its bot token the four read scopes, install it, and
invite the bot to each channel you want to read. The steps are in
[Slack app: scopes and bot membership](#slack-app-scopes-and-bot-membership).

Then register the server with your MCP client. The token comes from the
environment; the allowlist is a separate value:

```yaml
mcp_servers:
  slack:
    command: /usr/local/bin/mcp-slack
    transport: stdio
    env:
      SLACK_BOT_TOKEN: ${SLACK_BOT_TOKEN}
      SLACK_READ_ALLOWED_CHANNELS: "C0123456789,C0987654321"
```

Restart the client. It launches `mcp-slack` over stdio, and the three tools
appear. To prove the server works on its own before wiring a client, run the
[stdio smoke test](#running-locally).

## Install

Two supported paths. Pick one.

### Prebuilt binary

Each tagged release ships statically linked binaries for the platforms below.
Every archive holds the binary, `LICENSE`, and `README.md`. A `SHA256SUMS` file
covers all archives, and each archive carries a signed build-provenance
attestation.

| Platform | Asset |
| --- | --- |
| Linux amd64 | `mcp-slack_<version>_linux_amd64.tar.gz` |
| Linux arm64 | `mcp-slack_<version>_linux_arm64.tar.gz` |
| macOS amd64 | `mcp-slack_<version>_darwin_amd64.tar.gz` |
| macOS arm64 | `mcp-slack_<version>_darwin_arm64.tar.gz` |
| Windows amd64 | `mcp-slack_<version>_windows_amd64.zip` |

Download the archive for your platform along with `SHA256SUMS`, then check the
hash and the provenance before you trust the binary:

```bash
# Integrity: the archive matches the published checksum.
sha256sum -c SHA256SUMS --ignore-missing

# Provenance: GitHub built this archive from this repository.
gh attestation verify mcp-slack_<version>_linux_amd64.tar.gz \
  --repo matheus3301/mcp-slack

tar -xzf mcp-slack_<version>_linux_amd64.tar.gz
./mcp-slack --version
```

### From source

```bash
go install github.com/matheus3301/mcp-slack/cmd/mcp-slack@v0.1.0
```

Either way, `mcp-slack --version` prints the version, commit, build date, and Go
toolchain.

## The three tools

| Tool | Slack method | What it returns |
| --- | --- | --- |
| `slack_channels_list` | `conversations.info`, one call per allowlisted ID | Metadata for allowlisted channels: name, privacy, membership, topic, purpose, member count |
| `slack_conversations_history` | `conversations.history` | A bounded page of messages from one allowlisted channel |
| `slack_conversations_replies` | `conversations.replies` | A bounded page of replies in one thread of one allowlisted channel |

Every tool is annotated `readOnlyHint: true`, `idempotentHint: true`,
`destructiveHint: false`.

## Architecture

```
        stdin/stdout (MCP / JSON-RPC)
                  │
          ┌───────▼────────┐
          │  cmd/mcp-slack │  loads & validates config, wires deps
          └───────┬────────┘
                  │
       ┌──────────▼───────────┐     three tools, fixed at compile time
       │  internal/mcpserver  │     validate, check allowlist, shape result
       └──────────┬───────────┘
                  │ slackclient.API (interface, injected)
       ┌──────────▼───────────┐
       │ internal/slackclient │     adapter over slack-go:
       │                      │       bounded 429 retry
       │                      │       error sanitization
       │                      │       Slack types to minimal structs
       └──────────┬───────────┘
                  │ HTTPS (bot token)
             Slack Web API
```

The layout follows the standard Go convention: the executable lives under
`cmd/`, and everything reusable lives under `internal/` so nothing leaks into
another module's import graph.

- **`cmd/mcp-slack`** holds `main`: read config, build the client, register the
  tools, run over stdio, handle signals.
- **`internal/config`** loads and validates `SLACK_BOT_TOKEN` and
  `SLACK_READ_ALLOWED_CHANNELS`. It fails closed and owns the immutable
  `Allowlist`.
- **`internal/validate`** holds the validators for channel IDs, Slack
  timestamps, cursors, and page limits. Config and the tools share them.
- **`internal/slackclient`** defines a small `API` interface and an adapter over
  `slack-go`. The interface is the seam that lets the tools run in tests against
  a fake, with no network and no credentials. This layer also owns the bounded
  rate-limit retry and the error sanitization.
- **`internal/mcpserver`** registers the three tools with the MCP SDK, runs
  input validation and the allowlist check, and shapes deterministic output.

## Threat model

Assume the client on the other end is autonomous and can be steered by a
prompt-injection attack. Whatever the server can do, that client can be tricked
into doing. So the server does as little as possible. [`SECURITY.md`](SECURITY.md)
carries the full model. The short version:

- Bot token only (`xoxb-`). User tokens (`xoxp-`) and browser tokens
  (`xoxc-`, `xoxd-`) are rejected at startup.
- Every read passes through the allowlist. Only channels you list, and that the
  bot has joined, are readable. The server never enumerates the workspace.
- No write, no search, no dynamic method proxy. A static test enforces this.
- Bad config stops the process. No token or raw upstream detail reaches the logs
  or an error response. No message body is cached. Page sizes and retries are
  bounded. The server opens no network listener; it only speaks stdio.

## Slack app: scopes and bot membership

1. Create a Slack app and give its bot token the minimum read scopes:

   | Scope | Needed for |
   | --- | --- |
   | `channels:read` | metadata of public channels |
   | `groups:read` | metadata of private channels |
   | `channels:history` | history and replies in public channels |
   | `groups:history` | history and replies in private channels |

   Leave `mpim:*` and `im:*` off. The server rejects DMs and MPIMs, so those
   scopes buy you nothing here.

2. Install the app to the workspace and copy the Bot User OAuth Token. It starts
   with `xoxb-`.

3. Invite the bot to every channel you plan to allowlist. Run `/invite @your-bot`
   in each one. A bot reads only channels it has joined; without the invite,
   reads return `NOT_IN_CHANNEL`.

4. Collect the channel IDs, not the names. In Slack, open a channel, choose
   *View channel details*, and read the ID at the bottom (for example
   `C0123456789`).

## Environment variables

| Variable | Required | Format | Description |
| --- | --- | --- | --- |
| `SLACK_BOT_TOKEN` | yes | `xoxb-…` | Slack bot token. Never logged. Any other token type is rejected. |
| `SLACK_READ_ALLOWED_CHANNELS` | yes | `C…,G…`, comma-separated | The only channels that can be read. `C…` (public) and `G…` (private) IDs only. DMs and names are rejected. At least one is required. |

Both are validated at startup. A missing or malformed value stops the process.

## Running locally

You need Go 1.26.x. With [mise](https://mise.jdx.dev) the pinned toolchain comes
from `.mise.toml`.

```bash
# Build
go build -o dist/mcp-slack ./cmd/mcp-slack

# Run. An MCP client normally launches this; you rarely run it by hand.
export SLACK_BOT_TOKEN="xoxb-…"
export SLACK_READ_ALLOWED_CHANNELS="C0123456789,C0987654321"
./dist/mcp-slack
```

Drive it by hand with raw JSON-RPC over stdio:

```bash
{ printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'; sleep 1; } \
  | SLACK_BOT_TOKEN="xoxb-…" SLACK_READ_ALLOWED_CHANNELS="C0123456789" ./dist/mcp-slack
```

## Tool schemas and examples

The MCP SDK generates each input schema from a Go struct and validates arguments
before a handler runs. The server then re-validates and applies the allowlist.

### `slack_channels_list`

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `channel_ids` | `string[]` | no | A subset of allowlisted IDs to fetch. Omit it to return every allowlisted channel. IDs outside the allowlist are rejected. |

```jsonc
// arguments
{ "channel_ids": ["C0123456789"] }

// structured result
{
  "channels": [
    {
      "id": "C0123456789",
      "name": "general",
      "is_private": false,
      "is_archived": false,
      "is_member": true,
      "is_channel": true,
      "num_members": 42,
      "topic": "team topic",
      "purpose": "team purpose",
      "created": 1609459200
    }
  ]
}
```

### `slack_conversations_history`

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `channel_id` | `string` | yes | Allowlisted `C…`/`G…` ID |
| `limit` | `int` | no | `1..100`, default `20` |
| `cursor` | `string` | no | `next_cursor` from a previous response |
| `oldest` | `string` | no | Slack ts, for example `1699999999.123456` |
| `latest` | `string` | no | Slack ts |
| `inclusive` | `bool` | no | Include messages whose ts equals `oldest` or `latest` |

```jsonc
// arguments
{ "channel_id": "C0123456789", "limit": 50 }

// structured result
{
  "messages": [
    { "type": "message", "ts": "1699999999.000100", "user": "U1", "text": "…" }
  ],
  "has_more": true,
  "next_cursor": "dXNlcjpVMDYxTkZUVDI="
}
```

### `slack_conversations_replies`

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `channel_id` | `string` | yes | Allowlisted `C…`/`G…` ID |
| `thread_ts` | `string` | yes | ts of the thread's parent message, for example `1699999999.000100` |
| `limit`, `cursor`, `oldest`, `latest`, `inclusive` | | no | Same as history |

```jsonc
// arguments
{ "channel_id": "C0123456789", "thread_ts": "1699999999.000100", "limit": 100 }
```

Each message carries only the useful fields: `type`, `subtype`, `ts`,
`thread_ts`, `user`, `bot_id`, `username`, `text`, `reply_count`, `edited_ts`.

## Error codes

A tool error comes back as an MCP tool error (`isError: true`) with a stable
code. Slack and transport details never leak through it.

| Code | Meaning |
| --- | --- |
| `INVALID_REQUEST` | Input failed validation: bad ID, ts, cursor, or limit |
| `PERMISSION_DENIED` | Channel not allowlisted, missing scope, or a DM/MPIM |
| `CHANNEL_NOT_FOUND` | Channel does not exist or is not accessible |
| `NOT_IN_CHANNEL` | The bot has not joined the channel; invite it |
| `THREAD_NOT_FOUND` | Thread or message not found |
| `RATE_LIMITED` | Slack returned 429 after the bounded retries |
| `TIMEOUT` | Context deadline or cancellation |
| `AUTH_FAILED` | Token invalid, inactive, or revoked |
| `UPSTREAM_ERROR` | Any other Slack or transport error, kept opaque on purpose |

## Local development

```bash
make check        # fmt-check, vet, staticcheck, test, test-race, security, build
make test         # unit and integration tests
make test-race    # race detector
make lint         # go vet and staticcheck
make security     # govulncheck and gosec
make build        # reproducible Linux/amd64 binary into dist/
```

The tests cover four levels:

- Unit tests for every validator, the allowlist including its denials, the page
  limits, the response shaping, and each of the three tools.
- A fake Slack HTTP server exercises the real adapter: shaping, pagination, the
  429 retry, and error sanitization, all without credentials.
- An MCP protocol test connects a real client over an in-memory transport and
  asserts the exact tool names, the read-only annotations, the input schemas,
  and the read behavior against fakes.
- A regression test fails the build if a forbidden Slack write or search method,
  or a fourth tool, ever appears in the source.

## Security checks

CI and the Makefile run the same set, pinned to fixed versions:

- `gofmt`, `go vet`
- `staticcheck` v0.7.0
- `go test` and `go test -race`
- `gosec` v2.28.0
- `govulncheck` v1.6.0

[`SECURITY.md`](SECURITY.md) holds the policy, the threat-model table, and the
two residual findings, each documented and justified.

## Using it from an MCP client

The server is a stdio subprocess. A client launches it and speaks JSON-RPC over
stdin and stdout. The bot token comes from the environment. The read allowlist
is a separate value.

Field names differ by client, but the shape is common:

```yaml
mcp_servers:
  slack:
    command: /path/to/mcp-slack
    transport: stdio
    env:
      SLACK_BOT_TOKEN: ${SLACK_BOT_TOKEN}
      # The channels this server may read.
      SLACK_READ_ALLOWED_CHANNELS: "C0123456789,C0987654321"
```

A read grant is not a write grant. Adding a channel to
`SLACK_READ_ALLOWED_CHANNELS` lets the client read that channel through this
server. This server posts nowhere. If your client writes to Slack through some
other mechanism, that is configured there, and this allowlist has no bearing on
it.

Build a static Linux binary for a deployment target:

```bash
make build   # dist/mcp-slack, linux/amd64, static, stripped
```

## License

[MIT](LICENSE).
