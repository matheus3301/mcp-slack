# Configuration and examples

A deployment can grant reads, channel writes, or bot DMs independently.
Start with the existing read-only configuration and enable only the tools the
client needs. Every example uses placeholder IDs.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `SLACK_BOT_TOKEN` | Required | Installed app's bot token, supplied from the environment. |
| `SLACK_READ_ALLOWED_CHANNELS` | Required | Explicit channel IDs, or the single literal `*` for current member channels. |
| `SLACK_CAPABILITIES` | `legacy_read` | Comma-separated groups listed below. Include `legacy_read` to keep the original three tools. |
| `SLACK_ENABLED_TOOLS` | All tools in selected groups | An optional comma-separated subset of the selected groups' exact tool names. |
| `SLACK_WRITE_ALLOWED_CHANNELS` | No channel writes | Comma-separated explicit public/private channel IDs. No wildcards, users or DM IDs. |
| `SLACK_EXPECTED_TEAM_ID` | Required for expanded capabilities | Slack workspace ID, beginning with `T`. |
| `SLACK_EXPECTED_BOT_ID` | Unset | Expected Slack bot ID (beginning with `B`), checked against the token. |
| `SLACK_DM_ALLOWED_USERS` | None | Explicit Slack user IDs. All human recipients of a DM/MPIM must be listed. |
| `SLACK_DM_READ_ENABLED`, `SLACK_DM_WRITE_ENABLED` | `false` | Independent grants for one-to-one bot conversations; require `dms` and the user allowlist. |
| `SLACK_MPIM_READ_ENABLED`, `SLACK_MPIM_WRITE_ENABLED` | `false` | Independent grants for group bot conversations; require `dms` and the user allowlist. |
| `SLACK_STATE_DIR` | Unset | Absolute path to a private, persistent directory for post, schedule and upload receipts. Required when those tools are enabled. |
| `SLACK_ARTIFACT_ROOT` | Unset | Optional absolute root for upload paths. Only approved artifacts belong here. |

The capability groups are `legacy_read`, `diagnostics`, `conversations`,
`discovery`, `messages`, `schedule`, `reactions`, `pins`, `bookmarks`,
`files`, and `dms`. See the [tool catalog](../README.md#tool-catalog) for names
and the [scope table](../README.md#set-up-a-slack-app) for Slack permissions.
Environment changes take effect when the client restarts its stdio process.
On Windows, configure read and diagnostic tools only. Write-capable tools, DM
write flags and a write receipt directory require Linux or macOS.

## Publishing

This example enables diagnosis, posting and editing the bot's own messages in
one channel. A second process can provide the existing read toolset for a
different list of channels.

~~~yaml
mcp_servers:
  slack-write:
    command: /path/to/mcp-slack
    transport: stdio
    env:
      SLACK_BOT_TOKEN: ${SLACK_BOT_TOKEN}
      SLACK_READ_ALLOWED_CHANNELS: "C0123456789"
      SLACK_WRITE_ALLOWED_CHANNELS: "C0123456789"
      SLACK_EXPECTED_TEAM_ID: "T0123456789"
      SLACK_EXPECTED_BOT_ID: "B0123456789"
      SLACK_CAPABILITIES: "diagnostics,messages"
      SLACK_ENABLED_TOOLS: "slack_auth_test,slack_chat_post_message,slack_chat_update"
      SLACK_STATE_DIR: /path/to/private/slack-receipts
~~~

Create the receipt directory with access limited to the account that runs the
MCP. For example, on Unix:

~~~bash
install -d -m 700 /path/to/private/slack-receipts
~~~

Use `slack_auth_test` first. Then pass the exact destination and text to
`slack_chat_post_message`. Use a stable idempotency key for one logical post.

~~~json
{"channel_id":"C0123456789","text":"Maintenance starts at 18:00 UTC.","operation_key":"maint-2026-11-06-root"}
~~~

Use the returned timestamp to reply. Give the reply its own key:

~~~json
{"channel_id":"C0123456789","thread_ts":"1699999999.000100","text":"Preflight checks passed.","operation_key":"maint-2026-11-06-preflight"}
~~~

Only reuse a key for the same operation and payload. Keep the receipt so that an
uncertain result can be checked in Slack. Do not create a fresh key just to
retry a send whose outcome is unknown.

To enable scheduling, add the `schedule` group and select
`slack_chat_schedule_message`, `slack_chat_scheduled_messages_list` and
`slack_chat_delete_scheduled_message`. Pass `post_at` as future Unix seconds
within Slack's 120-day window. Use Slack's returned `scheduled_message_id` and
the same channel to cancel an item. A scheduled message ID is not the timestamp
of a posted message.

## Reactions, pins, and bookmarks

Enable the corresponding group and grant only the Slack scopes you need. An
example `slack_reactions_add` call, using Slack's emoji name without colons:

~~~json
{"channel_id":"C0123456789","ts":"1699999999.000100","name":"white_check_mark"}
~~~

Use the same channel and message timestamp for `slack_pins_add`. If the target
is a reply, provide its timestamp in `ts` and the parent's in `thread_ts`.
For `slack_bookmarks_add`, pass `channel_id`, `title`, and an HTTP(S) `link`.
Use the returned `bookmark_id` with the same channel to update or remove it.

## Direct messages

For a test installation, add `dms` and the groups containing the message and
read tools you need. Grant only the test recipients, then enable the operation
and conversation types explicitly:

~~~dotenv
SLACK_CAPABILITIES=legacy_read,diagnostics,messages,dms
SLACK_EXPECTED_TEAM_ID=T0123456789
SLACK_DM_ALLOWED_USERS=U0123456789,U0987654321
SLACK_DM_READ_ENABLED=true
SLACK_DM_WRITE_ENABLED=true
SLACK_MPIM_READ_ENABLED=false
SLACK_MPIM_WRITE_ENABLED=false
~~~

Use `slack_dms_open` to open a bot conversation, then pass the returned
conversation ID to the enabled send or read tool. For example:

~~~json
{"users":["U0123456789"]}
~~~

The server verifies participants before allowing access. Do not put a user ID in the channel field
of a posting tool. For group DMs, explicitly enable the MPIM flags and install
the bot with the corresponding Slack scopes.

## Files

Enable the `files` group only for a deployment with the Slack file scopes and a
specific need for file access. For local uploads use an artifact directory that
contains output intended for sharing. Do not point it at a home directory,
checkout, system directory or secret store. Uploads use Slack's external
upload flow, and downloads are bounded and scoped to a file attached to an
authorized message.
Uploads and downloads are limited to 1 MiB per file.

For an inline upload, use `slack_files_upload`:

~~~json
{"channel_id":"C0123456789","filename":"hello.txt","content_base64":"SGVsbG8K","operation_key":"hello-file-01"}
~~~

To upload from disk, use a relative `artifact_path` instead of
`content_base64`. For downloads, pass `channel_id`, the containing message's
`ts`, and its `file_id`; add `thread_ts` for a reply.

## Upgrade or roll back

An unchanged v0.2.0 configuration still exposes exactly three read tools.
Before enabling writes, verify the new binary's checksum and provenance, start
a separate stdio registration with the intended capability list, and compare
`tools/list` and `slack_auth_test` to your expected policy. Test writes in a
sandbox before enabling them for an operational channel. To roll back,
restore the previous binary and environment, then reconnect the client.
Keep the receipt directory so pending or ambiguous sends can be reconciled.
