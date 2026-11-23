package gmailtest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/gmail/gmailtest"
	"github.com/jabiOP/climail/internal/model"
)

var ctx = context.Background()

func send(t *testing.T, f *gmailtest.Fake, d compose.Draft, threadID string) {
	t.Helper()
	raw, err := d.Build(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Send(ctx, compose.WithFrom(raw, model.Address{Name: "Work Me", Email: f.Email}), threadID); err != nil {
		t.Fatal(err)
	}
}

// only returns the one thread a fake has, with its messages.
func only(t *testing.T, f *gmailtest.Fake) []model.Message {
	t.Helper()
	ids, _ := f.ThreadIDs(ctx, "", 10)
	if len(ids) != 1 {
		t.Fatalf("%s has threads %v", f.Email, ids)
	}
	msgs, _ := f.Thread(ctx, ids[0])
	return msgs
}

func TestConnect_DeliversBetweenAccountsInTheSameConversation(t *testing.T) {
	work, home, other := gmailtest.New("w@x.com"), gmailtest.New("h@x.com"), gmailtest.New("o@x.com")
	gmailtest.Connect(work, home)
	file := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(file, []byte("the notes"), 0o600)

	send(t, work, compose.Draft{Account: work.Email, To: "Home <h@x.com>", Subject: "Café plan", Body: "See attached.\n",
		Attach: []string{file}}, "")

	got := only(t, home)
	m := got[0]
	if m.Subject != "Café plan" || m.Body != "See attached.\n" || m.From != (model.Address{Name: "Work Me", Email: "w@x.com"}) ||
		!m.Has(model.Inbox) || !m.Unread() || len(m.Attachments) != 1 || m.Attachments[0].Filename != "notes.txt" {
		t.Fatalf("received %+v", m)
	}
	if data, _ := home.AttachmentData(ctx, m.ID, 0); string(data) != "the notes" {
		t.Errorf("attachment = %q", data)
	}
	if sent := only(t, work)[0]; sent.Body != "See attached.\n" || !sent.Has(model.Sent) || sent.MessageID != m.MessageID {
		t.Errorf("sent copy %+v", sent)
	}
	if ids, _ := other.ThreadIDs(ctx, "", 10); len(ids) != 0 {
		t.Error("delivered to an account not connected")
	}

	reply := compose.Reply(model.Thread{ID: m.ThreadID, Messages: got}, got[len(got)-1], home.Email, false, time.Now())
	reply.Body = "Sounds good.\n" + reply.Body
	send(t, home, reply, reply.ThreadID)

	if msgs := only(t, work); len(msgs) != 2 || msgs[1].Subject != "Re: Café plan" || !msgs[1].Has(model.Inbox) {
		t.Errorf("work's conversation: %+v", msgs)
	}
}
