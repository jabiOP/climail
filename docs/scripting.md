# Scripting with climail

climail's commands print TSV when piped (`id date unread from subject messages`), JSON Lines with `--json`, and its
actions read conversation IDs from stdin with `-`, so they fit status bars, cron jobs, fzf and jq. Every command is in
[cli.md](cli.md#cli).

Reads sync first when the cache is older than `stale_after`; in scripts that run often, add `--offline` and let the
daemon keep the cache fresh (`climail daemon install`).

## Status bar: unread mail

For waybar, polybar, i3blocks, SketchyBar or tmux's `status-right`:

```bash
n=$(climail count --offline) && [ "$n" -gt 0 ] && echo "✉ $n"
```

`count` adds up the Inbox of every account; `-a work` counts one, and a label counts that label
(`climail count -a work work/clients`).

## Tell me when new mail comes

Run this every few minutes from cron, launchd or a systemd timer. It remembers the count it last saw, so it tells
you once per new mail, not on every run:

```bash
state=~/.cache/climail-unread
n=$(climail count) || exit
[ "$n" -gt "$(cat "$state" 2>/dev/null || echo 0)" ] && notify-send "climail" "$n unread"
echo "$n" > "$state"
```

On macOS, replace `notify-send "climail" "$n unread"` with
`osascript -e "display notification \"$n unread\" with title \"climail\""`.

## Archive the newsletters

```bash
climail list -q newsletter -n 100 | climail archive -
```

`-q` matches words in the cached mail. For Gmail's own search syntax, use `search`:

```bash
climail search 'from:news@example.com older_than:7d' -n 100 | climail archive -
```

`u` in the TUI doesn't undo changes made from the CLI; `climail inbox -` with the same list does.

## Pick a conversation with fzf

```bash
climail list -n 100 | fzf --delimiter='\t' --with-nth=4,5 | cut -f1 | xargs -r climail show
```

The same with `reply`, `archive` or `download` instead of `show` acts on the one picked.

## Mail from a script

`send` takes the body on stdin. `--plain` sends it as it is, which suits logs and program output:

```bash
make test 2>&1 | tail -50 | climail send --plain --to me@example.com -s "Nightly tests: $(date +%F)"
```

Without `--plain`, the body is Markdown and goes out formatted. `--attach` adds a file (repeat it for more), and
names work as recipients when they match one person in your mail (`--to maya`).

## Save every invoice's attachments

```bash
climail search 'subject:invoice has:attachment newer_than:30d' |
  cut -f1 | while read -r id; do climail download "$id" -o ~/Documents/invoices; done
```

`download` prints the path of each file it saves.

## Today's mail in a daily note

```bash
climail list --json -n 100 | jq -r --arg d "$(date +%F)" \
  'select(.date | startswith($d)) | "- [ ] \(.from): \(.subject)"' >> ~/notes/$(date +%F).md
```

## JSON

`--json` prints one conversation per line: `account`, `id`, `date` (RFC 3339), `from`, `subject`, `snippet`,
`unread`, `starred`, `messages` and `labels` (label IDs). Who writes to you most, among the unread:

```bash
climail list --json -n 200 | jq -r 'select(.unread) | .from' | sort | uniq -c | sort -rn | head
```
