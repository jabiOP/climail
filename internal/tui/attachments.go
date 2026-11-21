package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
)

type downloadedMsg struct {
	paths []string
	open  bool // open the file once saved
	err   error
}

type forwardMsg struct {
	draft compose.Draft
	err   error // the attachments couldn't be fetched; the forward goes without them
}

// download fetches attachments into dir off the UI goroutine.
func (m Model) download(refs []model.AttachmentRef, dir string, open bool) tea.Cmd {
	if m.deps.Download == nil {
		return nil
	}
	dl, ctx, account := m.deps.Download, m.ctx, m.account
	return func() tea.Msg {
		paths, err := dl(ctx, account, refs, dir)
		return downloadedMsg{paths: paths, open: open, err: err}
	}
}

func (m Model) downloaded(msg downloadedMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.err != nil:
		m.message, m.isError = msg.err.Error(), true
	case msg.open && len(msg.paths) == 1:
		m.message = "Opened " + filepath.Base(msg.paths[0])
		if m.deps.OpenURL != nil {
			if err := m.deps.OpenURL(msg.paths[0]); err != nil {
				m.message, m.isError = "couldn't open it: "+err.Error(), true
			}
		}
	case len(msg.paths) == 1:
		m.message = fmt.Sprintf("Saved %s in %s", filepath.Base(msg.paths[0]), tilde(filepath.Dir(msg.paths[0])))
	default:
		m.message = fmt.Sprintf("Saved %d files in %s", len(msg.paths), tilde(filepath.Dir(msg.paths[0])))
	}
	return m, nil
}

// tilde shortens a path in the home folder to "~/…".
func tilde(path string) string {
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
			return "~/" + rest
		}
	}
	return path
}

// saveAll saves every attachment of the current conversation.
func (m Model) saveAll() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok || len(t.Attachments()) == 0 {
		m.message = "no attachments here"
		return m, nil
	}
	m.message = "Downloading…"
	return m, m.download(t.Attachments(), m.deps.DownloadDir, false)
}

// openAttachment opens the attachment, or asks which when there are several.
func (m Model) openAttachment() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	refs := t.Attachments()
	switch {
	case !ok || len(refs) == 0:
		m.message = "no attachments here"
		return m, nil
	case len(refs) == 1:
		return m, m.download(refs, m.deps.OpenDir, true)
	}
	m.choosing = refs
	return m, nil
}

// chooseKey picks the attachment to open by its number.
func (m Model) chooseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	refs := m.choosing
	m.choosing = nil
	k := msg.String()
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' && int(k[0]-'0') <= len(refs) {
		return m, m.download(refs[k[0]-'1':k[0]-'0'], m.deps.OpenDir, true)
	}
	return m, nil // anything else cancels
}

func (m Model) choosePrompt() string {
	var parts []string
	for i, r := range m.choosing[:min(len(m.choosing), 9)] {
		parts = append(parts, fmt.Sprintf("%d %s", i+1, r.Filename))
	}
	return "Open: " + strings.Join(parts, " · ") + " · esc cancel"
}

// forward drafts a forward, fetching the latest message's attachments first.
func (m Model) forward() (tea.Model, tea.Cmd) {
	t, _ := m.current()
	var refs []model.AttachmentRef
	for _, r := range t.Attachments() {
		if r.MessageID == t.Latest().ID {
			refs = append(refs, r)
		}
	}
	if len(refs) == 0 || m.deps.Download == nil {
		return m.startCompose(compose.Forward(t, m.account, nil, m.deps.Now()))
	}
	m.message = "Fetching the attachments to forward…"
	dl, ctx, account, now := m.deps.Download, m.ctx, m.account, m.deps.Now()
	dir := filepath.Join(m.deps.OpenDir, "forward-"+now.Format("20060102-150405.000"))
	return m, func() tea.Msg {
		paths, err := dl(ctx, account, refs, dir)
		return forwardMsg{draft: compose.Forward(t, account, paths, now), err: err}
	}
}
