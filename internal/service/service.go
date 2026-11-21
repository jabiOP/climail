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

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/store"
)

type Service struct{ store *store.Store }

func New(s *store.Store) *Service { return &Service{store: s} }

func (s *Service) Accounts(ctx context.Context) ([]string, error) { return s.store.Accounts(ctx) }

func (s *Service) Threads(ctx context.Context, account string, v store.View, limit, offset int) ([]model.Thread, error) {
	return s.store.Threads(ctx, account, v, limit, offset)
}

func (s *Service) Thread(ctx context.Context, account, id, label string) (model.Thread, error) {
	return s.store.Thread(ctx, account, id, label)
}

// Action is a change to conversations, made as label changes on their messages.
type Action struct {
	Done        string // what happened, for messages: "Archived"
	Add, Remove []string
	LatestOnly  bool // like Gmail, starring a conversation stars its latest message
}

var (
	Archive     = Action{Done: "Archived", Remove: []string{model.Inbox}}
	Trash       = Action{Done: "Moved to Trash", Add: []string{model.Trash}, Remove: []string{model.Inbox}}
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
	Pending  int       // messages with changes not yet sent to Gmail
	LastSync time.Time // zero if never
}

func (s *Service) Status(ctx context.Context, account string) (Status, error) {
	var st Status
	var err error
	if st.Pending, err = s.store.PendingCount(ctx, account); err != nil {
		return Status{}, err
	}
	st.LastSync, err = s.store.LastSync(ctx, account)
	return st, err
}
