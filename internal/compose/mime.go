package compose

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Build makes the RFC 5322 message Gmail sends. Gmail adds Message-ID, and
// delivers to Bcc without showing it to the others.
func (d Draft) Build(now time.Time) ([]byte, error) {
	if err := d.Check(); err != nil {
		return nil, err
	}
	var b bytes.Buffer
	header := func(name, value string) {
		if value != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", name, value)
		}
	}
	header("From", d.Account)
	for _, h := range []struct{ name, list string }{{"To", d.To}, {"Cc", d.Cc}, {"Bcc", d.Bcc}} {
		as, _ := parseList(h.list) // Check has read them
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
	text, err := qp(d.Body)
	if err != nil {
		return nil, err
	}
	if len(d.Attach) == 0 {
		header("Content-Type", "text/plain; charset=utf-8")
		header("Content-Transfer-Encoding", "quoted-printable")
		b.WriteString("\r\n" + text)
		return b.Bytes(), nil
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type": {"text/plain; charset=utf-8"}, "Content-Transfer-Encoding": {"quoted-printable"}})
	if err != nil {
		return nil, err
	}
	part.Write([]byte(text))
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
