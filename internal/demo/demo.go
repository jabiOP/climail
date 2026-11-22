// Package demo makes up two mailboxes for `climail demo`: enough variety
// (threads, labels, stars, attachments, quoted replies, HTML-style links) to
// try every part of the TUI without a Google account.
package demo

import (
	"fmt"
	"strings"
	"time"

	"github.com/jabiOP/climail/internal/model"
)

type Mailbox struct {
	Account  string
	Labels   []model.Label
	Messages []model.Message
	Files    map[string][]byte // attachment contents by "messageID/index"
}

// Name is the demo user's display name, which mail is sent with.
const Name = "Sam Rivera"

func addr(name, email string) model.Address { return model.Address{Name: name, Email: email} }

var (
	work = addr("", "you@work.example")
	home = addr("", "you@home.example")

	maya   = addr("Maya Chen", "maya@work.example")
	tom    = addr("Tom Okafor", "tom@work.example")
	priya  = addr("Priya Raman", "priya@client.example")
	ci     = addr("CI", "ci@builds.example")
	hr     = addr("People Team", "people@work.example")
	news   = addr("The Weekly Terminal", "hello@weekly.example")
	cal    = addr("Calendar", "calendar@work.example")
	mum    = addr("Mum", "mum@home.example")
	bank   = addr("Northwind Bank", "alerts@bank.example")
	alex   = addr("Alex", "alex@friends.example")
	travel = addr("Skyline Air", "booking@skyline.example")
	shop   = addr("Corner Bookshop", "orders@books.example")
)

// builder adds messages to one mailbox, minutes before now.
type builder struct {
	mb  Mailbox
	now time.Time
	n   int
}

// attach adds a PDF attachment saying text to the message last added.
func (b *builder) attach(m *model.Message, name, text string) {
	data := pdf(text)
	if b.mb.Files == nil {
		b.mb.Files = map[string][]byte{}
	}
	b.mb.Files[fmt.Sprintf("%s/%d", m.ID, len(m.Attachments))] = data
	m.Attachments = append(m.Attachments, model.Attachment{Filename: name, MimeType: "application/pdf", Size: int64(len(data))})
}

// pdf makes a one-page PDF showing text, so opened demo attachments show something.
func pdf(text string) []byte {
	stream := fmt.Sprintf("BT /F1 18 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var b strings.Builder
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return []byte(b.String())
}

func (b *builder) add(thread string, minutesAgo int, from model.Address, to []model.Address, subject, body string,
	labels ...string) *model.Message {
	b.n++
	m := model.Message{
		ID: fmt.Sprintf("%s-%02d", thread, b.n), ThreadID: thread, From: from, To: to, Subject: subject,
		Date: b.now.Add(-time.Duration(minutesAgo) * time.Minute), Body: body, Snippet: snippet(body), Labels: labels,
	}
	b.mb.Messages = append(b.mb.Messages, m)
	return &b.mb.Messages[len(b.mb.Messages)-1]
}

// snippet is the start of the body on one line, as Gmail makes them.
func snippet(body string) string {
	r := []rune(strings.Join(strings.Fields(body), " "))
	return string(r[:min(len(r), 120)])
}

// Mailboxes returns the demo accounts, newest mail a few minutes before now.
func Mailboxes(now time.Time) []Mailbox {
	return []Mailbox{workBox(now), homeBox(now)}
}

const (
	hour = 60
	day  = 24 * hour
)

func workBox(now time.Time) Mailbox {
	b := &builder{mb: Mailbox{Account: work.Email, Labels: []model.Label{
		{ID: "INBOX", Name: "INBOX", System: true}, {ID: "SENT", Name: "SENT", System: true},
		{ID: "Label_1", Name: "Clients", Color: "#16a766"},
		{ID: "Label_2", Name: "Clients/Northwind", Color: "#4a86e8"},
		{ID: "Label_3", Name: "Builds", Color: "#a479e2"},
		{ID: "Label_4", Name: "Receipts", Color: "#ffad47"},
	}}, now: now}
	me := []model.Address{work}

	b.add("t-launch", 3*day, maya, []model.Address{work, tom}, "Launch plan for the 2.0 release",
		"Hi both,\n\nHere's the draft plan for 2.0. The big items:\n\n• Freeze features on the 28th\n• Two weeks of beta with the Northwind team\n• Docs and the migration guide in parallel\n\nCan you each take a look and add your estimates by Friday?\n\nThanks,\nMaya",
		"INBOX")
	b.add("t-launch", 2*day, tom, []model.Address{maya, work}, "Re: Launch plan for the 2.0 release",
		"Looks good. I'd add a day for the release notes, last time they took longer than we thought.\n\nOn Tue, Maya Chen wrote:\n> Can you each take a look and add your estimates by Friday?",
		"INBOX")
	b.add("t-launch", 25, maya, []model.Address{work, tom}, "Re: Launch plan for the 2.0 release",
		"Agreed, added. @you: could you own the migration guide? I've put a first outline in the shared folder.\n\n> I'd add a day for the release notes",
		"INBOX", "UNREAD")
	b.attach(&b.mb.Messages[len(b.mb.Messages)-1], "launch-plan-v2.pdf", "Launch plan v2 (demo attachment)")

	b.add("t-north", 4*hour, priya, me, "Northwind: questions about the beta",
		"Hello,\n\nThanks for the invitation to the beta. A few questions before we sign up:\n\n1. Will our existing exports keep working?\n2. Is there a sandbox we can try first?\n3. Who should our admins contact during the two weeks?\n\nBest regards,\nPriya Raman\nHead of Operations, Northwind",
		"INBOX", "UNREAD", "Label_1", "Label_2", "IMPORTANT")

	b.add("t-ci", 50, ci, me, "✗ main failed: integration tests (#4821)",
		"Build #4821 failed on main.\n\nFailing step: integration tests\n  store_test.go:142: Threads = 3, want 2\n\nView the build[1]\n\n[1] https://builds.example/climail/4821",
		"INBOX", "UNREAD", "Label_3")
	b.add("t-ci2", 30, ci, me, "✓ main passed (#4822)", "Build #4822 passed on main in 3m 12s.", "INBOX", "Label_3")

	b.add("t-review", 6*hour, tom, me, "Can you review #312 when you get a chance?",
		"It's the change to how we page through large mailboxes. Nothing urgent, but it blocks the sidebar work.\n\nhttps://code.example/climail/pull/312",
		"INBOX", "STARRED")

	b.add("t-offsite", 1*day, hr, []model.Address{addr("Everyone", "all@work.example")}, "Team offsite: pick your sessions",
		"Hi everyone,\n\nThe offsite agenda is ready. Please pick two afternoon sessions by Wednesday so we can plan the rooms.\n\nMorning: keynote and roadmap\nAfternoon: workshops (see the form)\nEvening: dinner by the river\n\nThe People Team",
		"INBOX")

	b.add("t-news", 2*day, news, me, "Issue 142: the joy of keyboard-driven tools",
		"This week: why the best tools get out of your way[1], a deep dive into terminal rendering[2], and five shortcuts you'll use every day.\n\nRead in your browser[3]\n\n[1] https://weekly.example/142/keyboards\n[2] https://weekly.example/142/rendering\n[3] https://weekly.example/142",
		"INBOX")

	b.add("t-standup", 3*hour, cal, me, "Invitation: Design review @ Thu 15:00",
		"Design review\nThursday 15:00 – 16:00\nRoom: Orion\n\nAgenda: the new reader layout and keyboard shortcuts.",
		"INBOX", "UNREAD")

	b.add("t-invoice", 5*day, addr("Billing", "billing@saas.example"), me, "Your invoice for October",
		"Thanks for your business. Your invoice for October is attached.\n\nAmount: €49.00\nPaid with: card ending 4242",
		"Label_4")
	b.attach(&b.mb.Messages[len(b.mb.Messages)-1], "invoice-october.pdf", "Invoice: October, EUR 49.00 (demo)")

	b.add("t-sent", 7*hour, work, []model.Address{priya}, "Re: Northwind kickoff notes",
		"Thanks Priya, notes attached. Let me know if I missed anything.", "SENT", "Label_1")

	draft := b.add("t-draft", 90, work, []model.Address{maya}, "Ideas for the offsite",
		"Hi Maya,\n\nA few ideas for the offsite sessions:\n\n• A hands-on session on the new CLI", "DRAFT")
	draft.HTML = `<div dir="ltr">Hi Maya,<div><br></div><div>A few ideas for the <b>offsite</b> sessions:</div>` +
		`<ul><li>A hands-on session on the <a href="https://cli.example/docs">new CLI</a></li></ul></div>` // as Gmail writes it

	for i := range 12 { // older mail, so the list scrolls
		b.add(fmt.Sprintf("t-old%02d", i), (6+i)*day, []model.Address{maya, tom, hr, news}[i%4], me,
			[]string{"Weekly sync notes", "Quick question about the API", "Reminder: expenses", "Issue %d of The Weekly Terminal"}[i%4],
			"Older message kept for scrolling through a longer list.", "INBOX")
		if i%4 == 3 {
			b.mb.Messages[len(b.mb.Messages)-1].Subject = fmt.Sprintf("Issue %d of The Weekly Terminal", 141-i/4)
		}
	}
	return b.mb
}

func homeBox(now time.Time) Mailbox {
	b := &builder{mb: Mailbox{Account: home.Email, Labels: []model.Label{
		{ID: "INBOX", Name: "INBOX", System: true},
		{ID: "Label_9", Name: "Travel", Color: "#fb4c2f"},
	}}, now: now}
	me := []model.Address{home}

	b.add("h-sunday", 90, mum, me, "Sunday lunch?",
		"Are you coming on Sunday? I'm making the lemon cake. Bring Alex if you like!\n\nLove, Mum", "INBOX", "UNREAD")
	b.add("h-trip", 2*day, travel, me, "Your booking is confirmed: LIS → AMS",
		"Booking reference: K7Q2XP\n\nFri 12 Dec, 08:40 Lisbon → 12:55 Amsterdam\nSeat 14A\n\nCheck in online 24 hours before departure.",
		"INBOX", "STARRED", "Label_9")
	b.attach(&b.mb.Messages[len(b.mb.Messages)-1], "boarding-pass.pdf", "Boarding pass LIS-AMS, seat 14A (demo)")
	b.add("h-bank", 5*hour, bank, me, "Your monthly statement is ready", "Your November statement is ready to view in online banking.", "INBOX", "UNREAD")
	b.add("h-alex", 26*hour, alex, me, "Board games Friday?",
		"We're thinking of trying the new one Sam got. 8pm at mine?", "INBOX")
	b.add("h-alex", 20*hour, home, []model.Address{alex}, "Re: Board games Friday?", "I'm in! I'll bring snacks.\n\n> 8pm at mine?", "SENT")
	b.add("h-books", 3*day, shop, me, "Your order has shipped", "Your order #20391 (2 books) has shipped and should arrive on Tuesday.", "INBOX")
	return b.mb
}
