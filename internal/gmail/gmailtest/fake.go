// Package gmailtest provides Fake, an in-memory Gmail that behaves like the
// real one where the sync engine can tell: history IDs, expired history,
// 404s for unknown messages. It backs tests and the demo mailbox.
package gmailtest

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"net/mail"
	"slices"
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

	mu      sync.Mutex
	labels  []model.Label
	msgs    map[string]model.Message
	history []record
	next    uint64 // the next history ID
	oldest  uint64 // History calls starting before this get ErrHistoryExpired
	calls   map[string]int
	sent    [][]byte
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

func (f *Fake) ThreadIDs(ctx context.Context, max int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("ThreadIDs"); err != nil {
		return nil, err
	}
	latest := map[string]model.Message{}
	for _, m := range f.msgs {
		if l, ok := latest[m.ThreadID]; !hidden(m) && (!ok || m.Date.After(l.Date)) {
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

// Send delivers a sent mail to the sender's own mailbox (labelled SENT), as
// Gmail does, reading just enough of it to list it.
func (f *Fake) Send(ctx context.Context, raw []byte, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin("Send"); err != nil {
		return err
	}
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", gmail.ErrPermanent, err)
	}
	f.sent = append(f.sent, raw)
	id := fmt.Sprintf("sent-%d", len(f.sent))
	if threadID == "" {
		threadID = "thread-" + id
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	to, _ := msg.Header.AddressList("To")
	m := model.Message{Account: f.Email, ID: id, ThreadID: threadID, Subject: subject, Labels: []string{model.Sent},
		From: model.Address{Email: f.Email}, Date: time.Now()}
	for _, a := range to {
		m.To = append(m.To, model.Address{Name: a.Name, Email: a.Address})
	}
	f.msgs[id] = m
	f.record("added", m)
	return nil
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
