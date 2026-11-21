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

## Next

- TUI: adaptive layout, reading and triage
- Credential-free demo mode

## Later

- Compose, reply, reply-all, forward in `$EDITOR`; send queue
- Background sync daemon (systemd/launchd)
- Attachments: list, download, open
- Server-side search (Gmail query syntax) for mail outside the cache
- Log of changes Google rejected

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
- Spam and trash only show in their own views, even inside a conversation (Gmail's behavior).
