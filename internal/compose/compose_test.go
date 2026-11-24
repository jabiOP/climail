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

	d := reply(thread(orig), me, false, now)

	want := compose.Draft{Account: me, To: "Alice <alice@x.com>", Subject: "Re: Plan", ThreadID: "t1",
		InReplyTo: "<a@x>", References: "<z@x> <a@x>",
		Body: "\n\nOn Fri 20 Nov 14:00, Alice <alice@x.com> wrote:\n> Line one\n>> older\n"}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestReply_AllSkipsMeAndDuplicates(t *testing.T) {
	orig := model.Message{From: alice, To: []model.Address{self, bob, alice}, Cc: []model.Address{carol, bob}, Subject: "Re: Plan"}

	d := reply(thread(orig), me, true, now)

	if d.To != "Alice <alice@x.com>, bob@x.com" || d.Cc != "Carol <carol@x.com>" || d.Subject != "Re: Plan" {
		t.Errorf("To %q, Cc %q, Subject %q", d.To, d.Cc, d.Subject)
	}
}

func TestReply_SkipsMyOwnLatestMessage(t *testing.T) {
	d := reply(thread(
		model.Message{From: alice, To: []model.Address{self}, Subject: "Plan"},
		model.Message{From: self, To: []model.Address{alice}, Subject: "Re: Plan"},
	), me, false, now)

	if d.To != "Alice <alice@x.com>" {
		t.Errorf("To = %q", d.To)
	}
}

func TestReply_ToMyOwnMessageGoesToItsRecipients(t *testing.T) {
	d := reply(thread(model.Message{From: self, To: []model.Address{bob}, Subject: "Hi"}), me, false, now)

	if d.To != "bob@x.com" {
		t.Errorf("To = %q", d.To)
	}
}

func TestReply_UsesReplyTo(t *testing.T) {
	d := reply(thread(model.Message{From: alice, ReplyTo: []model.Address{bob}, Subject: "List"}), me, false, now)

	if d.To != "bob@x.com" {
		t.Errorf("To = %q", d.To)
	}
}

func TestForward(t *testing.T) {
	orig := model.Message{From: alice, To: []model.Address{self}, Subject: "Report", Body: "See attached.", Date: now}

	d := compose.Forward(orig, me, []string{"/tmp/r.pdf"}, now)

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

func TestText_SaysWhoItIsFrom(t *testing.T) {
	d := compose.Draft{Account: me, To: "a@x.com"}

	if !strings.HasPrefix(d.Text(), "From: "+me+"\nTo: a@x.com\n") {
		t.Errorf("Text() = %q, want From first", d.Text())
	}
	if alias := (compose.Draft{Account: me, From: "me@domain.com"}); !strings.HasPrefix(alias.Text(), "From: me@domain.com\n") {
		t.Errorf("Text() = %q, want the address it goes from", alias.Text())
	}
	// What From names is resolved later (an account, or an address one sends from).
	got, err := compose.Parse(strings.Replace(d.Text(), me, "other", 1), d)
	if err != nil || got.Account != me || got.From != "other" {
		t.Errorf("From edited: Account %q, From %q, %v", got.Account, got.From, err)
	}
	for _, text := range []string{strings.Replace(d.Text(), "From: "+me+"\n", "", 1), strings.Replace(d.Text(), me, "ME@x.com", 1)} {
		if got, err := compose.Parse(text, d); err != nil || got.Account != me || got.From != "" {
			t.Errorf("no From, or the account's own: Account %q, From %q, %v", got.Account, got.From, err)
		}
	}
}

func TestReply_FromTheAddressItWasSentTo(t *testing.T) {
	work := model.Address{Email: "me@domain.com"}
	toWork := model.Message{From: alice, To: []model.Address{work, bob}, Subject: "Plan"}
	toMe := model.Message{From: alice, To: []model.Address{self, bob}, Subject: "Plan"}

	d := compose.Reply(thread(toWork), toWork, me, true, now, "me@domain.com", me)
	if d.From != "me@domain.com" || d.To != "Alice <alice@x.com>, bob@x.com" {
		t.Errorf("From %q, To %q; want the alias it came to, and it left out of To", d.From, d.To)
	}
	if d := compose.Reply(thread(toMe), toMe, me, false, now, "me@domain.com", me); d.From != "" {
		t.Errorf("From %q; want the account's own, the address it came to", d.From)
	}
	other := model.Message{From: alice, To: []model.Address{bob}, Subject: "Plan"} // e.g. Bcc'd
	if d := compose.Reply(thread(other), other, me, false, now, "me@domain.com", me); d.From != "me@domain.com" {
		t.Errorf("From %q; want the default", d.From)
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

	c := compose.EditorCmd("/tmp/d.eml", 0)

	if diff := cmp.Diff([]string{"code", "--wait", "/tmp/d.eml"}, c.Args); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestWithFrom_ReplacesOrAddsTheSender(t *testing.T) {
	me := model.Address{Name: "José Díaz", Email: "me@example.com"}
	cases := map[string]string{
		"From: me@example.com\r\nTo: b@x.com\r\n\r\nhi": "From: =?utf-8?q?Jos=C3=A9_D=C3=ADaz?= <me@example.com>\r\nTo: b@x.com\r\n\r\nhi",
		"To: b@x.com\r\n\r\nhi":                         "From: =?utf-8?q?Jos=C3=A9_D=C3=ADaz?= <me@example.com>\r\nTo: b@x.com\r\n\r\nhi",
	}
	for raw, want := range cases {
		if got := string(compose.WithFrom([]byte(raw), me)); got != want {
			t.Errorf("WithFrom(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := string(compose.WithFrom([]byte("From: me@example.com\r\n\r\nhi"), model.Address{Email: "me@example.com"})); got != "From: me@example.com\r\n\r\nhi" {
		t.Errorf("without a name: %q", got)
	}
}

func TestResolve_TurnsShortNamesIntoAddresses(t *testing.T) {
	people := []model.Address{
		{Name: "Maya Chen", Email: "maya@work.example"},
		{Name: "Tom Okafor", Email: "tom@work.example"},
		{Name: "Tomás Ruiz", Email: "truiz@home.example"},
		{Name: "Chen, Wei", Email: "wei@x.example"},
	}
	lookup := func(q string) []model.Address {
		var out []model.Address
		for _, p := range people {
			if strings.Contains(strings.ToLower(p.Name+" "+p.Email), strings.ToLower(q)) {
				out = append(out, p)
			}
		}
		return out
	}
	d, err := compose.Resolve(compose.Draft{To: "maya, bob@x.com", Cc: "Tom", Bcc: "wei"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := compose.Draft{To: "Maya Chen <maya@work.example>, bob@x.com", Cc: "Tom Okafor <tom@work.example>",
		Bcc: `"Chen, Wei" <wei@x.example>`}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if err := d.Check(); err != nil {
		t.Errorf("resolved draft doesn't check: %v", err)
	}

	_, err = compose.Resolve(compose.Draft{To: "o"}, lookup)
	if err == nil || !strings.Contains(err.Error(), "Maya Chen <maya@work.example>") {
		t.Errorf("ambiguous: %v", err)
	}
	_, err = compose.Resolve(compose.Draft{Cc: "nobody"}, lookup)
	if err == nil || !strings.Contains(err.Error(), `Cc: no one matches "nobody"`) {
		t.Errorf("unknown: %v", err)
	}
	// A quoted name with a comma is one recipient, left alone.
	d, err = compose.Resolve(compose.Draft{To: `"Chen, Wei" <wei@x.example>`}, lookup)
	if err != nil || d.To != `"Chen, Wei" <wei@x.example>` {
		t.Errorf("quoted: %q, %v", d.To, err)
	}
}

func TestFromDraft_ContinuesAGmailDraft(t *testing.T) {
	reply := model.Message{ID: "d1", ThreadID: "t1", Subject: "Re: Plan", Body: "Half written\n",
		To: []model.Address{{Name: "Maya", Email: "maya@x.com"}}, Bcc: []model.Address{{Email: "boss@x.com"}},
		InReplyTo: "<m1@x>", References: "<m0@x> <m1@x>", Labels: []string{model.Draft}}

	d := compose.FromDraft(reply, me, []string{"/tmp/a.pdf"})

	want := compose.Draft{Account: me, To: "Maya <maya@x.com>", Bcc: "boss@x.com", Subject: "Re: Plan", Body: "Half written\n",
		Attach: []string{"/tmp/a.pdf"}, DraftID: "d1", ThreadID: "t1", InReplyTo: "<m1@x>", References: "<m0@x> <m1@x>"}
	if diff := cmp.Diff(want, d); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
	if back, err := compose.Parse(d.Text(), d); err != nil || !cmp.Equal(back, d) {
		t.Errorf("Parse(Text()) = %+v, %v", back, err)
	}

	// A new mail's draft is in a conversation of its own: sent, it starts one.
	if d := compose.FromDraft(model.Message{ID: "d2", ThreadID: "t2"}, me, nil); d.ThreadID != "" {
		t.Errorf("ThreadID = %q", d.ThreadID)
	}
}

func TestBuildDraft_TakesUnfinishedRecipients(t *testing.T) {
	raw, err := compose.Draft{Account: me, To: "maya, bob@x.com", Body: "todo\n"}.BuildDraft(now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\r\nTo: maya, bob@x.com\r\n") {
		t.Errorf("raw:\n%s", raw)
	}
	if _, err := (compose.Draft{Account: me, Attach: []string{"/no/such/file"}}).BuildDraft(now); err == nil {
		t.Error("a missing attachment: want an error")
	}
}

// reply is compose.Reply to the conversation's ReplyTarget, as r does.
func reply(t model.Thread, account string, all bool, now time.Time) compose.Draft {
	return compose.Reply(t, compose.ReplyTarget(t, account), account, all, now)
}

// A reply opens the editor on the line to write on: the first of the body.
func TestStartLine_ReplyStartsAtTheMessage(t *testing.T) {
	reply := compose.Draft{Account: me, To: "a@x.com", Subject: "Re: Hi", Body: "\n\nOn …, A wrote:\n> hi\n"}

	line := compose.StartLine(reply)

	if lines := strings.Split(reply.Text(), "\n"); line != 8 || lines[line-2] != "" || lines[line-1] != "" {
		t.Errorf("line %d; want 8, the body's first (empty) line after the headers' blank one", line)
	}
	if n := compose.StartLine(compose.Draft{Account: me, Body: "\n"}); n != 0 {
		t.Errorf("a new mail starts at line %d, want 0 (the top, for To)", n)
	}
}

func TestEditorCmd_OpensAtTheLine(t *testing.T) {
	for _, tc := range []struct{ editor, want string }{
		{"vim", "vim +8 /d.eml"},
		{"nvim -u NONE", "nvim -u NONE +8 /d.eml"},
		{"/usr/bin/nano", "/usr/bin/nano +8 /d.eml"},
		{"hx", "hx /d.eml:8"},
		{"code --wait", "code --wait /d.eml"}, // unknown: as before
	} {
		t.Setenv("VISUAL", tc.editor)

		if got := strings.Join(compose.EditorCmd("/d.eml", 8).Args, " "); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.editor, got, tc.want)
		}
	}
	t.Setenv("VISUAL", "vim")
	if got := strings.Join(compose.EditorCmd("/d.eml", 0).Args, " "); got != "vim /d.eml" {
		t.Errorf("line 0: %q", got)
	}
}
