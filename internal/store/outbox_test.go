package store_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/jabiOP/climail/internal/store"
)

func pending(t *testing.T, s *store.Store) []store.Op {
	t.Helper()
	ops, err := s.PendingOps(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

var ignoreBookkeeping = cmpopts.IgnoreFields(store.Op{}, "Version", "Attempts", "LastError")

func TestApplyLabels_ChangesLocallyAndQueues(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX", "UNREAD"))

	if _, err := s.ApplyLabels(ctx, acct, []string{"m1"}, []string{"STARRED"}, []string{"INBOX"}); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"STARRED", "UNREAD"}, labelsOf(t, s, "m1")); diff != "" {
		t.Errorf("labels mismatch (-want +got):\n%s", diff)
	}
	want := []store.Op{{Account: acct, MessageID: "m1", Add: []string{"STARRED"}, Remove: []string{"INBOX"}}}
	if diff := cmp.Diff(want, pending(t, s), ignoreBookkeeping); diff != "" {
		t.Errorf("ops mismatch (-want +got):\n%s", diff)
	}
}

func TestApplyLabels_QueuesOnlyActualChanges(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX"))

	s.ApplyLabels(ctx, acct, []string{"m1", "missing"}, []string{"INBOX"}, []string{"UNREAD"})

	if ops := pending(t, s); len(ops) != 0 {
		t.Errorf("queued %+v for a no-op change", ops)
	}
}

func TestApplyLabels_UndoBeforePushCancelsOut(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX", "UNREAD"))

	s.ApplyLabels(ctx, acct, []string{"m1"}, nil, []string{"INBOX", "UNREAD"})
	s.ApplyLabels(ctx, acct, []string{"m1"}, []string{"INBOX", "UNREAD"}, nil)

	if ops := pending(t, s); len(ops) != 0 {
		t.Errorf("ops = %+v, want none", ops)
	}
	if diff := cmp.Diff([]string{"INBOX", "UNREAD"}, labelsOf(t, s, "m1")); diff != "" {
		t.Errorf("labels mismatch (-want +got):\n%s", diff)
	}
}

func TestApplyLabels_MergesIntoPendingOp(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX", "UNREAD"))

	s.ApplyLabels(ctx, acct, []string{"m1"}, nil, []string{"UNREAD"})
	s.ApplyLabels(ctx, acct, []string{"m1"}, []string{"TRASH"}, []string{"INBOX"})

	want := []store.Op{{Account: acct, MessageID: "m1", Add: []string{"TRASH"}, Remove: []string{"INBOX", "UNREAD"}}}
	if diff := cmp.Diff(want, pending(t, s), ignoreBookkeeping); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteOp_KeepsOpChangedWhilePushing(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX", "UNREAD"))
	s.ApplyLabels(ctx, acct, []string{"m1"}, nil, []string{"UNREAD"})
	inFlight := pending(t, s)[0]
	s.ApplyLabels(ctx, acct, []string{"m1"}, nil, []string{"INBOX"})

	s.CompleteOp(ctx, inFlight)

	if ops := pending(t, s); len(ops) != 1 {
		t.Fatalf("ops = %+v, want the newer one kept", ops)
	}
	s.CompleteOp(ctx, pending(t, s)[0])
	if ops := pending(t, s); len(ops) != 0 {
		t.Errorf("ops = %+v, want none", ops)
	}
}

func TestFailOp_CountsAttempts(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX"))
	s.ApplyLabels(ctx, acct, []string{"m1"}, nil, []string{"INBOX"})

	s.FailOp(ctx, pending(t, s)[0], "boom")

	op := pending(t, s)[0]
	if op.Attempts != 1 || op.LastError != "boom" {
		t.Errorf("op = %+v", op)
	}
	if n, _ := s.PendingCount(ctx, acct); n != 1 {
		t.Errorf("PendingCount = %d", n)
	}
}

func TestApplyLabels_ReturnsActualChanges(t *testing.T) {
	s := openStore(t)
	put(t, s, msg("m1", "t", 0, "INBOX", "UNREAD"), msg("m2", "t", 1, "INBOX"))

	got, err := s.ApplyLabels(ctx, acct, []string{"m1", "m2"}, nil, []string{"UNREAD"})

	if err != nil {
		t.Fatal(err)
	}
	want := []store.Change{{MessageID: "m1", Added: []string{}, Removed: []string{"UNREAD"}}}
	if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}
