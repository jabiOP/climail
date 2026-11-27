# climail

Status: Initial development

Last updated: 2026-11-27

## Done

- Foundations: domain model, config and paths, SQLite store (threads, labels, outbox, sync state)
- Gmail adapter (HTML-to-text bodies, charsets) and in-memory fake
- Sync engine: push outbox, pull history (full resync when expired), cross-process lock
- OAuth (loopback + PKCE), per-account tokens in the keyring with a 0600 file fallback
- Service: thread actions (archive, trash, read/unread, star) with exact undo (the TUI keeps the last 50); mailbox list with unread counts
- CLI: auth, sync, labels, list, show, archive/trash/inbox/read/unread/star/unstar; TTY/TSV/JSON output; multi-account `-a`
- TUI: adaptive layout (sidebar | list | reader, or one pane), Gmail-style reader, triage keys, selection, undo, live search, accounts
- `climail demo`: the TUI on made-up mailboxes, no Google account needed; mail between its two accounts is delivered
- Compose, reply, reply all, forward in `$VISUAL`/`$EDITOR` (CLI and TUI); replies open on the message's first line; `send` for scripts; send queue with `outbox`
- The reader marks a message (`▶` and a bar), which reply, forward and the attachment keys act on: the one a reply answers, until `tab`/`shift+tab` move it (read together: across conversations); `enter` opens or closes it; `j`/`k` move it too when there's nothing more to scroll; `J`/`K` read the next/previous message
- Gmail search (`climail search`, `enter` in the TUI search) with Gmail's syntax; matches outside the cache are fetched into it
- Attachments: list, download, open (CLI and TUI); forwards carry the original's attachments
- Gmail's "Send mail as" addresses: new mail and forwards go from the default one, replies from the one the message was sent to; `From:` (and `send --from`) takes any of them; each goes out with its display name; reply all leaves all of them out
- Mail is written in Markdown and sent formatted (HTML in Gmail's default style, plus the plain text); `send --plain` for scripts
- Drafts written in Gmail are continued with their formatting as Markdown; climail asks first when fonts, colours or images would be lost
- Drafts live in Gmail's Drafts: `q` after editing saves one, `enter` in Drafts continues it (and asks even if left unchanged), sending removes it; `d` after editing discards (CLI too)
- Recipients by name: `maya` in To/Cc/Bcc (and `send --to`) becomes the one matching person; `climail contacts`
- Read Together (HEY-style): `T` shows the selected conversations, else the unread ones listed, on one page; each is archived (or replied to…) on its own as you read on, and undoing puts it back on the page
- Attachments saved on macOS are marked as downloaded (quarantine), so macOS asks before opening a risky one
- The config and data folders and the mail cache are private to the user, made so even if an earlier run left them open
- Mail can't send escape sequences to the terminal (clipboard writes, hidden links, faked screen content): TUI and CLI print it cleaned
- TUI: panes side by side are framed; the focused one has a double frame in the account's color, which also colors its title, the cursor row and the reader's bar (blue with All accounts, unless it is given a color: `C` on it, or `"all"` in `[account_colors]`)
- TUI shows how a sync's download goes (`⟳ you@work.example: downloading 120/300`; on an empty first list, in its middle)
- Log of what Gmail rejected (as in clindar): `climail log`, `L` in the TUI, and a count in the status bar; label changes refused or on mail deleted in Gmail, mail it won't send or save
- Sidebar: `J`/`K` go to the next/previous account
- Status bar: questions, messages and the sync state wrap onto more lines (up to a third of the screen) instead of being cut; only the key hints are cut
- Background sync daemon (systemd/launchd, from clindar); the TUI retries pushing every 3s while the daemon holds the lock
- All accounts in one list (TUI): `All accounts` in the sidebar and in `[`/`]`; system mailboxes merged newest first, unread counts added up; actions, replies, attachments and undo in each conversation's own account; Gmail search asks every account
- Mail says which account it goes from: a `From:` line in every draft (another account by address or its start, for new mail and forwards; replies and continued drafts stay in their account), and the send question starts with it
- `O` opens Gmail on the web where you are: the mailbox or search shown from the list, the conversation from the reader
- TUI launch: the reader stays empty until you move to or open a conversation (`preview_at_launch = true` previews the first); `sidebar_at_launch = false` opens with the mailboxes hidden; `account_at_launch` opens on an account or `"all"`; `esc` in the list (nothing selected or searched) closes the conversation in the reader
- `climail count` (unread, for status bars) and docs/scripting.md (status bars, notifications, cleanups, fzf, jq)
- TUI mouse (`mouse = true`): clicks select a mailbox, conversation or message and open it when already selected; the wheel moves the list and scrolls the reader
- Each account has a color (Google Calendar's event colors, as in clindar): sidebar dot, list title, a stripe on each conversation in All accounts; chosen from Google Calendar's 24 (clindar's palette, in a grid by family) with `C` in the sidebar, or `[account_colors]` in config.toml

## Next

- Manual check against real Gmail accounts (done: login, first and incremental sync, actions, sending with attachments,
  daemon on macOS, All accounts and colors, "Send mail as", `O` on mailboxes and conversations, attachment open,
  recipients by name, Read Together, first-sync progress on a fresh login): Bcc, reply in the right conversation,
  forward with attachments, Gmail search, drafts (save, continue one started on the web, send),
  formatted mail as it looks in Gmail, a draft's compose window opening in Gmail with `O`,
  macOS asking before opening a risky attachment (e.g. a `.terminal` file) with `A`,
  framed panes and account colors on a light terminal, the mouse (clicks and wheel, in iTerm2 and macOS Terminal)

## Later

- TUI: label picker to add/remove labels

## Known issues


## Decisions

- An account is chosen by address or its start (`-a work`); the first account added is the default.
- Architecture follows clindar: local SQLite cache, reads never touch the network, edits queue in an outbox. Fast, offline-capable UI; one write path.
- The list unit is the thread (conversation), as in Gmail.
- Multiple Google accounts from the start; every table is keyed by account.
- Keys follow clindar/mutt style: vim movement, single-key actions (`a` archive, `dd` trash, `s` sync).
- `r` replies to all and `R` to the sender only: replying to everyone is the common case, so it gets the easier key.
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
- Gmail's send-as addresses are cached at each sync (one cheap request), so writing knows the default offline; when mail is pushed they are fetched again, so its display name is always current. The draft's From holds the address; the push looks its name up.
- Contacts come from the cached mail (no Contacts API, no extra scope); each message's addresses are also kept in their own column so listing them doesn't read bodies (1.3 s → 0.1 s on 10k messages).
- Dependency `github.com/yuin/goldmark` v1.8.7 renders Markdown as HTML (CommonMark, no dependencies of its own). HTML typed in a body stays text, and no font is set, so Gmail's defaults apply.
- Only drafts keep their HTML in the cache, to be continued with their formatting. A draft whose HTML is what its text renders to was saved by climail: its text is continued as typed, not converted back.
- Text from mail is cleaned of control characters and direction overrides where it is printed (the TUI's `fit`/`row`, the CLI's stdout/stderr), not when stored: mail already cached is covered, and nothing escapes through a path that forgot to clean.
- Dependency `golang.org/x/sys` v0.50.0 (already in the module graph via `x/term`) sets the macOS quarantine mark on saved attachments, as Mail and browsers do.
- All accounts merges each account's newest conversations in the service (no cross-account SQL): the store stays per account, and N accounts × one page is cheap. Only system mailboxes are shown there; user labels belong to one account.
- The TUI tells conversations apart by account and ID: two accounts' IDs can collide (the demo's do).
- Account colors follow the order accounts were added, from Google Calendar's event palette, unless config.toml's `[account_colors]` names one. `C` writes its choice there by editing the table's lines, so the user's comments and layout survive (a TOML encoder would drop them).
- Rejections are logged in the store when sync drops them (clindar's log table and `log`/`L`); the TUI counts those since it opened, so old ones don't nag.
- Gmail calls that hit its rate limit (429, or 403 rateLimitExceeded) are retried in the client, waiting 1, 2, 4… 32 s: past the minute its per-user quota counts over, so a first sync of many threads finishes instead of failing. Google's errors are cut to one line (code and message).
- Gmail requests start at most 20 a second. Real accounts can get far less quota than Gmail documents (one hit it after ~60 threads), so a rate limit pauses every request of the client for the quota's minute (retrying sooner only spends it again) and halves the pace. A first sync saves every 25 threads, so one cut short (even killed) keeps what it fetched, then fetches only the rest. The CLI's progress line is written past the cleaning of mail text (which drops its \r) and says when the quota holds it up.
- Gmail web links name the account with `?authuser=<address>`: Gmail answers `/mail/u/<address>/` with a 404.
- The mouse is off unless `mouse = true` (as in clindar): while it's on, selecting text needs a modifier key. Unlike clindar, the wheel works: scrolling a list or a mail is what it's for, and a trackpad's flood of events just scrolls faster.
- Vulnerabilities are checked with `make vuln` (govulncheck v1.10.0, run with `go run`, so not a module dependency), separate from `make lint` because it needs the network.
