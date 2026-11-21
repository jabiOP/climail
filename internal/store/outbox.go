package store

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/jabiOP/climail/internal/model"
)

// Op is a pending change to one message's labels, waiting to be pushed.
// Trashing is the TRASH label here; the sync engine calls Gmail's trash
// endpoint for it.
type Op struct {
	Account   string
	MessageID string
	Add       []string
	Remove    []string
	Version   int64
	Attempts  int
	LastError string
}

// ApplyLabels changes the labels of the given cached messages and queues the
// change for Google, in one transaction. Only actual changes are queued:
// removing a label a message doesn't have does nothing.
func (s *Store) ApplyLabels(ctx context.Context, account string, ids, add, remove []string) error {
	return s.InTx(ctx, func(tx *Store) error {
		for _, id := range ids {
			m, err := tx.Message(ctx, account, id)
			if err == ErrNotFound {
				continue
			}
			if err != nil {
				return err
			}
			added := slices.DeleteFunc(slices.Clone(add), m.Has)
			removed := slices.DeleteFunc(slices.Clone(remove), func(l string) bool { return !m.Has(l) || slices.Contains(add, l) })
			if len(added) == 0 && len(removed) == 0 {
				continue
			}
			if err := tx.SetLabels(ctx, account, id, model.ApplyDelta(m.Labels, added, removed)); err != nil {
				return err
			}
			if err := tx.enqueue(ctx, account, id, added, removed); err != nil {
				return err
			}
		}
		return nil
	})
}

// enqueue merges an effective change (adds were absent, removes present) into
// the message's pending op. Google still has the state before the pending op,
// so adding back a label the op removes cancels that removal, and vice versa.
func (s *Store) enqueue(ctx context.Context, account, id string, add, remove []string) error {
	op, err := s.op(ctx, account, id)
	if err != nil && err != ErrNotFound {
		return err
	}
	for _, l := range add {
		if i := slices.Index(op.Remove, l); i >= 0 {
			op.Remove = slices.Delete(op.Remove, i, i+1)
		} else {
			op.Add = append(op.Add, l)
		}
	}
	for _, l := range remove {
		if i := slices.Index(op.Add, l); i >= 0 {
			op.Add = slices.Delete(op.Add, i, i+1)
		} else {
			op.Remove = append(op.Remove, l)
		}
	}
	if len(op.Add) == 0 && len(op.Remove) == 0 {
		return s.DropOp(ctx, account, id)
	}
	addJSON, _ := json.Marshal(sorted(op.Add))
	removeJSON, _ := json.Marshal(sorted(op.Remove))
	_, err = s.q.ExecContext(ctx, `INSERT INTO outbox (account_id, message_id, seq, version, add_labels, remove_labels)
		VALUES (?, ?, (SELECT coalesce(max(seq), 0) + 1 FROM outbox), 1, ?, ?)
		ON CONFLICT (account_id, message_id) DO UPDATE SET version = version + 1,
			add_labels = excluded.add_labels, remove_labels = excluded.remove_labels`,
		account, id, addJSON, removeJSON)
	return err
}

func sorted(s []string) []string {
	if s == nil {
		return []string{}
	}
	slices.Sort(s)
	return s
}

const opColumns = `account_id, message_id, version, add_labels, remove_labels, attempts, last_error`

func scanOp(scan func(...any) error) (Op, error) {
	var op Op
	var add, remove []byte
	if err := scan(&op.Account, &op.MessageID, &op.Version, &add, &remove, &op.Attempts, &op.LastError); err != nil {
		return Op{}, err
	}
	if err := json.Unmarshal(add, &op.Add); err != nil {
		return Op{}, err
	}
	return op, json.Unmarshal(remove, &op.Remove)
}

func (s *Store) op(ctx context.Context, account, id string) (Op, error) {
	row := s.q.QueryRowContext(ctx, `SELECT `+opColumns+` FROM outbox WHERE account_id = ? AND message_id = ?`, account, id)
	op, err := scanOp(row.Scan)
	return op, notFound(err)
}

// PendingOps lists the account's queued ops in the order they were first queued.
func (s *Store) PendingOps(ctx context.Context, account string) ([]Op, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+opColumns+` FROM outbox WHERE account_id = ? ORDER BY seq`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Op
	for rows.Next() {
		op, err := scanOp(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

// PendingCount is how many messages have changes not yet pushed.
func (s *Store) PendingCount(ctx context.Context, account string) (int, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM outbox WHERE account_id = ?`, account).Scan(&n)
	return n, err
}

// CompleteOp removes a pushed op, unless it changed while it was being
// pushed (its version moved on): then the newer change is still to be sent.
func (s *Store) CompleteOp(ctx context.Context, op Op) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM outbox WHERE account_id = ? AND message_id = ? AND version = ?`,
		op.Account, op.MessageID, op.Version)
	return err
}

// FailOp records a failed attempt; the op stays queued.
func (s *Store) FailOp(ctx context.Context, op Op, msg string) error {
	_, err := s.q.ExecContext(ctx, `UPDATE outbox SET attempts = attempts + 1, last_error = ?
		WHERE account_id = ? AND message_id = ?`, msg, op.Account, op.MessageID)
	return err
}

// DropOp removes a message's op whatever its version.
func (s *Store) DropOp(ctx context.Context, account, id string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM outbox WHERE account_id = ? AND message_id = ?`, account, id)
	return err
}
