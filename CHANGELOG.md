# Changelog

## v0.3.0

- Keep the three original read tools and the existing configuration defaults.
- Add opt-in bot messaging: root posts, thread replies, editing/deleting own
  messages, scheduling and cancellation.
- Add scoped conversation lookup, bounded history search, reactions, pins,
  bookmarks, file operations, and optional DMs and group DMs.
- Separate read, write, and DM access. Channel writes require explicit IDs and
  bot membership; message and file operations validate their destination.
- Protect post, schedule and upload retries with operation keys and sanitized
  receipts.
- Ship write features on Linux and macOS. The Windows build continues to
  support reads and diagnostics.

## v0.2.0

- Add member-scoped wildcard channel reads.

## v0.1.0

- Ship a read-only Slack MCP server with three tools and verified release
  archives for Linux, macOS, and Windows.
