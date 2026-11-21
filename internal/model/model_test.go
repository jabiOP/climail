package model_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/model"
)

func TestAddress_Short(t *testing.T) {
	tests := []struct {
		in   model.Address
		want string
	}{
		{model.Address{Name: "Alice Smith", Email: "alice@example.com"}, "Alice Smith"},
		{model.Address{Email: "bob@example.com"}, "bob"},
		{model.Address{Email: "weird"}, "weird"},
	}
	for _, tt := range tests {
		if got := tt.in.Short(); got != tt.want {
			t.Errorf("%v.Short() = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAddress_String(t *testing.T) {
	if got := (model.Address{Name: "Alice", Email: "a@x.com"}).String(); got != "Alice <a@x.com>" {
		t.Errorf("String() = %q", got)
	}
	if got := (model.Address{Email: "a@x.com"}).String(); got != "a@x.com" {
		t.Errorf("String() = %q", got)
	}
}

func TestApplyDelta(t *testing.T) {
	got := model.ApplyDelta([]string{"UNREAD", "INBOX"}, []string{"STARRED", "INBOX"}, []string{"UNREAD", "SPAM"})

	if diff := cmp.Diff([]string{"INBOX", "STARRED"}, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestApplyDelta_DoesNotModifyInput(t *testing.T) {
	in := []string{"INBOX", "UNREAD"}

	model.ApplyDelta(in, nil, []string{"INBOX"})

	if diff := cmp.Diff([]string{"INBOX", "UNREAD"}, in); diff != "" {
		t.Errorf("input changed (-want +got):\n%s", diff)
	}
}

func TestThread_Aggregates(t *testing.T) {
	alice := model.Address{Name: "Alice", Email: "alice@x.com"}
	bob := model.Address{Email: "bob@x.com"}
	th := model.Thread{Messages: []model.Message{
		{ID: "1", From: alice, Subject: "Plan", Labels: []string{"INBOX"}},
		{ID: "2", From: bob, Subject: "Re: Plan", Labels: []string{"INBOX", "UNREAD"}},
		{ID: "3", From: alice, Subject: "Re: Plan", Labels: []string{"STARRED"}, Attachments: []model.Attachment{{Filename: "a.pdf"}}},
	}}

	if !th.Unread() || !th.Starred() || !th.HasAttachments() {
		t.Errorf("Unread/Starred/HasAttachments = %v/%v/%v, want all true", th.Unread(), th.Starred(), th.HasAttachments())
	}
	if th.Subject() != "Plan" {
		t.Errorf("Subject() = %q, want the first message's", th.Subject())
	}
	if th.Latest().ID != "3" {
		t.Errorf("Latest().ID = %q", th.Latest().ID)
	}
	if diff := cmp.Diff([]string{"INBOX", "STARRED", "UNREAD"}, th.Labels()); diff != "" {
		t.Errorf("Labels mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]model.Address{alice, bob}, th.Senders()); diff != "" {
		t.Errorf("Senders mismatch (-want +got):\n%s", diff)
	}
}
