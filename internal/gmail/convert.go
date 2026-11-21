package gmail

import (
	"encoding/base64"
	"html"
	"io"
	"mime"
	"net/mail"
	"slices"
	"strings"
	"time"

	gmailapi "google.golang.org/api/gmail/v1"

	"golang.org/x/text/encoding/htmlindex"

	"github.com/jabiOP/climail/internal/model"
)

// FromAPI converts a message fetched with format "full".
func FromAPI(account string, m *gmailapi.Message) model.Message {
	headers := map[string]string{}
	if m.Payload != nil {
		for _, h := range m.Payload.Headers {
			headers[strings.ToLower(h.Name)] = h.Value
		}
	}
	labels := slices.Clone(m.LabelIds)
	slices.Sort(labels)
	from := parseAddresses(headers["from"])
	out := model.Message{
		Account: account, ID: m.Id, ThreadID: m.ThreadId, HistoryID: m.HistoryId,
		Date:       time.UnixMilli(m.InternalDate),
		To:         parseAddresses(headers["to"]),
		Cc:         parseAddresses(headers["cc"]),
		ReplyTo:    parseAddresses(headers["reply-to"]),
		Subject:    decodeHeader(headers["subject"]),
		Snippet:    strings.TrimSpace(html.UnescapeString(m.Snippet)), // Gmail escapes snippets ("&#39;")
		Labels:     labels,
		MessageID:  headers["message-id"],
		References: headers["references"],
	}
	if len(from) > 0 {
		out.From = from[0]
	}
	out.Body, out.Attachments = body(m.Payload)
	return out
}

var wordDecoder = mime.WordDecoder{CharsetReader: charsetReader}

func decodeHeader(s string) string {
	if d, err := wordDecoder.DecodeHeader(s); err == nil {
		return d
	}
	return s
}

func parseAddresses(s string) []model.Address {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parser := mail.AddressParser{WordDecoder: &wordDecoder}
	list, err := parser.ParseList(s)
	if err != nil { // malformed: keep what was there rather than nothing
		return []model.Address{{Email: strings.TrimSpace(s)}}
	}
	out := make([]model.Address, len(list))
	for i, a := range list {
		out[i] = model.Address{Name: a.Name, Email: a.Address}
	}
	return out
}

// body picks the text to show, preferring a text/plain part to converting
// HTML, and lists the attachments.
func body(root *gmailapi.MessagePart) (string, []model.Attachment) {
	var plain, htmlText string
	var atts []model.Attachment
	for _, p := range attachmentParts(root) {
		a := model.Attachment{Filename: p.Filename, MimeType: p.MimeType}
		if p.Body != nil {
			a.ID, a.Size = p.Body.AttachmentId, p.Body.Size
		}
		atts = append(atts, a)
	}
	var walk func(p *gmailapi.MessagePart)
	walk = func(p *gmailapi.MessagePart) {
		if p == nil || p.Filename != "" {
			return
		}
		mediaType, params, _ := mime.ParseMediaType(header(p, "Content-Type"))
		if mediaType == "" {
			mediaType = strings.ToLower(p.MimeType)
		}
		switch {
		case mediaType == "text/plain" && plain == "":
			plain = decodePart(p, params["charset"])
		case mediaType == "text/html" && htmlText == "":
			htmlText = decodePart(p, params["charset"])
		}
		for _, c := range p.Parts {
			walk(c)
		}
	}
	walk(root)
	if strings.TrimSpace(plain) != "" {
		return strings.TrimSpace(strings.ReplaceAll(plain, "\r\n", "\n")), atts
	}
	return HTMLToText(htmlText), atts
}

// attachmentParts are a message's parts with a file name, in order: the
// order of model.Message.Attachments, which downloads go by.
func attachmentParts(root *gmailapi.MessagePart) []*gmailapi.MessagePart {
	var out []*gmailapi.MessagePart
	var walk func(p *gmailapi.MessagePart)
	walk = func(p *gmailapi.MessagePart) {
		if p == nil {
			return
		}
		if p.Filename != "" {
			out = append(out, p)
			return
		}
		for _, c := range p.Parts {
			walk(c)
		}
	}
	walk(root)
	return out
}

// decodeBase64 reads Gmail's base64url, with or without padding.
func decodeBase64(s string) ([]byte, error) {
	if data, err := base64.URLEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func header(p *gmailapi.MessagePart, name string) string {
	for _, h := range p.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// decodePart returns a part's text as UTF-8. Gmail has already undone the
// transfer encoding (quoted-printable, base64) but not the charset.
func decodePart(p *gmailapi.MessagePart, charset string) string {
	if p.Body == nil || p.Body.Data == "" {
		return ""
	}
	data, err := decodeBase64(p.Body.Data)
	if err != nil {
		return ""
	}
	r, err := charsetReader(charset, strings.NewReader(string(data)))
	if err != nil {
		return string(data)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		return string(data)
	}
	return string(out)
}

func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "", "utf-8", "utf8", "us-ascii":
		return input, nil
	}
	enc, err := htmlindex.Get(charset)
	if err != nil {
		return nil, err
	}
	return enc.NewDecoder().Reader(input), nil
}

// LabelFromAPI converts a label from labels.list.
func LabelFromAPI(account string, l *gmailapi.Label) model.Label {
	out := model.Label{Account: account, ID: l.Id, Name: l.Name, System: l.Type == "system"}
	if l.Color != nil {
		out.Color, out.TextColor = l.Color.BackgroundColor, l.Color.TextColor
	}
	return out
}
