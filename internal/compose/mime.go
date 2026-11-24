package compose

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jabiOP/climail/internal/model"
)

// Build makes the RFC 5322 message Gmail sends. Gmail adds Message-ID, and
// delivers to Bcc without showing it to the others.
func (d Draft) Build(now time.Time) ([]byte, error) {
	if err := d.Check(); err != nil {
		return nil, err
	}
	return d.build(now)
}

// BuildDraft makes the message saved in Gmail's Drafts. It may be
// unfinished: no recipients, or ones still typed as names ("maya").
func (d Draft) BuildDraft(now time.Time) ([]byte, error) {
	for _, path := range d.Attach {
		if _, err := os.Stat(expandHome(path)); err != nil {
			return nil, fmt.Errorf("can't attach %s: %w", path, err)
		}
	}
	return d.build(now)
}

func (d Draft) build(now time.Time) ([]byte, error) {
	var b bytes.Buffer
	header := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", name, value)
		}
	}
	header("From", d.Sender())
	for _, h := range []struct{ name, list string }{{"To", d.To}, {"Cc", d.Cc}, {"Bcc", d.Bcc}} {
		as, err := parseList(h.list)
		if err != nil { // only in a draft: kept as typed
			header(h.name, mime.QEncoding.Encode("utf-8", h.list))
			continue
		}
		var out []string
		for _, a := range as {
			out = append(out, a.String()) // encodes non-ASCII names
		}
		header(h.name, strings.Join(out, ", "))
	}
	header("Subject", mime.QEncoding.Encode("utf-8", d.Subject))
	header("Date", now.Format(time.RFC1123Z))
	header("In-Reply-To", d.InReplyTo)
	header("References", d.References)
	header("MIME-Version", "1.0")
	ctype, content, err := d.text()
	if err != nil {
		return nil, err
	}
	if len(d.Attach) == 0 {
		header("Content-Type", ctype)
		if !d.formatted() {
			header("Content-Transfer-Encoding", "quoted-printable")
		}
		b.WriteString("\r\n")
		b.Write(content)
		return b.Bytes(), nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	h := textproto.MIMEHeader{"Content-Type": {ctype}}
	if !d.formatted() {
		h.Set("Content-Transfer-Encoding", "quoted-printable")
	}
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	part.Write(content)
	for _, path := range d.Attach {
		if err := attach(w, expandHome(path)); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	header("Content-Type", "multipart/mixed; boundary="+w.Boundary())
	b.WriteString("\r\n")
	b.Write(body.Bytes())
	return b.Bytes(), nil
}

func (d Draft) formatted() bool { return !d.Plain }

// text is the body's MIME entity: the text as typed, or (as Gmail sends
// mail) multipart/alternative with it and its Markdown rendered as HTML.
func (d Draft) text() (ctype string, content []byte, err error) {
	plain, err := qp(d.Body)
	if err != nil || !d.formatted() {
		return "text/plain; charset=utf-8", []byte(plain), err
	}
	html, err := HTML(d.Body)
	if err != nil {
		return "", nil, err
	}
	rich, err := qp(html)
	if err != nil {
		return "", nil, err
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for _, p := range []struct{ ctype, content string }{{"text/plain", plain}, {"text/html", rich}} {
		part, err := w.CreatePart(textproto.MIMEHeader{
			"Content-Type": {p.ctype + "; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
		if err != nil {
			return "", nil, err
		}
		part.Write([]byte(p.content))
	}
	if err := w.Close(); err != nil {
		return "", nil, err
	}
	return "multipart/alternative; boundary=" + w.Boundary(), b.Bytes(), nil
}

func qp(s string) (string, error) {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	if _, err := w.Write([]byte(strings.ReplaceAll(s, "\n", "\r\n"))); err != nil {
		return "", err
	}
	err := w.Close()
	return b.String(), err
}

func attach(w *multipart.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("attach %s: %w", path, err)
	}
	name := filepath.Base(path)
	ctype, params, err := mime.ParseMediaType(mime.TypeByExtension(filepath.Ext(path))) // may carry a charset
	if err != nil {
		ctype, params = "application/octet-stream", map[string]string{}
	}
	params["name"] = name
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {mime.FormatMediaType(ctype, params)},
		"Content-Disposition":       {mime.FormatMediaType("attachment", map[string]string{"filename": name})},
		"Content-Transfer-Encoding": {"base64"},
	})
	if err != nil {
		return err
	}
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 76 { // RFC 2045 line length
		fmt.Fprintf(part, "%s\r\n", enc[:76])
		enc = enc[76:]
	}
	_, err = fmt.Fprintf(part, "%s\r\n", enc)
	return err
}

// WithFrom sets a built message's sender, replacing its From line (Build
// writes the bare address), so mail goes out with the account's display name.
// FromAddress is the address on raw's From line; "" if there is none.
func FromAddress(raw []byte) string {
	line, _, _ := bytes.Cut(raw, []byte("\r\n"))
	value, ok := bytes.CutPrefix(line, []byte("From:"))
	if !ok {
		return ""
	}
	a, err := mail.ParseAddress(strings.TrimSpace(string(value)))
	if err != nil {
		return strings.TrimSpace(string(value))
	}
	return a.Address
}

func WithFrom(raw []byte, from model.Address) []byte {
	line := from.Email
	if from.Name != "" {
		line = (&mail.Address{Name: from.Name, Address: from.Email}).String()
	}
	rest := raw
	if bytes.HasPrefix(raw, []byte("From:")) {
		if i := bytes.Index(raw, []byte("\r\n")); i >= 0 {
			rest = raw[i+2:]
		}
	}
	return append([]byte("From: "+line+"\r\n"), rest...)
}
