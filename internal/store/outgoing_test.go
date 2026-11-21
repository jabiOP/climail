package store_test

import (
	"testing"

	"github.com/jabiOP/climail/internal/store"
)

func TestOutgoing_Lifecycle(t *testing.T) {
	s := openStore(t)
	id, err := s.QueueSend(ctx, store.Outgoing{Account: acct, Summary: "“Hi” to bob", Raw: []byte("raw"), Created: base})
	if err != nil {
		t.Fatal(err)
	}

	s.SendFailed(ctx, id, "network", false)
	if w, f, _ := s.SendCounts(ctx, acct); w != 1 || f != 0 {
		t.Errorf("after a transient failure: waiting %d, failed %d", w, f)
	}
	s.SendFailed(ctx, id, "invalid To", true)
	if w, f, _ := s.SendCounts(ctx, acct); w != 0 || f != 1 {
		t.Errorf("after a refusal: waiting %d, failed %d", w, f)
	}
	out, _ := s.Outgoing(ctx, acct)
	if len(out) != 1 || out[0].Failed != "invalid To" || out[0].Attempts != 2 || string(out[0].Raw) != "raw" {
		t.Errorf("Outgoing = %+v", out)
	}

	if err := s.RetrySend(ctx, acct, id); err != nil {
		t.Fatal(err)
	}
	if w, _, _ := s.SendCounts(ctx, acct); w != 1 {
		t.Errorf("after retry: waiting %d", w)
	}
	if err := s.DeleteSend(ctx, acct, id); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSend(ctx, acct, id); err != store.ErrNotFound {
		t.Errorf("deleting twice: err = %v, want ErrNotFound", err)
	}
}
