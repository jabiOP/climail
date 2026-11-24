package syncer_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/jabiOP/climail/internal/gmail/gmailtest"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/store"
	"github.com/jabiOP/climail/internal/syncer"
)

const acct = "me@example.com"

var (
	ctx  = context.Background()
	base = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
)

type world struct {
	store  *store.Store
	fake   *gmailtest.Fake
	engine *syncer.Engine
}

func newWorld(t *testing.T) *world {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	f := gmailtest.New(acct)
	f.SetLabels(model.Label{ID: "INBOX", Name: "INBOX", System: true})
	return &world{s, f, &syncer.Engine{Store: s, Remote: f, Account: acct, Now: func() time.Time { return base }, MaxThreads: 100}}
}

func msg(id, th string, minutes int, labels ...string) model.Message {
	return model.Message{ID: id, ThreadID: th, Date: base.Add(time.Duration(minutes) * time.Minute), Subject: th, Labels: labels}
}

func (w *world) sync(t *testing.T) {
	t.Helper()
	if err := w.engine.Sync(ctx); err != nil {
		t.Fatal(err)
	}
}

// local returns the cached labels of a message, or nil if it isn't cached.
func (w *world) local(t *testing.T, id string) []string {
	t.Helper()
	m, err := w.store.Message(ctx, acct, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return m.Labels
}

func (w *world) remote(t *testing.T, id string) []string {
	t.Helper()
	m, _ := w.fake.Get(id)
	return m.Labels
}

func (w *world) pending(t *testing.T) int {
	t.Helper()
	n, err := w.store.PendingCount(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSync_FirstTimeFetchesWholeThreadsAndLabels(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"), msg("a2", "a", 1, "SENT"), msg("b1", "b", 2, "INBOX", "UNREAD"))

	w.sync(t)

	threads, _ := w.store.Threads(ctx, acct, store.View{Label: model.Inbox}, 10, 0)
	if len(threads) != 2 || len(threads[1].Messages) != 2 {
		t.Fatalf("threads = %+v, want b then a (2 messages)", threads)
	}
	labels, _ := w.store.Labels(ctx, acct)
	if len(labels) != 1 {
		t.Errorf("labels = %v", labels)
	}
	if last, _ := w.store.LastSync(ctx, acct); !last.Equal(base) {
		t.Errorf("LastSync = %v", last)
	}
}

func TestSync_FullSyncFetchesAtMostMaxThreads(t *testing.T) {
	w := newWorld(t)
	w.engine.MaxThreads = 1
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"), msg("b1", "b", 1, "INBOX"))

	w.sync(t)

	if w.local(t, "b1") == nil || w.local(t, "a1") != nil {
		t.Errorf("want only the newest thread cached")
	}
}

func TestSync_ReportsFullSyncProgress(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"), msg("b1", "b", 1, "INBOX"))
	var last [2]int
	w.engine.Progress = func(done, total int) { last = [2]int{done, total} }

	w.sync(t)

	if last != [2]int{2, 2} {
		t.Errorf("last progress = %v, want 2 of 2", last)
	}
}

func TestSync_PullsChangesSinceLastSync(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX", "UNREAD"), msg("b1", "b", 1, "INBOX"), msg("c1", "c", 2, "INBOX"))
	w.sync(t)

	w.fake.Deliver(msg("a2", "a", 3, "INBOX", "UNREAD"))
	w.fake.Relabel("b1", []string{"STARRED"}, []string{"INBOX"})
	w.fake.Delete("c1")
	w.sync(t)

	if diff := cmp.Diff([]string{"INBOX", "UNREAD"}, w.local(t, "a2")); diff != "" {
		t.Errorf("new message mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"STARRED"}, w.local(t, "b1")); diff != "" {
		t.Errorf("relabeled mismatch (-want +got):\n%s", diff)
	}
	if w.local(t, "c1") != nil {
		t.Errorf("deleted message still cached")
	}
	if n := w.fake.Calls("ThreadIDs"); n != 1 {
		t.Errorf("ThreadIDs called %d times, want only the first sync to list threads", n)
	}
}

func TestSync_ExpiredHistoryResyncsInFull(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"))
	w.sync(t)
	w.fake.Delete("a1")
	w.fake.Deliver(msg("b1", "b", 1, "INBOX"))
	w.fake.ExpireHistory()

	w.sync(t)

	if w.local(t, "a1") != nil || w.local(t, "b1") == nil {
		t.Errorf("cache not rebuilt: a1 %v, b1 %v", w.local(t, "a1"), w.local(t, "b1"))
	}
}

func TestSync_PushesLabelChanges(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX", "UNREAD"))
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, []string{"STARRED"}, []string{"INBOX", "UNREAD"})

	w.sync(t)

	if diff := cmp.Diff([]string{"STARRED"}, w.remote(t, "a1")); diff != "" {
		t.Errorf("remote mismatch (-want +got):\n%s", diff)
	}
	if n := w.pending(t); n != 0 {
		t.Errorf("pending = %d", n)
	}
}

func TestSync_TrashUsesTrashEndpoints(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"))
	w.sync(t)

	w.store.ApplyLabels(ctx, acct, []string{"a1"}, []string{"TRASH"}, []string{"INBOX"})
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, []string{"INBOX"}, []string{"TRASH"})
	w.sync(t)

	if w.fake.Calls("Trash") != 1 || w.fake.Calls("Untrash") != 1 {
		t.Errorf("Trash/Untrash calls = %d/%d", w.fake.Calls("Trash"), w.fake.Calls("Untrash"))
	}
	if diff := cmp.Diff([]string{"INBOX"}, w.remote(t, "a1")); diff != "" {
		t.Errorf("remote mismatch (-want +got):\n%s", diff)
	}
}

func TestSync_OfflineKeepsChangesQueued(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"))
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, nil, []string{"INBOX"})
	w.fake.Err = errors.New("network down")

	err := w.engine.Sync(ctx)

	if !errors.Is(err, syncer.ErrPush) {
		t.Errorf("err = %v, want ErrPush", err)
	}
	if n := w.pending(t); n != 1 {
		t.Errorf("pending = %d, want the change kept", n)
	}
	w.fake.Err = nil
	w.sync(t)
	if diff := cmp.Diff([]string{}, w.remote(t, "a1"), cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("remote mismatch after reconnecting (-want +got):\n%s", diff)
	}
}

func TestSync_ChangeToMessageDeletedOnGmailIsDropped(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"))
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, nil, []string{"INBOX"})
	w.fake.Delete("a1")

	w.sync(t)

	if n := w.pending(t); n != 0 || w.local(t, "a1") != nil {
		t.Errorf("pending = %d, cached = %v; want both gone", n, w.local(t, "a1"))
	}
}

func TestPull_KeepsChangesNotYetPushed(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX", "UNREAD"))
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, nil, []string{"UNREAD"})
	w.fake.Relabel("a1", []string{"STARRED"}, nil)

	if err := w.engine.Pull(ctx); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"INBOX", "STARRED"}, w.local(t, "a1")); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestWithLock_OnlyOneSyncAtATime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.lock")
	unlock, ok, err := syncer.TryLock(path)
	if err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}

	ran, err := syncer.WithLock(ctx, path, func(context.Context) error { return nil })
	if ran || err != nil {
		t.Errorf("WithLock while locked: ran = %v, err = %v", ran, err)
	}
	unlock()
	ran, err = syncer.WithLock(ctx, path, func(context.Context) error { return nil })
	if !ran || err != nil {
		t.Errorf("WithLock after unlock: ran = %v, err = %v", ran, err)
	}
}

func TestSync_SendsQueuedMailAndPullsItBack(t *testing.T) {
	w := newWorld(t)
	w.sync(t)
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Summary: "hi", Raw: []byte("To: bob@x.com\r\nSubject: Hi\r\n\r\nbody")})

	w.sync(t)

	if n := len(w.fake.Sent()); n != 1 {
		t.Fatalf("sent %d", n)
	}
	if waiting, failed, _ := w.store.SendCounts(ctx, acct); waiting+failed != 0 {
		t.Errorf("queue: %d waiting, %d failed", waiting, failed)
	}
	threads, _ := w.store.Threads(ctx, acct, store.View{Label: model.Sent}, 10, 0)
	if len(threads) != 1 || threads[0].Subject() != "Hi" {
		t.Errorf("Sent = %+v", threads)
	}
}

func TestSync_RefusedMailIsKeptAndDoesNotBlock(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "a", 0, "INBOX"))
	w.sync(t)
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("not a mail")})
	w.store.ApplyLabels(ctx, acct, []string{"a1"}, nil, []string{"INBOX"})

	w.sync(t)

	if waiting, failed, _ := w.store.SendCounts(ctx, acct); waiting != 0 || failed != 1 {
		t.Errorf("queue: %d waiting, %d failed; want it kept as failed", waiting, failed)
	}
	if w.pending(t) != 0 {
		t.Error("the label change behind it wasn't pushed")
	}
}

func TestSync_OfflineKeepsMailQueued(t *testing.T) {
	w := newWorld(t)
	w.sync(t)
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("To: b@x.com\r\n\r\nx")})
	w.fake.Err = errors.New("network down")

	w.engine.Sync(ctx)

	if waiting, _, _ := w.store.SendCounts(ctx, acct); waiting != 1 {
		t.Errorf("waiting = %d", waiting)
	}
}

func TestSearch_FetchesMatchesNotCached(t *testing.T) {
	w := newWorld(t)
	w.engine.MaxThreads = 1
	old := msg("a1", "a", 0, "Label_1")
	old.Body = "the quarterly invoice"
	w.fake.Deliver(old, msg("b1", "b", 5, "INBOX"), msg("c1", "c", 1, "TRASH"))
	w.sync(t) // caches only b, the newest

	ids, err := w.engine.Search(ctx, "invoice", 10)

	if err != nil || len(ids) != 1 || ids[0] != "a" {
		t.Fatalf("ids %v, err %v", ids, err)
	}
	if w.local(t, "a1") == nil {
		t.Error("the match wasn't cached")
	}
	if n := w.fake.Calls("Thread"); n != 2 {
		t.Errorf("Thread called %d times, want once more for the one match not cached", n)
	}
}

func TestSync_SendsFromTheAccountsDisplayName(t *testing.T) {
	w := newWorld(t)
	w.fake.Name = "Me Myself"
	w.sync(t)
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("From: " + acct + "\r\nTo: b@x.com\r\n\r\nx")})

	w.sync(t)

	sent := w.fake.Sent()
	if len(sent) != 1 || !strings.HasPrefix(string(sent[0]), "From: \"Me Myself\" <"+acct+">\r\nTo: b@x.com\r\n") {
		t.Errorf("sent %q", sent)
	}
}

func TestSync_SavesDraftsAndReplacesTheOneContinued(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("d1", "t1", 0, model.Draft))
	w.sync(t)

	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("To: maya\r\nSubject: Plan\r\n\r\nmore"), Draft: "d1", Save: true})
	w.sync(t)

	if w.local(t, "d1") != nil {
		t.Error("the draft continued is still there")
	}
	threads, _ := w.store.Threads(ctx, acct, store.View{Label: model.Draft}, 10, 0)
	if len(threads) != 1 || threads[0].Subject() != "Plan" || threads[0].Latest().Body != "more" {
		t.Errorf("Drafts = %+v", threads)
	}
	if out, _ := w.store.Outgoing(ctx, acct); len(out) != 0 || len(w.fake.Sent()) != 0 {
		t.Errorf("queue %v, sent %d", out, len(w.fake.Sent()))
	}
}

func TestSync_SendingADraftRemovesIt(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("d1", "t1", 0, model.Draft))
	w.sync(t)

	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("To: b@x.com\r\n\r\nx"), Draft: "d1"})
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("To: b@x.com\r\n\r\ny"), Draft: "gone"})
	w.sync(t)

	if _, ok := w.fake.Get("d1"); ok || w.local(t, "d1") != nil {
		t.Error("the draft is still there")
	}
	if n := len(w.fake.Sent()); n != 2 {
		t.Errorf("sent %d, want both (a draft already gone doesn't stop the mail)", n)
	}
}

// logged is the sync log's messages, newest first.
func (w *world) logged(t *testing.T) []string {
	t.Helper()
	entries, err := w.store.Logs(ctx, acct, 10)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Message)
	}
	return out
}

func TestSync_RejectedChangesAreLogged(t *testing.T) {
	w := newWorld(t)
	w.fake.Deliver(msg("a1", "Plan", 0, "INBOX"), msg("b1", "Lunch", 1, "INBOX"))
	w.sync(t)
	w.store.ApplyLabels(ctx, acct, []string{"a1", "b1"}, nil, []string{"INBOX"})
	w.fake.Refuse("a1")
	w.fake.Delete("b1")
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Summary: "“Hi” to Maya", Raw: []byte("not a mail")})

	w.sync(t)

	want := []string{
		"“Lunch”: no longer in Gmail; the change was dropped",
		"“Plan”: Gmail rejected the change (archive); it shows as in Gmail again",
		"“Hi” to Maya: Gmail refused to send it; it waits in the outbox",
	}
	if diff := cmp.Diff(want, w.logged(t), cmpopts.SortSlices(func(a, b string) bool { return a < b })); diff != "" {
		t.Errorf("log (-want +got):\n%s", diff)
	}
	if !slices.Contains(w.remote(t, "a1"), "INBOX") || !slices.Contains(w.local(t, "a1"), "INBOX") {
		t.Errorf("a1 local %v, remote %v; want Gmail's state back", w.local(t, "a1"), w.remote(t, "a1"))
	}
}

func TestSync_SendsFromAnAliasWithItsName(t *testing.T) {
	w := newWorld(t)
	w.fake.Name = "Me Myself"
	w.fake.Aliases = []model.SendAs{{Email: "me@domain.com", Name: "Me at work", Default: true}}
	w.sync(t)

	if got, _ := w.store.SendAs(ctx, acct); len(got) != 2 || got[0].Email != "me@domain.com" {
		t.Fatalf("cached send-as %v, want the alias first (the default) and the account", got)
	}
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("From: me@domain.com\r\nTo: b@x.com\r\n\r\nx")})
	w.store.QueueSend(ctx, store.Outgoing{Account: acct, Raw: []byte("From: stranger@y.com\r\nTo: b@x.com\r\n\r\nx")})

	w.sync(t)

	sent := w.fake.Sent()
	if len(sent) != 2 || !strings.HasPrefix(string(sent[0]), "From: \"Me at work\" <me@domain.com>\r\n") ||
		!strings.HasPrefix(string(sent[1]), "From: \"Me Myself\" <"+acct+">\r\n") {
		t.Errorf("sent %q; want the alias with its name, and an address not ours replaced by the account's", sent)
	}
}
