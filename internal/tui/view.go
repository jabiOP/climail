package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
)

// layout is the width of each pane; 0 means hidden.
type layout struct{ side, list, reader int }

const sidebarWidth = 28

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
	rest := m.width - 1 // the separator between list and reader
	if m.sidebarShown() {
		l.side = sidebarWidth
		rest -= sidebarWidth + 1
	}
	l.list = rest / 2
	l.reader = rest - l.list
	return l
}

func (m Model) readerWidth() int {
	if w := m.layout().reader; w > 0 {
		return w
	}
	return m.width
}

func (m Model) View() string {
	h := max(m.height-1, 1) // the status bar takes the last line
	if m.help {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, helpBox.Render(helpText()),
			lipgloss.WithWhitespaceChars(" "))
	}
	l := m.layout()
	var cols [][]string
	if l.side > 0 {
		cols = append(cols, m.sidebar(l.side, h))
	}
	if l.list > 0 {
		cols = append(cols, m.list(l.list, h))
	}
	if l.reader > 0 {
		cols = append(cols, m.reader(l.reader, h))
	}
	sep := ruleStyle.Render("│")
	lines := make([]string, h)
	for i := range lines {
		parts := make([]string, len(cols))
		for c, col := range cols {
			parts[c] = col[i]
		}
		lines[i] = strings.Join(parts, sep)
	}
	return strings.Join(append(lines, m.statusBar()), "\n")
}

// paneTitle draws a pane's first line: accented when the pane has the focus.
func (m Model) paneTitle(p pane, left, right string, w int) string {
	st := bold
	if p == m.focus {
		st = title
	}
	rw := width(right)
	return row(plain, s(" ", plain), s(fit(left, max(w-rw-2, 0)), st), s(fitRight(right, rw)+" ", dim))
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
	lines := []string{m.paneTitle(paneSidebar, "climail", "", w)}
	rows := m.sideRows()
	visible := h - 1
	offset := max(m.side-visible+1, 0)
	for i := offset; i < len(rows) && len(lines) < h; i++ {
		r := rows[i]
		base := plain
		if i == m.side && m.focus == paneSidebar {
			base = lipgloss.NewStyle().Background(cursorBg)
		}
		if r.box == nil { // an account
			mark, st, count := "▸ ", dim, ""
			if r.account == m.account {
				mark, st = "▾ ", bold
			} else if n := m.others[r.account]; n > 0 {
				count = fmt.Sprint(n)
			}
			lines = append(lines, row(base, s(" "+mark, st), s(fit(r.account, w-4-width(count)), st),
				s(count+" ", accentFg)))
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
			st = title
		}
		nameW := w - 2 - width(indent) - width(dot) - width(count) - 1
		lines = append(lines, row(base, s(" "+indent, plain), s(dot, dotStyle), s(fit(b.Name, nameW)+" ", st),
			s(count, st), s(" ", plain)))
	}
	return pad(lines, w, h)
}

// --- conversation list ---

func (m Model) listTitle(w int) string {
	if m.searching {
		in := m.input
		in.Width = max(w-4, 1)
		return " " + fit(in.View(), w-1)
	}
	left := m.box.Name
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
	right := ""
	if len(m.accounts) > 1 || !m.sidebarShown() {
		right = m.account
	}
	return m.paneTitle(paneList, left, right, w)
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

func (m Model) list(w, h int) []string {
	lines := []string{m.listTitle(w)}
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
		return append(lines, centered("Downloading your mail…", w, h-1)...)
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
func (m Model) listRow(t model.Thread, isCursor bool, w, dateW int, now time.Time) string {
	base := plain
	bar := " "
	if isCursor {
		bar = "▌"
		if m.focus == paneList {
			base = lipgloss.NewStyle().Background(cursorBg)
		}
	}
	mark, markStyle := "  ", accentFg
	switch {
	case m.selected[t.ID]:
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
	restW := w - 1 - 2 - 2 - fromW - 2 - 3 - dateW - 1 // bar, mark, star, from, gap, clip+gap, date, edge
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
		s(bar, accentFg), s(mark, markStyle), s(star, starStyle),
		s(fromText, text), s(count, dim), s("  ", plain),
		s(fit(subject, subjW), text), s(snippet, dim),
		s(" "+clip+" ", dim), s(fitRight(output.ShortDate(t.Latest().Date, now), dateW), text), s(" ", plain))
}

// --- reader ---

func (m Model) reader(w, h int) []string {
	t, ok := m.current()
	if !ok {
		return append([]string{m.paneTitle(paneReader, "", "", w)}, centered("No conversation selected", w, h-1)...)
	}
	left, right := output.Subject(t.Subject()), ""
	if n := len(t.Messages); n > 1 {
		right = plural(n, "message")
	}
	if m.reading() {
		left, right = fmt.Sprintf("Together %d/%d · %s", m.togetherAt()+1, len(m.togetherThreads()), left), ""
	}
	lines := []string{m.paneTitle(paneReader, left, right, w)}
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
	for _, b := range m.boxes {
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
		mark := "  "
		if o.ID == t.ID {
			mark = "▶ "
		}
		pointed := ""
		if o.ID == t.ID {
			pointed = compose.ReplyTarget(o, o.Account).ID
		}
		body, _ := m.threadLines(o, w, pointed)
		lines := append([]string{row(plain, s(" ", plain), s(fit(mark+output.Subject(o.Subject()), max(w-2, 10)), title), s(" ", plain))},
			body...)
		if o.ID == t.ID {
			barLines(lines)
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
		barLines(out[from:to])
	}
	return out, starts
}

// barLines draws an accent bar down the left edge of lines, in their margin.
func barLines(lines []string) {
	for i, l := range lines {
		if strings.HasPrefix(l, " ") { // not already barred
			lines[i] = accentFg.Render("▌") + strings.TrimPrefix(l, " ")
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

// --- status bar ---

func (m Model) statusBar() string {
	right := m.syncText()
	leftW := max(m.width-width(right)-3, 0)
	var left seg
	switch {
	case m.composing != nil && m.composing.asked:
		text, isErr := m.composePrompt()
		st := accentFg
		if isErr {
			st = errStyle
		}
		left = s(fit(text, leftW), st.Bold(true))
	case m.choosing != nil:
		left = s(fit(m.choosePrompt(), leftW), accentFg.Bold(true))
	case m.askDraft != nil:
		left = s(fit("Fonts, colours → Gmail default · e edit · O open in Gmail · esc", leftW), accentFg.Bold(true))
	case m.message != "" && m.isError:
		left = s(fit(m.message, leftW), errStyle)
	case m.message != "":
		left = s(fit(m.message, leftW), accentFg)
	case m.pending != "":
		left = s(fit(m.pending+"…", leftW), accentFg)
	default:
		left = s(fit(m.hints(), leftW), dim)
	}
	rightStyle := dim
	if m.syncError != "" {
		rightStyle = errStyle
	}
	return row(plain, s(" ", plain), left, s(" "+right+" ", rightStyle))
}

func (m Model) hints() string {
	switch m.focus {
	case paneSidebar:
		return "enter open · [ ] accounts · l list · ? help"
	case paneReader:
		if m.reading() {
			return "j/k scroll · n/p next/prev · a archive this · r reply · esc back · ? help"
		}
		return "j/k scroll · n/p next/prev · r reply · f forward · a archive · dd trash · esc back · ? help"
	}
	return "enter open · c compose · r reply · a archive · dd trash · * star · x select · / search · ? help"
}

func (m Model) syncText() string {
	var parts []string
	switch {
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
