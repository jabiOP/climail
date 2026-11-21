package store

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jabiOP/climail/internal/model"
)

func encodeLabels(labels []string) string {
	if len(labels) == 0 {
		return ","
	}
	return "," + strings.Join(labels, ",") + ","
}

func decodeLabels(s string) []string {
	return slices.DeleteFunc(strings.Split(s, ","), func(l string) bool { return l == "" })
}

// searchText is what local search looks in, lowercased once on write.
func searchText(m model.Message) string {
	parts := []string{m.Subject, m.From.String(), m.Snippet, m.Body}
	for _, a := range slices.Concat(m.To, m.Cc) {
		parts = append(parts, a.String())
	}
	for _, a := range m.Attachments {
		parts = append(parts, a.Filename)
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// UpsertMessages stores messages, replacing any stored with the same ID.
func (s *Store) UpsertMessages(ctx context.Context, msgs []model.Message) error {
	return s.InTx(ctx, func(tx *Store) error {
		for _, m := range msgs {
			labels := m.Labels
			m.Labels = nil // the labels column is their only copy
			data, err := json.Marshal(m)
			if err != nil {
				return err
			}
			if _, err := tx.q.ExecContext(ctx, `INSERT INTO messages (account_id, id, thread_id, ts, labels, search, data)
				VALUES (?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT (account_id, id) DO UPDATE SET thread_id = excluded.thread_id, ts = excluded.ts,
					labels = excluded.labels, search = excluded.search, data = excluded.data`,
				m.Account, m.ID, m.ThreadID, m.Date.UnixMilli(), encodeLabels(labels), searchText(m), data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) DeleteMessages(ctx context.Context, account string, ids []string) error {
	return s.InTx(ctx, func(tx *Store) error {
		for _, id := range ids {
			if _, err := tx.q.ExecContext(ctx, `DELETE FROM messages WHERE account_id = ? AND id = ?`, account, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearMessages forgets every cached message of the account (before a full resync).
func (s *Store) ClearMessages(ctx context.Context, account string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM messages WHERE account_id = ?`, account)
	return err
}

// SetLabels replaces a cached message's labels. A message not in the cache is ignored.
func (s *Store) SetLabels(ctx context.Context, account, id string, labels []string) error {
	slices.Sort(labels)
	_, err := s.q.ExecContext(ctx, `UPDATE messages SET labels = ? WHERE account_id = ? AND id = ?`,
		encodeLabels(slices.Compact(labels)), account, id)
	return err
}

// HasMessage reports whether the message is cached.
func (s *Store) HasMessage(ctx context.Context, account, id string) (bool, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE account_id = ? AND id = ?`, account, id).Scan(&n)
	return n > 0, err
}

// Message returns one cached message.
func (s *Store) Message(ctx context.Context, account, id string) (model.Message, error) {
	var data []byte
	var labels string
	err := s.q.QueryRowContext(ctx, `SELECT data, labels FROM messages WHERE account_id = ? AND id = ?`, account, id).
		Scan(&data, &labels)
	if err != nil {
		return model.Message{}, notFound(err)
	}
	return decodeMessage(data, labels)
}

func decodeMessage(data []byte, labels string) (model.Message, error) {
	var m model.Message
	if err := json.Unmarshal(data, &m); err != nil {
		return model.Message{}, err
	}
	m.Labels = decodeLabels(labels)
	return m, nil
}

// View chooses which threads a list shows.
type View struct {
	Label  string // a label ID; model.AllMail for everything except spam and trash
	Search string // words, any case, that one message of each thread must all contain
}

// Anywhere, as a label, shows messages wherever they are, spam and trash included.
const Anywhere = "*"

// visible is the SQL condition for the messages a view shows. Like Gmail,
// spam and trash only appear in their own views, even inside a conversation.
func visible(label string) string {
	switch label {
	case Anywhere:
		return `1`
	case model.Trash:
		return `instr(labels, ',TRASH,') > 0`
	case model.Spam:
		return `instr(labels, ',SPAM,') > 0`
	}
	return `instr(labels, ',TRASH,') = 0 AND instr(labels, ',SPAM,') = 0`
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// Threads lists the threads in a view, most recent first. Each thread holds
// only the messages the view shows.
func (s *Store) Threads(ctx context.Context, account string, v View, limit, offset int) ([]model.Thread, error) {
	cond := visible(v.Label)
	args := []any{account}
	if v.Label != model.AllMail && v.Label != model.Trash && v.Label != model.Spam {
		cond += ` AND instr(labels, ',' || ? || ',') > 0`
		args = append(args, v.Label)
	}
	for _, w := range strings.Fields(strings.ToLower(v.Search)) {
		cond += ` AND search LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(w)+"%")
	}
	rows, err := s.q.QueryContext(ctx, `SELECT thread_id, max(ts) AS last FROM messages
		WHERE account_id = ? AND `+cond+` GROUP BY thread_id ORDER BY last DESC, thread_id LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		var last int64
		if err := rows.Scan(&id, &last); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.loadThreads(ctx, account, v.Label, ids)
}

// HasThread reports whether any message of the thread is cached.
func (s *Store) HasThread(ctx context.Context, account, id string) (bool, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE account_id = ? AND thread_id = ?`, account, id).Scan(&n)
	return n > 0, err
}

// ThreadsByID loads threads in the given order, every message included
// (spam and trash too: search results show them as found).
func (s *Store) ThreadsByID(ctx context.Context, account string, ids []string) ([]model.Thread, error) {
	return s.loadThreads(ctx, account, Anywhere, ids)
}

// Thread returns one thread with the messages the label's view shows.
func (s *Store) Thread(ctx context.Context, account, id, label string) (model.Thread, error) {
	threads, err := s.loadThreads(ctx, account, label, []string{id})
	if err != nil {
		return model.Thread{}, err
	}
	if len(threads) == 0 {
		return model.Thread{}, ErrNotFound
	}
	return threads[0], nil
}

// loadThreads loads the visible messages of the given threads, keeping their order.
func (s *Store) loadThreads(ctx context.Context, account, label string, ids []string) ([]model.Thread, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []any{account}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.q.QueryContext(ctx, `SELECT thread_id, data, labels FROM messages
		WHERE account_id = ? AND thread_id IN (?`+strings.Repeat(",?", len(ids)-1)+`) AND `+visible(label)+`
		ORDER BY ts, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string][]model.Message{}
	for rows.Next() {
		var thread, labels string
		var data []byte
		if err := rows.Scan(&thread, &data, &labels); err != nil {
			return nil, err
		}
		m, err := decodeMessage(data, labels)
		if err != nil {
			return nil, err
		}
		byID[thread] = append(byID[thread], m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []model.Thread
	for _, id := range ids {
		if msgs := byID[id]; len(msgs) > 0 {
			out = append(out, model.Thread{Account: account, ID: id, Messages: msgs})
		}
	}
	return out, nil
}

// UnreadCounts is, per label ID, how many threads have an unread message
// with that label (model.AllMail: across the account), as Gmail counts them.
func (s *Store) UnreadCounts(ctx context.Context, account string) (map[string]int, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT thread_id, labels FROM messages
		WHERE account_id = ? AND instr(labels, ',UNREAD,') > 0`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	threads := map[string]map[string]bool{} // label → thread IDs
	for rows.Next() {
		var thread, labels string
		if err := rows.Scan(&thread, &labels); err != nil {
			return nil, err
		}
		ls := decodeLabels(labels)
		hidden := slices.Contains(ls, model.Trash) || slices.Contains(ls, model.Spam)
		if !hidden {
			ls = append(ls, model.AllMail)
		}
		for _, l := range ls {
			if hidden && l != model.Trash && l != model.Spam {
				continue
			}
			if threads[l] == nil {
				threads[l] = map[string]bool{}
			}
			threads[l][thread] = true
		}
	}
	counts := map[string]int{}
	for l, ts := range threads {
		counts[l] = len(ts)
	}
	return counts, rows.Err()
}
