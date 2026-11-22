package gmailtest

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"

	"github.com/jabiOP/climail/internal/gmail"
	"github.com/jabiOP/climail/internal/model"
)

// parse reads a raw mail as climail builds them (plain text, or
// multipart/mixed with attachments) into a message without ID, labels or
// thread, and its attachments' contents.
func parse(raw []byte) (model.Message, [][]byte, error) {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return model.Message{}, nil, fmt.Errorf("%w: %v", gmail.ErrPermanent, err)
	}
	h := msg.Header
	subject, _ := new(mime.WordDecoder).DecodeHeader(h.Get("Subject"))
	m := model.Message{Subject: subject, MessageID: h.Get("Message-ID"), References: h.Get("References")}
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		m.From = model.Address{Name: from[0].Name, Email: from[0].Address}
	}
	m.To, m.Cc = addressList(h, "To"), addressList(h, "Cc")
	if irt := h.Get("In-Reply-To"); irt != "" && !strings.Contains(m.References, irt) {
		m.References = strings.TrimSpace(m.References + " " + irt)
	}
	ctype, params, _ := mime.ParseMediaType(h.Get("Content-Type"))
	if !strings.HasPrefix(ctype, "multipart/") {
		body, err := decode(msg.Body, h.Get("Content-Transfer-Encoding"))
		m.Body = text(body)
		return m, nil, err
	}
	var files [][]byte
	r := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := r.NextPart() // decodes quoted-printable itself
		if err == io.EOF {
			break
		}
		if err != nil {
			return model.Message{}, nil, fmt.Errorf("%w: %v", gmail.ErrPermanent, err)
		}
		data, err := decode(part, part.Header.Get("Content-Transfer-Encoding"))
		if err != nil {
			return model.Message{}, nil, fmt.Errorf("%w: %v", gmail.ErrPermanent, err)
		}
		if name := part.FileName(); name != "" {
			ptype, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			m.Attachments = append(m.Attachments, model.Attachment{Filename: name, MimeType: ptype, Size: int64(len(data))})
			files = append(files, data)
		} else if m.Body == "" {
			m.Body = text(data)
		}
	}
	return m, files, nil
}

func addressList(h mail.Header, name string) []model.Address {
	as, _ := h.AddressList(name)
	var out []model.Address
	for _, a := range as {
		out = append(out, model.Address{Name: a.Name, Email: a.Address})
	}
	return out
}

func decode(r io.Reader, encoding string) ([]byte, error) {
	switch strings.ToLower(encoding) {
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	}
	return io.ReadAll(r)
}

func text(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }

func snippet(body string) string {
	r := []rune(strings.Join(strings.Fields(body), " "))
	return string(r[:min(len(r), 120)])
}
