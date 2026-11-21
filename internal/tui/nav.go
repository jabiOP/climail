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
		if m.undo == nil {
			m.message = "nothing to undo"
			return m, nil
		}
		u := *m.undo
		m.undo = nil
		return m, m.undoCmd(u)
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
			m.focus = paneList
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
		return m.startCompose(compose.Reply(t, m.account, key.Matches(msg, keys.ReplyAll), m.deps.Now()))
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
// the conversation out of the mailbox moves on to the next one.
func (m Model) act(a service.Action) (tea.Model, tea.Cmd) {
	targets := m.targets()
	if len(targets) == 0 {
		return m, nil
	}
	if m.focus == paneReader && a.Leaves {
		m.openOnLoad = true
	}
	m.selected = map[string]bool{}
	return m, m.apply(targets, a, true)
}

func (m Model) openInBrowser(t model.Thread) tea.Cmd {
	if m.deps.OpenURL == nil {
		return nil
	}
	link := fmt.Sprintf("https://mail.google.com/mail/u/%s/#all/%s", url.PathEscape(t.Account), t.ID)
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
	m.focus = paneReader
	if !t.Unread() {
		return m, nil
	}
	return m, m.apply([]model.Thread{t}, service.MarkRead, false)
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
	case key.Matches(msg, keys.Down):
		m.scrollBy(1)
	case key.Matches(msg, keys.Up):
		m.scrollBy(-1)
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
		m.focus = paneList
	case key.Matches(msg, keys.Next), key.Matches(msg, keys.Prev):
		n := 1
		if key.Matches(msg, keys.Prev) {
			n = -1
		}
		before := m.cursor
		next, cmd := m.moveBy(n)
		m = next.(Model)
		if m.cursor == before {
			return m, cmd
		}
		_, open := m.openThread()
		return m, tea.Batch(cmd, open)
	case key.Matches(msg, keys.Expand):
		t, ok := m.current()
		if !ok {
			return m, nil
		}
		all := len(m.expanded) == len(t.Messages)
		m.expanded = map[string]bool{t.Latest().ID: true}
		if !all {
			for _, msg := range t.Messages {
				m.expanded[msg.ID] = true
			}
		}
		m.scrollBy(0)
	default:
		return m.threadKey(msg)
	}
	return m, nil
}

func (m *Model) scrollBy(n int) {
	lines := len(m.readerLines(m.readerWidth()))
	m.scroll = max(min(m.scroll+n, lines-m.listRows()), 0)
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
	m.undo = nil // undo is per account
	m.message = account
	return m.openBox(model.Inbox)
}
