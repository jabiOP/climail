package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/service"
	"github.com/jabiOP/climail/internal/store"
)

const acct = "me@example.com"

var ctx = context.Background()

func setup(t *testing.T, msgs ...model.Message) (*service.Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.UpsertMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	return service.New(st), st
}

func msg(id, th string, minutes int, labels ...string) model.Message {
	return model.Message{Account: acct, ID: id, ThreadID: th, Labels: labels,
		Date: time.Date(2026, 11, 20, 9, minutes, 0, 0, time.UTC)}
}

func thread(t *testing.T, svc *service.Service, id string) model.Thread {
	t.Helper()
	th, err := svc.Thread(ctx, acct, id, model.AllMail)
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func labels(t *testing.T, st *store.Store, id string) []string {
	t.Helper()
	m, err := st.Message(ctx, acct, id)
	if err != nil {
		t.Fatal(err)
	}
	return m.Labels
}

func TestApply_ChangesEveryMessage(t *testing.T) {
	svc, st := setup(t, msg("a1", "a", 0, "INBOX", "UNREAD"), msg("a2", "a", 1, "INBOX", "UNREAD"))

	if _, err := svc.Apply(ctx, acct, []model.Thread{thread(t, svc, "a")}, service.Archive); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"a1", "a2"} {
		if diff := cmp.Diff([]string{"UNREAD"}, labels(t, st, id)); diff != "" {
			t.Errorf("%s mismatch (-want +got):\n%s", id, diff)
		}
	}
}

func TestApply_StarMarksOnlyLatestMessage(t *testing.T) {
	svc, st := setup(t, msg("a1", "a", 0, "INBOX"), msg("a2", "a", 1, "INBOX"))

	svc.Apply(ctx, acct, []model.Thread{thread(t, svc, "a")}, service.Star)

	if labels(t, st, "a1")[0] != "INBOX" || len(labels(t, st, "a1")) != 1 {
		t.Errorf("a1 = %v, want unstarred", labels(t, st, "a1"))
	}
	if diff := cmp.Diff([]string{"INBOX", "STARRED"}, labels(t, st, "a2")); diff != "" {
		t.Errorf("a2 mismatch (-want +got):\n%s", diff)
	}
}

func TestUndo_RestoresExactly(t *testing.T) {
	svc, st := setup(t, msg("a1", "a", 0, "INBOX", "UNREAD"), msg("a2", "a", 1, "INBOX"))
	undo, _ := svc.Apply(ctx, acct, []model.Thread{thread(t, svc, "a")}, service.Trash)

	if err := svc.Undo(ctx, undo); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"INBOX", "UNREAD"}, labels(t, st, "a1")); diff != "" {
		t.Errorf("a1 mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"INBOX"}, labels(t, st, "a2")); diff != "" {
		t.Errorf("a2 mismatch (-want +got):\n%s", diff)
	}
	if n, _ := st.PendingCount(ctx, acct); n != 0 {
		t.Errorf("pending = %d, want undo before sync to leave nothing to send", n)
	}
}

func TestApply_NothingToChangeIsEmptyUndo(t *testing.T) {
	svc, _ := setup(t, msg("a1", "a", 0, "SENT"))

	undo, err := svc.Apply(ctx, acct, []model.Thread{thread(t, svc, "a")}, service.Archive)

	if err != nil || !undo.Empty() {
		t.Errorf("undo = %+v, err = %v; want empty", undo, err)
	}
}

func TestMailboxes_SystemFirstThenNestedUserLabels(t *testing.T) {
	svc, st := setup(t, msg("a1", "a", 0, "INBOX", "UNREAD", "Label_2"), msg("b1", "b", 1, "SENT", "UNREAD"))
	st.ReplaceLabels(ctx, acct, []model.Label{
		{ID: "INBOX", Name: "INBOX", System: true}, {ID: "CATEGORY_SOCIAL", Name: "CATEGORY_SOCIAL", System: true},
		{ID: "Label_2", Name: "work/Clients", Color: "#ff0000"}, {ID: "Label_1", Name: "Work"}, {ID: "Label_3", Name: "Bills"},
	})

	got, err := svc.Mailboxes(ctx, acct)

	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range got {
		names = append(names, b.Name)
	}
	want := []string{"Inbox", "Starred", "Sent", "Drafts", "All Mail", "Spam", "Trash", "Bills", "Work", "Clients"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("names mismatch (-want +got):\n%s", diff)
	}
	if got[0].Unread != 1 || got[2].Unread != 0 {
		t.Errorf("Inbox unread = %d, Sent unread = %d; want 1 and no count", got[0].Unread, got[2].Unread)
	}
	clients := got[9]
	if clients.Depth != 1 || clients.Unread != 1 || clients.Color != "#ff0000" || clients.Path != "work/Clients" {
		t.Errorf("Clients = %+v", clients)
	}
}

func TestResolveLabel(t *testing.T) {
	svc, st := setup(t)
	st.ReplaceLabels(ctx, acct, []model.Label{{ID: "Label_2", Name: "Work/Clients"}})

	for in, want := range map[string]string{"inbox": "INBOX", "all mail": model.AllMail, "work/clients": "Label_2", "Label_2": "Label_2"} {
		got, err := svc.ResolveLabel(ctx, acct, in)
		if err != nil || got.ID != want {
			t.Errorf("ResolveLabel(%q) = %q, %v; want %q", in, got.ID, err, want)
		}
	}
	if _, err := svc.ResolveLabel(ctx, acct, "nope"); err == nil {
		t.Error("unknown label: want an error")
	}
}

func TestSend_QueuesAndCountsAsPending(t *testing.T) {
	svc, _ := setup(t)

	err := svc.Send(ctx, compose.Draft{Account: acct, To: "bob@x.com", Subject: "Hi", Body: "x\n"}, time.Now())

	if err != nil {
		t.Fatal(err)
	}
	st, _ := svc.Status(ctx, acct)
	out, _ := svc.Outbox(ctx, acct)
	if st.Pending != 1 || len(out) != 1 || out[0].Summary != "“Hi” to bob" {
		t.Errorf("status %+v, outbox %+v", st, out)
	}
}

func TestSend_RefusesInvalidDraft(t *testing.T) {
	svc, _ := setup(t)

	if err := svc.Send(ctx, compose.Draft{Account: acct, Subject: "Hi"}, time.Now()); err == nil {
		t.Error("want an error for a draft without recipients")
	}
}
