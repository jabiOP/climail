package store

import (
	"context"
	"time"
)

// Outgoing is a mail waiting to be sent, or saved as a draft (Save), or one
// Gmail refused (Failed).
type Outgoing struct {
	ID       int64
	Account  string
	ThreadID string
	Summary  string
	Raw      []byte
	Draft    string // message ID of the Gmail draft it replaces (Save) or sends
	Save     bool
	Attempts int
	Failed   string
	Created  time.Time
}

// QueueSend adds a mail to send with the next sync.
func (s *Store) QueueSend(ctx context.Context, o Outgoing) (int64, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO outgoing (account_id, thread_id, summary, raw, draft, save, created)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, o.Account, o.ThreadID, o.Summary, o.Raw, o.Draft, o.Save, o.Created.Unix())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Outgoing lists the account's mail waiting to be sent and refused, oldest first.
func (s *Store) Outgoing(ctx context.Context, account string) ([]Outgoing, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, account_id, thread_id, summary, raw, draft, save, attempts, failed, created
		FROM outgoing WHERE account_id = ? ORDER BY id`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outgoing
	for rows.Next() {
		var o Outgoing
		var created int64
		if err := rows.Scan(&o.ID, &o.Account, &o.ThreadID, &o.Summary, &o.Raw, &o.Draft, &o.Save, &o.Attempts, &o.Failed, &created); err != nil {
			return nil, err
		}
		o.Created = time.Unix(created, 0)
		out = append(out, o)
	}
	return out, rows.Err()
}

// SendFailed records a failed attempt. A permanent failure stops retrying
// until RetrySend; a transient one is retried with the next sync.
func (s *Store) SendFailed(ctx context.Context, id int64, msg string, permanent bool) error {
	failed := ""
	if permanent {
		failed = msg
	}
	_, err := s.q.ExecContext(ctx, `UPDATE outgoing SET attempts = attempts + 1, failed = ? WHERE id = ?`, failed, id)
	return err
}

// RetrySend queues a refused mail again.
func (s *Store) RetrySend(ctx context.Context, account string, id int64) error {
	return s.changeOne(ctx, `UPDATE outgoing SET failed = '' WHERE account_id = ? AND id = ?`, account, id)
}

// DeleteSend forgets a mail: sent, or discarded by the user.
func (s *Store) DeleteSend(ctx context.Context, account string, id int64) error {
	return s.changeOne(ctx, `DELETE FROM outgoing WHERE account_id = ? AND id = ?`, account, id)
}

// changeOne runs a statement that must change exactly one row.
func (s *Store) changeOne(ctx context.Context, query string, args ...any) error {
	res, err := s.q.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		err = ErrNotFound
	}
	return err
}

// SendCounts is how many of the account's mails wait to be sent (drafts to
// save aside), and how many Gmail refused.
func (s *Store) SendCounts(ctx context.Context, account string) (waiting, failed int, err error) {
	err = s.q.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE failed = '' AND NOT save), count(*) FILTER (WHERE failed != '')
		FROM outgoing WHERE account_id = ?`, account).Scan(&waiting, &failed)
	return waiting, failed, err
}
