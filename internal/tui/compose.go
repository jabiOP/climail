package tui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jabiOP/climail/internal/compose"
)

// composing is a mail being written: in the editor, or back from it and
// waiting for y (send), e (edit again) or q (keep the draft).
type composing struct {
	base  compose.Draft // the template, and what the file doesn't hold
	path  string
	draft compose.Draft // as edited
	err   error         // why it can't be sent as it is
	asked bool          // back from the editor: the question is showing

	unreadable bool // the file couldn't be read as a mail: it stays on disk
}

type editedMsg struct{ err error }

// sentMsg reports a mail queued to send, or a draft queued to save.
type sentMsg struct {
	summary string
	from    string // the account it goes from
	saving  bool
	path    string // the draft file, kept when saving failed
	err     error
}

// startCompose writes the draft and opens it in the editor; the TUI steps
// aside until the editor exits.
func (m Model) startCompose(base compose.Draft) (tea.Model, tea.Cmd) {
	path, err := compose.WriteDraft(m.deps.DraftDir, base, m.deps.Now())
	if err != nil {
		m.message, m.isError = "couldn't write the draft: "+err.Error(), true
		return m, nil
	}
	m.composing = &composing{base: base, path: path}
	return m, m.editCmd()
}

func (m Model) editCmd() tea.Cmd {
	return tea.ExecProcess(m.deps.Editor(m.composing.path), func(err error) tea.Msg { return editedMsg{err} })
}

func (m Model) edited(msg editedMsg) (tea.Model, tea.Cmd) {
	c := m.composing
	if c == nil {
		return m, nil
	}
	if msg.err != nil {
		m.composing = nil
		m.message, m.isError = "editor: "+msg.err.Error()+" (set $EDITOR to the one you use)", true
		return m, nil
	}
	text, err := os.ReadFile(c.path)
	if err != nil {
		m.composing = nil
		m.message, m.isError = err.Error(), true
		return m, nil
	}
	if string(text) == c.base.Text() && c.base.DraftID == "" { // a draft continued is ready as it is
		os.Remove(c.path)
		m.composing = nil
		m.message = "Not sent: the draft wasn't changed"
		return m, nil
	}
	next := *c
	next.asked = true
	next.draft, next.err = compose.Parse(string(text), c.base)
	next.unreadable = next.err != nil
	if next.err == nil {
		parsed := next.draft
		next.draft, next.err = m.deps.Service.Prepare(m.ctx, parsed)
		if next.err == nil && next.draft.Text() != parsed.Text() { // names became addresses: show them on "e"
			next.err = os.WriteFile(c.path, []byte(next.draft.Text()), 0o600)
		}
	}
	m.composing = &next
	return m, nil
}

// composeKey answers the question asked after editing.
func (m Model) composeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := m.composing
	switch msg.String() {
	case "y":
		if c.err != nil {
			return m, nil
		}
		m.composing = nil
		svc, ctx, now, d, path := m.deps.Service, m.ctx, m.deps.Now(), c.draft, c.path
		return m, func() tea.Msg {
			err := svc.Send(ctx, d, now)
			if err == nil {
				os.Remove(path)
			}
			return sentMsg{summary: d.Summary(), from: d.Account, err: err}
		}
	case "e":
		return m, m.editCmd()
	case "d": // neither sent nor saved
		m.composing = nil
		os.Remove(c.path)
		m.message = "Discarded"
		if c.base.DraftID != "" {
			m.message = "Discarded your edits; the draft stays in Drafts as it was"
		}
		return m, nil
	case "q", "n", "esc":
		m.composing = nil
		if c.unreadable {
			m.message = "Not saved. The draft is in " + c.path
			return m, nil
		}
		svc, ctx, now, d, path := m.deps.Service, m.ctx, m.deps.Now(), c.draft, c.path
		return m, func() tea.Msg {
			err := svc.SaveDraft(ctx, d, now)
			if err == nil {
				os.Remove(path)
			}
			return sentMsg{saving: true, path: path, err: err}
		}
	}
	return m, nil
}

// composePrompt is the status bar's question after editing.
func (m Model) composePrompt() (string, bool) {
	c := m.composing
	switch {
	case c.unreadable:
		return c.err.Error() + " · e edit · q leave it on disk · d discard", true
	case c.err != nil:
		return c.err.Error() + " · e edit · q save to Drafts · d discard", true
	}
	return "From " + c.draft.Account + ": send " + c.draft.Summary() + "? y send · e edit · q save to Drafts · d discard", false
}
