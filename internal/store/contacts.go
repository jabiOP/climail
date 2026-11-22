package store

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jabiOP/climail/internal/model"
)

// addresses is a message's addresses, kept apart from its data so listing
// contacts doesn't read every body.
type addresses struct {
	From   model.Address
	To, Cc []model.Address
}

// Contacts lists the people in the account's cached mail: first those it
// has written to, most often first, then those it has heard from or seen
// copied. Each carries the latest name they were seen with.
func (s *Store) Contacts(ctx context.Context, account string) ([]model.Address, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT instr(labels, ',SENT,') > 0, people
		FROM messages WHERE account_id = ? ORDER BY ts`, account)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type person struct {
		model.Address
		sent, seen int
	}
	people := map[string]*person{}
	see := func(a model.Address, sent bool) {
		email := strings.ToLower(a.Email)
		if email == "" || email == strings.ToLower(account) {
			return
		}
		p := people[email]
		if p == nil {
			p = &person{Address: model.Address{Email: email}}
			people[email] = p
		}
		if a.Name != "" {
			p.Name = a.Name // rows come oldest first: the latest name wins
		}
		if sent {
			p.sent++
		} else {
			p.seen++
		}
	}
	for rows.Next() {
		var sent bool
		var data []byte
		if err := rows.Scan(&sent, &data); err != nil {
			return nil, err
		}
		var p addresses
		if err := json.Unmarshal(data, &p); err != nil {
			return nil, err
		}
		if !sent {
			see(p.From, false)
		}
		for _, a := range slices.Concat(p.To, p.Cc) {
			see(a, sent)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	list := make([]*person, 0, len(people))
	for _, p := range people {
		list = append(list, p)
	}
	slices.SortFunc(list, func(a, b *person) int {
		return cmp.Or(cmp.Compare(b.sent, a.sent), cmp.Compare(b.seen, a.seen), cmp.Compare(a.Email, b.Email))
	})
	out := make([]model.Address, len(list))
	for i, p := range list {
		out[i] = p.Address
	}
	return out, nil
}
