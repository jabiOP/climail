package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jabiOP/climail/internal/config"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
	"github.com/jabiOP/climail/internal/service"
)

// layout is the width of each pane; 0 means hidden.
type layout struct{ side, list, reader int }

const sidebarWidth = 30 // with its border

func (m Model) layout() layout {
	if !m.wide() { // one pane at a time
		switch m.focus {
		case paneSidebar:
			return layout{side: m.width}
		case paneReader:
			return layout{reader: m.width}
		}
		return layout{list: m.width}
	}
	l := layout{}
	rest := m.width
	if m.sidebarShown() {
		l.side = sidebarWidth
		rest -= sidebarWidth
	}
	l.list = rest / 2
	l.reader = rest - l.list
	return l
}

// readerWidth is the width the reader draws in, inside its border when boxed.
func (m Model) readerWidth() int {
	if w := m.layout().reader; w > 0 && m.wide() {
		return w - 2
	}
	return m.width
}

func (m Model) View() string {
	status := m.statusLines()
	h := max(m.height-len(status), 1) // the status bar takes the last lines
	if m.help {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpBox.Render(helpText()),
			lipgloss.WithWhitespaceChars(" "))
	}
	if m.log != nil {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpBox.Render(m.logText()),
			lipgloss.WithWhitespaceChars(" "))
	}
	if m.picking != "" {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpBox.Render(m.pickText()),
			lipgloss.WithWhitespaceChars(" "))
	}
	l := m.layout()
	var cols [][]string
	for _, c := range []struct {
		p pane
		w int
	}{{paneSidebar, l.side}, {paneList, l.list}, {paneReader, l.reader}} {
		switch {
		case c.w == 0:
		case m.wide():
			cols = append(cols, m.boxed(c.p, c.w, h))
		default:
			cols = append(cols, m.paneLines(c.p, c.w, h))
		}
	}
	lines := make([]string, h)
	for i := range lines {
		for _, col := range cols {
			lines[i] += col[i]
		}
	}
	return strings.Join(append(lines, status...), "\n")
}

func (m Model) paneLines(p pane, w, h int) []string {
	switch p {
	case paneSidebar:
		return m.sidebar(w, h)
	case paneReader:
		return m.reader(w, h)
	}
	return m.list(w, h)
}

// boxed draws a pane inside a border, its title in the top edge. The pane
// with the focus has a double border in the account's color, so where you
// are shows at a glance.
func (m Model) boxed(p pane, w, h int) []string {
	body := m.paneLines(p, w-2, h-1)[1:] // its title row goes in the border
	edge, v, hz, tl, tr, bl, br := ruleStyle, "│", "─", "╭", "╮", "╰", "╯"
	titleSt := bold
	if p == m.focus {
		edge, v, hz, tl, tr, bl, br = m.hiFg(), "║", "═", "╔", "╗", "╚", "╝"
		titleSt = m.hiFg().Bold(true)
	}
	left, right, rightSt := m.titleParts(p, w-6)
	segs := []seg{s(tl+hz, edge)}
	room := w - 4 // the corners and an edge either side
	if right != "" {
		right = fit(right, min(width(right), max(room/2-2, 0)))
		room -= width(right) + 2
	}
	if left != "" && room > 2 {
		left = fit(left, min(width(left), room-2))
		segs = append(segs, s(" ", edge), s(left, titleSt), s(" ", edge))
		room -= width(left) + 2
	}
	segs = append(segs, s(strings.Repeat(hz, max(room, 0)), edge))
	if right != "" {
		segs = append(segs, s(" "+right+" ", rightSt))
	}
	out := []string{row(plain, append(segs, s(hz+tr, edge))...)}
	for _, l := range body {
		out = append(out, edge.Render(v)+l+edge.Render(v))
	}
	return append(out, edge.Render(bl+strings.Repeat(hz, max(w-2, 0))+br))
}

// titleParts are a pane's title (left) and its note (right), w cells at most.
func (m Model) titleParts(p pane, w int) (left, right string, rightSt lipgloss.Style) {
	switch p {
	case paneSidebar:
		return "climail", "", dim
	case paneReader:
		t, ok := m.current()
		if !ok || m.noPreview {
			return "", "", dim
		}
		left = output.Subject(t.Subject())
		if n := len(t.Messages); n > 1 {
			right = plural(n, "message")
		}
		if m.reading() {
			left, right = fmt.Sprintf("Together %d/%d · %s", m.togetherAt()+1, len(m.togetherThreads()), left), ""
		}
		return left, right, dim
	}
	return m.listTitleParts(w)
}

// paneTitle draws a pane's first line: in the account's color when the pane has the focus.
func (m Model) paneTitle(p pane, w int) string {
	if p == paneList && m.searching {
		in := m.input
		in.Width = max(w-4, 1)
		return " " + fit(in.View(), w-1)
	}
	left, right, rightSt := m.titleParts(p, w)
	st := bold
	if p == m.focus {
		st = m.hiFg().Bold(true)
	}
	rw := width(right)
	return row(plain, s(" ", plain), s(fit(left, max(w-rw-2, 0)), st), s(fitRight(right, rw)+" ", rightSt))
}

// pad fills lines up to h with blank lines of width w.
func pad(lines []string, w, h int) []string {
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return lines[:h]
}

// centered is a pane's message when it has nothing to show.
func centered(text string, w, h int) []string {
	lines := pad(nil, w, h)
	if h > 2 {
		lines[h/3] = dim.Render(fit(strings.Repeat(" ", max((w-width(text))/2, 0))+text, w))
	}
	return lines
}

// --- sidebar ---

func (m Model) sidebar(w, h int) []string {
	lines := []string{m.paneTitle(paneSidebar, w)}
	rows := m.sideRows()
	visible := h - 1
	offset := max(m.side-visible+1, 0)
	for i := offset; i < len(rows) && len(lines) < h; i++ {
		r := rows[i]
		base := plain
		if i == m.side && m.focus == paneSidebar {
			base = lipgloss.NewStyle().Background(m.hiBg())
		}
		if r.box == nil { // an account, or all of them
			mark, st, count, dot := "▸ ", dim, "", "● "
			if r.account == service.AllAccounts {
				dot = ""
			}
			if r.account == m.shown() {
				mark, st = "▾ ", bold
			} else if n := m.unreadIn(r.account); n > 0 {
				count = fmt.Sprint(n)
			}
			lines = append(lines, row(base, s(" "+mark, st), s(dot, m.accountStyle(r.account)),
				s(fit(accountName(r.account), w-4-width(dot)-width(count)), st), s(count+" ", accentFg)))
			continue
		}
		b := r.box
		indent := strings.Repeat("  ", 1+b.Depth)
		dot, dotStyle := "", plain
		if b.Color != "" {
			dot, dotStyle = "● ", labelStyle(b.Color)
		}
		st, count := plain, ""
		if b.Unread > 0 {
			st, count = bold, fmt.Sprint(b.Unread)
		}
		if b.ID == m.box.ID && m.search == "" {
			st = m.hiFg().Bold(true)
		}
		nameW := w - 2 - width(indent) - width(dot) - width(count) - 1
		lines = append(lines, row(base, s(" "+indent, plain), s(dot, dotStyle), s(fit(b.Name, nameW)+" ", st),
			s(count, st), s(" ", plain)))
	}
	return pad(lines, w, h)
}

// unreadIn is how many conversations are unread in an account's Inbox, or in all of them.
func (m Model) unreadIn(account string) int {
	if account != service.AllAccounts {
		return m.others[account]
	}
	n := 0
	for _, c := range m.others {
		n += c
	}
	return n
}

// --- conversation list ---

// listTitleParts says what the list shows, and whose it is when that isn't obvious.
func (m Model) listTitleParts(w int) (left, right string, rightSt lipgloss.Style) {
	if m.searching { // typing, in the border: the cursor is drawn by hand
		return "/ " + m.input.Value() + "█", "", dim
	}
	left = m.box.Name
	var notes []string
	switch {
	case m.search != "" && m.asking:
		left = fmt.Sprintf("Search “%s”", m.search)
		notes = append(notes, "asking Gmail…")
	case m.search != "" && m.found != nil:
		left = fmt.Sprintf("Search “%s” on Gmail", m.search)
		notes = append(notes, plural(len(m.threads), "conversation"))
	case m.search != "":
		left = fmt.Sprintf("Search “%s”", m.search)
		notes = append(notes, plural(len(m.threads), "conversation"))
	}
	if m.search == "" && m.box.Unread > 0 {
		notes = append(notes, fmt.Sprintf("%d unread", m.box.Unread))
	}
	if n := len(m.selected); n > 0 {
		notes = append(notes, fmt.Sprintf("%d selected", n))
	}
	if len(notes) > 0 {
		left += " · " + strings.Join(notes, " · ")
	}
	rightSt = dim
	if len(m.accounts) > 1 || !m.sidebarShown() {
		right, rightSt = accountName(m.shown()), m.accountStyle(m.shown())
	}
	return left, right, rightSt
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func (m Model) list(w, h int) []string {
	lines := []string{m.paneTitle(paneList, w)}
	switch {
	case !m.loaded:
		return append(lines, centered("Loading…", w, h-1)...)
	case len(m.threads) == 0 && m.asking:
		return append(lines, centered("Asking Gmail…", w, h-1)...)
	case len(m.threads) == 0 && m.found != nil:
		return append(lines, centered(fmt.Sprintf("Gmail found nothing for “%s”", m.search), w, h-1)...)
	case len(m.threads) == 0 && m.search != "":
		return append(lines, centered(fmt.Sprintf("Nothing matches “%s”", m.search), w, h-1)...)
	case len(m.threads) == 0 && m.status.LastSync.IsZero() && m.syncing:
		text := "Downloading your mail…"
		if p := m.progress; p.total > 0 {
			text = fmt.Sprintf("Downloading %s: %d of %d conversations…", accountName(p.account), p.done, p.total)
		}
		return append(lines, centered(text, w, h-1)...)
	case len(m.threads) == 0:
		return append(lines, centered("No conversations in "+m.box.Name, w, h-1)...)
	}
	now := m.deps.Now()
	dateW := 0
	end := min(m.offset+h-1, len(m.threads))
	for _, t := range m.threads[m.offset:end] {
		dateW = max(dateW, width(output.ShortDate(t.Latest().Date, now)))
	}
	for i := m.offset; i < end; i++ {
		lines = append(lines, m.listRow(m.threads[i], i == m.cursor, w, dateW, now))
	}
	return pad(lines, w, h)
}

// listRow draws one conversation:
//
//	▌● ★ Alice, Bob (3)   Subject — snippet               📎 Nov 3
//
// Showing every account, a stripe in the account's color follows the bar.
func (m Model) listRow(t model.Thread, isCursor bool, w, dateW int, now time.Time) string {
	base := plain
	bar := " "
	if isCursor {
		bar = "▌"
		if m.focus == paneList {
			base = lipgloss.NewStyle().Background(m.hiBg())
		}
	}
	stripe := ""
	if m.all {
		stripe = "▌ "
		w -= width(stripe)
	}
	mark, markStyle := "  ", accentFg
	switch {
	case m.selected[threadKey(t)]:
		mark = "✓ "
	case t.Unread():
		mark = "● "
	}
	star := "  "
	if t.Starred() {
		star = "★ "
	}
	clip := "  "
	if t.HasAttachments() {
		clip = "📎"
	}
	text := plain
	if t.Unread() {
		text = bold
	}
	from := output.Senders(t)
	count := ""
	if n := len(t.Messages); n > 1 {
		count = fmt.Sprintf(" %d", n)
	}
	fromW := max(min(w/4, 24), 10)
	restW := w - 1 - 2 - 2 - fromW - 2 - 4 - dateW - 1 // bar, mark, star, from, gap, gap+clip+gap, date, edge
	subject := output.Subject(t.Subject())
	subjW := min(width(subject), max(restW, 0))
	snippet := ""
	if restW-subjW > 3 {
		snippet = fit(" — "+t.Latest().Snippet, restW-subjW)
	} else {
		subjW = max(restW, 0)
	}
	fromText := fit(from, fromW-width(count))
	return row(base,
		s(bar, m.hiFg()), s(stripe, m.accountStyle(t.Account)), s(mark, markStyle), s(star, starStyle),
		s(fromText, text), s(count, dim), s("  ", plain),
		s(fit(subject, subjW), text), s(snippet, dim),
		s(" "+clip+" ", dim), s(fitRight(output.ShortDate(t.Latest().Date, now), dateW), text), s(" ", plain))
}

// --- reader ---

func (m Model) reader(w, h int) []string {
	if _, ok := m.current(); !ok || m.noPreview {
		return append([]string{m.paneTitle(paneReader, w)}, centered("No conversation selected", w, h-1)...)
	}
	lines := []string{m.paneTitle(paneReader, w)}
	body := m.readerLines(w)
	end := min(m.scroll+h-1, len(body))
	if m.scroll < end {
		lines = append(lines, body[m.scroll:end]...)
	}
	return pad(lines, w, h)
}

// shownLabels are the labels worth showing on a conversation, by name.
func (m Model) shownLabels(t model.Thread) []string {
	names := map[string]string{}
	for _, b := range m.boxesOf[t.Account] {
		names[b.ID] = b.Path
	}
	var out []string
	for _, id := range t.Labels() {
		switch id {
		case model.Unread, model.Sent, model.Draft, model.Important:
			continue
		}
		if name, ok := names[id]; ok {
			out = append(out, name)
		}
	}
	return out
}

// readerLines draws the whole conversation, or the ones read together under
// their subjects, the current one with a bar down its left edge, w cells
// wide; the reader scrolls over it.
func (m Model) readerLines(w int) []string {
	t, ok := m.current()
	if !ok {
		return nil
	}
	if !m.reading() {
		pointed, _ := m.pointed()
		lines, _ := m.threadLines(t, w, pointed.ID)
		return lines
	}
	var out []string
	for i, o := range m.togetherThreads() {
		if i > 0 {
			out = append(out, strings.Repeat(" ", w))
		}
		current := threadKey(o) == threadKey(t)
		mark := "  "
		if current {
			mark = "▶ "
		}
		pointed := ""
		if current {
			msg, _ := m.pointed()
			pointed = msg.ID
		}
		body, _ := m.threadLines(o, w, pointed)
		lines := append([]string{row(plain, s(" ", plain), s(fit(mark+output.Subject(o.Subject()), max(w-2, 10)), title), s(" ", plain))},
			body...)
		if current {
			barLines(lines, m.hiFg())
		}
		out = append(out, lines...)
	}
	return out
}

// threadLines draws one conversation, w cells wide, and the line each
// message starts on. The pointed message is marked when there is more than
// one: ▶ on its sender, a bar down its left edge.
func (m Model) threadLines(t model.Thread, w int, pointed string) (out []string, starts []int) {
	target := ""
	if len(t.Messages) > 1 {
		target = pointed
	}
	cw := max(w-2, 10) // a space of margin each side
	line := func(segs ...seg) string {
		return row(plain, append([]seg{s(" ", plain)}, append(segs, s(" ", plain))...)...)
	}
	blank := line(s(fit("", cw), plain))
	if labels := m.shownLabels(t); len(labels) > 0 {
		out = append(out, line(s(fit(strings.Join(labels, " · "), cw), dim)))
	}
	now := m.deps.Now()
	from, to := -1, -1 // the target's lines
	for i, msg := range t.Messages {
		if from >= 0 && to < 0 {
			to = len(out)
		}
		if i > 0 || len(out) > 0 {
			out = append(out, line(s(strings.Repeat("─", cw), ruleStyle)))
		}
		starts = append(starts, len(out))
		if msg.ID == target {
			from = len(out)
		}
		date := output.MessageDate(msg.Date, now)
		arrow := ""
		if msg.ID == target {
			arrow = "▶ "
		}
		if !m.expanded[msg.ID] {
			nameW := min(max(cw/3, 12), 24)
			snipW := max(cw-nameW-width(date)-2, 0)
			out = append(out, line(s(fit(arrow+msg.From.Short(), nameW)+" ", bold), s(fit(msg.Snippet, snipW)+" ", dim),
				s(date, dim)))
			continue
		}
		name, email := arrow+msg.From.Name, " <"+msg.From.Email+">"
		if msg.From.Name == "" {
			name, email = arrow+msg.From.Email, ""
		}
		nameW := max(cw-width(date)-1, 0)
		name = fit(name, min(width(name), nameW))
		out = append(out, line(s(name, bold), s(fit(email, nameW-width(name)), dim), s(" "+date, dim)))
		for _, hdr := range m.recipientLines(msg, t.Account) {
			out = append(out, line(s(fit(hdr, cw), dim)))
		}
		out = append(out, blank)
		out = append(out, bodyLines(msg.Body, cw, line)...)
		out = append(out, blank)
	}
	if from >= 0 {
		if to < 0 {
			to = len(out)
		}
		barLines(out[from:to], m.hiFg())
	}
	return out, starts
}

// barLines draws a bar down the left edge of lines, in their margin.
func barLines(lines []string, st lipgloss.Style) {
	for i, l := range lines {
		if strings.HasPrefix(l, " ") { // not already barred
			lines[i] = st.Render("▌") + strings.TrimPrefix(l, " ")
		}
	}
}

// recipientLines are Gmail's compact "to me, Tom Okafor" lines, and the attachments.
func (m Model) recipientLines(msg model.Message, account string) []string {
	names := func(as []model.Address) string {
		out := make([]string, len(as))
		for i, a := range as {
			out[i] = a.Short()
			if a.Email == account {
				out[i] = "me"
			}
		}
		return strings.Join(out, ", ")
	}
	var out []string
	if len(msg.To) > 0 {
		out = append(out, "to "+names(msg.To))
	}
	if len(msg.Cc) > 0 {
		out = append(out, "cc "+names(msg.Cc))
	}
	for _, a := range msg.Attachments {
		out = append(out, "📎 "+a.Filename+" · "+output.Size(a.Size))
	}
	return out
}

// bodyLines wraps a message body to cw cells, dimming quoted text and link footnotes.
func bodyLines(body string, cw int, line func(...seg) string) []string {
	if strings.TrimSpace(body) == "" {
		return []string{line(s(fit("(no text)", cw), dim))}
	}
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(body, "\t", "    "), "\n") {
		st := plain
		trimmed := strings.TrimSpace(para)
		if strings.HasPrefix(trimmed, ">") || isFootnote(trimmed) {
			st = quoted
		}
		for _, l := range strings.Split(ansi.Wrap(para, cw, ""), "\n") {
			out = append(out, line(s(fit(l, cw), st)))
		}
	}
	return out
}

// isFootnote spots the "[3] https://…" lines HTML mail's links become.
func isFootnote(l string) bool {
	var n int
	var rest string
	_, err := fmt.Sscanf(l, "[%d] %s", &n, &rest)
	return err == nil && strings.HasPrefix(rest, "http")
}

// logText is the L pop-up: what Gmail rejected lately, newest first.
func (m Model) logText() string {
	lines := []string{title.Render("What Gmail rejected"), ""}
	w := max(min(m.width-8, 110), 20)
	for _, e := range m.log {
		when := e.Time.In(m.deps.Now().Location()).Format("Jan 2 15:04")
		lines = append(lines, dim.Render(when+"  "+accountName(e.Account))+"  "+fit(e.Message, max(w-width(when+accountName(e.Account))-4, 10)))
	}
	if len(m.log) == 0 {
		lines = append(lines, "Nothing yet.")
	}
	return strings.Join(append(lines, "", dim.Render("climail log shows more · press any key to close")), "\n")
}

// pickText is the C pop-up: the colors to give an account.
func (m Model) pickText() string {
	lines := []string{title.Render("Color for " + accountName(m.picking)), ""}
	line := ""
	for i, c := range config.Colors {
		if i > 0 && c.Row != config.Colors[i-1].Row {
			lines, line = append(lines, line), ""
		}
		mark := "  "
		if i == m.pickAt {
			mark = "▶ "
		}
		line += accentFg.Render(mark) + lipgloss.NewStyle().Foreground(lipgloss.Color(c.Hex)).Render("● ") + fit(c.Name, 15)
	}
	lines = append(lines, line, "", dim.Render("h/j/k/l move · enter choose · esc cancel"))
	return strings.Join(lines, "\n")
}

// --- status bar ---

// statusLeft is what the status bar says on its left: a question, a message,
// or (when there's nothing to say) the keys to try, which alone may be cut.
func (m Model) statusLeft() (text string, st lipgloss.Style, cut bool) {
	switch {
	case m.composing != nil && m.composing.asked:
		text, isErr := m.composePrompt()
		if isErr {
			return text, errStyle.Bold(true), false
		}
		return text, accentFg.Bold(true), false
	case m.choosing != nil:
		return m.choosePrompt(), accentFg.Bold(true), false
	case m.askDraft != nil:
		return "Fonts, colours → Gmail default · e edit · O open in Gmail · esc", accentFg.Bold(true), false
	case m.message != "" && m.isError:
		return m.message, errStyle, false
	case m.message != "":
		return m.message, accentFg, false
	case m.pending != "":
		return m.pending + "…", accentFg, false
	}
	return m.hints(), dim, true
}

// statusLines draws the status bar: one line, or more when a message or the
// sync state doesn't fit, so nothing that matters is cut (up to a third of
// the screen).
func (m Model) statusLines() []string {
	text, st, cut := m.statusLeft()
	right, rightSt := m.syncText(), dim
	if m.syncError != "" || m.rejected > 0 {
		rightSt = errStyle
	}
	w, rw := m.width, width(right)
	switch {
	case width(text)+rw+3 <= w, cut && rw+3 <= w-10: // one line; hints may be cut
		return []string{row(plain, s(" ", plain), s(fit(text, max(w-rw-3, 0)), st), s(" "+right+" ", rightSt))}
	case cut: // the sync state needs the room: one line of hints above it
		text = fit(text, w-2)
	}
	lefts := strings.Split(ansi.Wrap(text, max(w-2, 1), ""), "\n")
	var out []string
	for _, l := range lefts[:len(lefts)-1] {
		out = append(out, row(plain, s(" ", plain), s(fit(l, w-2), st), s(" ", plain)))
	}
	last := lefts[len(lefts)-1]
	if width(last)+rw+3 <= w { // the sync state fits beside the end of the message
		return m.mostStatus(append(out, row(plain, s(" ", plain), s(fit(last, w-rw-3), st), s(" "+right+" ", rightSt))))
	}
	out = append(out, row(plain, s(" ", plain), s(fit(last, w-2), st), s(" ", plain)))
	for _, l := range strings.Split(ansi.Wrap(right, max(w-2, 1), ""), "\n") {
		out = append(out, row(plain, s(fitRight(l, w-1), rightSt), s(" ", plain)))
	}
	return m.mostStatus(out)
}

// mostStatus keeps the status bar to a third of the screen: its last lines,
// which say what to do.
func (m Model) mostStatus(out []string) []string {
	if most := max(m.height/3, 1); len(out) > most {
		out = out[len(out)-most:] // the end says what to do
	}
	return out
}

func (m Model) hints() string {
	switch m.focus {
	case paneSidebar:
		return "enter open · J/K accounts · C color · l list · ? help"
	case paneReader:
		if m.reading() {
			return "j/k scroll · n/p next/prev · a archive this · r reply all · esc back · ? help"
		}
		return "j/k scroll · n/p next/prev · r reply all · f forward · a archive · dd trash · esc back · ? help"
	}
	return "enter open · c compose · r reply all · a archive · dd trash · * star · x select · / search · ? help"
}

func (m Model) syncText() string {
	var parts []string
	switch {
	case m.syncing && m.progress.total > 0:
		parts = append(parts, fmt.Sprintf("⟳ %s: downloading %d/%d", accountName(m.progress.account), m.progress.done, m.progress.total))
	case m.syncing:
		parts = append(parts, "⟳ syncing")
	case m.syncError != "":
		parts = append(parts, m.syncError)
	case m.status.LastSync.IsZero():
		parts = append(parts, "not synced yet")
	default:
		parts = append(parts, "synced "+ago(m.deps.Now().Sub(m.status.LastSync)))
	}
	if m.status.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d to send", m.status.Pending))
	}
	if m.rejected > 0 {
		parts = append(parts, fmt.Sprintf("%d rejected: L to see", m.rejected))
	}
	return strings.Join(parts, " · ")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
