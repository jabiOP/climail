// Package store persists accounts, labels, messages, the outbox and sync state
// in SQLite. It is the only package that writes SQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

var ErrNotFound = errors.New("not found")

// querier is satisfied by both *sql.DB and *sql.Tx, so every method works
// the same inside or outside a transaction.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type Store struct {
	db *sql.DB // nil when this Store is bound to a transaction
	q  querier
}

// Open opens (creating if needed) the database at path and migrates it.
// WAL lets the TUI, the CLI and a sync read while one of them writes;
// _txlock=immediate takes the write lock up front, so two processes that
// read then write don't fail with SQLITE_BUSY.
func Open(path string) (*Store, error) {
	if err := private(path); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=journal_mode(WAL)" +
		"&_pragma=busy_timeout(5000)" +
		"&_pragma=synchronous(NORMAL)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := migrate(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return &Store{db: db, q: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// private makes the cache readable by the user alone, creating it so: it
// holds mail. SQLite gives its -wal and -shm files the database's mode.
func private(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	f.Close()
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("make %s private: %w", p, err)
		}
	}
	return nil
}

// InTx runs fn inside one transaction. If fn returns an error, nothing it did
// is saved. Calling InTx on a Store already inside a transaction joins it.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.db == nil {
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(&Store{q: tx}); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	for i := version; i < len(migrations); i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// PRAGMA can't take a bound parameter, hence Sprintf. i is our own int, not user input.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// migrations[i] upgrades the schema from version i to i+1.
// NEVER edit a migration that has been released: append a new one.
var migrations = []string{`
CREATE TABLE accounts (
	id    TEXT PRIMARY KEY,          -- the Google address
	added INTEGER NOT NULL           -- order accounts are shown in
);
CREATE TABLE labels (
	account_id TEXT NOT NULL,
	id         TEXT NOT NULL,
	data       TEXT NOT NULL,        -- JSON model.Label
	PRIMARY KEY (account_id, id)
);
CREATE TABLE messages (
	account_id TEXT NOT NULL,
	id         TEXT NOT NULL,
	thread_id  TEXT NOT NULL,
	ts         INTEGER NOT NULL,     -- unix milliseconds
	labels     TEXT NOT NULL,        -- ",INBOX,UNREAD,": the only copy of the labels
	search     TEXT NOT NULL,        -- lowercased text local search matches
	data       TEXT NOT NULL,        -- JSON model.Message, without Labels
	PRIMARY KEY (account_id, id)
);
CREATE INDEX messages_thread ON messages (account_id, thread_id);
CREATE INDEX messages_ts ON messages (account_id, ts);
CREATE TABLE outbox (
	account_id    TEXT NOT NULL,
	message_id    TEXT NOT NULL,
	seq           INTEGER NOT NULL,  -- push order
	version       INTEGER NOT NULL,  -- bumped by every change to the row
	add_labels    TEXT NOT NULL,     -- JSON []string
	remove_labels TEXT NOT NULL,
	attempts      INTEGER NOT NULL DEFAULT 0,
	last_error    TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (account_id, message_id)
);
CREATE TABLE sync_state (
	account_id   TEXT PRIMARY KEY,
	history_id   INTEGER NOT NULL DEFAULT 0,
	last_success INTEGER NOT NULL DEFAULT 0
);
`, `
CREATE TABLE outgoing (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	account_id TEXT NOT NULL,
	thread_id  TEXT NOT NULL DEFAULT '',  -- a reply's conversation
	summary    TEXT NOT NULL,             -- what it is, for people: "“Re: Plan” to Maya"
	raw        BLOB NOT NULL,             -- the RFC 5322 message
	attempts   INTEGER NOT NULL DEFAULT 0,
	failed     TEXT NOT NULL DEFAULT '',  -- why Gmail refused it; not retried until asked
	created    INTEGER NOT NULL
);
`, `
ALTER TABLE messages ADD COLUMN people TEXT NOT NULL DEFAULT '{}';  -- JSON {From, To, Cc}, for contacts
UPDATE messages SET people = json_object('From', json_extract(data, '$.From'), 'To', json_extract(data, '$.To'),
	'Cc', json_extract(data, '$.Cc'));
`, `
ALTER TABLE outgoing ADD COLUMN draft TEXT NOT NULL DEFAULT '';  -- message ID of the Gmail draft it replaces or sends
ALTER TABLE outgoing ADD COLUMN save INTEGER NOT NULL DEFAULT 0; -- 1: save it in Drafts instead of sending it
`, `
CREATE TABLE log (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	ts         INTEGER NOT NULL,          -- unix seconds
	kind       TEXT NOT NULL,
	account_id TEXT NOT NULL,
	message_id TEXT NOT NULL DEFAULT '',  -- the message changed, if any
	message    TEXT NOT NULL,             -- what happened, for people
	detail     TEXT NOT NULL DEFAULT ''   -- Gmail's error
);
`}

// notFound turns sql.ErrNoRows into ErrNotFound.
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
