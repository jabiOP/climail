// Package compose turns what the user writes in their editor into a mail:
// draft templates for new mail, replies and forwards, the draft file format,
// and the MIME message Gmail sends.
package compose

import (
	"errors"
	"fmt"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
)

// Draft is a mail being written. To, Cc and Bcc are as typed:
// "Maya <maya@example.com>, tom@example.com".
type Draft struct {
	Account     string // sent from this address
	To, Cc, Bcc string
	Subject     string
	Body        string
	Attach      []string // file paths
	Plain       bool     // send the body as typed; otherwise its Markdown is also sent formatted

	// A reply keeps these, so Gmail files it in the conversation.
	ThreadID, InReplyTo, References string

	// DraftID is the message ID of the Gmail draft this continues: saving
	// replaces that draft, sending removes it.
	DraftID string
}

// ReplyTarget is the message a reply answers: the conversation's latest
// from someone else, else its latest.
func ReplyTarget(t model.Thread, account string) model.Message {
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if t.Messages[i].From.Email != account {
			return t.Messages[i]
		}
	}
	return t.Latest()
}

// Reply drafts a reply to orig, one of the conversation's messages (usually
// its ReplyTarget), quoting it; all replies to everyone on it.
func Reply(t model.Thread, orig model.Message, account string, all bool, now time.Time) Draft {
	var to, cc []model.Address
	switch {
	case orig.From.Email == account: // replying to my own message: to its recipients
		to = orig.To
	case len(orig.ReplyTo) > 0:
		to = orig.ReplyTo
	default:
		to = []model.Address{orig.From}
	}
	if all {
		to = append(to, orig.To...)
		cc = orig.Cc
	}
	to = others(to, account, nil)
	cc = others(cc, account, to)
	return Draft{
		Account: account, To: addresses(to), Cc: addresses(cc),
		Subject:  prefixed("Re: ", orig.Subject),
		Body:     fmt.Sprintf("\n\nOn %s, %s wrote:\n%s", output.MessageDate(orig.Date, now), orig.From, quote(orig.Body)),
		ThreadID: t.ID, InReplyTo: orig.MessageID,
		References: strings.TrimSpace(orig.References + " " + orig.MessageID),
	}
}

// Forward drafts a forward of a message. attach are its attachments,
// downloaded, to send along.
func Forward(orig model.Message, account string, attach []string, now time.Time) Draft {
	header := fmt.Sprintf("---------- Forwarded message ---------\nFrom: %s\nDate: %s\nSubject: %s\nTo: %s\n",
		orig.From, output.MessageDate(orig.Date, now), orig.Subject, addresses(orig.To))
	if len(orig.Cc) > 0 {
		header += "Cc: " + addresses(orig.Cc) + "\n"
	}
	return Draft{Account: account, Subject: prefixed("Fwd: ", orig.Subject), Attach: attach,
		Body: "\n\n" + header + "\n" + orig.Body}
}

// FromDraft continues a draft saved in Gmail, its formatting as Markdown
// (see DraftBody). attach are its attachments, downloaded.
func FromDraft(m model.Message, account string, attach []string) Draft {
	body, _ := DraftBody(m)
	d := Draft{Account: account, To: addresses(m.To), Cc: addresses(m.Cc), Bcc: addresses(m.Bcc), Subject: m.Subject,
		Body: body, Attach: attach, DraftID: m.ID, InReplyTo: m.InReplyTo, References: m.References}
	if d.InReplyTo != "" || d.References != "" { // a reply: it goes in the conversation
		d.ThreadID = m.ThreadID
	}
	return d
}

// others drops the account itself and addresses already in skip, and duplicates.
func others(as []model.Address, account string, skip []model.Address) []model.Address {
	var out []model.Address
	for _, a := range as {
		seen := func(b model.Address) bool { return strings.EqualFold(a.Email, b.Email) }
		if !strings.EqualFold(a.Email, account) && !slices.ContainsFunc(skip, seen) && !slices.ContainsFunc(out, seen) {
			out = append(out, a)
		}
	}
	return out
}

func addresses(as []model.Address) string { return output.Addresses(as) }

// prefixed adds "Re: " or "Fwd: " unless the subject already starts with it.
func prefixed(prefix, subject string) string {
	if strings.HasPrefix(strings.ToLower(subject), strings.ToLower(prefix)) {
		return subject
	}
	return prefix + subject
}

func quote(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, ">") {
			lines[i] = ">" + l
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}

// Text is the draft as the file the user edits: headers, From first, a blank line, the body.
func (d Draft) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\nTo: %s\nCc: %s\nBcc: %s\nSubject: %s\n", d.Account, d.To, d.Cc, d.Bcc, d.Subject)
	if len(d.Attach) == 0 {
		b.WriteString("Attach: \n")
	}
	for _, a := range d.Attach {
		fmt.Fprintf(&b, "Attach: %s\n", a)
	}
	b.WriteString("\n" + d.Body)
	return b.String()
}

// Parse reads an edited draft file. base gives what the file doesn't hold:
// how a reply is threaded, and the account when there's no From. From is as
// typed (an account's address or its start); a reply, or a draft continued,
// stays in its account.
func Parse(text string, base Draft) (Draft, error) {
	d := Draft{Account: base.Account, ThreadID: base.ThreadID, InReplyTo: base.InReplyTo, References: base.References,
		DraftID: base.DraftID}
	head, body, _ := strings.Cut(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	for _, line := range strings.Split(head, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return Draft{}, fmt.Errorf("the headers at the top need a blank line after them (%q isn't a header)", line)
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "from":
			if value != "" {
				d.Account = value
			}
		case "to":
			d.To = value
		case "cc":
			d.Cc = value
		case "bcc":
			d.Bcc = value
		case "subject":
			d.Subject = value
		case "attach":
			if value != "" {
				d.Attach = append(d.Attach, value)
			}
		default:
			return Draft{}, fmt.Errorf("unknown header %q (use From, To, Cc, Bcc, Subject or Attach)", name)
		}
	}
	if (d.ThreadID != "" || d.DraftID != "") && !strings.EqualFold(d.Account, base.Account) {
		return Draft{}, fmt.Errorf("From: this goes from %s, the account its conversation is in", base.Account)
	}
	d.Body = strings.TrimRight(body, " \t\n") + "\n"
	return d, nil
}

// Check finds what would stop the draft from being sent.
func (d Draft) Check() error {
	n := 0
	for name, list := range map[string]string{"To": d.To, "Cc": d.Cc, "Bcc": d.Bcc} {
		as, err := parseList(list)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		n += len(as)
	}
	if n == 0 {
		return errors.New("no recipients: fill in To")
	}
	for _, path := range d.Attach {
		if _, err := os.Stat(expandHome(path)); err != nil {
			return fmt.Errorf("can't attach %s: %w", path, err)
		}
	}
	return nil
}

func parseList(s string) ([]*mail.Address, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	as, err := mail.ParseAddressList(s)
	if err != nil {
		return nil, fmt.Errorf("can't read %q as addresses: %w", s, err)
	}
	return as, nil
}

func expandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}

// Summary says what is about to be sent: "“Re: Plan” to Maya Chen, tom"
// (just "“Re: Plan”" for a draft without recipients).
func (d Draft) Summary() string {
	var names []string
	for _, list := range []string{d.To, d.Cc, d.Bcc} {
		as, _ := parseList(list)
		for _, a := range as {
			names = append(names, model.Address{Name: a.Name, Email: a.Address}.Short())
		}
	}
	s := fmt.Sprintf("“%s”", output.Subject(d.Subject))
	if len(names) > 0 {
		s += " to " + strings.Join(names, ", ")
	}
	if n := len(d.Attach); n > 0 {
		s += fmt.Sprintf(" (%d attached)", n)
	}
	return s
}

// EditorCmd opens path in the user's editor: $VISUAL, else $EDITOR, else vi.
// The variable may hold arguments ("code --wait").
func EditorCmd(path string) *exec.Cmd {
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	args := strings.Fields(editor)
	if len(args) == 0 {
		args = []string{"vi"}
	}
	return exec.Command(args[0], append(args[1:], path)...)
}

// WriteDraft saves a draft's text in dir, private to the user, and returns
// its path. The .eml name makes editors treat it as mail.
func WriteDraft(dir string, d Draft, now time.Time) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "draft-"+now.Format("20060102-150405.000")+".eml")
	return path, os.WriteFile(path, []byte(d.Text()), 0o600)
}

// Resolve replaces each recipient typed without an @ ("maya") with the one
// person lookup finds for it. A name that matches no one, or several, is an
// error that lists who it could be.
func Resolve(d Draft, lookup func(query string) []model.Address) (Draft, error) {
	return resolve(d, lookup, true)
}

// ResolveWhatCan is Resolve for a draft being kept: names that match no
// one, or several, stay as typed.
func ResolveWhatCan(d Draft, lookup func(query string) []model.Address) Draft {
	d, _ = resolve(d, lookup, false)
	return d
}

func resolve(d Draft, lookup func(query string) []model.Address, strict bool) (Draft, error) {
	for _, h := range []struct {
		name string
		list *string
	}{{"To", &d.To}, {"Cc", &d.Cc}, {"Bcc", &d.Bcc}} {
		parts := splitRecipients(*h.list)
		changed := false
		for i, p := range parts {
			if strings.Contains(p, "@") {
				continue
			}
			a, err := pick(p, lookup(p))
			if err != nil && !strict {
				continue
			}
			if err != nil {
				return Draft{}, fmt.Errorf("%s: %w", h.name, err)
			}
			parts[i], changed = formatAddress(a), true
		}
		if changed {
			*h.list = strings.Join(parts, ", ")
		}
	}
	return d, nil
}

// pick chooses the one match for query; with several, one whose name,
// address or the part before its @ is exactly query.
func pick(query string, matches []model.Address) (model.Address, error) {
	if len(matches) == 1 {
		return matches[0], nil
	}
	for _, a := range matches {
		local, _, _ := strings.Cut(a.Email, "@")
		if strings.EqualFold(a.Name, query) || strings.EqualFold(a.Email, query) || strings.EqualFold(local, query) {
			return a, nil
		}
	}
	if len(matches) == 0 {
		return model.Address{}, fmt.Errorf("no one matches %q (write the address)", query)
	}
	var names []string
	for _, a := range matches[:min(len(matches), 4)] {
		names = append(names, a.String())
	}
	more := ""
	if len(matches) > 4 {
		more = fmt.Sprintf(" and %d more", len(matches)-4)
	}
	return model.Address{}, fmt.Errorf("%q could be %s%s: write more of it", query, strings.Join(names, ", "), more)
}

// formatAddress writes an address the way Parse reads it back, quoting
// names with characters that would split it.
func formatAddress(a model.Address) string {
	if strings.ContainsAny(a.Name, `,;:<>@"()[]\.`) {
		a.Name = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a.Name) + `"`
	}
	return a.String()
}

// splitRecipients splits a header's recipients at commas outside quotes and
// angle brackets, trimmed, empty ones dropped.
func splitRecipients(s string) []string {
	var parts []string
	var cur strings.Builder
	quoted, angled, escaped := false, false, false
	flush := func() {
		if p := strings.TrimSpace(cur.String()); p != "" {
			parts = append(parts, p)
		}
		cur.Reset()
	}
	for _, r := range s {
		switch {
		case escaped:
			escaped = false
		case r == '\\' && quoted:
			escaped = true
		case r == '"':
			quoted = !quoted
		case r == '<' && !quoted:
			angled = true
		case r == '>' && !quoted:
			angled = false
		case r == ',' && !quoted && !angled:
			flush()
			continue
		}
		cur.WriteRune(r)
	}
	flush()
	return parts
}
