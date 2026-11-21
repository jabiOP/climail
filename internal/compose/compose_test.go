package compose_test

import (
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
)

const me = "me@x.com"

var now = time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)

var (
	alice = model.Address{Name: "Alice", Email: "alice@x.com"}
	bob   = model.Address{Email: "bob@x.com"}
	carol = model.Address{Name: "Carol", Email: "carol@x.com"}
	self  = model.Address{Email: me}
)

func thread(msgs ...model.Message) model.Thread {
	return model.Thread{Account: me, ID: "t1", Messages: msgs}
}

func TestReply_ToSenderQuoted(t *testing.T) {
	orig := model.Message{From: alice, To: []model.Address{self, bob}, Cc: []model.Address{carol}, Subject: "Plan",
		Body: "Line one\n> older", MessageID: "<a@x>", References: "<z@x>", Date: now.Add(-time.Hour)}

	d := compose.Reply(thread(orig), me, false, now)

	want := compose.Draft{Account: me, To: "Alice <alice@x.com>", Subject: "Re: Plan", ThreadID: "t1",
		InReplyTo: "<a@x>", References: "<z@x> <a@x>",
		Body: "\n\nOn Fri 20 Nov 14:00, Alice <alice@x.com> wrote:\n> Line one\n>> older\n"}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestReply_AllSkipsMeAndDuplicates(t *testing.T) {
	orig := model.Message{From: alice, To: []model.Address{self, bob, alice}, Cc: []model.Address{carol, bob}, Subject: "Re: Plan"}

	d := compose.Reply(thread(orig), me, true, now)

	if d.To != "Alice <alice@x.com>, bob@x.com" || d.Cc != "Carol <carol@x.com>" || d.Subject != "Re: Plan" {
		t.Errorf("To %q, Cc %q, Subject %q", d.To, d.Cc, d.Subject)
	}
}

func TestReply_SkipsMyOwnLatestMessage(t *testing.T) {
	d := compose.Reply(thread(
		model.Message{From: alice, To: []model.Address{self}, Subject: "Plan"},
		model.Message{From: self, To: []model.Address{alice}, Subject: "Re: Plan"},
	), me, false, now)

	if d.To != "Alice <alice@x.com>" {
		t.Errorf("To = %q", d.To)
	}
}

func TestReply_ToMyOwnMessageGoesToItsRecipients(t *testing.T) {
	d := compose.Reply(thread(model.Message{From: self, To: []model.Address{bob}, Subject: "Hi"}), me, false, now)

	if d.To != "bob@x.com" {
		t.Errorf("To = %q", d.To)
	}
}

func TestReply_UsesReplyTo(t *testing.T) {
	d := compose.Reply(thread(model.Message{From: alice, ReplyTo: []model.Address{bob}, Subject: "List"}), me, false, now)

	if d.To != "bob@x.com" {
		t.Errorf("To = %q", d.To)
	}
}

func TestForward(t *testing.T) {
	orig := model.Message{From: alice, To: []model.Address{self}, Subject: "Report", Body: "See attached.", Date: now}

	d := compose.Forward(thread(orig), me, []string{"/tmp/r.pdf"}, now)

	if d.Subject != "Fwd: Report" || d.To != "" || d.ThreadID != "" || len(d.Attach) != 1 {
		t.Errorf("draft = %+v", d)
	}
	if !strings.Contains(d.Body, "---------- Forwarded message ---------\nFrom: Alice <alice@x.com>") ||
		!strings.HasSuffix(d.Body, "\nSee attached.") {
		t.Errorf("body = %q", d.Body)
	}
}

func TestTextAndParse_RoundTrip(t *testing.T) {
	d := compose.Draft{Account: me, To: "a@x.com", Cc: "b@x.com", Subject: "Hi", Body: "Hello\n",
		Attach: []string{"/tmp/a", "/tmp/b"}, ThreadID: "t1", InReplyTo: "<a>", References: "<a>"}

	got, err := compose.Parse(d.Text(), compose.Draft{Account: me, ThreadID: "t1", InReplyTo: "<a>", References: "<a>"})

	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(d, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestParse_Errors(t *testing.T) {
	for _, text := range []string{"To: a@x.com\nnot a header\n\nbody", "To: a@x.com\nFrm: b\n\nbody"} {
		if _, err := compose.Parse(text, compose.Draft{}); err == nil {
			t.Errorf("Parse(%q): want an error", text)
		}
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		d    compose.Draft
		want string
	}{
		{"no recipients", compose.Draft{Subject: "x"}, "no recipients"},
		{"bad address", compose.Draft{To: "not an address"}, "To:"},
		{"missing attachment", compose.Draft{To: "a@x.com", Attach: []string{"/no/such/file"}}, "can't attach"},
	}
	for _, tt := range tests {
		if err := tt.d.Check(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
}

func TestBuild_PlainText(t *testing.T) {
	d := compose.Draft{Account: me, To: "Zoë <zoe@x.com>", Bcc: "b@x.com", Subject: "Café ☕", Body: "Ça va?\n",
		InReplyTo: "<a@x>", References: "<a@x>"}

	raw, err := d.Build(now)
	if err != nil {
		t.Fatal(err)
	}

	msg, err := mail.ReadMessage(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	to, _ := msg.Header.AddressList("To")
	if subject != "Café ☕" || to[0].Name != "Zoë" || msg.Header.Get("Bcc") != "<b@x.com>" ||
		msg.Header.Get("In-Reply-To") != "<a@x>" || msg.Header.Get("From") != me {
		t.Errorf("headers = %v (subject %q)", msg.Header, subject)
	}
	body, _ := io.ReadAll(msg.Body)
	if !strings.Contains(string(body), "=C3=87a va?") {
		t.Errorf("body not quoted-printable UTF-8: %q", body)
	}
}

func TestBuild_WithAttachment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	os.WriteFile(path, []byte("attached text"), 0o600)
	d := compose.Draft{Account: me, To: "a@x.com", Subject: "Notes", Body: "See file.\n", Attach: []string{path}}

	raw, err := d.Build(now)
	if err != nil {
		t.Fatal(err)
	}

	msg, _ := mail.ReadMessage(strings.NewReader(string(raw)))
	_, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	r := multipart.NewReader(msg.Body, params["boundary"])
	r.NextPart() // the text
	part, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(part) // multipart decodes quoted-printable, not base64
	if part.FileName() != "notes.txt" || !strings.HasPrefix(string(data), "YXR0YWNoZWQgdGV4dA==") ||
		!strings.HasPrefix(part.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("attachment %q = %q", part.FileName(), data)
	}
}

func TestSummary(t *testing.T) {
	d := compose.Draft{To: "Alice <a@x.com>, bob@x.com", Subject: "Hi", Attach: []string{"x"}}

	if got := d.Summary(); got != "“Hi” to Alice, bob (1 attached)" {
		t.Errorf("Summary = %q", got)
	}
}

func TestEditorCmd(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "code --wait")

	c := compose.EditorCmd("/tmp/d.eml")

	if diff := cmp.Diff([]string{"code", "--wait", "/tmp/d.eml"}, c.Args); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}
