# climail

Status: Initial development

Last updated: 2026-11-21

## Done

- Foundations: domain model, config and paths, SQLite store (threads, labels, outbox, sync state)

## Next

- Gmail adapter and in-memory fake
- Sync engine (push outbox, pull history) with cross-process lock
- OAuth login, multi-account
- CLI: auth, sync, list, show, labels, triage commands
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

- Architecture follows clindar: local SQLite cache, reads never touch the network, edits queue in an outbox. Fast, offline-capable UI; one write path.
- The list unit is the thread (conversation), as in Gmail.
- Multiple Google accounts from the start; every table is keyed by account.
- Keys follow clindar/mutt style: vim movement, single-key actions (`a` archive, `dd` trash, `s` sync).
- Adaptive TUI layout: sidebar | list | reader when wide, one full-screen pane at a time when narrow.
- Labels are stored once, as a `,A,B,` column; membership queries use `instr`. Simple and fast enough for a cache of thousands of messages.
- Outbox holds one label delta per message; a change that undoes a pending one cancels it. Trash is the TRASH label, pushed through Gmail's trash endpoint. Label deltas are idempotent, so there are no conflicts.
- Spam and trash only show in their own views, even inside a conversation (Gmail's behavior).
