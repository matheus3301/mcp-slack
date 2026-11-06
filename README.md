# mcp-slack

A Slack server for [Model Context Protocol](https://modelcontextprotocol.io)
clients. Read channel history and threads, or opt into posting, scheduling,
reactions, pins, bookmarks, files, and direct messages. The client talks to a Go
subprocess over stdio; every Slack action uses the identity of your installed bot.

The default configuration exposes the same three read tools as v0.2.0. Reading
a channel does not authorize writing to it. Write grants name channel IDs
explicitly, and direct messages have a separate policy.

- [Install](#install)
- [Set up a Slack app](#set-up-a-slack-app)
- [Configure your MCP client](#configure-your-mcp-client)
- [Tool catalog](#tool-catalog)
- [Examples](#examples)
- [Security and troubleshooting](#security-and-troubleshooting)
- [Develop and release](#develop-and-release)

## Install

Download the archive for your operating system and architecture from the
[releases page](https://github.com/matheus3301/mcp-slack/releases). Releases
include Linux and macOS archives for amd64 and arm64, a Windows amd64 ZIP,
SHA256SUMS, and GitHub build provenance. Windows supports read and diagnostic
tools; the release rejects write-enabled Windows configurations until native
receipt durability and artifact access are supported. For example, on Linux amd64:

~~~bash
gh release download v0.3.0 --repo matheus3301/mcp-slack \
  --pattern 'mcp-slack_v0.3.0_linux_amd64.tar.gz' --pattern SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify mcp-slack_v0.3.0_linux_amd64.tar.gz \
  --repo matheus3301/mcp-slack
tar -xzf mcp-slack_v0.3.0_linux_amd64.tar.gz
./mcp-slack --version
~~~

On macOS, use `shasum -a 256 -c SHA256SUMS` with the matching archive entry.
From source, use the pinned toolchain in [go.mod](go.mod):

~~~bash
go install github.com/matheus3301/mcp-slack/cmd/mcp-slack@v0.3.0
~~~

## Set up a Slack app

Create a Slack app for a workspace you administer, install it, and invite its bot
to the channels it should access. Store the **bot token** in your MCP client's
secret store or process environment. Start with the scopes for the channel types
you read and add only the scopes needed for the capabilities you enable.

| Capability | Bot scopes |
| --- | --- |
| Public channel metadata and history | `channels:read`, `channels:history` |
| Private channel metadata and history | `groups:read`, `groups:history` |
| Send, reply, edit/delete own messages, schedule and cancel | `chat:write` |
| Look up allowed users | `users:read` |
| Read or change reactions | `reactions:read`, `reactions:write` |
| Read or change pins | `pins:read`, `pins:write` |
| Read or change bookmarks | `bookmarks:read`, `bookmarks:write` |
| Read or upload files | `files:read`, `files:write` |
| Direct message metadata, history, and opening conversations | `im:read`, `im:history`, `im:write` |
| Group DM metadata, history, and opening conversations | `mpim:read`, `mpim:history`, `mpim:write` |

Opening a DM uses [conversations.open](https://docs.slack.dev/reference/methods/conversations.open/);
sending the message also needs `chat:write`. Reinstall your app if Slack requires
it after a scope change. Do not add `chat:write.public` or a human token to
work around channel membership.

## Configure your MCP client

Point your client at the installed binary. Substitute example IDs with the
actual Slack conversation IDs; channel names are not policy inputs. Client
configuration formats vary. This is the usual stdio shape:

~~~yaml
mcp_servers:
  slack-read:
    command: /path/to/mcp-slack
    transport: stdio
    env:
      SLACK_BOT_TOKEN: ${SLACK_BOT_TOKEN}
      SLACK_READ_ALLOWED_CHANNELS: "C0123456789,C0987654321"
~~~

Use separate registrations of the same binary when your client assigns
different trust levels to read and write tools. The server's authorization checks
still apply even if the client trusts a tool.

- **Read policy:** `SLACK_READ_ALLOWED_CHANNELS` is a nonempty comma-separated
  list of public or private channel IDs. A single `*` means the channels the
  bot has joined. It cannot be combined with IDs.
- **Write policy:** `SLACK_WRITE_ALLOWED_CHANNELS` names specific channel IDs.
  It never accepts `*`, a channel name, a user ID or a DM ID. A bot still has
  to be a member of the destination channel.
- **DM policy:** a separate opt-in controls whether the MCP can open, read, or
  write bot DMs. Every human participant must be explicitly allowed. Giving the
  bot access to a channel does not allow it to contact the members directly.
- **Capability policy:** tools are registered only when enabled. Keep the
  client's tool allowlist narrow as a second line of defense.

See [configuration and examples](docs/configuration.md) for the complete
environment reference, including channel publishing and a test DM setup.

## Tool catalog

These are the 29 tools available across configurations. The first three tools
in **Legacy reads** are the only tools registered with a v0.2.0 configuration.

| Group | Tool | What it does |
| --- | --- | --- |
| Legacy reads | `slack_channels_list` | List allowed member channels. |
| Legacy reads | `slack_conversations_history` | Read a bounded page of conversation history. |
| Legacy reads | `slack_conversations_replies` | Read a bounded page of thread replies. |
| Diagnostics | `slack_auth_test` | Identify the connected workspace and bot. |
| Conversation lookup | `slack_conversations_info` | Get metadata for an allowed conversation. |
| Conversation lookup | `slack_conversations_members` | Page through members of an allowed conversation. |
| Conversation lookup | `slack_chat_get_permalink` | Get the Slack link for a message in an allowed conversation. |
| Conversation lookup | `slack_users_info` | Look up a user within the configured access policy. |
| Conversation lookup | `slack_messages_search` | Search a bounded amount of one channel's history. |
| Messages | `slack_chat_post_message` | Send a root message or reply with a root `thread_ts`. |
| Messages | `slack_chat_update` | Edit the bot's own message. |
| Messages | `slack_chat_delete` | Delete the bot's own message when enabled. |
| Scheduling | `slack_chat_schedule_message` | Schedule a message to an allowed channel. |
| Scheduling | `slack_chat_scheduled_messages_list` | List the bot's scheduled messages in a channel. |
| Scheduling | `slack_chat_delete_scheduled_message` | Cancel one of the bot's scheduled messages in that channel. |
| Reactions | `slack_reactions_get` | Read reactions on a message. |
| Reactions | `slack_reactions_add` | Add a reaction as the bot. |
| Reactions | `slack_reactions_remove` | Remove a reaction the bot added. |
| Pins | `slack_pins_list` | List a channel's pins. |
| Pins | `slack_pins_add` | Pin a message in its channel. |
| Pins | `slack_pins_remove` | Unpin a message in its channel. |
| Bookmarks | `slack_bookmarks_list` | List a channel's bookmarks. |
| Bookmarks | `slack_bookmarks_add` | Add a bookmark to a channel. |
| Bookmarks | `slack_bookmarks_edit` | Edit a bookmark in that channel. |
| Bookmarks | `slack_bookmarks_remove` | Remove a bookmark from that channel. |
| Files | `slack_files_info` | Read metadata for a file attached to an authorized message. |
| Files | `slack_files_download` | Retrieve bounded file content from an authorized message. |
| Files | `slack_files_upload` | Share bounded content or an approved local artifact in one channel or thread. |
| DMs | `slack_dms_open` | Open a bot DM or group DM with authorized recipients. Use the returned conversation ID for the message and history tools. |

The JSON schemas are available via MCP `tools/list`. Slack timestamps are
strings throughout, so clients can round-trip all fractional digits. The
bounded search uses history pages. It does not use Slack's
[user-token search API](https://docs.slack.dev/reference/methods/search.messages/)
or claim to search the whole workspace.

## Examples

Read a page with `slack_conversations_history`:

~~~json
{"channel_id":"C0123456789","limit":20}
~~~

Read the thread under a known parent with `slack_conversations_replies`:

~~~json
{"channel_id":"C0123456789","thread_ts":"1699999999.000100","limit":20}
~~~

For publishing, give `slack_chat_post_message` the channel ID and final text.
For a reply, use the timestamp of the root message in `thread_ts`; do not
substitute the timestamp of another reply. Save the receipt so you can verify the
message or reply to it later. See [publishing examples](docs/configuration.md#publishing)
for root posts, replies, and idempotency.

## Security and troubleshooting

The server validates destination and object ownership itself. It does not accept
a Slack method name from the model, log message content or credentials, or
listen on a network port. A read grant does not imply a write grant. A failed
write with an uncertain result should be reconciled by reading the destination,
rather than by resubmitting it with a different operation key.

- **Tool missing:** check the server's capabilities and the client's tool filter.
  Restart or reconnect the stdio client after changing the environment.
- **Permission denied:** verify the relevant allowlist, bot membership and
  the required Slack scope. A successful token check does not confer write access.
- **Rate limited:** respect Slack's [rate limits and Retry-After](https://docs.slack.dev/apis/web-api/rate-limits/).
  Prefer thread replies over posting duplicate channel messages.
- **Upload failed:** check artifact bounds, membership and `files:write`. Uploads
  use [files.getUploadURLExternal](https://docs.slack.dev/reference/methods/files.getUploadURLExternal/)
  and [files.completeUploadExternal](https://docs.slack.dev/reference/methods/files.completeUploadExternal/).

See [SECURITY.md](SECURITY.md) for policy and reporting.

## Develop and release

The [official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
validates typed inputs and implements stdio. [slack-go](https://github.com/slack-go/slack)
provides the Slack Web API client. `cmd/mcp-slack` starts the process;
`internal/config` loads policy; `internal/mcpserver` checks tool arguments and
authorization; `internal/slackclient` handles Slack calls and error shaping.
There is no database to provision.

~~~bash
make test         # unit and protocol tests
make test-race    # concurrency checks
make check        # formatting, vet, staticcheck, tests, security scans, build
make build        # static Linux/amd64 binary in dist/
~~~

Tests use a fake Slack API; never put workspace credentials in CI. A version
tag runs the [release workflow](.github/workflows/release.yml), which verifies the
source, builds the archives, writes checksums, and attests provenance. See the
[changelog](CHANGELOG.md) and [architecture decisions](docs/adr/).

## License

[MIT](LICENSE).
