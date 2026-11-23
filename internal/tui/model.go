// Package tui is the Bubble Tea interface: mailboxes, a conversation list and
// a reader, side by side on wide terminals and one at a time on narrow ones.
// Update never blocks: anything touching the store runs in a tea.Cmd and
// reports back with a message.
package tui

import (
	"context"
	"errors"
	"net/url"
	"os/exec"
	"slices"
	"time"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/service"
	"github.com/jabiOP/climail/internal/store"
)

// Deps are the TUI's connections to the rest of the app.
type Deps struct {
	Service      *service.Service
	Sync         func(context.Context) error // syncs every account; nil disables syncing (tests)
	Now          func() time.Time
	SyncInterval time.Duration      // 0 disables periodic sync (tests)
	OpenURL      func(string) error // opens a link in the browser; nil when there's none
	DraftDir     string             // where drafts are written for the editor
	Editor       func(path string) *exec.Cmd
	// Download saves attachments into dir and returns their paths; nil: no downloads.
	Download    func(ctx context.Context, account string, refs []model.AttachmentRef, dir string) ([]string, error)
	DownloadDir string // where D saves
	OpenDir     string // where attachments opened or forwarded are fetched to
	// Search asks Gmail and returns matching conversation IDs, fetched into
	// the cache; nil: local search only.
	Search func(ctx context.Context, account, query string) ([]string, error)
}

type pane int

const (
	paneSidebar pane = iota
	paneList
	paneReader
)

// wideWidth is the narrowest terminal that shows the panes side by side.
const wideWidth = 120

// pageSize is how many more conversations load when the list nears its end.
const pageSize = 100

type Model struct {
	deps          Deps
	ctx           context.Context
	width, height int
	focus         pane
	hideSidebar   bool // b: more room for the list and reader on wide screens

	accounts []string
	account  string            // the account shown
	others   map[string]int    // unread in each account's Inbox, for the sidebar
	boxes    []service.Mailbox // the account's mailboxes
	box      service.Mailbox   // the mailbox shown
	side     int               // sidebar cursor: index into sideRows()

	threads []model.Thread
	cursor  int // list selection
	offset  int // first list row shown
	limit   int // how many conversations to load
	loaded  bool
	loadSeq int // only the newest load is applied (search loads on every key)

	selected map[string]bool // x: thread IDs acted on together

	readerID   string          // the conversation the reader shows
	scroll     int             // reader: first line shown
	expanded   map[string]bool // reader: message IDs shown in full
	openOnLoad bool            // after archiving from the reader: open the next conversation
	together   []string        // T: the conversations the reader shows on one page, by ID; nil: one
	showOnLoad string          // reading together: the conversation to make current after the next load
	pointedID  string          // tab, shift+tab: the message ▶ marks, when in the current conversation

	search    string   // the search shown ("" for a mailbox)
	searching bool     // typing a search
	found     []string // the conversations Gmail found for the search, in its order; nil: local matches
	asking    bool     // waiting for Gmail's answer
	input     textinput.Model
	before    service.Mailbox // the mailbox to return to when the search is cleared

	status    service.Status
	syncing   bool
	syncError string
	checkPend bool // after a sync: retry soon if changes are still waiting (the daemon held the lock)
	retrying  bool // a retryMsg is on its way
	message   string
	isError   bool
	undos     []undoEntry // the changes u undoes, the newest last
	pending   string      // first key of a two-key command: "d" or "g"
	help      bool
	composing *composing            // a mail being written
	choosing  []model.AttachmentRef // A: which attachment to open
	askDraft  *model.Message        // a draft whose formatting Markdown can't hold: edit it anyway?
}

// --- messages ---

type loadedMsg struct {
	seq      int
	accounts []string
	account  string
	others   map[string]int
	boxes    []service.Mailbox
	threads  []model.Thread
	status   service.Status
	err      error
}

type syncDoneMsg struct{ err error }
type tickMsg struct{}

// retryMsg retries a sync while local changes are still waiting.
type retryMsg struct{}

// pushRetry is how soon that is. Tests lower it.
var pushRetry = 3 * time.Second

type actionDoneMsg struct {
	undo   service.Undo
	record bool // offer it for undo
	page   page
	err    error
}

// page is where a change was made while reading together, so that undoing it
// puts the conversation back on the page.
type page struct {
	box    string   // the mailbox read
	ids    []string // the conversations on the page then
	thread string   // the one changed
}

// undoEntry is one change u can undo.
type undoEntry struct {
	undo service.Undo
	page page
}

// undoneMsg reports an undo.
type undoneMsg struct {
	page page
	err  error
}

// maxUndos is how many changes u can undo, newest first.
const maxUndos = 50

type searchedMsg struct {
	query string
	ids   []string
	err   error
}

type infoMsg struct {
	text  string
	isErr bool
}

func New(ctx context.Context, d Deps) Model {
	in := textinput.New()
	in.Prompt = "/ "
	in.Placeholder = "search all mail"
	in.Cursor.SetMode(cursor.CursorStatic)
	return Model{
		deps: d, ctx: ctx, width: 80, height: 24, focus: paneList, limit: pageSize,
		box: service.Mailbox{ID: model.Inbox, Name: "Inbox"}, selected: map[string]bool{}, input: in,
		syncing: d.Sync != nil, // Init starts one
		loadSeq: 1,             // Init's load
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadCmd(m.loadSeq), m.syncCmd(), m.tick(), tea.SetWindowTitle("climail"))
}

// --- commands (run off the UI goroutine) ---

// load reloads everything shown from the store.
func (m *Model) load() tea.Cmd {
	m.loadSeq++
	return m.loadCmd(m.loadSeq)
}

func (m Model) loadCmd(seq int) tea.Cmd {
	svc, ctx := m.deps.Service, m.ctx
	account, view, limit, found := m.account, store.View{Label: m.box.ID, Search: m.search}, m.limit, m.found
	return func() tea.Msg {
		msg := loadedMsg{seq: seq, others: map[string]int{}}
		if msg.accounts, msg.err = svc.Accounts(ctx); msg.err != nil {
			return msg
		}
		if len(msg.accounts) == 0 {
			return msg
		}
		msg.account = account
		if !slices.Contains(msg.accounts, account) {
			msg.account = msg.accounts[0]
		}
		for _, a := range msg.accounts {
			boxes, err := svc.Mailboxes(ctx, a)
			if err != nil {
				msg.err = err
				return msg
			}
			msg.others[a] = boxes[0].Unread // Inbox comes first
			if a == msg.account {
				msg.boxes = boxes
			}
		}
		if found != nil && msg.account == account {
			msg.threads, msg.err = svc.ThreadsByID(ctx, account, found)
		} else {
			msg.threads, msg.err = svc.Threads(ctx, msg.account, view, limit, 0)
		}
		if msg.err != nil {
			return msg
		}
		msg.status, msg.err = svc.Status(ctx, msg.account)
		return msg
	}
}

func (m Model) syncCmd() tea.Cmd {
	if m.deps.Sync == nil {
		return nil
	}
	sync, ctx := m.deps.Sync, m.ctx
	return func() tea.Msg { return syncDoneMsg{err: sync(ctx)} }
}

func (m Model) tick() tea.Cmd {
	if m.deps.SyncInterval <= 0 {
		return nil
	}
	return tea.Tick(m.deps.SyncInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// apply runs an action on threads; record offers it for undo.
func (m Model) apply(threads []model.Thread, a service.Action, record bool) tea.Cmd {
	svc, ctx, account := m.deps.Service, m.ctx, m.account
	return func() tea.Msg {
		undo, err := svc.Apply(ctx, account, threads, a)
		return actionDoneMsg{undo: undo, record: record, err: err}
	}
}

func (m Model) undoCmd(e undoEntry) tea.Cmd {
	svc, ctx := m.deps.Service, m.ctx
	return func() tea.Msg {
		return undoneMsg{page: e.page, err: svc.Undo(ctx, e.undo)}
	}
}

// syncErrorText says briefly why a sync failed: a network failure is just
// "offline" (changes wait), anything else as it is.
func syncErrorText(err error) string {
	var urlErr *url.Error
	if errors.As(err, &urlErr) || errors.Is(err, context.DeadlineExceeded) {
		return "offline"
	}
	return "sync failed: " + err.Error()
}

// --- update ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampOffset()
		return m, nil
	case loadedMsg:
		if msg.seq != m.loadSeq {
			return m, nil // a newer load is on its way
		}
		return m.applyLoaded(msg)
	case syncDoneMsg:
		m.syncing = false
		m.syncError = ""
		if msg.err != nil {
			m.syncError = syncErrorText(msg.err)
		}
		m.checkPend = msg.err == nil // offline: wait for the next tick instead
		return m, m.load()
	case retryMsg:
		m.retrying = false
		m.syncing = m.deps.Sync != nil
		return m, m.syncCmd()
	case tickMsg:
		m.syncing = m.deps.Sync != nil
		return m, tea.Batch(m.syncCmd(), m.tick())
	case actionDoneMsg:
		if msg.err != nil {
			m.message, m.isError = msg.err.Error(), true
			return m, m.load()
		}
		if msg.record && !msg.undo.Empty() {
			m.undos = append(m.undos, undoEntry{msg.undo, msg.page})
			m.undos = m.undos[max(len(m.undos)-maxUndos, 0):]
			m.message, m.isError = msg.undo.Done+" · u to undo", false
		}
		m.syncing = m.deps.Sync != nil
		return m, tea.Batch(m.load(), m.syncCmd())
	case searchedMsg:
		if msg.query != m.search {
			return m, nil // the search changed meanwhile
		}
		m.asking = false
		if msg.err != nil {
			m.message, m.isError = "couldn't search Gmail ("+syncErrorText(msg.err)+"): showing local matches", true
			return m, nil
		}
		m.found = append([]string{}, msg.ids...)
		m.cursor, m.offset = 0, 0
		return m, m.load()
	case downloadedMsg:
		return m.downloaded(msg)
	case composeMsg:
		m.message = ""
		if msg.err != nil {
			m.message, m.isError = "the attachments aren't included: "+msg.err.Error(), true
		}
		next, cmd := m.startCompose(msg.draft)
		if msg.err != nil { // keep the warning visible after the editor
			n := next.(Model)
			n.message, n.isError = m.message, true
			return n, cmd
		}
		return next, cmd
	case editedMsg:
		return m.edited(msg)
	case sentMsg:
		switch {
		case msg.err != nil && msg.saving:
			m.message, m.isError = "not saved: "+msg.err.Error()+". The draft is in "+msg.path, true
			return m, nil
		case msg.err != nil:
			m.message, m.isError = "not sent: "+msg.err.Error(), true
			return m, nil
		case msg.saving:
			m.message, m.isError = "Saved in Drafts", false
		default:
			m.message, m.isError = "Sending "+msg.summary, false
		}
		m.syncing = m.deps.Sync != nil
		return m, tea.Batch(m.load(), m.syncCmd())
	case undoneMsg:
		m.message, m.isError = "Undone", false
		if msg.err != nil {
			m.message, m.isError = "undo failed: "+msg.err.Error(), true
		} else {
			m.backOnPage(msg.page)
		}
		m.syncing = m.deps.Sync != nil
		return m, tea.Batch(m.load(), m.syncCmd())
	case infoMsg:
		m.message, m.isError = msg.text, msg.isErr
		m.syncing = m.deps.Sync != nil
		return m, tea.Batch(m.load(), m.syncCmd())
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// applyLoaded applies fresh data, keeping the cursor on the same conversation.
func (m Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.message, m.isError = "couldn't read the mail cache: "+msg.err.Error(), true
		return m, nil
	}
	var keep string
	if t, ok := m.current(); ok {
		keep = t.ID
	}
	if msg.account != m.account {
		keep = ""
	}
	m.accounts, m.account, m.others, m.boxes, m.status = msg.accounts, msg.account, msg.others, msg.boxes, msg.status
	m.threads, m.loaded = msg.threads, true
	if i := slices.IndexFunc(m.threads, func(t model.Thread) bool { return t.ID == keep }); i >= 0 {
		m.cursor = i
	}
	m.cursor = max(min(m.cursor, len(m.threads)-1), 0)
	for _, b := range m.boxes { // the mailbox's name and count may have changed
		if b.ID == m.box.ID {
			m.box = b
		}
	}
	if m.focus != paneSidebar {
		m.side = m.boxRow()
	}
	m.clampOffset()
	m.followReader()
	if m.reading() {
		m.keepTogether()
	}
	var cmd tea.Cmd
	if m.checkPend && m.status.Pending > 0 && !m.retrying {
		m.retrying = true
		cmd = tea.Tick(pushRetry, func(time.Time) tea.Msg { return retryMsg{} })
	}
	m.checkPend = false
	if m.openOnLoad {
		m.openOnLoad = false
		if t, ok := m.current(); ok {
			cmd = tea.Batch(cmd, m.apply([]model.Thread{t}, service.MarkRead, false))
		} else {
			m.focus = paneList
		}
	}
	return m, cmd
}

// current is the conversation under the list cursor.
func (m Model) current() (model.Thread, bool) {
	if m.cursor < 0 || m.cursor >= len(m.threads) {
		return model.Thread{}, false
	}
	return m.threads[m.cursor], true
}

// followReader points the reader at the current conversation: from the top,
// with unread messages and the last one expanded, as Gmail opens them.
func (m *Model) followReader() {
	if m.reading() {
		return // the page read together stays as it opened
	}
	t, ok := m.current()
	if !ok {
		m.readerID = ""
		return
	}
	if t.ID == m.readerID {
		return
	}
	m.readerID, m.scroll, m.pointedID = t.ID, 0, ""
	m.expanded = map[string]bool{t.Latest().ID: true}
	for _, msg := range t.Messages {
		if msg.Unread() {
			m.expanded[msg.ID] = true
		}
	}
}

// pointed is the message ▶ marks, which reply, forward and the attachment
// keys act on: the one tab and shift+tab moved to in the current
// conversation, else the one a reply answers.
func (m Model) pointed() (model.Message, bool) {
	t, ok := m.current()
	if !ok {
		return model.Message{}, false
	}
	if i := slices.IndexFunc(t.Messages, func(msg model.Message) bool { return msg.ID == m.pointedID }); i >= 0 {
		return t.Messages[i], true
	}
	return compose.ReplyTarget(t, t.Account), true
}

// pointBy moves ▶ n messages along the current conversation (reading
// together, on into the next or previous one), scrolling the reader to it
// when it's out of view.
func (m *Model) pointBy(n int) {
	t, ok := m.current()
	cur, _ := m.pointed()
	if !ok {
		return
	}
	i := slices.IndexFunc(t.Messages, func(msg model.Message) bool { return msg.ID == cur.ID }) + n
	if at := m.togetherAt() + n; m.reading() && (i < 0 || i >= len(t.Messages)) && at >= 0 && at < len(m.togetherThreads()) {
		m.showTogether(at)
		t, _ = m.current()
		i = 0
		if n < 0 {
			i = len(t.Messages) - 1
		}
	}
	i = max(min(i, len(t.Messages)-1), 0)
	m.pointedID = t.Messages[i].ID
	if line := m.pointedLine(); line < m.scroll || line >= m.scroll+m.listRows()-1 {
		m.scroll = line
		m.scrollBy(0)
	}
}

// pointedLine is the reader line the ▶ message starts on.
func (m Model) pointedLine() int {
	t, _ := m.current()
	p, _ := m.pointed()
	w := m.readerWidth()
	_, starts := m.threadLines(t, w, "")
	line := starts[max(slices.IndexFunc(t.Messages, func(msg model.Message) bool { return msg.ID == p.ID }), 0)]
	if m.reading() {
		line += m.togetherStarts(w)[m.togetherAt()] + 1 // under its subject
	}
	return line
}

// readBy moves ▶ n messages along, like tab, and opens that message at the
// top of the reader, even below the end of the conversation.
func (m *Model) readBy(n int) {
	m.pointBy(n)
	p, ok := m.pointed()
	if !ok {
		return
	}
	m.expanded[p.ID] = true
	m.scroll = m.pointedLine()
	m.scrollBy(0)
}

// Run starts the TUI full-screen and blocks until the user quits.
func Run(ctx context.Context, d Deps) error {
	_, err := tea.NewProgram(New(ctx, d), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil // Ctrl-C via the context
	}
	return err
}
