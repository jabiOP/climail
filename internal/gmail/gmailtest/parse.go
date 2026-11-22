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
	m := model.Message{Subject: subject, MessageID: h.Get("Message-ID"), InReplyTo: h.Get("In-Reply-To"),
		References: h.Get("References")}
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		m.From = model.Address{Name: from[0].Name, Email: from[0].Address}
	}
	m.To, m.Cc, m.Bcc = addressList(h, "To"), addressList(h, "Cc"), addressList(h, "Bcc")
	var files [][]byte
	if err := readPart(&m, &files, h.Get("Content-Type"), h.Get("Content-Transfer-Encoding"), msg.Body); err != nil {
		return model.Message{}, nil, fmt.Errorf("%w: %v", gmail.ErrPermanent, err)
	}
	return m, files, nil
}

// readPart reads a MIME entity into m: the first text/plain part is the
// body, files are attachments, multiparts are read part by part.
func readPart(m *model.Message, files *[][]byte, ctype, encoding string, r io.Reader) error {
	media, params, _ := mime.ParseMediaType(ctype)
	if !strings.HasPrefix(media, "multipart/") {
		data, err := decode(r, encoding)
		if err == nil && m.Body == "" && (media == "text/plain" || media == "") {
			m.Body = text(data)
		}
		return err
	}
	mr := multipart.NewReader(r, params["boundary"])
	for {
		part, err := mr.NextPart() // decodes quoted-printable itself
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if name := part.FileName(); name != "" {
			data, err := decode(part, part.Header.Get("Content-Transfer-Encoding"))
			if err != nil {
				return err
			}
			ptype, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			m.Attachments = append(m.Attachments, model.Attachment{Filename: name, MimeType: ptype, Size: int64(len(data))})
			*files = append(*files, data)
			continue
		}
		if err := readPart(m, files, part.Header.Get("Content-Type"), part.Header.Get("Content-Transfer-Encoding"), part); err != nil {
			return err
		}
	}
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
