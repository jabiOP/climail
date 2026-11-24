package store

import (
	"context"
	"errors"
	"time"
)

// HistoryID is where the next incremental sync starts (0: never synced).
func (s *Store) HistoryID(ctx context.Context, account string) (uint64, error) {
	var id uint64
	err := s.q.QueryRowContext(ctx, `SELECT history_id FROM sync_state WHERE account_id = ?`, account).Scan(&id)
	if errors.Is(notFound(err), ErrNotFound) {
		return 0, nil
	}
	return id, err
}

func (s *Store) SetHistoryID(ctx context.Context, account string, id uint64) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO sync_state (account_id, history_id) VALUES (?, ?)
		ON CONFLICT (account_id) DO UPDATE SET history_id = excluded.history_id`, account, id)
	return err
}

// LastSync returns when the account last completed a sync (zero if never).
func (s *Store) LastSync(ctx context.Context, account string) (time.Time, error) {
	var ts int64
	err := s.q.QueryRowContext(ctx, `SELECT last_success FROM sync_state WHERE account_id = ?`, account).Scan(&ts)
	if errors.Is(notFound(err), ErrNotFound) {
		return time.Time{}, nil
	}
	if err != nil || ts == 0 {
		return time.Time{}, err
	}
	return time.Unix(ts, 0), nil
}

func (s *Store) SetLastSync(ctx context.Context, account string, t time.Time) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO sync_state (account_id, last_success) VALUES (?, ?)
		ON CONFLICT (account_id) DO UPDATE SET last_success = excluded.last_success`, account, t.Unix())
	return err
}

// Log kinds.
const (
	LogError = "error" // a change or a mail Gmail rejected for good
)

// LogEntry is something sync did that the user should hear about.
type LogEntry struct {
	ID        int64
	Time      time.Time
	Kind      string
	Account   string
	MessageID string
	Message   string
	Detail    string
}

func (s *Store) AddLog(ctx context.Context, e LogEntry) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO log (ts, kind, account_id, message_id, message, detail)
		VALUES (?, ?, ?, ?, ?, ?)`, e.Time.Unix(), e.Kind, e.Account, e.MessageID, e.Message, e.Detail)
	return err
}

// Logs returns an account's entries ("": every account's), newest first.
func (s *Store) Logs(ctx context.Context, account string, limit int) ([]LogEntry, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, ts, kind, account_id, message_id, message, detail
		FROM log WHERE ? IN ('', account_id) ORDER BY id DESC LIMIT ?`, account, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogEntry
	for rows.Next() {
		var e LogEntry
		var ts int64
		if err := rows.Scan(&e.ID, &ts, &e.Kind, &e.Account, &e.MessageID, &e.Message, &e.Detail); err != nil {
			return nil, err
		}
		e.Time = time.Unix(ts, 0)
		out = append(out, e)
	}
	return out, rows.Err()
}

// CountLogsSince counts an account's entries ("": every account's) from since on.
func (s *Store) CountLogsSince(ctx context.Context, account string, since time.Time) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM log WHERE ? IN ('', account_id) AND ts >= ?`,
		account, since.Unix()).Scan(&n)
	return n, err
}
