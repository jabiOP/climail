// Package gmailtest provides Fake, an in-memory Gmail that behaves like the
// real one where the sync engine can tell: history IDs, expired history,
// 404s for unknown messages. It backs tests and the demo mailbox.
package gmailtest

import (
	"bytes"
	"context"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/model"
)

type record struct {
	id       uint64
	kind     string // "added", "deleted", "labels"
	message  string
	threadID string
}

type Fake struct {
	Email string
	// Err, when set, makes every call fail with it (e.g. to act offline).
	Err error
	// Name is the display name Gmail's send-as settings give.
	Name string

	mu      sync.Mutex
	labels  []model.Label
	msgs    map[string]model.Message
	history []record
	next    uint64 // the next history ID
	oldest  uint64 // History calls starting before this get ErrHistoryExpired
	calls   map[string]int
	sent    [][]byte
	peers   []*Fake           // other accounts mail is delivered to (Connect)
	files   map[string][]byte // attachment data by "messageID/index"
}

func New(email string) *Fake {
	return &Fake{Email: email, msgs: map[string]model.Message{}, next: 100, calls: map[string]int{}, files: map[string][]byte{}}
}

func (f *Fake) record(kind string, m model.Message) {
	f.next++
	f.history = append(f.history, record{id: f.next, kind: kind, message: m.ID, threadID: m.ThreadID})
}

// SetLabels sets the label list Labels returns.
func (f *Fake) SetLabels(labels ...model.Label) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.labels = labels
}

// Deliver adds messages, as if they had just arrived (or been sent).
func (f *Fake) Deliver(msgs ...model.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, m := range msgs {
		m.Account = f.Email
		f.msgs[m.ID] = m
		f.record("added", m)
	}
}

// Relabel changes a message's labels, as if from another device.
func (f *Fake) Relabel(id string, add, remove []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.msgs[id]
	m.Labels = model.ApplyDelta(m.Labels, add, remove)
	f.msgs[id] = m
	f.record("labels", m)
}

// Delete removes a message for good, as if from another device.
func (f *Fake) Delete(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("deleted", f.msgs[id])
	delete(f.msgs, id)
}

// ExpireHistory forgets the history so far, as Google does after about a week.
func (f *Fake) ExpireHistory() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.oldest = f.next + 1
	f.history = nil
}

// Get returns the fake's copy of a message, for assertions.
func (f *Fake) Get(id string) (model.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.msgs[id]
	return m, ok
}

// Calls is how many times a Remote method was called.
func (f *Fake) Calls(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

// begin counts a call; the caller holds f.mu.
func (f *Fake) begin(method string) error {
	f.calls[method]++
	return f.Err
}

func (f *Fake) Profile(ctx context.Context) (string, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Profile"); err != nil {
		return "", 0, err
	}
	return f.Email, f.next, nil
}

func (f *Fake) Labels(ctx context.Context) ([]model.Label, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Labels"); err != nil {
		return nil, err
	}
	return slices.Clone(f.labels), nil
}

func hidden(m model.Message) bool { return m.Has(model.Trash) || m.Has(model.Spam) }

// ThreadIDs matches a query like a much simpler Gmail: every word must be in
// a message's subject, body or sender, spam and trash included.
func (f *Fake) ThreadIDs(ctx context.Context, query string, max int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("ThreadIDs"); err != nil {
		return nil, err
	}
	latest := map[string]model.Message{}
	for _, m := range f.msgs {
		if query == "" && hidden(m) || !matches(m, query) {
			continue
		}
		if l, ok := latest[m.ThreadID]; !ok || m.Date.After(l.Date) {
			latest[m.ThreadID] = m
		}
	}
	var ids []string
	for id := range latest {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, func(a, b string) int { return latest[b].Date.Compare(latest[a].Date) })
	return ids[:min(max, len(ids))], nil
}

func matches(m model.Message, query string) bool {
	text := strings.ToLower(m.Subject + " " + m.Body + " " + m.From.String())
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func (f *Fake) Thread(ctx context.Context, id string) ([]model.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Thread"); err != nil {
		return nil, err
	}
	var out []model.Message
	for _, m := range f.msgs {
		if m.ThreadID == id {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		return nil, gmail.ErrNotFound
	}
	slices.SortFunc(out, func(a, b model.Message) int { return a.Date.Compare(b.Date) })
	return out, nil
}

func (f *Fake) MessageLabels(ctx context.Context, id string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("MessageLabels"); err != nil {
		return nil, err
	}
	m, ok := f.msgs[id]
	if !ok {
		return nil, gmail.ErrNotFound
	}
	return slices.Clone(m.Labels), nil
}

func (f *Fake) History(ctx context.Context, start uint64) (model.Changes, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("History"); err != nil {
		return model.Changes{}, 0, err
	}
	if start < f.oldest {
		return model.Changes{}, 0, gmail.ErrHistoryExpired
	}
	var ch model.Changes
	for _, r := range f.history {
		if r.id <= start {
			continue
		}
		switch r.kind {
		case "added":
			ch.Threads = append(ch.Threads, r.threadID)
		case "deleted":
			ch.Deleted = append(ch.Deleted, r.message)
		case "labels":
			ch.Relabeled = append(ch.Relabeled, r.message)
		}
	}
	for _, s := range []*[]string{&ch.Threads, &ch.Deleted, &ch.Relabeled} {
		slices.Sort(*s)
		*s = slices.Compact(*s)
	}
	return ch, f.next, nil
}

func (f *Fake) change(method, id string, add, remove []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(method); err != nil {
		return err
	}
	m, ok := f.msgs[id]
	if !ok {
		return gmail.ErrNotFound
	}
	m.Labels = model.ApplyDelta(m.Labels, add, remove)
	f.msgs[id] = m
	f.record("labels", m)
	return nil
}

func (f *Fake) Modify(ctx context.Context, id string, add, remove []string) error {
	return f.change("Modify", id, add, remove)
}

func (f *Fake) Trash(ctx context.Context, id string) error {
	return f.change("Trash", id, []string{model.Trash}, nil)
}

func (f *Fake) Untrash(ctx context.Context, id string) error {
	return f.change("Untrash", id, nil, []string{model.Trash})
}

// Send files a sent mail in the sender's own mailbox (labelled SENT), as
// Gmail does, and delivers it to connected fakes it is addressed to.
func (f *Fake) Send(ctx context.Context, raw []byte, threadID string) error {
	f.mu.Lock()
	if err := f.begin("Send"); err != nil {
		f.mu.Unlock()
		return err
	}
	m, files, err := parse(raw)
	if err != nil {
		f.mu.Unlock()
		return err
	}
	f.sent = append(f.sent, raw)
	m.ID = fmt.Sprintf("sent-%d", len(f.sent))
	if threadID == "" {
		threadID = "thread-" + m.ID
	}
	if m.From.Email == "" {
		m.From.Email = f.Email
	}
	m.ThreadID, m.Labels = threadID, []string{model.Sent}
	m.MessageID = fmt.Sprintf("<%s.%s@fake>", m.ID, f.Email) // Gmail gives every sent mail one
	f.add(m, files)
	var to []*Fake
	for _, p := range f.peers {
		if p.addressedIn(raw) {
			to = append(to, p)
		}
	}
	f.mu.Unlock() // before taking a peer's lock: two fakes may send to each other at once
	for _, p := range to {
		p.receive(m, files)
	}
	return nil
}

// SaveDraft keeps a draft as a message labelled DRAFT, replacing the draft
// message replaces (as Gmail gives an updated draft a new message).
func (f *Fake) SaveDraft(ctx context.Context, replaces string, raw []byte, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("SaveDraft"); err != nil {
		return err
	}
	m, files, err := parse(raw)
	if err != nil {
		return err
	}
	f.deleteDraft(replaces)
	m.ID = fmt.Sprintf("draft-%d", f.next)
	if threadID == "" {
		threadID = "thread-" + m.ID
	}
	m.ThreadID, m.Labels = threadID, []string{model.Draft}
	f.add(m, files)
	return nil
}

func (f *Fake) DeleteDraft(ctx context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("DeleteDraft"); err != nil {
		return err
	}
	f.deleteDraft(messageID)
	return nil
}

// deleteDraft removes a draft's message, if there is one. f.mu is held.
func (f *Fake) deleteDraft(id string) {
	if m, ok := f.msgs[id]; ok && m.Has(model.Draft) {
		f.record("deleted", m)
		delete(f.msgs, id)
	}
}

// Connect makes fakes deliver mail to one another, like two real accounts.
func Connect(fakes ...*Fake) {
	for _, f := range fakes {
		f.mu.Lock()
		f.peers = slices.DeleteFunc(slices.Clone(fakes), func(p *Fake) bool { return p == f })
		f.mu.Unlock()
	}
}

// addressedIn reports whether a raw mail's To, Cc or Bcc holds this account.
func (f *Fake) addressedIn(raw []byte) bool {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return false
	}
	for _, h := range []string{"To", "Cc", "Bcc"} {
		as, _ := msg.Header.AddressList(h)
		if slices.ContainsFunc(as, func(a *mail.Address) bool { return strings.EqualFold(a.Address, f.Email) }) {
			return true
		}
	}
	return false
}

// receive delivers a mail sent from another fake, unread in the inbox, in
// the conversation it replies to if this account has it.
func (f *Fake) receive(m model.Message, files [][]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m.ID = fmt.Sprintf("in-%d", f.next)
	m.ThreadID = "thread-" + m.ID
	for _, other := range f.msgs {
		if other.MessageID != "" && strings.Contains(m.InReplyTo+" "+m.References, other.MessageID) {
			m.ThreadID = other.ThreadID
			break
		}
	}
	m.Labels, m.Bcc = []string{model.Inbox, model.Unread}, nil // Bcc isn't shown to those it's sent to
	f.add(m, files)
}

// add stores a new message with its attachments' contents. f.mu is held.
func (f *Fake) add(m model.Message, files [][]byte) {
	m.Account, m.Date, m.Snippet = f.Email, time.Now(), snippet(m.Body)
	for i, data := range files {
		f.files[fmt.Sprintf("%s/%d", m.ID, i)] = data
	}
	f.msgs[m.ID] = m
	f.record("added", m)
}

func (f *Fake) SendAsName(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Name, f.begin("SendAsName")
}

// Sent lists the raw mails sent, oldest first.
func (f *Fake) Sent() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.sent)
}

// SetAttachmentData sets what downloading a message's index-th attachment
// gives. Without it, a download gives "contents of NAME".
func (f *Fake) SetAttachmentData(messageID string, index int, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[fmt.Sprintf("%s/%d", messageID, index)] = data
}

func (f *Fake) AttachmentData(ctx context.Context, messageID string, index int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("AttachmentData"); err != nil {
		return nil, err
	}
	m, ok := f.msgs[messageID]
	if !ok || index < 0 || index >= len(m.Attachments) {
		return nil, gmail.ErrNotFound
	}
	if data, ok := f.files[fmt.Sprintf("%s/%d", messageID, index)]; ok {
		return data, nil
	}
	return []byte("contents of " + m.Attachments[index].Filename), nil
}
