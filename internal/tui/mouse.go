package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
)

// handleMouse turns clicks and the wheel into what the keys do: a click
// selects a mailbox, conversation or message where enter would open it, and
// opens it when it already was; the wheel moves the list and scrolls the reader.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if !m.deps.Mouse || msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if m.help || m.log != nil { // any click closes them
		m.help, m.log = false, nil
		return m, nil
	}
	if m.searching || m.composing != nil || m.choosing != nil || m.links != nil || m.askDraft != nil || m.picking != "" {
		return m, nil // questions and the search are answered with keys
	}
	p, ok := m.paneAt(msg.X)
	row := msg.Y - 1 // under the pane's title
	if !ok || row < 0 || row >= m.listRows() {
		return m, nil // the frame and the status bar
	}
	m.message, m.isError = "", false
	switch msg.Button {
	case tea.MouseButtonWheelDown, tea.MouseButtonWheelUp:
		n := 1
		if msg.Button == tea.MouseButtonWheelUp {
			n = -1
		}
		switch p {
		case paneList:
			return m.moveBy(n)
		case paneReader:
			m.scrollBy(n)
		}
		return m, nil
	case tea.MouseButtonLeft:
		switch p {
		case paneSidebar:
			return m.clickSidebar(row)
		case paneList:
			return m.clickList(row)
		}
		return m.clickReader(row)
	}
	return m, nil
}

// paneAt is the pane drawn at column x.
func (m Model) paneAt(x int) (pane, bool) {
	l := m.layout()
	for _, c := range []struct {
		p pane
		w int
	}{{paneSidebar, l.side}, {paneList, l.list}, {paneReader, l.reader}} {
		if x < c.w {
			return c.p, true
		}
		x -= c.w
	}
	return 0, false
}

func (m Model) clickSidebar(row int) (tea.Model, tea.Cmd) {
	i := max(m.side-m.listRows()+1, 0) + row // as sidebar() scrolls it
	if i >= len(m.sideRows()) {
		return m, nil
	}
	m.focus, m.side = paneSidebar, i
	return m.sidebarKey(enter)
}

func (m Model) clickList(row int) (tea.Model, tea.Cmd) {
	i := m.offset + row
	if i >= len(m.threads) {
		return m, nil
	}
	if m.focus == paneList && i == m.cursor {
		return m.listKey(enter)
	}
	m.focus = paneList
	return m.moveBy(i - m.cursor)
}

// clickReader opens the conversation shown, and on a message's first line,
// opens or closes that message (one conversation at a time; read together,
// the click only opens).
func (m Model) clickReader(row int) (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok || m.noPreview {
		return m, nil
	}
	var cmd tea.Cmd
	if m.focus != paneReader {
		var next tea.Model
		next, cmd = m.openThread()
		m = next.(Model)
	}
	if m.reading() {
		return m, cmd
	}
	_, starts := m.threadLines(t, m.readerWidth(), "")
	i := slices.Index(starts, m.scroll+row)
	if i < 0 {
		return m, cmd
	}
	m.pointedID = t.Messages[i].ID
	next, _ := m.readerKey(enter)
	return next, cmd
}

var enter = tea.KeyMsg{Type: tea.KeyEnter}
