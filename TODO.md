# climail

Status: Initial development

Last updated: 2026-11-23

## Done

- Foundations: domain model, config and paths, SQLite store (threads, labels, outbox, sync state)
- Gmail adapter (HTML-to-text bodies, charsets) and in-memory fake
- Sync engine: push outbox, pull history (full resync when expired), cross-process lock
- OAuth (loopback + PKCE), per-account tokens in the keyring with a 0600 file fallback
- Service: thread actions (archive, trash, read/unread, star) with exact undo (the TUI keeps the last 50); mailbox list with unread counts
- CLI: auth, sync, labels, list, show, archive/trash/inbox/read/unread/star/unstar; TTY/TSV/JSON output; multi-account `-a`
- TUI: adaptive layout (sidebar | list | reader, or one pane), Gmail-style reader, triage keys, selection, undo, live search, accounts
- `climail demo`: the TUI on made-up mailboxes, no Google account needed; mail between its two accounts is delivered
- Compose, reply, reply all, forward in `$VISUAL`/`$EDITOR` (CLI and TUI); `send` for scripts; send queue with `outbox`
- The reader marks a message (`▶` and a bar), which reply, forward and the attachment keys act on: the one a reply answers, until `tab`/`shift+tab` move it (read together: across conversations); `enter` opens or closes it
- Gmail search (`climail search`, `enter` in the TUI search) with Gmail's syntax; matches outside the cache are fetched into it
- Attachments: list, download, open (CLI and TUI); forwards carry the original's attachments
- Mail goes out with the display name from Gmail's "Send mail as" settings
- Mail is written in Markdown and sent formatted (HTML in Gmail's default style, plus the plain text); `send --plain` for scripts
- Drafts written in Gmail are continued with their formatting as Markdown; climail asks first when fonts, colours or images would be lost
- Drafts live in Gmail's Drafts: `q` after editing saves one, `enter` in Drafts continues it, sending removes it
- Recipients by name: `maya` in To/Cc/Bcc (and `send --to`) becomes the one matching person; `climail contacts`
- Read Together (HEY-style): `T` shows the selected conversations, else the unread ones listed, on one page; each is archived (or replied to…) on its own as you read on, and undoing puts it back on the page
- Background sync daemon (systemd/launchd, from clindar); the TUI retries pushing every 3s while the daemon holds the lock

## Next

- Manual check against real Gmail accounts: login, first sync, incremental sync, every action reaching Gmail;
  sending (new, reply in the right conversation, forward with attachments, Bcc), daemon install on macOS,
  attachment download and open, Gmail search, display name on sent mail,
  drafts (save, continue one started on the web, send), recipients by name,
  formatted mail as it looks in Gmail, `O` on a draft opening it in Gmail's compose window, Read Together

## Later

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
- Drafts are edited as a plain file with mutt-style headers and kept in Gmail's Drafts, queued like outgoing mail so it works offline. Drafts are found by their message ID (Gmail's draft IDs aren't cached); a continued draft leaves the local Drafts at once, so it can't be continued twice before a sync.
- Attachments are downloaded on demand, not cached; a download refetches the message because Gmail's attachment IDs change between fetches.
- Spam and trash only show in their own views, even inside a conversation (Gmail's behavior).
- The sender's display name is read from Gmail's send-as settings when mail is pushed, not stored: always current, one request per batch.
- Contacts come from the cached mail (no Contacts API, no extra scope); each message's addresses are also kept in their own column so listing them doesn't read bodies (1.3 s → 0.1 s on 10k messages).
- Dependency `github.com/yuin/goldmark` v1.8.7 renders Markdown as HTML (CommonMark, no dependencies of its own). HTML typed in a body stays text, and no font is set, so Gmail's defaults apply.
- Only drafts keep their HTML in the cache, to be continued with their formatting. A draft whose HTML is what its text renders to was saved by climail: its text is continued as typed, not converted back.
