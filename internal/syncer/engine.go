// Package syncer moves changes between the local store and Gmail: push the
// outbox, then pull what changed since the last sync.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/store"
)

// Remote is what the engine needs from Gmail. *gmail.Client implements it for
// real; *gmailtest.Fake for tests and the demo.
type Remote interface {
	Profile(ctx context.Context) (email string, historyID uint64, err error)
	Labels(ctx context.Context) ([]model.Label, error)
	ThreadIDs(ctx context.Context, query string, max int) ([]string, error) // query "": newest, outside spam and trash
	Thread(ctx context.Context, id string) ([]model.Message, error)
	MessageLabels(ctx context.Context, id string) ([]string, error)
	History(ctx context.Context, start uint64) (model.Changes, uint64, error)
	Modify(ctx context.Context, id string, add, remove []string) error
	Trash(ctx context.Context, id string) error
	Untrash(ctx context.Context, id string) error
	Send(ctx context.Context, raw []byte, threadID string) error
	SendAs(ctx context.Context) ([]model.SendAs, error)                                // the addresses mail can go from, with their names
	SaveDraft(ctx context.Context, replaces string, raw []byte, threadID string) error // replaces: a draft's message ID, or ""
	DeleteDraft(ctx context.Context, messageID string) error                           // nil if it's already gone
	AttachmentData(ctx context.Context, messageID string, index int) ([]byte, error)
}

// ErrPush and ErrPull tell which half of Sync failed: after a pull error,
// local changes have already reached Gmail.
var (
	ErrPush = errors.New("push")
	ErrPull = errors.New("pull")
)

type Engine struct {
	Store      *store.Store
	Remote     Remote
	Account    string
	Now        func() time.Time
	MaxThreads int // how many threads a full sync fetches

	// Progress, if set, hears how a full sync's fetching goes.
	Progress func(done, total int)
}

// Sync pushes local changes, then pulls remote ones. If the push hits a
// transient error (offline), it stops there; queued changes stay queued.
func (e *Engine) Sync(ctx context.Context) error {
	if err := e.Push(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrPush, err)
	}
	if err := e.Pull(ctx); err != nil {
		return fmt.Errorf("%w: %w", ErrPull, err)
	}
	return e.Store.SetLastSync(ctx, e.Account, e.Now())
}

// logOp records a label change Gmail won't take, naming the conversation.
func (e *Engine) logOp(ctx context.Context, op store.Op, what string, cause error) error {
	subject := op.MessageID
	if m, err := e.Store.Message(ctx, e.Account, op.MessageID); err == nil {
		subject = m.Subject
	}
	return e.Store.AddLog(ctx, store.LogEntry{Time: e.Now(), Kind: store.LogError, Account: e.Account,
		MessageID: op.MessageID, Message: fmt.Sprintf("“%s”: %s", subject, what), Detail: cause.Error()})
}

// describe says what a label change does, as the TUI's keys name it.
func describe(op store.Op) string {
	names := map[string]string{
		"-" + model.Inbox: "archive", "+" + model.Inbox: "move to Inbox", "+" + model.Trash: "move to Trash",
		"-" + model.Trash: "take out of Trash", "-" + model.Unread: "mark read", "+" + model.Unread: "mark unread",
		"+" + model.Starred: "star", "-" + model.Starred: "unstar",
	}
	var out []string
	for _, d := range append(prefixed("+", op.Add), prefixed("-", op.Remove)...) {
		if name, ok := names[d]; ok {
			out = append(out, name)
		} else if d[0] == '+' {
			out = append(out, "add label "+d[1:])
		} else {
			out = append(out, "remove label "+d[1:])
		}
	}
	return strings.Join(out, ", ")
}

func prefixed(p string, ids []string) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = p + id
	}
	return out
}

// Push sends queued mail, then queued label changes. It stops at the first
// transient error.
func (e *Engine) Push(ctx context.Context) error {
	if err := e.pushSends(ctx); err != nil {
		return err
	}
	ops, err := e.Store.PendingOps(ctx, e.Account)
	if err != nil {
		return err
	}
	for _, op := range ops {
		err := e.send(ctx, op)
		switch {
		case err == nil:
			err = e.Store.CompleteOp(ctx, op)
		case errors.Is(err, gmail.ErrNotFound): // deleted on Gmail meanwhile
			lerr := e.logOp(ctx, op, "no longer in Gmail; the change was dropped", err)
			err = errors.Join(lerr, e.Store.DropOp(ctx, e.Account, op.MessageID),
				e.Store.DeleteMessages(ctx, e.Account, []string{op.MessageID}))
		case errors.Is(err, gmail.ErrPermanent): // retrying won't help: show Gmail's state again
			lerr := e.logOp(ctx, op, "Gmail rejected the change ("+describe(op)+"); it shows as in Gmail again", err)
			err = errors.Join(lerr, e.Store.DropOp(ctx, e.Account, op.MessageID))
			if labels, lerr := e.Remote.MessageLabels(ctx, op.MessageID); lerr == nil && err == nil {
				err = e.Store.SetLabels(ctx, e.Account, op.MessageID, labels)
			}
		default:
			return errors.Join(err, e.Store.FailOp(ctx, op, err.Error()))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// pushSends sends queued mail and saves queued drafts. One Gmail refuses is kept, marked failed, for
// the user to fix or discard; it never blocks the rest.
func (e *Engine) pushSends(ctx context.Context) error {
	out, err := e.Store.Outgoing(ctx, e.Account)
	if err != nil {
		return err
	}
	var sendAs []model.SendAs // asked for once, only when there's mail to send
	for _, o := range out {
		if o.Failed != "" {
			continue
		}
		if sendAs == nil {
			if err := e.pullSendAs(ctx); err != nil && !errors.Is(err, gmail.ErrPermanent) { // permanent: the cached ones
				return err
			}
			if sendAs, err = e.Store.SendAs(ctx, e.Account); err != nil {
				return err
			}
			sendAs = append(sendAs, model.SendAs{Email: e.Account}) // never nil; the account's own, if not listed
		}
		raw := compose.WithFrom(o.Raw, sender(sendAs, compose.FromAddress(o.Raw), e.Account))
		var err error
		switch {
		case o.Save:
			err = e.Remote.SaveDraft(ctx, o.Draft, raw, o.ThreadID)
		case o.Draft != "":
			// The draft goes first: if sending then fails and is retried,
			// the mail can't go twice.
			if err = e.Remote.DeleteDraft(ctx, o.Draft); err == nil {
				err = e.Remote.Send(ctx, raw, o.ThreadID)
			}
		default:
			err = e.Remote.Send(ctx, raw, o.ThreadID)
		}
		switch {
		case err == nil:
			err = e.Store.DeleteSend(ctx, e.Account, o.ID)
		case errors.Is(err, gmail.ErrPermanent), errors.Is(err, gmail.ErrNotFound):
			what := "Gmail refused to send it"
			if o.Save {
				what = "Gmail refused to save it in Drafts"
			}
			err = errors.Join(e.Store.AddLog(ctx, store.LogEntry{Time: e.Now(), Kind: store.LogError, Account: e.Account,
				Message: o.Summary + ": " + what + "; it waits in the outbox", Detail: err.Error()}),
				e.Store.SendFailed(ctx, o.ID, err.Error(), true))
		default:
			return errors.Join(err, e.Store.SendFailed(ctx, o.ID, err.Error(), false))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// send applies one op. Gmail won't add or remove TRASH by label: it has its
// own endpoints.
func (e *Engine) send(ctx context.Context, op store.Op) error {
	add, remove := op.Add, op.Remove
	if slices.Contains(add, model.Trash) {
		if err := e.Remote.Trash(ctx, op.MessageID); err != nil {
			return err
		}
		add = without(add, model.Trash)
	}
	if slices.Contains(remove, model.Trash) {
		if err := e.Remote.Untrash(ctx, op.MessageID); err != nil {
			return err
		}
		remove = without(remove, model.Trash)
	}
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	return e.Remote.Modify(ctx, op.MessageID, add, remove)
}

func without(s []string, x string) []string {
	return slices.DeleteFunc(slices.Clone(s), func(v string) bool { return v == x })
}

// Pull brings the cache up to date: incrementally from the last history ID,
// or in full the first time and when Gmail no longer has that history.
func (e *Engine) Pull(ctx context.Context) error {
	start, err := e.Store.HistoryID(ctx, e.Account)
	if err != nil {
		return err
	}
	if start == 0 {
		return e.fullPull(ctx)
	}
	changes, latest, err := e.Remote.History(ctx, start)
	if errors.Is(err, gmail.ErrHistoryExpired) {
		return e.fullPull(ctx)
	}
	if err != nil {
		return err
	}
	if err := e.pullLabels(ctx); err != nil {
		return err
	}
	if err := e.pullSendAs(ctx); err != nil && !errors.Is(err, gmail.ErrPermanent) {
		return err
	}
	fetched, err := e.fetchThreads(ctx, changes.Threads)
	if err != nil {
		return err
	}
	if err := e.save(ctx, fetched); err != nil {
		return err
	}
	if err := e.Store.DeleteMessages(ctx, e.Account, changes.Deleted); err != nil {
		return err
	}
	for _, id := range changes.Relabeled {
		if err := e.relabel(ctx, id, fetched); err != nil {
			return err
		}
	}
	return e.Store.SetHistoryID(ctx, e.Account, latest)
}

// relabel refreshes the labels of a cached message changed elsewhere.
func (e *Engine) relabel(ctx context.Context, id string, fetched []model.Message) error {
	if slices.ContainsFunc(fetched, func(m model.Message) bool { return m.ID == id }) {
		return nil // just stored with its current labels
	}
	cached, err := e.Store.HasMessage(ctx, e.Account, id)
	if err != nil || !cached {
		return err
	}
	labels, err := e.Remote.MessageLabels(ctx, id)
	if errors.Is(err, gmail.ErrNotFound) {
		return e.Store.DeleteMessages(ctx, e.Account, []string{id})
	}
	if err != nil {
		return err
	}
	msgs, err := e.keepPending(ctx, []model.Message{{ID: id, Labels: labels}})
	if err != nil {
		return err
	}
	return e.Store.SetLabels(ctx, e.Account, id, msgs[0].Labels)
}

func (e *Engine) fullPull(ctx context.Context) error {
	// The history ID comes first, so changes made while we fetch are pulled next time.
	_, historyID, err := e.Remote.Profile(ctx)
	if err != nil {
		return err
	}
	if err := e.pullLabels(ctx); err != nil {
		return err
	}
	if err := e.pullSendAs(ctx); err != nil && !errors.Is(err, gmail.ErrPermanent) { // permanent: e.g. no settings scope
		return err
	}
	ids, err := e.Remote.ThreadIDs(ctx, "", e.MaxThreads)
	if err != nil {
		return err
	}
	// A first sync cut short (Gmail's rate limit, the TUI quit) kept what it
	// fetched: fetch only the rest. (Resyncing an expired history refetches all.)
	todo := ids
	if start, err := e.Store.HistoryID(ctx, e.Account); err != nil {
		return err
	} else if start == 0 {
		todo = nil
		for _, id := range ids {
			if cached, err := e.Store.HasThread(ctx, e.Account, id); err != nil {
				return err
			} else if !cached {
				todo = append(todo, id)
			}
		}
	}
	if skipped, progress := len(ids)-len(todo), e.Progress; skipped > 0 && progress != nil {
		e.Progress = func(done, total int) { progress(done+skipped, total+skipped) }
		defer func() { e.Progress = progress }()
	}
	msgs, fetchErr := e.fetchThreads(ctx, todo)
	// Keep what was fetched even when the rest failed or the sync was cancelled.
	if err := e.save(context.WithoutCancel(ctx), msgs); err != nil || fetchErr != nil {
		return errors.Join(fetchErr, err)
	}
	return e.Store.InTx(ctx, func(tx *store.Store) error {
		if err := tx.KeepThreads(ctx, e.Account, ids); err != nil { // gone from Gmail, or no longer among the newest
			return err
		}
		return tx.SetHistoryID(ctx, e.Account, historyID)
	})
}

// pullSendAs caches the addresses the account can send from.
func (e *Engine) pullSendAs(ctx context.Context) error {
	list, err := e.Remote.SendAs(ctx)
	if err != nil {
		return err
	}
	return e.Store.ReplaceSendAs(ctx, e.Account, list)
}

// sender is who a mail goes from: the address its From names, when the
// account can send from it, else the account's own; with its display name.
func sender(sendAs []model.SendAs, from, account string) model.Address {
	for _, want := range []string{from, account} {
		for _, a := range sendAs {
			if strings.EqualFold(a.Email, want) {
				return model.Address{Name: a.Name, Email: a.Email}
			}
		}
	}
	return model.Address{Email: account}
}

func (e *Engine) pullLabels(ctx context.Context) error {
	labels, err := e.Remote.Labels(ctx)
	if err != nil {
		return err
	}
	return e.Store.ReplaceLabels(ctx, e.Account, labels)
}

// save stores fetched messages, keeping local changes not yet pushed.
func (e *Engine) save(ctx context.Context, msgs []model.Message) error {
	msgs, err := e.keepPending(ctx, msgs)
	if err != nil {
		return err
	}
	for i := range msgs {
		msgs[i].Account = e.Account
	}
	return e.Store.UpsertMessages(ctx, msgs)
}

// keepPending reapplies queued label changes over labels fetched from
// Gmail: local changes win until they are pushed.
func (e *Engine) keepPending(ctx context.Context, msgs []model.Message) ([]model.Message, error) {
	ops, err := e.Store.PendingOps(ctx, e.Account)
	if err != nil || len(ops) == 0 {
		return msgs, err
	}
	byID := map[string]store.Op{}
	for _, op := range ops {
		byID[op.MessageID] = op
	}
	for i, m := range msgs {
		if op, ok := byID[m.ID]; ok {
			msgs[i].Labels = model.ApplyDelta(m.Labels, op.Add, op.Remove)
		}
	}
	return msgs, nil
}

// Search asks Gmail for up to max conversations matching query (Gmail's
// search syntax), fetches the ones not cached yet, and returns their IDs in
// Gmail's order.
func (e *Engine) Search(ctx context.Context, query string, max int) ([]string, error) {
	ids, err := e.Remote.ThreadIDs(ctx, query, max)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, id := range ids {
		cached, err := e.Store.HasThread(ctx, e.Account, id)
		if err != nil {
			return nil, err
		}
		if !cached {
			missing = append(missing, id)
		}
	}
	msgs, err := e.fetchThreads(ctx, missing)
	if err != nil {
		return nil, err
	}
	return ids, e.save(ctx, msgs)
}

// fetchWorkers is how many threads are fetched at once: fast, and well
// within Gmail's per-user rate limit.
const fetchWorkers = 8

// fetchThreads fetches whole threads in parallel. A thread deleted meanwhile is skipped.
func (e *Engine) fetchThreads(ctx context.Context, ids []string) ([]model.Message, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu    sync.Mutex
		out   []model.Message
		first error
		done  int
		wg    sync.WaitGroup
	)
	jobs := make(chan string)
	for range min(fetchWorkers, len(ids)) {
		wg.Go(func() {
			for id := range jobs {
				msgs, err := e.Remote.Thread(ctx, id)
				mu.Lock()
				switch {
				case err == nil:
					out = append(out, msgs...)
				case errors.Is(err, gmail.ErrNotFound):
				case first == nil:
					first = err
					cancel()
				}
				done++
				if e.Progress != nil {
					e.Progress(done, len(ids))
				}
				mu.Unlock()
			}
		})
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	return out, first
}
