# climail

Status: Initial development

Last updated: 2026-11-21

## Done

- Foundations: domain model, config and paths, SQLite store (threads, labels, outbox, sync state)
- Gmail adapter (HTML-to-text bodies, charsets) and in-memory fake
- Sync engine: push outbox, pull history (full resync when expired), cross-process lock
- OAuth (loopback + PKCE), per-account tokens in the keyring with a 0600 file fallback
- Service: thread actions (archive, trash, read/unread, star) with exact undo; mailbox list with unread counts
- CLI: auth, sync, labels, list, show, archive/trash/inbox/read/unread/star/unstar; TTY/TSV/JSON output; multi-account `-a`
- TUI: adaptive layout (sidebar | list | reader, or one pane), Gmail-style reader, triage keys, selection, undo, live search, accounts
- `climail demo`: the TUI on made-up mailboxes, no Google account needed
- Compose, reply, reply all, forward in `$VISUAL`/`$EDITOR` (CLI and TUI); `send` for scripts; send queue with `outbox`
- Gmail search (`climail search`, `enter` in the TUI search) with Gmail's syntax; matches outside the cache are fetched into it
- Attachments: list, download, open (CLI and TUI); forwards carry the original's attachments
- Background sync daemon (systemd/launchd, from clindar); the TUI retries pushing every 3s while the daemon holds the lock

## Next

- Manual check against real Gmail accounts: login, first sync, incremental sync, every action reaching Gmail;
  sending (new, reply in the right conversation, forward with attachments, Bcc), daemon install on macOS,
  attachment download and open, Gmail search

## Later

- Keep unsent drafts in Gmail's Drafts (today they stay as local files)
- Send with the account's display name (Gmail send-as settings)
- Log of changes Google rejected
- TUI: show first-sync progress; label picker to add/remove labels; mouse

## Known issues

## Decisions

- An account is chosen by address or its start (`-a work`); the first account added is the default.
- Architecture follows clindar: local SQLite cache, reads never touch the network, edits queue in an outbox. Fast, offline-capable UI; one write path.
- The list unit is the thread (conversation), as in Gmail.
- Multiple Google accounts from the start; every table is keyed by account.
- Keys follow clindar/mutt style: vim movement, single-key actions (`a` archive, `dd` trash, `s` sync).
- Adaptive TUI layout: sidebar | list | reader when wide, one full-screen pane at a time when narrow.
- Labels are stored once, as a `,A,B,` column; membership queries use `instr`. Simple and fast enough for a cache of thousands of messages.
- Outbox holds one label delta per message; a change that undoes a pending one cancels it. Trash is the TRASH label, pushed through Gmail's trash endpoint. Label deltas are idempotent, so there are no conflicts.
- Sync fetches whole threads (threads.get), so conversations are complete; a full sync takes the newest `sync_threads` threads.
- Dependencies `golang.org/x/net/html` (HTML-only mail as text) and `golang.org/x/text` (non-UTF-8 charsets): both already in the module graph via the Google client.
- The reader beside the list previews without marking read; `enter` opens and marks read. After archive/trash from the reader, the next conversation opens (Gmail's behavior).
- `/` filters the local cache as you type (instant, offline); `enter` asks Gmail, whose syntax the local filter doesn't understand. Search results show every message, spam and trash included.
- Outgoing mail is queued in the store and pushed before label changes, so writing works offline; mail Gmail refuses is kept as failed, never dropped.
- Drafts are edited as a plain file with mutt-style headers; a draft left unsent stays on disk and its path is shown.
- Attachments are downloaded on demand, not cached; a download refetches the message because Gmail's attachment IDs change between fetches.
- Spam and trash only show in their own views, even inside a conversation (Gmail's behavior).
