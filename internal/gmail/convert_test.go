package gmail_test

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	gmailapi "google.golang.org/api/gmail/v1"

	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/model"
)

func b64(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

func part(mimeType, contentType, data string) *gmailapi.MessagePart {
	p := &gmailapi.MessagePart{MimeType: mimeType, Body: &gmailapi.MessagePartBody{Data: b64(data)}}
	if contentType != "" {
		p.Headers = []*gmailapi.MessagePartHeader{{Name: "Content-Type", Value: contentType}}
	}
	return p
}

func TestFromAPI_HeadersAndLabels(t *testing.T) {
	date := time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)
	m := &gmailapi.Message{
		Id: "m1", ThreadId: "t1", HistoryId: 7, InternalDate: date.UnixMilli(),
		LabelIds: []string{"UNREAD", "INBOX"}, Snippet: "It&#39;s here",
		Payload: &gmailapi.MessagePart{
			MimeType: "text/plain",
			Headers: []*gmailapi.MessagePartHeader{
				{Name: "From", Value: `"Smith, Alice" <alice@example.com>`},
				{Name: "To", Value: "bob@example.com, Carol <carol@example.com>"},
				{Name: "Subject", Value: "=?UTF-8?Q?Caf=C3=A9?="},
				{Name: "Message-ID", Value: "<abc@mail>"},
			},
			Body: &gmailapi.MessagePartBody{Data: b64("Hello\r\nthere\r\n")},
		},
	}

	got := gmail.FromAPI("me@x.com", m)

	want := model.Message{
		Account: "me@x.com", ID: "m1", ThreadID: "t1", HistoryID: 7, Date: date.Local(),
		From:    model.Address{Name: "Smith, Alice", Email: "alice@example.com"},
		To:      []model.Address{{Email: "bob@example.com"}, {Name: "Carol", Email: "carol@example.com"}},
		Subject: "Café", Snippet: "It's here", Body: "Hello\nthere",
		Labels: []string{"INBOX", "UNREAD"}, MessageID: "<abc@mail>",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}

func TestFromAPI_MalformedAddressKept(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{Headers: []*gmailapi.MessagePartHeader{{Name: "From", Value: "not an address"}}}}

	if got := gmail.FromAPI("a", m).From; got.Email != "not an address" {
		t.Errorf("From = %+v", got)
	}
}

func TestFromAPI_PrefersPlainTextAndListsAttachments(t *testing.T) {
	m := &gmailapi.Message{Payload: &gmailapi.MessagePart{
		MimeType: "multipart/mixed",
		Parts: []*gmailapi.MessagePart{
			{MimeType: "multipart/alternative", Parts: []*gmailapi.MessagePart{
				part("text/plain", "text/plain; charset=utf-8", "plain version"),
				part("text/html", "text/html; charset=utf-8", "<p>html version</p>"),
			}},
			{MimeType: "application/pdf", Filename: "report.pdf", Body: &gmailapi.MessagePartBody{AttachmentId: "att1", Size: 1234}},
		},
	}}

	got := gmail.FromAPI("a", m)

	if got.Body != "plain version" {
		t.Errorf("Body = %q", got.Body)
	}
	want := []model.Attachment{{ID: "att1", Filename: "report.pdf", MimeType: "application/pdf", Size: 1234}}
	if diff := cmp.Diff(want, got.Attachments); diff != "" {
		t.Errorf("attachments mismatch (-want +got):\n%s", diff)
	}
}

func TestFromAPI_ConvertsHTMLOnlyBody(t *testing.T) {
	m := &gmailapi.Message{Payload: part("text/html", "", "<p>Hi <b>there</b></p><p>Bye</p>")}

	if got := gmail.FromAPI("a", m).Body; got != "Hi there\n\nBye" {
		t.Errorf("Body = %q", got)
	}
}

func TestFromAPI_DecodesCharset(t *testing.T) {
	m := &gmailapi.Message{Payload: part("text/plain", "text/plain; charset=iso-8859-1", "caf\xe9")}

	if got := gmail.FromAPI("a", m).Body; got != "café" {
		t.Errorf("Body = %q", got)
	}
}

func TestHTMLToText(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"paragraphs and breaks", "<div>one<br>two</div><p>three</p>", "one\ntwo\n\nthree"},
		{"drops style and script", "<style>p{}</style><script>x()</script>text", "text"},
		{"collapses whitespace", "a   \n  b&nbsp;c", "a b c"},
		{"bullets", "<ul><li>a</li><li>b</li></ul>", "• a\n• b"},
		{"links as footnotes", `<a href="https://x.com/t?id=1">Read</a> and <a href="mailto:a@b">mail</a>`,
			"Read[1] and mail\n\n[1] https://x.com/t?id=1"},
		{"image alt text", `<img src="x.png" alt="Logo"> hi`, "Logo hi"},
		{"entities", "Tom &amp; Jerry &lt;3", "Tom & Jerry <3"},
		{"at most one blank line", "<p>a</p><p></p><p></p><p>b</p>", "a\n\nb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gmail.HTMLToText(tt.in); got != tt.want {
				t.Errorf("HTMLToText(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLabelFromAPI(t *testing.T) {
	got := gmail.LabelFromAPI("a", &gmailapi.Label{Id: "Label_1", Name: "Work", Type: "user",
		Color: &gmailapi.LabelColor{BackgroundColor: "#16a766", TextColor: "#ffffff"}})

	want := model.Label{Account: "a", ID: "Label_1", Name: "Work", Color: "#16a766", TextColor: "#ffffff"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}
