package tui

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/service"
)

func (m Model) wide() bool { return m.width >= wideWidth }

// sidebarShown: the mailboxes are on screen beside the list.
func (m Model) sidebarShown() bool { return m.wide() && !m.hideSidebar }

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Keys typed fast can arrive together ("gt"): outside a text field, take them one by one.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste && !m.searching {
		var cmds []tea.Cmd
		var next tea.Model = m
		for _, r := range msg.Runes {
			var cmd tea.Cmd
			next, cmd = next.(Model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			cmds = append(cmds, cmd)
		}
		return next, tea.Batch(cmds...)
	}
	if m.help {
		m.help = false
		return m, nil
	}
	if m.searching {
		return m.handleSearchKey(msg)
	}
	if m.composing != nil {
		return m.composeKey(msg)
	}
	if m.choosing != nil {
		return m.chooseKey(msg)
	}
	if m.askDraft != nil {
		return m.askDraftKey(msg)
	}
	m.message, m.isError = "", false
	if p := m.pending; p != "" {
		m.pending = ""
		k := msg.String()
		i := slices.IndexFunc(goTo, func(g struct{ key, box, name string }) bool { return g.key == k })
		switch {
		case p == "d" && k == "d":
			return m.act(service.Trash)
		case p == "g" && k == "g":
			return m.top()
		case p == "g" && i >= 0:
			return m.openBox(goTo[i].box)
		}
		return m, nil // an unknown pair does nothing, like vim
	}
	switch {
	case key.Matches(msg, keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, keys.Help):
		m.help = true
		return m, nil
	case key.Matches(msg, keys.Sync):
		if m.deps.Sync == nil {
			return m, nil
		}
		m.syncing = true
		return m, m.syncCmd()
	case key.Matches(msg, keys.Search):
		m.searching = true
		m.input.SetValue(m.search)
		m.input.CursorEnd()
		return m, m.input.Focus()
	case key.Matches(msg, keys.PrevAccount):
		return m.switchAccount(-1)
	case key.Matches(msg, keys.NextAccount):
		return m.switchAccount(1)
	case key.Matches(msg, keys.Sidebar):
		m.hideSidebar = !m.hideSidebar
		if m.hideSidebar && m.focus == paneSidebar {
			m.focus = paneList
		}
		return m, nil
	case key.Matches(msg, keys.Compose):
		return m.startCompose(compose.Draft{Account: m.account, Body: "\n"})
	case key.Matches(msg, keys.Undo):
		if len(m.undos) == 0 {
			m.message = "nothing to undo"
			return m, nil
		}
		e := m.undos[len(m.undos)-1]
		m.undos = m.undos[:len(m.undos)-1]
		return m, m.undoCmd(e)
	case key.Matches(msg, keys.Top), key.Matches(msg, keys.Trash):
		m.pending = msg.String()
		return m, nil
	}
	switch m.focus {
	case paneSidebar:
		return m.sidebarKey(msg)
	case paneReader:
		return m.readerKey(msg)
	}
	return m.listKey(msg)
}

func (m Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.searching = false
		m.input.Blur()
		if m.search != "" && m.input.Value() == m.search {
			return m, nil // keep the search already shown
		}
		return m.clearSearch()
	case "enter":
		m.searching = false
		m.input.Blur()
		if m.input.Value() == "" {
			return m.clearSearch()
		}
		m.focus = paneList
		if m.deps.Search == nil || m.found != nil {
			return m, nil
		}
		m.asking = true
		search, ctx, account, query := m.deps.Search, m.ctx, m.account, m.search
		return m, func() tea.Msg {
			ids, err := search(ctx, account, query)
			return searchedMsg{query: query, ids: ids, err: err}
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if m.input.Value() == m.search {
		return m, cmd
	}
	if m.search == "" { // starting a search: remember where to come back to
		m.before = m.box
	}
	m.search, m.found, m.asking = m.input.Value(), nil, false
	m.box = service.Mailbox{ID: model.AllMail, Name: "All Mail"}
	if m.search == "" {
		m.box = m.before
	}
	m.cursor, m.offset, m.limit = 0, 0, pageSize
	return m, tea.Batch(cmd, m.load())
}

func (m Model) clearSearch() (tea.Model, tea.Cmd) {
	if m.search == "" {
		return m, nil
	}
	m.search, m.box, m.found, m.asking = "", m.before, nil, false
	m.cursor, m.offset, m.limit = 0, 0, pageSize
	m.input.SetValue("")
	return m, m.load()
}

// --- the list ---

func (m Model) listKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Down):
		return m.moveBy(1)
	case key.Matches(msg, keys.Up):
		return m.moveBy(-1)
	case key.Matches(msg, keys.PageDown):
		return m.moveBy(m.listRows())
	case key.Matches(msg, keys.PageUp):
		return m.moveBy(-m.listRows())
	case key.Matches(msg, keys.HalfDown):
		return m.moveBy(m.listRows() / 2)
	case key.Matches(msg, keys.HalfUp):
		return m.moveBy(-m.listRows() / 2)
	case key.Matches(msg, keys.Bottom):
		return m.moveBy(len(m.threads))
	case key.Matches(msg, keys.Open) && m.box.ID == model.Draft:
		return m.continueDraft()
	case key.Matches(msg, keys.Open), key.Matches(msg, keys.Right):
		return m.openThread()
	case key.Matches(msg, keys.Left):
		if m.sidebarShown() || !m.wide() {
			m.focus = paneSidebar
			m.side = m.boxRow()
		}
		return m, nil
	case key.Matches(msg, keys.Back):
		switch {
		case len(m.selected) > 0:
			m.selected = map[string]bool{}
		case m.search != "":
			return m.clearSearch()
		}
		return m, nil
	case key.Matches(msg, keys.Together):
		return m.readTogether()
	case key.Matches(msg, keys.Select):
		if t, ok := m.current(); ok {
			if m.selected[t.ID] {
				delete(m.selected, t.ID)
			} else {
				m.selected[t.ID] = true
			}
			return m.moveBy(1)
		}
		return m, nil
	}
	return m.threadKey(msg)
}

// threadKey handles the actions on conversations, from the list or the reader.
func (m Model) threadKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	targets := m.targets()
	if len(targets) == 0 {
		return m, nil
	}
	switch {
	case key.Matches(msg, keys.Archive):
		return m.act(service.Archive)
	case key.Matches(msg, keys.Inbox):
		return m.act(service.MoveToInbox)
	case key.Matches(msg, keys.Read):
		if slices.ContainsFunc(targets, model.Thread.Unread) {
			return m.act(service.MarkRead)
		}
		if m.focus == paneReader { // read on, unread: back to the list, as in Gmail
			m.focus, m.together = paneList, nil
		}
		return m.act(service.MarkUnread)
	case key.Matches(msg, keys.Star):
		if slices.ContainsFunc(targets, func(t model.Thread) bool { return !t.Starred() }) {
			return m.act(service.Star)
		}
		return m.act(service.Unstar)
	case key.Matches(msg, keys.Browser):
		return m, m.openInBrowser(targets[0])
	case key.Matches(msg, keys.Reply), key.Matches(msg, keys.ReplyAll):
		t, _ := m.current()
		orig, _ := m.pointed()
		return m.startCompose(compose.Reply(t, orig, m.account, key.Matches(msg, keys.ReplyAll), m.deps.Now()))
	case key.Matches(msg, keys.Forward):
		return m.forward()
	case key.Matches(msg, keys.SaveFiles):
		return m.saveAll()
	case key.Matches(msg, keys.OpenFile):
		return m.openAttachment()
	}
	return m, nil
}

// targets are the conversations an action applies to: the selected ones in
// the list, else the current one.
func (m Model) targets() []model.Thread {
	if m.focus == paneList && len(m.selected) > 0 {
		var out []model.Thread
		for _, t := range m.threads {
			if m.selected[t.ID] {
				out = append(out, t)
			}
		}
		return out
	}
	if t, ok := m.current(); ok {
		return []model.Thread{t}
	}
	return nil
}

// act applies an action to the targets. From the reader, an action that takes
// the conversation out of the mailbox moves on to the next one (on the page,
// when reading together).
func (m Model) act(a service.Action) (tea.Model, tea.Cmd) {
	targets := m.targets()
	if len(targets) == 0 {
		return m, nil
	}
	var on page
	if m.reading() {
		on = page{box: m.box.ID, ids: slices.Clone(m.together), thread: targets[0].ID}
	}
	switch {
	case m.reading() && a.Leaves:
		m.dropTogether(targets[0].ID)
	case m.focus == paneReader && a.Leaves:
		m.openOnLoad = true
	}
	m.selected = map[string]bool{}
	cmd := m.apply(targets, a, true)
	return m, func() tea.Msg {
		msg := cmd()
		if done, ok := msg.(actionDoneMsg); ok {
			done.page = on
			return done
		}
		return msg
	}
}

func (m Model) openInBrowser(t model.Thread) tea.Cmd {
	if m.deps.OpenURL == nil {
		return nil
	}
	return m.openLink(fmt.Sprintf("https://mail.google.com/mail/u/%s/#all/%s", url.PathEscape(t.Account), t.ID))
}

// openLink opens a link in the browser.
func (m Model) openLink(link string) tea.Cmd {
	if m.deps.OpenURL == nil {
		return nil
	}
	open := m.deps.OpenURL
	return func() tea.Msg {
		if err := open(link); err != nil {
			return infoMsg{text: "couldn't open the browser: " + err.Error(), isErr: true}
		}
		return nil
	}
}

func (m Model) openThread() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok {
		return m, nil
	}
	m.focus, m.together = paneReader, nil
	if !t.Unread() {
		return m, nil
	}
	return m, m.apply([]model.Thread{t}, service.MarkRead, false)
}

// readTogether shows several conversations in the reader, one after the
// other, and marks them read: the selected ones, else the unread ones listed.
// The one at the top of the reader is current: actions apply to it alone.
func (m Model) readTogether() (tea.Model, tea.Cmd) {
	threads := m.targets()
	if len(m.selected) == 0 {
		threads = slices.DeleteFunc(slices.Clone(m.threads), func(t model.Thread) bool { return !t.Unread() })
	}
	if len(threads) == 0 {
		m.message = "nothing unread here (x selects conversations to read together)"
		return m, nil
	}
	m.together, m.expanded = nil, map[string]bool{}
	for _, t := range threads {
		m.together = append(m.together, t.ID)
		m.expanded[t.Latest().ID] = true
		for _, msg := range t.Messages {
			if msg.Unread() {
				m.expanded[msg.ID] = true
			}
		}
	}
	m.focus, m.scroll, m.readerID, m.selected, m.pointedID = paneReader, 0, "", map[string]bool{}, ""
	m.scrollBy(0)
	return m, m.apply(threads, service.MarkRead, false)
}

// reading reports whether the reader shows conversations together.
func (m Model) reading() bool { return m.together != nil && m.focus == paneReader }

// togetherThreads are the conversations read together that are still listed.
func (m Model) togetherThreads() []model.Thread {
	var out []model.Thread
	for _, id := range m.together {
		if i := slices.IndexFunc(m.threads, func(t model.Thread) bool { return t.ID == id }); i >= 0 {
			out = append(out, m.threads[i])
		}
	}
	return out
}

// togetherStarts is the reader line each conversation read together starts on.
func (m Model) togetherStarts(w int) []int {
	var starts []int
	n := 0
	for i, t := range m.togetherThreads() {
		if i > 0 {
			n++ // the blank line between them
		}
		starts = append(starts, n)
		lines, _ := m.threadLines(t, w, "")
		n += len(lines) + 1 // and its subject
	}
	return starts
}

// togetherAt is the index of the current conversation among those read together.
func (m Model) togetherAt() int {
	t, _ := m.current()
	return max(slices.IndexFunc(m.togetherThreads(), func(o model.Thread) bool { return o.ID == t.ID }), 0)
}

// showTogether scrolls the conversation at index i, read together, to the top.
func (m *Model) showTogether(i int) {
	if starts := m.togetherStarts(m.readerWidth()); i >= 0 && i < len(starts) {
		m.scroll = starts[i]
		m.scrollBy(0)
	}
}

// dropTogether takes a conversation off the page read together; the next one
// becomes current, or, with none left, the list is back.
func (m *Model) dropTogether(id string) {
	at := m.togetherAt()
	m.together = slices.DeleteFunc(m.together, func(o string) bool { return o == id })
	if len(m.togetherThreads()) == 0 {
		m.focus, m.together = paneList, nil
		m.followReader()
		return
	}
	m.showTogether(min(at, len(m.togetherThreads())-1))
}

// keepTogether, after a load, drops the conversations no longer listed.
func (m *Model) keepTogether() {
	var ids []string
	for _, t := range m.togetherThreads() {
		ids = append(ids, t.ID)
	}
	if len(ids) == 0 {
		m.focus, m.together = paneList, nil
		m.followReader()
		return
	}
	m.together = ids
	for _, t := range m.togetherThreads() { // back on the page: as it opened
		if !slices.ContainsFunc(t.Messages, func(msg model.Message) bool { return m.expanded[msg.ID] }) {
			m.expanded[t.Latest().ID] = true
		}
	}
	if i := slices.Index(ids, m.showOnLoad); i >= 0 {
		m.showTogether(i)
	} else {
		m.scrollBy(0)
	}
	m.showOnLoad = ""
}

// backOnPage, after undoing a change made while reading together, puts the
// conversation back on that page, current: on the page read now, else, still
// in that mailbox's list, the page reopens.
func (m *Model) backOnPage(p page) {
	if p.thread == "" || !m.reading() && (m.focus != paneList || m.box.ID != p.box) {
		return
	}
	keep := map[string]bool{p.thread: true}
	for _, id := range p.ids {
		keep[id] = !m.reading() || keep[id]
	}
	for _, id := range m.together {
		keep[id] = true
	}
	var ids []string
	for _, id := range append(slices.Clone(p.ids), m.together...) {
		if keep[id] {
			ids = append(ids, id)
			keep[id] = false
		}
	}
	m.together, m.focus, m.showOnLoad = ids, paneReader, p.thread
}

// listRows is how many conversations fit on screen.
func (m Model) listRows() int { return max(m.height-2, 1) } // minus the header and the status bar

// top goes to the first conversation, mailbox or line, wherever the focus is.
func (m Model) top() (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneSidebar:
		m.side = 0
	case paneReader:
		m.scroll = 0
	default:
		m = m.moveTo(0)
	}
	return m, nil
}

func (m Model) moveTo(i int) Model {
	m.cursor = max(min(i, len(m.threads)-1), 0)
	m.clampOffset()
	m.followReader()
	return m
}

// moveBy moves the list cursor, loading more conversations near the end.
func (m Model) moveBy(n int) (tea.Model, tea.Cmd) {
	m = m.moveTo(m.cursor + n)
	if m.cursor >= len(m.threads)-m.listRows() && len(m.threads) == m.limit {
		m.limit += pageSize
		return m, m.load()
	}
	return m, nil
}

// clampOffset scrolls the list so the cursor is on screen.
func (m *Model) clampOffset() {
	rows := m.listRows()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	m.offset = max(min(m.offset, len(m.threads)-rows), 0)
}

// --- the reader ---

func (m Model) readerKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	page := m.listRows()
	switch {
	case key.Matches(msg, keys.Down): // scroll; with nothing more to scroll, ▶ moves
		m.scrollOrPoint(1)
	case key.Matches(msg, keys.Up):
		m.scrollOrPoint(-1)
	case key.Matches(msg, keys.PageDown):
		m.scrollBy(page - 1)
	case key.Matches(msg, keys.PageUp):
		m.scrollBy(1 - page)
	case key.Matches(msg, keys.HalfDown):
		m.scrollBy(page / 2)
	case key.Matches(msg, keys.HalfUp):
		m.scrollBy(-page / 2)
	case key.Matches(msg, keys.Bottom):
		m.scrollBy(1 << 20)
	case key.Matches(msg, keys.Back), key.Matches(msg, keys.Left):
		m.focus, m.together = paneList, nil
		m.followReader()
	case key.Matches(msg, keys.Next), key.Matches(msg, keys.Prev):
		n := 1
		if key.Matches(msg, keys.Prev) {
			n = -1
		}
		if m.reading() {
			m.showTogether(m.togetherAt() + n)
			return m, nil
		}
		before := m.cursor
		next, cmd := m.moveBy(n)
		m = next.(Model)
		if m.cursor == before {
			return m, cmd
		}
		_, open := m.openThread()
		return m, tea.Batch(cmd, open)
	case key.Matches(msg, keys.Open): // open or close the ▶ message
		p, ok := m.pointed()
		if !ok {
			return m, nil
		}
		m.expanded[p.ID] = !m.expanded[p.ID]
		m.pointedID = p.ID
		m.pointBy(0)
	case key.Matches(msg, keys.NextMessage):
		m.pointBy(1)
	case key.Matches(msg, keys.PrevMessage):
		m.pointBy(-1)
	case key.Matches(msg, keys.Expand):
		t, ok := m.current()
		if !ok {
			return m, nil
		}
		all := !slices.ContainsFunc(t.Messages, func(msg model.Message) bool { return !m.expanded[msg.ID] })
		if !m.reading() {
			m.expanded = map[string]bool{}
		}
		for _, msg := range t.Messages { // read together, the others stay as they are
			m.expanded[msg.ID] = !all || msg.ID == t.Latest().ID
		}
		m.scrollBy(0)
	default:
		return m.threadKey(msg)
	}
	return m, nil
}

// scrollBy scrolls the reader. Read together, the conversation at the top is
// current, and the last one can be scrolled to the top.
// scrollOrPoint scrolls the reader a line, or, when it can't scroll that
// way, moves ▶ to the next or previous message.
func (m *Model) scrollOrPoint(n int) {
	before := m.scroll
	m.scrollBy(n)
	if m.scroll == before {
		m.pointBy(n)
	}
}

func (m *Model) scrollBy(n int) {
	w := m.readerWidth()
	end := len(m.readerLines(w)) - m.listRows()
	if !m.reading() {
		m.scroll = max(min(m.scroll+n, end), 0)
		return
	}
	starts := m.togetherStarts(w)
	if len(starts) == 0 {
		return // before the load that puts them back
	}
	m.scroll = max(min(m.scroll+n, max(end, starts[len(starts)-1])), 0)
	at := 0
	for i, s := range starts {
		if s <= m.scroll {
			at = i
		}
	}
	id := m.togetherThreads()[at].ID
	m.cursor = slices.IndexFunc(m.threads, func(t model.Thread) bool { return t.ID == id })
	m.clampOffset()
}

// --- the sidebar ---

// sideRow is one sidebar line: an account, or one of its mailboxes.
type sideRow struct {
	account string
	box     *service.Mailbox
}

func (m Model) sideRows() []sideRow {
	var rows []sideRow
	for _, a := range m.accounts {
		rows = append(rows, sideRow{account: a})
		if a == m.account {
			for i := range m.boxes {
				rows = append(rows, sideRow{account: a, box: &m.boxes[i]})
			}
		}
	}
	return rows
}

// boxRow is the sidebar row of the mailbox shown.
func (m Model) boxRow() int {
	for i, r := range m.sideRows() {
		if r.box != nil && r.box.ID == m.box.ID {
			return i
		}
	}
	return 0
}

func (m Model) sidebarKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	rows := m.sideRows()
	switch {
	case key.Matches(msg, keys.Down):
		m.side = min(m.side+1, len(rows)-1)
	case key.Matches(msg, keys.Up):
		m.side = max(m.side-1, 0)
	case key.Matches(msg, keys.Bottom):
		m.side = len(rows) - 1
	case key.Matches(msg, keys.Open), key.Matches(msg, keys.Right):
		if m.side >= len(rows) {
			return m, nil
		}
		r := rows[m.side]
		if r.box == nil {
			if r.account == m.account {
				return m, nil
			}
			return m.useAccount(r.account)
		}
		return m.openBox(r.box.ID)
	case key.Matches(msg, keys.Back):
		m.focus = paneList
	}
	return m, nil
}

// openBox shows a mailbox in the list.
func (m Model) openBox(id string) (tea.Model, tea.Cmd) {
	box := service.Mailbox{ID: id}
	for _, b := range m.boxes {
		if b.ID == id {
			box = b
		}
	}
	m.box, m.search, m.focus, m.found, m.asking = box, "", paneList, nil, false
	m.input.SetValue("")
	m.cursor, m.offset, m.limit, m.selected = 0, 0, pageSize, map[string]bool{}
	m.side = m.boxRow()
	return m, m.load()
}

func (m Model) switchAccount(step int) (tea.Model, tea.Cmd) {
	if len(m.accounts) < 2 {
		m.message = "only one account (add one with: climail auth login)"
		return m, nil
	}
	i := slices.Index(m.accounts, m.account)
	return m.useAccount(m.accounts[(i+step+len(m.accounts))%len(m.accounts)])
}

func (m Model) useAccount(account string) (tea.Model, tea.Cmd) {
	m.account, m.boxes, m.threads = account, nil, nil
	m.undos = nil // undo is per account
	m.message = account
	return m.openBox(model.Inbox)
}
