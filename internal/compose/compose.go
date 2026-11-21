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

	// A reply keeps these, so Gmail files it in the conversation.
	ThreadID, InReplyTo, References string
}

// Reply drafts a reply to a conversation's latest message from someone
// else, quoting it; all replies to everyone on it.
func Reply(t model.Thread, account string, all bool, now time.Time) Draft {
	orig := t.Latest()
	for i := len(t.Messages) - 1; i >= 0; i-- {
		if t.Messages[i].From.Email != account {
			orig = t.Messages[i]
			break
		}
	}
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

// Forward drafts a forward of a conversation's latest message. attach are
// its attachments, downloaded, to send along.
func Forward(t model.Thread, account string, attach []string, now time.Time) Draft {
	orig := t.Latest()
	header := fmt.Sprintf("---------- Forwarded message ---------\nFrom: %s\nDate: %s\nSubject: %s\nTo: %s\n",
		orig.From, output.MessageDate(orig.Date, now), orig.Subject, addresses(orig.To))
	if len(orig.Cc) > 0 {
		header += "Cc: " + addresses(orig.Cc) + "\n"
	}
	return Draft{Account: account, Subject: prefixed("Fwd: ", orig.Subject), Attach: attach,
		Body: "\n\n" + header + "\n" + orig.Body}
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

// Text is the draft as the file the user edits: headers, a blank line, the body.
func (d Draft) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\nCc: %s\nBcc: %s\nSubject: %s\n", d.To, d.Cc, d.Bcc, d.Subject)
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
// the account and how a reply is threaded.
func Parse(text string, base Draft) (Draft, error) {
	d := Draft{Account: base.Account, ThreadID: base.ThreadID, InReplyTo: base.InReplyTo, References: base.References}
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
			return Draft{}, fmt.Errorf("unknown header %q (use To, Cc, Bcc, Subject or Attach)", name)
		}
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

// Summary says what is about to be sent: "“Re: Plan” to Maya Chen, tom".
func (d Draft) Summary() string {
	var names []string
	for _, list := range []string{d.To, d.Cc, d.Bcc} {
		as, _ := parseList(list)
		for _, a := range as {
			names = append(names, model.Address{Name: a.Name, Email: a.Address}.Short())
		}
	}
	s := fmt.Sprintf("“%s” to %s", output.Subject(d.Subject), strings.Join(names, ", "))
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
