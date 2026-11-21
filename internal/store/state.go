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
