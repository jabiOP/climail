// Package model holds the domain types every other package shares. Gmail's
// own API types never leak past internal/gmail.
package model

import (
	"slices"
	"strings"
	"time"
)

// System label IDs. User labels have opaque IDs like "Label_12".
const (
	Inbox     = "INBOX"
	Unread    = "UNREAD"
	Starred   = "STARRED"
	Important = "IMPORTANT"
	Sent      = "SENT"
	Draft     = "DRAFT"
	Trash     = "TRASH"
	Spam      = "SPAM"
	AllMail   = "" // the pseudo-label "every message except spam and trash"
)

type Address struct {
	Name  string
	Email string
}

// String is the full form, "Alice Smith <alice@example.com>".
func (a Address) String() string {
	if a.Name == "" {
		return a.Email
	}
	return a.Name + " <" + a.Email + ">"
}

// Short is what lists show: the name, else the part of the address before @.
func (a Address) Short() string {
	if a.Name != "" {
		return a.Name
	}
	if i := strings.IndexByte(a.Email, '@'); i > 0 {
		return a.Email[:i]
	}
	return a.Email
}

type Attachment struct {
	ID       string // Gmail's attachment ID, for downloading
	Filename string
	MimeType string
	Size     int64
}

type Message struct {
	Account     string
	ID          string
	ThreadID    string
	HistoryID   uint64
	Date        time.Time
	From        Address
	To          []Address
	Cc          []Address
	Bcc         []Address // only on mail this account sent, and its drafts
	ReplyTo     []Address
	Subject     string
	Snippet     string
	Body        string   // plain text, converted from HTML when that's all there is
	HTML        string   // the HTML body, kept only for drafts: they're continued with their formatting
	Labels      []string // label IDs, sorted
	MessageID   string   // the Message-ID header, for threading replies
	InReplyTo   string
	References  string
	Attachments []Attachment
}

func (m Message) Has(label string) bool { return slices.Contains(m.Labels, label) }
func (m Message) Unread() bool          { return m.Has(Unread) }

// ApplyDelta returns labels with add added and remove removed, sorted and
// without duplicates. A label in both add and remove ends up added.
func ApplyDelta(labels, add, remove []string) []string {
	out := slices.DeleteFunc(slices.Clone(labels), func(l string) bool { return slices.Contains(remove, l) })
	out = append(out, add...)
	slices.Sort(out)
	return slices.Compact(out)
}

type Label struct {
	Account   string
	ID        string
	Name      string // user labels nest with "/": "Work/Clients"
	System    bool
	Color     string // "#rrggbb" background Gmail shows it with; "" for none
	TextColor string
}

// Thread is a conversation: its messages, oldest first.
type Thread struct {
	Account  string
	ID       string
	Messages []Message
}

func (t Thread) Latest() Message { return t.Messages[len(t.Messages)-1] }

// Subject is the first message's subject, the one the conversation started with.
func (t Thread) Subject() string { return t.Messages[0].Subject }

func (t Thread) any(f func(Message) bool) bool { return slices.ContainsFunc(t.Messages, f) }

func (t Thread) Unread() bool  { return t.any(Message.Unread) }
func (t Thread) Starred() bool { return t.any(func(m Message) bool { return m.Has(Starred) }) }
func (t Thread) HasAttachments() bool {
	return t.any(func(m Message) bool { return len(m.Attachments) > 0 })
}

// Labels is every label on any of the thread's messages, sorted.
func (t Thread) Labels() []string {
	var out []string
	for _, m := range t.Messages {
		out = append(out, m.Labels...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// MessageIDs lists the IDs of the thread's messages.
func (t Thread) MessageIDs() []string {
	ids := make([]string, len(t.Messages))
	for i, m := range t.Messages {
		ids[i] = m.ID
	}
	return ids
}

// AttachmentRef is one attachment of a conversation, and where it is: the
// index-th attachment of a message.
type AttachmentRef struct {
	MessageID string
	Index     int
	Attachment
}

// Attachments lists every attachment in the conversation, oldest message first.
func (t Thread) Attachments() []AttachmentRef {
	var out []AttachmentRef
	for _, m := range t.Messages {
		for i, a := range m.Attachments {
			out = append(out, AttachmentRef{MessageID: m.ID, Index: i, Attachment: a})
		}
	}
	return out
}

// Senders are the distinct senders in order of first appearance; the list
// shows them like Gmail does: "Alice, Bob".
func (t Thread) Senders() []Address {
	var out []Address
	for _, m := range t.Messages {
		if !slices.ContainsFunc(out, func(a Address) bool { return a.Email == m.From.Email }) {
			out = append(out, m.From)
		}
	}
	return out
}

// Changes is what happened in a mailbox after a point in its history.
type Changes struct {
	Threads   []string // threads with new messages: fetched whole
	Deleted   []string // message IDs deleted for good
	Relabeled []string // message IDs whose labels changed
}
