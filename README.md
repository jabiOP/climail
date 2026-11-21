# climail

## Purpose

A fast terminal client for Gmail: a keyboard-driven TUI for reading and triaging mail across several Google
accounts, and a Unix-friendly CLI for scripts. Mail is cached locally, so reading is instant and works offline;
changes sync in the background.

## Usage

First run:

1. Create your Google OAuth client (about 10 minutes, once): [docs/setup-google-cloud.md](docs/setup-google-cloud.md).
2. `climail auth setup ~/Downloads/client_secret_*.json`
3. `climail auth login` (opens your browser, then downloads your recent mail). Repeat to add more accounts.

To try it without a Google account: `climail demo` opens the TUI on a made-up mailbox with two accounts, in a
temporary folder that is removed on exit.

### TUI

`climail` opens it. On a terminal at least 120 columns wide it shows mailboxes, the conversation list and a reader
side by side (the reader previews the conversation under the cursor without marking it read); narrower, one pane at a
time. `?` lists every key.

| Keys | Action |
|---|---|
| `j`/`k`, `J`/`K`, `ctrl-d`/`ctrl-u`, `gg`/`G` | move; page; half page; top/bottom (the reader scrolls) |
| `h`/`l`, `enter`, `esc` | move between panes; open; back (also clears a selection or search) |
| `n`/`p` | next/previous conversation, from the reader |
| `a`, `dd`, `I` | archive, move to Trash, move to Inbox |
| `N`, `*` | mark read/unread, star/unstar |
| `x`, `u` | select (actions then apply to every selected conversation), undo the last change |
| `o`, `O` | expand/collapse all messages, open in Gmail on the web |
| `/` | search mail as you type (local cache); `enter` then searches all of Gmail, with its syntax (`from:maya has:attachment`); `esc` cancels |
| `gi` `gs` `gt` `gd` `ga` `g#` `g!` | go to Inbox, Starred, Sent, Drafts, All Mail, Trash, Spam |
| `c`, `r`, `R`, `f` | write a new mail, reply, reply all, forward with attachments (in your editor) |
| `A`, `D` | open an attachment (choose by number when there are several), save them all in `download_dir` |
| `[`/`]`, `b`, `s`, `q` | previous/next account, hide mailboxes, sync now, quit |

Writing mail opens `$VISUAL` (or `$EDITOR`, else `vi`) on a draft: `To:`, `Cc:`, `Bcc:`, `Subject:` and `Attach:`
lines (one file per `Attach:` line), a blank line, then the body. Replies quote the message and stay in its
conversation. When the editor closes, climail asks: `y` send, `e` edit again, `q` keep the draft (its path is shown).
An unchanged draft is dropped. Mail is queued and sent with the next sync, so writing works offline; mail Gmail refuses
stays in `climail outbox`.

### CLI

Commands (`climail COMMAND --help` for details):

```text
climail auth setup FILE | login | status | logout ACCOUNT
climail sync [--full] [-a ACCOUNT]          sync every account, or one
climail daemon [install|uninstall|restart]  sync in the background
climail labels [-a ACCOUNT]                 mailboxes and labels, with unread counts
climail list [LABEL] [-q WORDS] [-n N]      conversations, newest first (default: Inbox)
climail show ID                             a conversation as plain text
climail search QUERY... [--offline]         search all of Gmail, with Gmail's syntax
climail compose [--to A] [-s SUBJECT]       write a new mail in your editor
climail reply ID [--all] | forward ID       reply or forward in your editor
climail send --to A [-s S] [--attach F] < BODY   send without an editor (scripts)
climail outbox [retry ID | discard ID]      mail waiting to be sent, or refused by Gmail
climail attachments ID                      a conversation's attachments, numbered
climail download ID [N...] [-o DIR] [--open]   save attachments (all, or by number); prints the paths
climail archive|trash|inbox ID... | -       move conversations
climail read|unread|star|unstar ID... | -   mark conversations
```

- `-a ACCOUNT` takes an address or its start (`-a work`); the default is the first account added.
- Output is human-readable on a terminal and TSV in a pipe (`id date unread from subject messages`); `--json`
  prints JSON Lines. With `-`, actions read IDs from stdin, so `climail list -q newsletter | climail archive -` works.
- Reads sync first when the cache is older than 2 minutes; `--offline` skips that. Changes are made locally at once
  and sent to Gmail right after (or with the next sync when offline).

Configuration, all optional, in `~/.config/climail/config.toml`:

```toml
sync_interval = "30s"  # how often the TUI and the daemon sync
stale_after = "2m"     # CLI reads sync first when the last sync is older
sync_timeout = "10s"   # how long a CLI command waits for that sync
sync_threads = 300     # conversations a first (or --full) sync downloads per account
download_dir = "~/Downloads"  # where attachments are saved
```

To keep mail fresh, and changes flowing to Gmail, even when climail isn't open:

```bash
climail daemon install
```

It writes a systemd user service (Linux) or a launchd agent (macOS) and prints the command that starts it. Without
it, the TUI syncs while open and CLI commands sync when the cache is stale.

Mail is cached in `~/.local/share/climail/`; login tokens are in the system keyring.

## Requirements

Go 1.26 or newer. Linux or macOS.

## Installation

## Development

Build:

```bash
make install   # go install ., then restart the daemon (if installed) on the new version
```

Test:

```bash
make test      # go test -race ./...
```

Lint/format:

```bash
make lint      # gofmt check and go vet
```
