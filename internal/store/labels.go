package store

import (
	"context"
	"encoding/json"

	"github.com/jabiOP/climail/internal/model"
)

// ReplaceLabels makes labels the account's whole label list.
func (s *Store) ReplaceLabels(ctx context.Context, account string, labels []model.Label) error {
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM labels WHERE account_id = ?`, account); err != nil {
			return err
		}
		for _, l := range labels {
			l.Account = account
			data, err := json.Marshal(l)
			if err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO labels (account_id, id, data) VALUES (?, ?, ?)`,
				account, l.ID, data); err != nil {
				return err
			}
		}
		return nil
	})
}

// Labels lists the account's labels in no particular order.
func (s *Store) Labels(ctx context.Context, account string) ([]model.Label, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT data FROM labels WHERE account_id = ?`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Label
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var l model.Label
		if err := json.Unmarshal(data, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
