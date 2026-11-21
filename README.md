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

Commands (`climail COMMAND --help` for details):

```text
climail auth setup FILE | login | status | logout ACCOUNT
climail sync [--full] [-a ACCOUNT]          sync every account, or one
climail labels [-a ACCOUNT]                 mailboxes and labels, with unread counts
climail list [LABEL] [-q WORDS] [-n N]      conversations, newest first (default: Inbox)
climail show ID                             a conversation as plain text
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
sync_interval = "30s"  # how often the TUI syncs
stale_after = "2m"     # CLI reads sync first when the last sync is older
sync_timeout = "10s"   # how long a CLI command waits for that sync
sync_threads = 300     # conversations a first (or --full) sync downloads per account
```

Mail is cached in `~/.local/share/climail/`; login tokens are in the system keyring.

## Requirements

Go 1.26 or newer. Linux or macOS.

## Installation

## Development

Build:

```bash
make install   # go install .
```

Test:

```bash
make test      # go test -race ./...
```

Lint/format:

```bash
make lint      # gofmt check and go vet
```
