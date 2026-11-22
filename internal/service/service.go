// Package service is what the CLI and the TUI share: reading the cache and
// changing threads, at the level a user thinks in (conversations, mailboxes).
package service

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/store"
)

type Service struct{ store *store.Store }

func New(s *store.Store) *Service { return &Service{store: s} }

func (s *Service) Accounts(ctx context.Context) ([]string, error) { return s.store.Accounts(ctx) }

func (s *Service) Threads(ctx context.Context, account string, v store.View, limit, offset int) ([]model.Thread, error) {
	return s.store.Threads(ctx, account, v, limit, offset)
}

// ThreadsByID loads threads in the given order (search results).
func (s *Service) ThreadsByID(ctx context.Context, account string, ids []string) ([]model.Thread, error) {
	return s.store.ThreadsByID(ctx, account, ids)
}

func (s *Service) Thread(ctx context.Context, account, id, label string) (model.Thread, error) {
	return s.store.Thread(ctx, account, id, label)
}

// Action is a change to conversations, made as label changes on their messages.
type Action struct {
	Done        string // what happened, for messages: "Archived"
	Add, Remove []string
	LatestOnly  bool // like Gmail, starring a conversation stars its latest message
	Leaves      bool // takes conversations out of the mailbox they're in
}

var (
	Archive     = Action{Done: "Archived", Remove: []string{model.Inbox}, Leaves: true}
	Trash       = Action{Done: "Moved to Trash", Add: []string{model.Trash}, Remove: []string{model.Inbox}, Leaves: true}
	MoveToInbox = Action{Done: "Moved to Inbox", Add: []string{model.Inbox}, Remove: []string{model.Trash, model.Spam}}
	MarkRead    = Action{Done: "Marked as read", Remove: []string{model.Unread}}
	MarkUnread  = Action{Done: "Marked as unread", Add: []string{model.Unread}}
	Star        = Action{Done: "Starred", Add: []string{model.Starred}, LatestOnly: true}
	Unstar      = Action{Done: "Unstarred", Remove: []string{model.Starred}}
)

// Undo reverses one Apply exactly, whatever the messages looked like before.
type Undo struct {
	Account string
	Done    string
	changes []store.Change
}

// Empty reports whether the Apply changed nothing (so there's nothing to undo).
func (u Undo) Empty() bool { return len(u.changes) == 0 }

// Apply changes the threads' messages locally and queues the change for Gmail.
func (s *Service) Apply(ctx context.Context, account string, threads []model.Thread, a Action) (Undo, error) {
	var ids []string
	for _, t := range threads {
		if a.LatestOnly {
			ids = append(ids, t.Latest().ID)
		} else {
			ids = append(ids, t.MessageIDs()...)
		}
	}
	changes, err := s.store.ApplyLabels(ctx, account, ids, a.Add, a.Remove)
	return Undo{Account: account, Done: a.Done, changes: changes}, err
}

// Undo puts back what an Apply changed.
func (s *Service) Undo(ctx context.Context, u Undo) error {
	return s.store.InTx(ctx, func(tx *store.Store) error {
		for _, c := range u.changes {
			if _, err := tx.ApplyLabels(ctx, u.Account, []string{c.MessageID}, c.Removed, c.Added); err != nil {
				return err
			}
		}
		return nil
	})
}

// Mailbox is one entry of the label list: a system mailbox or a user label.
type Mailbox struct {
	ID     string // label ID; model.AllMail for All Mail
	Name   string // as shown: "Inbox"; a nested label's last part ("Clients" for "Work/Clients")
	Path   string // the full name: "Work/Clients"
	Depth  int    // how deeply a user label is nested
	Unread int    // unread conversations, for mailboxes where Gmail counts them
	Color  string
}

// systemMailboxes are shown first, in Gmail's order. Other system labels
// (categories, IMPORTANT, CHAT…) are left out.
var systemMailboxes = []Mailbox{
	{ID: model.Inbox, Name: "Inbox"}, {ID: model.Starred, Name: "Starred"},
	{ID: model.Sent, Name: "Sent"}, {ID: model.Draft, Name: "Drafts"},
	{ID: model.AllMail, Name: "All Mail"}, {ID: model.Spam, Name: "Spam"}, {ID: model.Trash, Name: "Trash"},
}

// counted are the system mailboxes Gmail shows an unread count for.
var counted = []string{model.Inbox, model.Spam}

// Mailboxes lists the system mailboxes, then user labels by name.
func (s *Service) Mailboxes(ctx context.Context, account string) ([]Mailbox, error) {
	labels, err := s.store.Labels(ctx, account)
	if err != nil {
		return nil, err
	}
	unread, err := s.store.UnreadCounts(ctx, account)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(systemMailboxes)
	for i, m := range out {
		out[i].Path = m.Name
		if slices.Contains(counted, m.ID) {
			out[i].Unread = unread[m.ID]
		}
	}
	var user []Mailbox
	for _, l := range labels {
		if l.System {
			continue
		}
		parts := strings.Split(l.Name, "/")
		user = append(user, Mailbox{ID: l.ID, Name: parts[len(parts)-1], Path: l.Name, Depth: len(parts) - 1,
			Unread: unread[l.ID], Color: l.Color})
	}
	slices.SortFunc(user, func(a, b Mailbox) int {
		return cmp.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path))
	})
	return append(out, user...), nil
}

// ResolveLabel finds a mailbox by its name (any case, "Work/Clients" for
// nested labels) or label ID.
func (s *Service) ResolveLabel(ctx context.Context, account, name string) (Mailbox, error) {
	boxes, err := s.Mailboxes(ctx, account)
	if err != nil {
		return Mailbox{}, err
	}
	for _, b := range boxes {
		if strings.EqualFold(b.Path, name) || (b.ID != model.AllMail && b.ID == name) {
			return b, nil
		}
	}
	return Mailbox{}, fmt.Errorf("no label %q (see `climail labels`)", name)
}

type Status struct {
	Pending  int       // changes and mails not yet sent to Gmail
	Unsent   int       // mails Gmail refused (see the outbox)
	LastSync time.Time // zero if never
}

func (s *Service) Status(ctx context.Context, account string) (Status, error) {
	var st Status
	var err error
	if st.Pending, err = s.store.PendingCount(ctx, account); err != nil {
		return Status{}, err
	}
	waiting, unsent, err := s.store.SendCounts(ctx, account)
	if err != nil {
		return Status{}, err
	}
	st.Pending += waiting
	st.Unsent = unsent
	st.LastSync, err = s.store.LastSync(ctx, account)
	return st, err
}

// Send queues a draft; it goes with the next sync.
func (s *Service) Send(ctx context.Context, d compose.Draft, now time.Time) error {
	raw, err := d.Build(now)
	if err != nil {
		return err
	}
	return s.queue(ctx, d, raw, store.Outgoing{Summary: d.Summary(), Created: now})
}

// queue queues raw, taking the draft it continues out of the cached Drafts
// at once (Gmail's change comes with the next sync), so it can't be
// continued twice.
func (s *Service) queue(ctx context.Context, d compose.Draft, raw []byte, o store.Outgoing) error {
	o.Account, o.ThreadID, o.Raw, o.Draft = d.Account, d.ThreadID, raw, d.DraftID
	return s.store.InTx(ctx, func(tx *store.Store) error {
		if d.DraftID != "" {
			if err := tx.DeleteMessages(ctx, d.Account, []string{d.DraftID}); err != nil {
				return err
			}
		}
		_, err := tx.QueueSend(ctx, o)
		return err
	})
}

// SaveDraft queues the draft to be saved in Gmail's Drafts, replacing the
// one it continues. It may be unfinished; names that match one person
// become their address, the rest stay as typed.
func (s *Service) SaveDraft(ctx context.Context, d compose.Draft, now time.Time) error {
	if all, err := s.store.Contacts(ctx, d.Account); err == nil {
		d = compose.ResolveWhatCan(d, func(query string) []model.Address { return matching(all, query) })
	}
	raw, err := d.BuildDraft(now)
	if err != nil {
		return err
	}
	return s.queue(ctx, d, raw, store.Outgoing{Summary: "draft " + d.Summary(), Save: true, Created: now})
}

// Contacts lists the people in the account's mail whose name or address
// holds every word of query, people written to most first.
func (s *Service) Contacts(ctx context.Context, account, query string) ([]model.Address, error) {
	all, err := s.store.Contacts(ctx, account)
	return matching(all, query), err
}

func matching(people []model.Address, query string) []model.Address {
	words := strings.Fields(strings.ToLower(query))
	return slices.DeleteFunc(slices.Clone(people), func(a model.Address) bool {
		text := strings.ToLower(a.Name + " " + a.Email)
		return slices.ContainsFunc(words, func(w string) bool { return !strings.Contains(text, w) })
	})
}

// Prepare readies an edited draft for sending: recipients typed as a name
// ("maya") become the person's address, then the draft is checked.
func (s *Service) Prepare(ctx context.Context, d compose.Draft) (compose.Draft, error) {
	all, err := s.store.Contacts(ctx, d.Account)
	if err != nil {
		return d, err
	}
	resolved, err := compose.Resolve(d, func(query string) []model.Address { return matching(all, query) })
	if err != nil {
		return d, err
	}
	return resolved, resolved.Check()
}

// Outbox lists mail waiting to be sent and mail Gmail refused.
func (s *Service) Outbox(ctx context.Context, account string) ([]store.Outgoing, error) {
	return s.store.Outgoing(ctx, account)
}

// RetrySend queues a refused mail again; DiscardSend forgets it.
func (s *Service) RetrySend(ctx context.Context, account string, id int64) error {
	return s.store.RetrySend(ctx, account, id)
}

func (s *Service) DiscardSend(ctx context.Context, account string, id int64) error {
	return s.store.DeleteSend(ctx, account, id)
}
