// Package output formats threads for people (dates, senders, conversations)
// and for scripts (TSV, JSON Lines). The TUI uses the same formatting.
package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jabiOP/climail/internal/model"
)

// ShortDate is how lists show a date: the time today, "Nov 3" this year,
// "2024-11-03" before.
func ShortDate(t, now time.Time) string {
	t = t.In(now.Location())
	switch {
	case sameDay(t, now):
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2")
	default:
		return t.Format("2006-01-02")
	}
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.YearDay() == b.YearDay()
}

// MessageDate is how a message's header shows its date: "Fri 20 Nov 14:35",
// with the year when it isn't this year's.
func MessageDate(t, now time.Time) string {
	t = t.In(now.Location())
	if t.Year() == now.Year() {
		return t.Format("Mon 2 Jan 15:04")
	}
	return t.Format("Mon 2 Jan 2006 15:04")
}

// Senders names a thread's participants like Gmail's list: "Alice, Bob",
// with "me" for the account itself.
func Senders(t model.Thread) string {
	var names []string
	for _, a := range t.Senders() {
		if a.Email == t.Account {
			names = append(names, "me")
		} else {
			names = append(names, a.Short())
		}
	}
	return strings.Join(names, ", ")
}

// Size is a byte count for people: "512 B", "12 KB", "3.4 MB".
func Size(n int64) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%d KB", n/1000)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/1e6)
	}
}

type Mode int

const (
	Pretty Mode = iota // for a terminal
	TSV
	JSON
)

// ChooseMode picks the output format from flags; without one, Pretty on a
// terminal and TSV in a pipe.
func ChooseMode(json, tsv, isTTY bool) (Mode, error) {
	switch {
	case json && tsv:
		return 0, errors.New("choose one of --json and --tsv")
	case json:
		return JSON, nil
	case tsv:
		return TSV, nil
	case isTTY:
		return Pretty, nil
	}
	return TSV, nil
}

// threadJSON is the stable JSON schema for a thread (one per line).
type threadJSON struct {
	Account  string    `json:"account"`
	ID       string    `json:"id"`
	Date     time.Time `json:"date"`
	From     string    `json:"from"`
	Subject  string    `json:"subject"`
	Snippet  string    `json:"snippet"`
	Unread   bool      `json:"unread"`
	Starred  bool      `json:"starred"`
	Messages int       `json:"messages"`
	Labels   []string  `json:"labels"`
}

// Threads prints a thread list. TSV columns: id, date, unread, from, subject, messages.
func Threads(w io.Writer, mode Mode, threads []model.Thread, now time.Time) error {
	for _, t := range threads {
		latest := t.Latest()
		var err error
		switch mode {
		case JSON:
			err = json.NewEncoder(w).Encode(threadJSON{
				Account: t.Account, ID: t.ID, Date: latest.Date.In(now.Location()), From: Senders(t),
				Subject: t.Subject(), Snippet: latest.Snippet, Unread: t.Unread(), Starred: t.Starred(),
				Messages: len(t.Messages), Labels: t.Labels(),
			})
		case TSV:
			unread := 0
			if t.Unread() {
				unread = 1
			}
			_, err = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%d\n", t.ID, latest.Date.In(now.Location()).Format(time.RFC3339),
				unread, tsvField(Senders(t)), tsvField(t.Subject()), len(t.Messages))
		default:
			mark := " "
			if t.Unread() {
				mark = "●"
			}
			from := Senders(t)
			if n := len(t.Messages); n > 1 {
				from += fmt.Sprintf(" (%d)", n)
			}
			_, err = fmt.Fprintf(w, "%s %-10s  %-24s  %s  [%s]\n", mark, ShortDate(latest.Date, now),
				clip(from, 24), Subject(t.Subject()), t.ID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Subject is a subject as shown, with a placeholder for none.
func Subject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(no subject)"
	}
	return s
}

func tsvField(s string) string { return strings.NewReplacer("\t", " ", "\n", " ").Replace(s) }

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Conversation prints a whole thread as plain text.
func Conversation(w io.Writer, t model.Thread, now time.Time) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", Subject(t.Subject()))
	for _, m := range t.Messages {
		fmt.Fprintf(&b, "\n── %s · %s\n", m.From, MessageDate(m.Date, now))
		for _, h := range MessageHeaders(m) {
			fmt.Fprintf(&b, "%s %s\n", h[0], h[1])
		}
		fmt.Fprintf(&b, "\n%s\n", m.Body)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// MessageHeaders are the recipient and attachment lines shown above a body.
func MessageHeaders(m model.Message) [][2]string {
	var out [][2]string
	if len(m.To) > 0 {
		out = append(out, [2]string{"To:", Addresses(m.To)})
	}
	if len(m.Cc) > 0 {
		out = append(out, [2]string{"Cc:", Addresses(m.Cc)})
	}
	if len(m.Attachments) > 0 {
		var names []string
		for _, a := range m.Attachments {
			names = append(names, fmt.Sprintf("%s (%s)", a.Filename, Size(a.Size)))
		}
		out = append(out, [2]string{"Attached:", strings.Join(names, ", ")})
	}
	return out
}

func Addresses(as []model.Address) string {
	s := make([]string, len(as))
	for i, a := range as {
		s[i] = a.String()
	}
	return strings.Join(s, ", ")
}

// Clean makes text from mail safe to print on a terminal: it drops control
// characters (escape sequences start with them: colours, cursor moves,
// clipboard writes, links) except newlines and tabs, and the characters that
// reverse the direction of text ("invoice\u202efdp.exe" shows as
// "invoiceexe.pdf"). Invalid bytes become U+FFFD: some terminals read a lone
// 0x9b as an escape.
func Clean(s string) string {
	if !needsCleaning(s) {
		return s
	}
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == utf8.RuneError && !strings.HasPrefix(s[i:], "\uFFFD"):
			b.WriteRune(utf8.RuneError)
		case unsafeRune(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func needsCleaning(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	return strings.IndexFunc(s, unsafeRune) >= 0
}

func unsafeRune(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f: // C0, DEL, C1
		return true
	case r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069: // direction embeddings, overrides, isolates
		return true
	}
	return false
}

// CleanWriter is a writer that Cleans what goes through it, for output that
// prints text from mail. Each Write is cleaned on its own, so a character
// split between two writes would be lost: write whole lines.
func CleanWriter(w io.Writer) io.Writer { return cleanWriter{w} }

type cleanWriter struct{ w io.Writer }

func (c cleanWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(c.w, Clean(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
