package tui

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
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

// composeMsg is a draft whose attachments were fetched first.
type composeMsg struct {
	draft compose.Draft
	err   error // the attachments couldn't be fetched; the draft goes without them
}

// download fetches attachments of the current conversation into dir off the UI goroutine.
func (m Model) download(refs []model.AttachmentRef, dir string, open bool) tea.Cmd {
	if m.deps.Download == nil {
		return nil
	}
	t, _ := m.current()
	dl, ctx, account := m.deps.Download, m.ctx, t.Account
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

// pointedAttachments are the attachments of the message ▶ marks.
func (m Model) pointedAttachments() []model.AttachmentRef {
	t, _ := m.current()
	msg, _ := m.pointed()
	return attachmentsOf(t, msg.ID)
}

// attachmentsOf are the attachments of one of the conversation's messages.
func attachmentsOf(t model.Thread, messageID string) []model.AttachmentRef {
	var refs []model.AttachmentRef
	for _, r := range t.Attachments() {
		if r.MessageID == messageID {
			refs = append(refs, r)
		}
	}
	return refs
}

// saveAll saves every attachment of the message ▶ marks.
func (m Model) saveAll() (tea.Model, tea.Cmd) {
	refs := m.pointedAttachments()
	if len(refs) == 0 {
		m.message = "no attachments here"
		return m, nil
	}
	m.message = "Downloading…"
	return m, m.download(refs, m.deps.DownloadDir, false)
}

// openAttachment opens the attachment, or asks which when there are several.
func (m Model) openAttachment() (tea.Model, tea.Cmd) {
	refs := m.pointedAttachments()
	switch {
	case len(refs) == 0:
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

// forward drafts a forward of the message ▶ marks, fetching its attachments first.
func (m Model) forward() (tea.Model, tea.Cmd) {
	t, _ := m.current()
	msg, _ := m.pointed()
	return m.composeWith(t, msg.ID, "forward-", func(paths []string) compose.Draft {
		return compose.Forward(msg, t.Account, paths, m.deps.Now())
	})
}

// continueDraft opens the conversation's draft in the editor, its
// attachments fetched first.
func (m Model) continueDraft() (tea.Model, tea.Cmd) {
	t, ok := m.current()
	if !ok {
		return m, nil
	}
	i := slices.IndexFunc(t.Messages, func(msg model.Message) bool { return msg.Has(model.Draft) })
	if i < 0 {
		return m.openThread()
	}
	d := t.Messages[i]
	if _, lossy := compose.DraftBody(d); lossy {
		m.askDraft = &d
		return m, nil
	}
	return m.editDraft(t, d)
}

// askDraftKey answers whether to edit a draft whose formatting Markdown
// can't hold: e edits it anyway, O opens it in Gmail on the web.
func (m Model) askDraftKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	d := *m.askDraft
	m.askDraft = nil
	switch msg.String() {
	case "e":
		t, _ := m.current()
		return m.editDraft(t, d)
	case "O":
		return m, m.openLink(fmt.Sprintf("https://mail.google.com/mail/u/%s/#drafts?compose=%s", url.PathEscape(d.Account), d.ID))
	}
	return m, nil // anything else cancels
}

func (m Model) editDraft(t model.Thread, d model.Message) (tea.Model, tea.Cmd) {
	return m.composeWith(t, d.ID, "draft-", func(paths []string) compose.Draft { return compose.FromDraft(d, t.Account, paths) })
}

// composeWith opens a draft made with the attachments of one of the
// conversation's messages, downloaded into a folder of their own.
func (m Model) composeWith(t model.Thread, messageID, dirPrefix string, draft func(paths []string) compose.Draft) (tea.Model, tea.Cmd) {
	refs := attachmentsOf(t, messageID)
	if len(refs) == 0 || m.deps.Download == nil {
		return m.startCompose(draft(nil))
	}
	m.message = "Fetching the attachments…"
	dl, ctx, account := m.deps.Download, m.ctx, t.Account
	dir := filepath.Join(m.deps.OpenDir, dirPrefix+m.deps.Now().Format("20060102-150405.000"))
	return m, func() tea.Msg {
		paths, err := dl(ctx, account, refs, dir)
		return composeMsg{draft: draft(paths), err: err}
	}
}
