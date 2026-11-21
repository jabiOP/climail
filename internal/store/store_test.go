package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/store"
)

const acct = "me@example.com"

var ctx = context.Background()

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

var base = time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)

// msg builds a message in thread th, sent minutes after base.
func msg(id, th string, minutes int, labels ...string) model.Message {
	return model.Message{
		Account: acct, ID: id, ThreadID: th, Date: base.Add(time.Duration(minutes) * time.Minute),
		From: model.Address{Name: "Alice", Email: "alice@example.com"}, Subject: "Subject " + th,
		Body: "body of " + id, Labels: labels,
	}
}

func put(t *testing.T, s *store.Store, msgs ...model.Message) {
	t.Helper()
	if err := s.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
}

func threadIDs(t *testing.T, s *store.Store, v store.View) []string {
	t.Helper()
	threads, err := s.Threads(ctx, acct, v, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, th := range threads {
		ids = append(ids, th.ID)
	}
	return ids
}

func labelsOf(t *testing.T, s *store.Store, id string) []string {
	t.Helper()
	m, err := s.Message(ctx, acct, id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Labels
}

func TestMessage_RoundTrip(t *testing.T) {
	s := openStore(t)
	want := msg("m1", "t1", 0, "INBOX", "UNREAD")
	want.Attachments = []model.Attachment{{ID: "a", Filename: "x.pdf", Size: 3}}
	put(t, s, want)

	got, err := s.Message(ctx, acct, "m1")

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestMessage_NotFound(t *testing.T) {
	_, err := openStore(t).Message(ctx, acct, "nope")

	if err != store.ErrNotFound {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestThreads_InLabelNewestFirst(t *testing.T) {
	s := openStore(t)
	put(t, s,
		msg("a1", "a", 0, "INBOX"),
		msg("b1", "b", 1, "INBOX"),
		msg("a2", "a", 2, "INBOX"), // a's newest message makes it the newest thread
		msg("c1", "c", 3, "SENT"),
	)

	if diff := cmp.Diff([]string{"a", "b"}, threadIDs(t, s, store.View{Label: model.Inbox})); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"c", "a", "b"}, threadIDs(t, s, store.View{})); diff != "" {
		t.Errorf("all mail mismatch (-want +got):\n%s", diff)
	}
}

func TestThreads_HoldWholeConversationOldestFirst(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("a2", "a", 5, "SENT"), msg("a1", "a", 0, "INBOX"))

	threads, err := s.Threads(ctx, acct, store.View{Label: model.Inbox}, 10, 0)

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"a1", "a2"}, threads[0].MessageIDs()); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestThreads_TrashAndSpamOnlyInTheirOwnViews(t *testing.T) {
	s := openStore(t)
	put(t, s,
		msg("a1", "a", 0, "INBOX"), msg("a2", "a", 1, "TRASH"),
		msg("b1", "b", 2, "SPAM"),
	)

	threads, _ := s.Threads(ctx, acct, store.View{Label: model.Inbox}, 10, 0)
	if diff := cmp.Diff([]string{"a1"}, threads[0].MessageIDs()); diff != "" {
		t.Errorf("inbox shows trashed message (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a"}, threadIDs(t, s, store.View{})); diff != "" {
		t.Errorf("all mail mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a"}, threadIDs(t, s, store.View{Label: model.Trash})); diff != "" {
		t.Errorf("trash mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"b"}, threadIDs(t, s, store.View{Label: model.Spam})); diff != "" {
		t.Errorf("spam mismatch (-want +got):\n%s", diff)
	}
}

func TestThreads_LimitAndOffset(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("a1", "a", 0, "INBOX"), msg("b1", "b", 1, "INBOX"), msg("c1", "c", 2, "INBOX"))

	threads, err := s.Threads(ctx, acct, store.View{Label: model.Inbox}, 1, 1)

	if err != nil || len(threads) != 1 || threads[0].ID != "b" {
		t.Errorf("Threads = %v, %v; want just b", threads, err)
	}
}

func TestThreads_SearchMatchesAllWordsAnyCase(t *testing.T) {
	s := openStore(t)
	a := msg("a1", "a", 0, "INBOX")
	a.Subject, a.Body = "Quarterly REPORT", "numbers attached"
	b := msg("b1", "b", 1, "INBOX")
	b.Subject = "Report"
	put(t, s, a, b)

	got := threadIDs(t, s, store.View{Search: "report NUMBERS"})

	if diff := cmp.Diff([]string{"a"}, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestThreads_SearchTreatsWildcardsLiterally(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("a1", "a", 0, "INBOX"))

	if got := threadIDs(t, s, store.View{Search: "%"}); len(got) != 0 {
		t.Errorf("search %% matched %v", got)
	}
}

func TestThread_ByID(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("a1", "a", 0, "INBOX"))

	th, err := s.Thread(ctx, acct, "a", model.Inbox)
	if err != nil || th.Subject() != "Subject a" {
		t.Errorf("Thread = %v, %v", th, err)
	}
	if _, err := s.Thread(ctx, acct, "zz", model.Inbox); err != store.ErrNotFound {
		t.Errorf("missing thread: err = %v, want ErrNotFound", err)
	}
}

func TestUnreadCounts_CountThreads(t *testing.T) {
	s := openStore(t)
	put(t, s,
		msg("a1", "a", 0, "INBOX", "UNREAD"), msg("a2", "a", 1, "INBOX", "UNREAD"),
		msg("b1", "b", 2, "INBOX", "Label_1", "UNREAD"),
		msg("c1", "c", 3, "INBOX"),
		msg("d1", "d", 4, "TRASH", "UNREAD"),
	)

	got, err := s.UnreadCounts(ctx, acct)

	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"INBOX": 2, "Label_1": 1, "UNREAD": 2, model.AllMail: 2, "TRASH": 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestLabels_Replace(t *testing.T) {
	s := openStore(t)
	s.ReplaceLabels(ctx, acct, []model.Label{{ID: "old"}})

	if err := s.ReplaceLabels(ctx, acct, []model.Label{{ID: "INBOX", Name: "INBOX", System: true}}); err != nil {
		t.Fatal(err)
	}

	got, _ := s.Labels(ctx, acct)
	want := []model.Label{{Account: acct, ID: "INBOX", Name: "INBOX", System: true}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestAccounts_InOrderAdded(t *testing.T) {
	s := openStore(t)
	for _, a := range []string{"z@x.com", "a@x.com", "z@x.com"} {
		if err := s.EnsureAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	got, _ := s.Accounts(ctx)

	if diff := cmp.Diff([]string{"z@x.com", "a@x.com"}, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestDeleteAccount_RemovesItsData(t *testing.T) {
	s := openStore(t)
	s.EnsureAccount(ctx, acct)
	put(t, s, msg("a1", "a", 0, "INBOX"))
	s.ApplyLabels(ctx, acct, []string{"a1"}, nil, []string{"INBOX"})

	if err := s.DeleteAccount(ctx, acct); err != nil {
		t.Fatal(err)
	}

	accounts, _ := s.Accounts(ctx)
	n, _ := s.PendingCount(ctx, acct)
	if len(accounts) != 0 || n != 0 || len(threadIDs(t, s, store.View{})) != 0 {
		t.Errorf("left behind: accounts %v, %d ops", accounts, n)
	}
}

func TestSyncState(t *testing.T) {
	s := openStore(t)
	if id, _ := s.HistoryID(ctx, acct); id != 0 {
		t.Errorf("fresh HistoryID = %d", id)
	}

	s.SetHistoryID(ctx, acct, 42)
	s.SetLastSync(ctx, acct, base)

	id, _ := s.HistoryID(ctx, acct)
	last, _ := s.LastSync(ctx, acct)
	if id != 42 || !last.Equal(base) {
		t.Errorf("HistoryID = %d, LastSync = %v", id, last)
	}
}

func TestThreadsByID_KeepsOrderAndShowsEverything(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("a1", "a", 0, "INBOX"), msg("a2", "a", 1, "TRASH"), msg("b1", "b", 2, "SPAM"))

	threads, err := s.ThreadsByID(ctx, acct, []string{"b", "missing", "a"})

	if err != nil || len(threads) != 2 || threads[0].ID != "b" || len(threads[1].Messages) != 2 {
		t.Errorf("threads %+v, err %v", threads, err)
	}
	if ok, _ := s.HasThread(ctx, acct, "a"); !ok {
		t.Error("HasThread(a) = false")
	}
}
