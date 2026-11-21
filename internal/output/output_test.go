package output_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
)

var now = time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)

func TestShortDate(t *testing.T) {
	tests := []struct {
		in   time.Time
		want string
	}{
		{time.Date(2026, 11, 20, 9, 5, 0, 0, time.UTC), "09:05"},
		{time.Date(2026, 3, 4, 9, 5, 0, 0, time.UTC), "Mar 4"},
		{time.Date(2025, 12, 31, 9, 5, 0, 0, time.UTC), "2025-12-31"},
	}
	for _, tt := range tests {
		if got := output.ShortDate(tt.in, now); got != tt.want {
			t.Errorf("ShortDate(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int64]string{512: "512 B", 12_345: "12 KB", 3_400_000: "3.4 MB"} {
		if got := output.Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestChooseMode(t *testing.T) {
	if m, _ := output.ChooseMode(false, false, true); m != output.Pretty {
		t.Errorf("terminal: mode %v, want Pretty", m)
	}
	if m, _ := output.ChooseMode(false, false, false); m != output.TSV {
		t.Errorf("pipe: mode %v, want TSV", m)
	}
	if _, err := output.ChooseMode(true, true, true); err == nil {
		t.Error("--json --tsv: want an error")
	}
}

func sample() model.Thread {
	me := model.Address{Email: "me@x.com"}
	alice := model.Address{Name: "Alice", Email: "alice@x.com"}
	return model.Thread{Account: "me@x.com", ID: "t1", Messages: []model.Message{
		{ID: "1", From: alice, To: []model.Address{me}, Subject: "Plan\tA", Date: now.Add(-time.Hour), Body: "hi", Labels: []string{"INBOX"}},
		{ID: "2", From: me, To: []model.Address{alice}, Subject: "Re: Plan", Date: now, Body: "yo", Labels: []string{"INBOX", "UNREAD"},
			Attachments: []model.Attachment{{Filename: "a.pdf", Size: 2000}}},
	}}
}

func TestThreads_TSV(t *testing.T) {
	var b bytes.Buffer

	output.Threads(&b, output.TSV, []model.Thread{sample()}, now)

	want := "t1\t2026-11-20T15:00:00Z\t1\tAlice, me\tPlan A\t2\n"
	if b.String() != want {
		t.Errorf("got %q, want %q", b.String(), want)
	}
}

func TestThreads_JSON(t *testing.T) {
	var b bytes.Buffer

	output.Threads(&b, output.JSON, []model.Thread{sample()}, now)

	for _, want := range []string{`"id":"t1"`, `"unread":true`, `"messages":2`, `"from":"Alice, me"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("JSON %s lacks %s", b.String(), want)
		}
	}
}

func TestThreads_Pretty(t *testing.T) {
	var b bytes.Buffer

	output.Threads(&b, output.Pretty, []model.Thread{sample()}, now)

	if got := b.String(); !strings.HasPrefix(got, "● 15:00") || !strings.Contains(got, "Alice, me (2)") {
		t.Errorf("got %q", got)
	}
}

func TestConversation(t *testing.T) {
	var b bytes.Buffer

	output.Conversation(&b, sample(), now)

	for _, want := range []string{"Plan\tA\n", "── Alice <alice@x.com> · Fri 20 Nov 2026 14:00", "To: me@x.com", "Attached: a.pdf (2 KB)", "\nyo\n"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("conversation lacks %q:\n%s", want, b.String())
		}
	}
}
