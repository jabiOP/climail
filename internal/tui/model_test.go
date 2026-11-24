package tui

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jabiOP/climail/internal/compose"
	"github.com/jabiOP/climail/internal/demo"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/output"
	"github.com/jabiOP/climail/internal/service"
	"github.com/jabiOP/climail/internal/store"
)

var update = flag.Bool("update", false, "rewrite golden files in testdata/")

func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.Ascii) // golden files contain no ANSI codes
	os.Exit(m.Run())
}

var now = time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)

const workAcct, homeAcct = "you@work.example", "you@home.example"

type fixture struct {
	t        *testing.T
	st       *store.Store
	m        tea.Model
	editLine int // the line the editor was last opened at
}

// newFixture opens the TUI on the demo mailboxes, as `climail demo` does.
func newFixture(t *testing.T, w, h int) *fixture {
	t.Helper()
	return newFixtureWith(t, w, h, Deps{})
}

// newFixtureWith is newFixture with extra Deps (e.g. Sync).
func newFixtureWith(t *testing.T, w, h int, d Deps) *fixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for _, mb := range demo.Mailboxes(now) {
		st.EnsureAccount(ctx, mb.Account)
		st.ReplaceLabels(ctx, mb.Account, mb.Labels)
		for i := range mb.Messages {
			mb.Messages[i].Account = mb.Account
		}
		if err := st.UpsertMessages(ctx, mb.Messages); err != nil {
			t.Fatal(err)
		}
		st.SetLastSync(ctx, mb.Account, now.Add(-2*time.Minute))
	}
	f := &fixture{t: t, st: st}
	d.Service, d.Now, d.DraftDir = service.New(st), func() time.Time { return now }, t.TempDir()
	if d.OpenDir == "" {
		d.OpenDir = t.TempDir()
	}
	d.Editor = func(_ string, line int) *exec.Cmd { // tests send editedMsg themselves
		f.editLine = line
		return exec.Command("true")
	}
	m := New(ctx, d)
	f.m = f.drain(m, m.Init())
	f.send(tea.WindowSizeMsg{Width: w, Height: h})
	return f
}

// drain runs cmd and feeds its messages back into the model until nothing is
// left, as the Bubble Tea runtime would, but synchronously.
func (f *fixture) drain(m tea.Model, cmd tea.Cmd) tea.Model {
	f.t.Helper()
	for i := 0; cmd != nil; i++ {
		if i > 100 {
			f.t.Fatal("drain: command loop never settled")
		}
		switch msg := cmd().(type) {
		case nil:
			return m
		case tea.BatchMsg:
			for _, c := range msg {
				m = f.drain(m, c)
			}
			return m
		default:
			if _, ok := msg.(tea.Cmd); ok { // e.g. a cursor blink; not needed here
				return m
			}
			m, cmd = m.Update(msg)
		}
	}
	return m
}

func (f *fixture) send(msgs ...tea.Msg) {
	f.t.Helper()
	for _, msg := range msgs {
		var cmd tea.Cmd
		f.m, cmd = f.m.Update(msg)
		f.m = f.drain(f.m, cmd)
	}
}

// keys sends key presses: named keys ("enter", "esc", "tab", "ctrl+d") or
// strings typed one rune at a time.
func (f *fixture) keys(names ...string) {
	f.t.Helper()
	special := map[string]tea.KeyType{"enter": tea.KeyEnter, "esc": tea.KeyEsc, "tab": tea.KeyTab,
		"ctrl+d": tea.KeyCtrlD, "shift+tab": tea.KeyShiftTab, "ctrl+u": tea.KeyCtrlU, "backspace": tea.KeyBackspace, "space": tea.KeySpace}
	for _, n := range names {
		if k, ok := special[n]; ok {
			f.send(tea.KeyMsg{Type: k})
			continue
		}
		for _, r := range n {
			f.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
}

func (f *fixture) model() Model { return f.m.(Model) }

func (f *fixture) view() string { return f.m.View() }

func (f *fixture) labels(id string) []string {
	f.t.Helper()
	m, err := f.st.Message(context.Background(), workAcct, id)
	if err != nil {
		f.t.Fatal(err)
	}
	return m.Labels
}

func (f *fixture) currentID() string {
	t, _ := f.model().current()
	return t.ID
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		os.MkdirAll("testdata", 0o755)
		os.WriteFile(path, []byte(got), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run `go test ./internal/tui -update` to create it)", err)
	}
	if got != string(want) {
		t.Errorf("view differs from %s (rerun with -update if intended, then review the diff)\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestView_Wide(t *testing.T) {
	f := newFixture(t, 150, 22)

	golden(t, "wide", f.view())
}

func TestView_Narrow(t *testing.T) {
	f := newFixture(t, 80, 16)

	golden(t, "narrow", f.view())
}

func TestView_NarrowReader(t *testing.T) {
	f := newFixture(t, 80, 24)

	f.keys("enter")

	golden(t, "narrow_reader", f.view())
}

// Every line must fill the terminal exactly: a line one cell too wide shifts
// the panes to its right and leaves stale cells behind (seen in iTerm2).
func TestView_EveryLineFillsTheWidth(t *testing.T) {
	for _, w := range []int{80, 100, 126, 127, 150, 200} {
		for _, keys := range [][]string{nil, {"enter"}, {"]", "]"}} {
			f := newFixture(t, w, 24)
			f.keys(keys...)
			for i, l := range strings.Split(f.view(), "\n") {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("width %d, keys %v: line %d is %d cells wide: %q", w, keys, i, got, l)
				}
			}
		}
	}
}

// A message too long for the status bar wraps onto more lines, which the
// panes give up, instead of being cut.
func TestStatusBar_LongMessageWraps(t *testing.T) {
	for _, w := range []int{80, 130} {
		f := newFixture(t, w, 24)
		long := "not sent: Gmail says the recipient address is invalid, check the To line and try again " +
			"(this message is long on purpose, to need more than one line of the status bar)"
		m := f.model()
		m.message, m.isError = long, true
		f.m = m

		lines := strings.Split(f.view(), "\n")
		if len(lines) != 24 {
			t.Errorf("width %d: %d lines, want 24", w, len(lines))
		}
		for i, l := range lines {
			if lipgloss.Width(l) != w {
				t.Errorf("width %d: line %d is %d wide", w, i, lipgloss.Width(l))
			}
		}
		var text string
		for _, l := range lines[len(lines)-3:] {
			text += strings.TrimSpace(l) + " "
		}
		if !strings.Contains(strings.Join(strings.Fields(text), " "), "to need more than one line of the status bar)") {
			t.Errorf("width %d: message cut:\n%s", w, f.view())
		}
	}
}

func TestView_Help(t *testing.T) {
	f := newFixture(t, 80, 24)

	f.keys("?")

	golden(t, "help", f.view())
	if lines := strings.Count(f.view(), "\n") + 1; lines > 24 {
		t.Errorf("help is %d lines tall, want it to fit 24", lines)
	}
}

func has(labels []string, l string) bool { return slices.Contains(labels, l) }

func (f *fixture) listed() []string {
	var ids []string
	for _, t := range f.model().threads {
		ids = append(ids, t.ID)
	}
	return ids
}

func TestOpen_MarksReadAndShowsReader(t *testing.T) {
	f := newFixture(t, 80, 24)

	f.keys("enter")

	if f.model().focus != paneReader {
		t.Errorf("focus = %v, want the reader", f.model().focus)
	}
	if has(f.labels("t-launch-03"), model.Unread) {
		t.Error("opened conversation still unread")
	}
	f.keys("esc")
	if f.model().focus != paneList {
		t.Errorf("esc: focus = %v, want the list", f.model().focus)
	}
}

func TestPreview_DoesNotMarkRead(t *testing.T) {
	f := newFixture(t, 150, 30)

	f.keys("j", "j") // the reader beside the list follows the cursor

	if f.currentID() != "t-ci" || !has(f.labels("t-ci-05"), model.Unread) {
		t.Errorf("current = %s, labels %v; want t-ci, still unread", f.currentID(), f.labels("t-ci-05"))
	}
	if !strings.Contains(f.view(), "main failed: integration tests") {
		t.Error("reader doesn't show the conversation under the cursor")
	}
}

func TestArchive_ThenUndo(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("a")

	if has(f.labels("t-launch-01"), model.Inbox) || slices.Contains(f.listed(), "t-launch") {
		t.Fatal("not archived")
	}
	if f.currentID() != "t-ci2" {
		t.Errorf("cursor on %s, want the next conversation", f.currentID())
	}
	if !strings.Contains(f.view(), "Archived · u to undo") {
		t.Errorf("no undo hint:\n%s", f.view())
	}

	f.keys("u")

	if !has(f.labels("t-launch-01"), model.Inbox) || !slices.Contains(f.listed(), "t-launch") {
		t.Error("undo didn't restore the conversation")
	}
	if n, _ := f.st.PendingCount(context.Background(), workAcct); n != 0 {
		t.Errorf("pending = %d, want undo to cancel the queued change", n)
	}
}

func TestTrash_NeedsDD(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("d", "j")
	if !has(f.labels("t-launch-01"), model.Inbox) {
		t.Fatal("a single d trashed")
	}
	f.keys("k", "d", "d")

	if !has(f.labels("t-launch-01"), model.Trash) {
		t.Errorf("labels = %v, want TRASH", f.labels("t-launch-01"))
	}
}

func TestSelect_ActsOnAllSelected(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("x", "x")
	if !strings.Contains(f.view(), "2 selected") {
		t.Errorf("no selection count:\n%s", f.view())
	}
	f.keys("a")

	if slices.Contains(f.listed(), "t-launch") || slices.Contains(f.listed(), "t-ci2") || !slices.Contains(f.listed(), "t-ci") {
		t.Errorf("listed = %v", f.listed())
	}
	if len(f.model().selected) != 0 {
		t.Error("selection kept after acting on it")
	}
}

func TestToggles_ReadAndStar(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("N", "*")
	if has(f.labels("t-launch-03"), model.Unread) || !has(f.labels("t-launch-03"), model.Starred) {
		t.Errorf("labels = %v, want read and starred", f.labels("t-launch-03"))
	}
	f.keys("N", "*")
	if !has(f.labels("t-launch-03"), model.Unread) || has(f.labels("t-launch-03"), model.Starred) {
		t.Errorf("labels = %v, want unread and unstarred", f.labels("t-launch-03"))
	}
}

func TestSearch_LiveThenEscRestoresMailbox(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("/", "invoice")

	if diff := f.listed(); len(diff) != 1 || diff[0] != "t-invoice" {
		t.Errorf("listed = %v, want the archived invoice (search covers all mail)", f.listed())
	}
	f.keys("enter")
	if !strings.Contains(f.view(), "Search “invoice”") {
		t.Errorf("no search title:\n%s", f.view())
	}
	f.keys("esc")
	if f.model().box.ID != model.Inbox || f.model().search != "" || len(f.listed()) < 5 {
		t.Errorf("box %q, search %q; want back in the inbox", f.model().box.ID, f.model().search)
	}
}

func TestSearch_NothingFound(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("/", "zzzz", "enter")

	if !strings.Contains(f.view(), "Nothing matches “zzzz”") {
		t.Errorf("view:\n%s", f.view())
	}
}

func TestAccounts_Switch(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("]")

	if f.model().account != homeAcct || f.listed()[0] != "h-sunday" {
		t.Errorf("account %s, listed %v", f.model().account, f.listed())
	}
	f.keys("[")
	if f.model().account != workAcct {
		t.Errorf("account %s, want back to work", f.model().account)
	}
}

func TestAccounts_AllInOneInbox(t *testing.T) {
	f := newFixture(t, 150, 30)

	f.keys("[") // before the first account: all of them

	m := f.model()
	accounts := map[string]bool{}
	for _, th := range m.threads {
		accounts[th.Account] = true
		if !slices.Contains(th.Labels(), model.Inbox) {
			t.Errorf("%s is listed but not in an Inbox", th.ID)
		}
	}
	if !m.all || !accounts[workAcct] || !accounts[homeAcct] {
		t.Fatalf("all %v, accounts listed %v", m.all, accounts)
	}
	for i := 1; i < len(m.threads); i++ {
		if m.threads[i].Latest().Date.After(m.threads[i-1].Latest().Date) {
			t.Errorf("not newest first at %d", i)
		}
	}
	if !strings.Contains(f.view(), "All accounts") || !strings.Contains(f.view(), "║ ▌ ") {
		t.Errorf("no title or account stripes:\n%s", f.view())
	}
	if want := m.others[workAcct] + m.others[homeAcct]; m.boxes[0].Unread != want {
		t.Errorf("Inbox unread %d, want %d", m.boxes[0].Unread, want)
	}
	f.keys("[")
	if f.model().all || f.model().account != homeAcct {
		t.Errorf("[: all %v, account %s, want the last account", f.model().all, f.model().account)
	}
	f.keys("]", "]")
	if f.model().all || f.model().account != workAcct {
		t.Errorf("] ]: all %v, account %s, want the first account", f.model().all, f.model().account)
	}
}

func TestAccounts_AllActsInEachConversationsAccount(t *testing.T) {
	f := newFixture(t, 100, 24)
	f.keys("[")
	for f.model().threads[f.model().cursor].Account != homeAcct {
		f.keys("j")
	}
	th, _ := f.model().current()
	f.keys("r")
	if c := f.model().composing; c == nil || c.base.Account != homeAcct {
		t.Errorf("reply not written from the home account: %+v", c)
	}
	reset := f.model()
	reset.composing = nil // as if the editor closed on an unchanged draft
	f.m = reset

	f.keys("a")

	m, err := f.st.Message(context.Background(), homeAcct, th.Latest().ID)
	if err != nil {
		t.Fatal(err)
	}
	if has(m.Labels, model.Inbox) {
		t.Errorf("%s still in the home Inbox: %v", th.ID, m.Labels)
	}
	f.keys("u")
	m, _ = f.st.Message(context.Background(), homeAcct, th.Latest().ID)
	if !has(m.Labels, model.Inbox) {
		t.Errorf("undo: %s not back in the home Inbox: %v", th.ID, m.Labels)
	}
}

func TestAccounts_EachHasItsColor(t *testing.T) {
	f := newFixture(t, 100, 24)
	m := f.model()

	if m.accountStyle(workAcct).GetForeground() == m.accountStyle(homeAcct).GetForeground() {
		t.Errorf("both accounts drawn in %v", m.accountStyle(workAcct).GetForeground())
	}
}

func TestAccounts_ColorFromConfig(t *testing.T) {
	f := newFixtureWith(t, 100, 24, Deps{AccountColors: map[string]string{homeAcct: "Basil"}})

	if got := f.model().accountStyle(homeAcct).GetForeground(); got != lipgloss.Color("#0b8043") {
		t.Errorf("home drawn in %v, want basil", got)
	}
}

func TestAccounts_PickAColor(t *testing.T) {
	var saved []string
	f := newFixtureWith(t, 130, 24, Deps{SaveAccountColor: func(account, color string) error {
		saved = append(saved, account+"="+color)
		return nil
	}})
	f.keys("h") // the mailboxes
	for r := f.model().sideRows()[f.model().side]; r.box != nil || r.account != workAcct; r = f.model().sideRows()[f.model().side] {
		f.keys("k")
	}

	f.keys("C")
	if !strings.Contains(f.view(), "Cherry blossom") {
		t.Fatalf("no colors to pick from:\n%s", f.view())
	}
	f.keys("kkkkk", "enter") // from peacock (blues) up to the reds: tomato

	if !slices.Equal(saved, []string{workAcct + "=tomato"}) {
		t.Errorf("saved %v", saved)
	}
	if got := f.model().accountStyle(workAcct).GetForeground(); got != lipgloss.Color("#d50000") {
		t.Errorf("work drawn in %v, want tomato", got)
	}
	if strings.Contains(f.view(), "Cherry blossom") {
		t.Error("the picker is still open")
	}
}

func TestFocus_DoubleBorderInTheAccountsColor(t *testing.T) {
	f := newFixture(t, 150, 24)

	f.keys("l") // the reader

	if v := f.view(); !strings.Contains(v, "╔═ Launch plan") || !strings.Contains(v, "╭─ Inbox") {
		t.Errorf("the reader isn't the one with the double border:\n%s", v)
	}
	m := f.model()
	if got, want := m.hiFg().GetForeground(), m.accountStyle(workAcct).GetForeground(); got != want {
		t.Errorf("focus drawn in %v, want the account's %v", got, want)
	}
}

func TestSidebar_JKMoveBetweenAccounts(t *testing.T) {
	f := newFixture(t, 150, 24)
	f.keys("h") // the mailboxes, on Inbox
	row := func() sideRow { return f.model().sideRows()[f.model().side] }

	f.keys("J")
	if r := row(); r.box != nil || r.account != homeAcct {
		t.Errorf("J: on %+v, want the home account", r)
	}
	f.keys("K")
	if r := row(); r.box != nil || r.account != workAcct {
		t.Errorf("K: on %+v, want the work account", r)
	}
	f.keys("K")
	if r := row(); r.box != nil || r.account != service.AllAccounts {
		t.Errorf("K again: on %+v, want All accounts", r)
	}
}

func TestGoTo_Mailboxes(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("g", "t")
	if f.model().box.ID != model.Sent || f.listed()[0] != "t-sent" {
		t.Errorf("gt: box %q, listed %v", f.model().box.ID, f.listed())
	}
	f.keys("g", "s")
	if f.model().box.ID != model.Starred || f.listed()[0] != "t-review" {
		t.Errorf("gs: box %q, listed %v", f.model().box.ID, f.listed())
	}
	f.keys("G", "g", "g")
	if f.model().cursor != 0 {
		t.Errorf("gg: cursor %d", f.model().cursor)
	}
}

func TestSidebar_OpensLabelAndOtherAccount(t *testing.T) {
	f := newFixture(t, 80, 24)

	f.keys("h")
	if f.model().focus != paneSidebar || !strings.Contains(f.view(), "Northwind") {
		t.Fatalf("sidebar not shown:\n%s", f.view())
	}
	for f.model().sideRows()[f.model().side].box == nil || f.model().sideRows()[f.model().side].box.Name != "Builds" {
		f.keys("j")
	}
	f.keys("enter")

	if f.model().box.Name != "Builds" || f.model().focus != paneList || len(f.listed()) != 2 {
		t.Errorf("box %q, focus %v, listed %v", f.model().box.Name, f.model().focus, f.listed())
	}
	f.keys("h", "G", "enter")
	if f.model().account != homeAcct {
		t.Errorf("account %s, want the home account", f.model().account)
	}
}

func TestReader_ArchiveMovesToNextConversation(t *testing.T) {
	f := newFixture(t, 80, 24)
	f.keys("enter")

	f.keys("a")

	if f.model().focus != paneReader || f.currentID() != "t-ci2" {
		t.Errorf("focus %v on %s, want the reader on the next conversation", f.model().focus, f.currentID())
	}
}

func TestReader_NextMarksRead(t *testing.T) {
	f := newFixture(t, 80, 24)
	f.keys("j", "enter")

	f.keys("n")

	if f.currentID() != "t-ci" || has(f.labels("t-ci-05"), model.Unread) {
		t.Errorf("current %s, labels %v; want t-ci, read", f.currentID(), f.labels("t-ci-05"))
	}
}

func TestReader_ExpandAll(t *testing.T) {
	f := newFixture(t, 80, 40)
	f.keys("enter")
	if strings.Contains(f.view(), "Freeze features") {
		t.Fatal("read message expanded before o")
	}

	f.keys("o")

	if !strings.Contains(f.view(), "Freeze features") {
		t.Errorf("o didn't expand:\n%s", f.view())
	}
}

func TestHelp_AnyKeyCloses(t *testing.T) {
	f := newFixture(t, 100, 40)

	f.keys("?", "j")

	if f.model().help || f.model().cursor != 0 {
		t.Errorf("help %v, cursor %d; want help closed and the key not acted on", f.model().help, f.model().cursor)
	}
}

func TestSync_RetriedWhileChangesWait(t *testing.T) {
	old := pushRetry
	pushRetry = time.Millisecond
	t.Cleanup(func() { pushRetry = old })
	syncs := 0
	var st *store.Store
	f := newFixtureWith(t, 100, 24, Deps{Sync: func(ctx context.Context, _ func(string, int, int)) error {
		syncs++
		if syncs == 3 { // the daemon let go of the lock: push
			ops, _ := st.PendingOps(ctx, workAcct)
			for _, op := range ops {
				st.CompleteOp(ctx, op)
			}
		}
		return nil // skipped: someone else held the lock
	}})
	st = f.st
	syncs = 0

	f.keys("a")

	if syncs != 3 {
		t.Errorf("syncs = %d, want the change retried until it went", syncs)
	}
}

// editDraft does what the user would in the editor, then returns to the TUI.
func (f *fixture) editDraft(edit func(old string) string) {
	f.t.Helper()
	c := f.model().composing
	if c == nil {
		f.t.Fatal("not composing")
	}
	old, _ := os.ReadFile(c.path)
	os.WriteFile(c.path, []byte(edit(string(old))), 0o600)
	f.send(editedMsg{})
}

func (f *fixture) outbox() []string {
	f.t.Helper()
	out, err := f.st.Outgoing(context.Background(), workAcct)
	if err != nil {
		f.t.Fatal(err)
	}
	var s []string
	for _, o := range out {
		s = append(s, o.Summary)
	}
	return s
}

func TestSync_ShowsHowTheDownloadGoes(t *testing.T) {
	f := newFixtureWith(t, 150, 24, Deps{Sync: func(ctx context.Context, progress func(string, int, int)) error {
		progress(homeAcct, 120, 300)
		return nil
	}})

	m := f.model()
	m.syncing = true // a sync under way
	f.m = m
	f.send(progressMsg{account: homeAcct, done: 120, total: 300})

	if !strings.Contains(f.view(), "downloading 120/300") {
		t.Errorf("no progress shown:\n%s", f.view())
	}
	f.keys("s") // a sync that reports through the channel, then ends
	if strings.Contains(f.view(), "downloading") {
		t.Errorf("progress still shown after the sync ended:\n%s", f.view())
	}
}

func TestLog_RejectedChangesShowWithL(t *testing.T) {
	f := newFixture(t, 150, 24)
	f.st.AddLog(context.Background(), store.LogEntry{Time: now, Kind: store.LogError, Account: workAcct,
		Message: "“Plan”: Gmail rejected the change (archive); it shows as in Gmail again"})

	f.keys("g", "i") // reload

	if !strings.Contains(f.view(), "1 rejected: L to see") {
		t.Fatalf("no notice:\n%s", f.view())
	}
	f.keys("L")
	if !strings.Contains(f.view(), "“Plan”: Gmail rejected the change (archive)") {
		t.Errorf("log not shown:\n%s", f.view())
	}
	f.keys("x") // any key closes it
	if strings.Contains(f.view(), "“Plan”: Gmail rejected") {
		t.Error("log still shown")
	}
}

func TestReply_EditConfirmSend(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("R")
	f.editDraft(func(old string) string { return strings.Replace(old, "\n\n", "\n\nWill do!", 1) })

	if !strings.Contains(f.view(), "From you@work.example: send “Re: Launch plan for the 2.0 release” to Maya Chen?") {
		t.Fatalf("no confirmation:\n%s", f.view())
	}
	f.keys("y")

	if diff := f.outbox(); len(diff) != 1 || diff[0] != "“Re: Launch plan for the 2.0 release” to Maya Chen" {
		t.Errorf("outbox = %v", f.outbox())
	}
	if f.model().composing != nil {
		t.Error("still composing")
	}
}

func TestReply_EditorOpensAtTheMessage(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("r")

	if want := compose.StartLine(f.model().composing.base); f.editLine != want || want == 0 {
		t.Errorf("editor at line %d, want %d (the body's first)", f.editLine, want)
	}
}

func TestCompose_FromTheDefaultSendAsAddress(t *testing.T) {
	f := newFixture(t, 100, 24)
	f.st.ReplaceSendAs(context.Background(), workAcct, []model.SendAs{{Email: workAcct}, {Email: "me@domain.com", Default: true}})

	f.keys("c")

	if c := f.model().composing; c == nil || c.base.From != "me@domain.com" || !strings.HasPrefix(c.base.Text(), "From: me@domain.com\n") {
		t.Errorf("composing %+v; want it from the default send-as address", c)
	}
}

func TestReplyAll_IncludesEveryone(t *testing.T) {
	f := newFixture(t, 130, 24)

	f.keys("r")
	f.editDraft(func(old string) string { return old + "ok\n" })

	if !strings.Contains(f.view(), "From you@work.example: send “Re: Launch plan for the 2.0 release” to Maya Chen, Tom Okafor?") {
		t.Errorf("view:\n%s", f.view())
	}
}

func TestCompose_ErrorThenQuitSavesToDrafts(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("c")
	f.editDraft(func(old string) string {
		return strings.Replace(old, "Subject: ", "Subject: Later", 1) + "no recipient\n"
	})
	if !strings.Contains(f.view(), "no recipients: fill in To · e edit · q save to Drafts") {
		t.Fatalf("view:\n%s", f.view())
	}
	f.keys("y") // can't send this
	if f.model().composing == nil {
		t.Fatal("y sent an invalid draft")
	}
	path := f.model().composing.path
	f.keys("q")

	out, _ := f.st.Outgoing(context.Background(), workAcct)
	if len(out) != 1 || !out[0].Save || out[0].Summary != "draft “Later”" || !strings.Contains(f.view(), "Saved in Drafts") {
		t.Errorf("queued %+v\n%s", out, f.view())
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the local draft file is still there")
	}
}

func TestCompose_UnreadableDraftStaysOnDisk(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("c")
	f.editDraft(func(old string) string { return "Frm: oops\n" + old })
	path := f.model().composing.path
	f.keys("q")

	if _, err := os.Stat(path); err != nil || len(f.outbox()) != 0 || !strings.Contains(f.view(), "Not saved. The draft is in") {
		t.Errorf("draft %v, outbox %v\n%s", err, f.outbox(), f.view())
	}
}

func TestDrafts_EnterContinuesTheDraftAndSendingRemovesIt(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("g", "d", "enter")

	c := f.model().composing
	if c == nil || c.base.DraftID == "" || c.base.To != "Maya Chen <maya@work.example>" || c.base.Subject != "Ideas for the offsite" {
		t.Fatalf("composing %+v", c)
	}
	f.editDraft(func(old string) string { return old + "A walk\n" })
	f.keys("y")

	out, _ := f.st.Outgoing(context.Background(), workAcct)
	if len(out) != 1 || out[0].Save || out[0].Draft != c.base.DraftID {
		t.Errorf("queued %+v", out)
	}
	if !strings.Contains(f.view(), "No conversations in Drafts") {
		t.Errorf("the draft is still listed:\n%s", f.view())
	}
}

// A draft continued from Drafts is ready as it is: closing the editor
// without changes still asks, so it can be sent.
func TestDrafts_UnchangedContinuedDraftCanBeSent(t *testing.T) {
	f := newFixture(t, 100, 24)
	f.keys("g", "d", "enter")

	f.editDraft(func(old string) string { return old })

	if !strings.Contains(f.view(), "send “Ideas for the offsite”") {
		t.Fatalf("not asked:\n%s", f.view())
	}
	f.keys("y")
	if out, _ := f.st.Outgoing(context.Background(), workAcct); len(out) != 1 || out[0].Save {
		t.Errorf("queued %+v", out)
	}
}

func TestCompose_DiscardNeitherSendsNorSaves(t *testing.T) {
	f := newFixture(t, 100, 24)
	f.keys("c")
	path := f.model().composing.path
	f.editDraft(func(old string) string { return strings.Replace(old, "To: ", "To: bob@example.com", 1) })

	if !strings.Contains(f.view(), "d discard") {
		t.Errorf("the question doesn't offer d:\n%s", f.view())
	}
	f.keys("d")

	if f.model().composing != nil || len(f.outbox()) != 0 || !strings.Contains(f.view(), "Discarded") {
		t.Errorf("composing %v, outbox %v:\n%s", f.model().composing, f.outbox(), f.view())
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("the draft file is still on disk")
	}
}

func TestCompose_UnchangedIsDropped(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("c")
	f.editDraft(func(old string) string { return old })

	if f.model().composing != nil || !strings.Contains(f.view(), "wasn't changed") {
		t.Errorf("view:\n%s", f.view())
	}
}

func TestKeys_TypedTogetherAreTakenOneByOne(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("gt")})

	if f.model().box.ID != model.Sent {
		t.Errorf("box = %q, want Sent", f.model().box.ID)
	}
}

// downloads records what the TUI asked to download, and "saves" each file as dir/name.
type downloads struct {
	dirs   []string
	names  []string
	opened []string
}

func (d *downloads) deps() Deps {
	return Deps{
		Download: func(_ context.Context, _ string, refs []model.AttachmentRef, dir string) ([]string, error) {
			var paths []string
			for _, r := range refs {
				d.dirs, d.names = append(d.dirs, dir), append(d.names, r.Filename)
				paths = append(paths, filepath.Join(dir, r.Filename))
			}
			return paths, nil
		},
		DownloadDir: "/dl", OpenDir: "/open",
		OpenURL: func(p string) error { d.opened = append(d.opened, p); return nil },
	}
}

func TestAttachments_SaveAll(t *testing.T) {
	d := &downloads{}
	f := newFixtureWith(t, 100, 24, d.deps())

	f.keys("D")

	if len(d.names) != 1 || d.names[0] != "launch-plan-v2.pdf" || d.dirs[0] != "/dl" {
		t.Errorf("downloaded %v into %v", d.names, d.dirs)
	}
	if !strings.Contains(f.view(), "Saved launch-plan-v2.pdf in /dl") {
		t.Errorf("view:\n%s", f.view())
	}
}

func TestAttachments_OpenOne(t *testing.T) {
	d := &downloads{}
	f := newFixtureWith(t, 100, 24, d.deps())

	f.keys("A")

	if len(d.opened) != 1 || d.opened[0] != "/open/launch-plan-v2.pdf" {
		t.Errorf("opened %v", d.opened)
	}
}

func TestAttachments_NoneHere(t *testing.T) {
	d := &downloads{}
	f := newFixtureWith(t, 100, 24, d.deps())

	f.keys("j", "A")

	if len(d.names) != 0 || !strings.Contains(f.view(), "no attachments here") {
		t.Errorf("downloaded %v", d.names)
	}
}

func TestForward_FetchesAttachmentsIntoDraft(t *testing.T) {
	d := &downloads{}
	f := newFixtureWith(t, 100, 24, d.deps())

	f.keys("f")

	c := f.model().composing
	if c == nil || len(c.base.Attach) != 1 || !strings.HasPrefix(c.base.Attach[0], "/open/forward-") {
		t.Fatalf("composing %+v", c)
	}
	text, _ := os.ReadFile(c.path)
	if !strings.Contains(string(text), "Attach: /open/forward-") || !strings.Contains(string(text), "Subject: Fwd: Re: Launch plan") {
		t.Errorf("draft:\n%s", text)
	}
}

func TestAttachments_ChooseWhichToOpen(t *testing.T) {
	d := &downloads{}
	f := newFixtureWith(t, 100, 24, d.deps())
	m := model.Message{Account: workAcct, ID: "two-1", ThreadID: "two", Subject: "Photos", Date: now, Labels: []string{"INBOX"},
		Attachments: []model.Attachment{{Filename: "a.jpg"}, {Filename: "b.jpg"}}}
	f.st.UpsertMessages(context.Background(), []model.Message{m})
	reload := f.model()
	cmd := reload.load()
	f.m = reload
	f.send(cmd())

	f.keys("k", "A")
	if !strings.Contains(f.view(), "Open: 1 a.jpg · 2 b.jpg · esc cancel") {
		t.Fatalf("view:\n%s", f.view())
	}
	f.keys("2")

	if len(d.opened) != 1 || d.opened[0] != "/open/b.jpg" {
		t.Errorf("opened %v", d.opened)
	}
}

func TestSearch_EnterAsksGmail(t *testing.T) {
	var asked []string
	f := newFixtureWith(t, 100, 24, Deps{Search: func(_ context.Context, account, q string) ([]string, error) {
		asked = append(asked, account+": "+q)
		return []string{"t-news", "t-invoice"}, nil // Gmail's order, and its own idea of a match
	}})

	f.keys("/", "from:weekly")
	if len(f.listed()) != 0 || len(asked) != 0 {
		t.Fatalf("while typing: listed %v, asked %v", f.listed(), asked)
	}
	f.keys("enter")

	if diff := f.listed(); len(diff) != 2 || diff[0] != "t-news" || diff[1] != "t-invoice" {
		t.Errorf("listed %v", f.listed())
	}
	if len(asked) != 1 || asked[0] != workAcct+": from:weekly" {
		t.Errorf("asked %v", asked)
	}
	if !strings.Contains(f.view(), "Search “from:weekly” on Gmail · 2 conversations") {
		t.Errorf("view:\n%s", f.view())
	}
	f.keys("esc")
	if f.model().box.ID != model.Inbox || f.model().found != nil {
		t.Errorf("esc: box %q, found %v", f.model().box.ID, f.model().found)
	}
}

func TestSearch_GmailUnreachableKeepsLocalMatches(t *testing.T) {
	f := newFixtureWith(t, 100, 24, Deps{Search: func(context.Context, string, string) ([]string, error) {
		return nil, context.DeadlineExceeded
	}})

	f.keys("/", "invoice", "enter")

	if diff := f.listed(); len(diff) != 1 || diff[0] != "t-invoice" {
		t.Errorf("listed %v", f.listed())
	}
	if !strings.Contains(f.view(), "couldn't search Gmail (offline): showing local matches") {
		t.Errorf("view:\n%s", f.view())
	}
}

func TestCompose_NamesBecomeAddressesInTheDraft(t *testing.T) {
	f := newFixture(t, 100, 24)

	f.keys("c")
	f.editDraft(func(old string) string {
		return strings.Replace(strings.Replace(old, "To: ", "To: maya, tom", 1), "Subject: ", "Subject: Hi", 1) + "hello\n"
	})

	if !strings.Contains(f.view(), "From you@work.example: send “Hi” to Maya Chen, Tom Okafor?") {
		t.Fatalf("view:\n%s", f.view())
	}
	text, _ := os.ReadFile(f.model().composing.path)
	if !strings.Contains(string(text), "To: Maya Chen <maya@work.example>, Tom Okafor <tom@work.example>\n") {
		t.Errorf("draft file not updated:\n%s", text)
	}
}

func TestOpenInGmail_ReaderOpensTheConversation(t *testing.T) {
	var opened []string
	f := newFixtureWith(t, 100, 24, Deps{OpenURL: func(u string) error { opened = append(opened, u); return nil }})
	th, _ := f.model().current()

	f.keys("enter", "O")
	// Gmail answers /mail/u/<address>/ with a 404; ?authuser=<address> works.
	if want := gmailLink(th.Account, "all/"+th.ID); len(opened) != 1 || opened[0] != want {
		t.Errorf("opened %v, want %s", opened, want)
	}
	if !strings.HasPrefix(opened[0], "https://mail.google.com/mail/?authuser=you%40work.example#") {
		t.Errorf("opened %s", opened[0])
	}
}

func TestOpenInGmail_ListOpensTheMailbox(t *testing.T) {
	var opened []string
	f := newFixtureWith(t, 100, 24, Deps{OpenURL: func(u string) error { opened = append(opened, u); return nil }})

	f.keys("O", "g", "t", "O", "/", "invoice", "enter", "O")
	want := []string{gmailLink(workAcct, "inbox"), gmailLink(workAcct, "sent"), gmailLink(workAcct, "search/invoice")}
	if !slices.Equal(opened, want) {
		t.Errorf("opened %v, want %v", opened, want)
	}
}

func TestGmailView(t *testing.T) {
	for _, c := range []struct {
		box    service.Mailbox
		search string
		want   string
	}{
		{service.Mailbox{ID: model.Inbox}, "", "inbox"},
		{service.Mailbox{ID: model.Draft}, "", "drafts"},
		{service.Mailbox{ID: model.AllMail}, "", "all"},
		{service.Mailbox{ID: "Label_2", Path: "Work/Clients"}, "", "label/Work%2FClients"},
		{service.Mailbox{ID: "Label_3", Path: "To do"}, "", "label/To+do"},
		{service.Mailbox{ID: model.Inbox}, "from:maya invoice", "search/from%3Amaya+invoice"},
	} {
		if got := gmailView(c.box, c.search); got != c.want {
			t.Errorf("gmailView(%v, %q) = %q, want %q", c.box, c.search, got, c.want)
		}
	}
}

func TestDrafts_FormattingMarkdownCantHoldAsksFirst(t *testing.T) {
	var opened []string
	f := newFixtureWith(t, 100, 24, Deps{OpenURL: func(u string) error { opened = append(opened, u); return nil }})
	d := model.Message{Account: workAcct, ID: "web1", ThreadID: "tweb", Subject: "From the web", Date: now,
		To: []model.Address{{Email: "maya@work.example"}}, Labels: []string{model.Draft}, Body: "Big news",
		HTML: `<div dir="ltr"><font size="6">Big</font> <b>news</b></div>`}
	f.st.UpsertMessages(context.Background(), []model.Message{d})

	f.keys("g", "d", "enter")
	if f.model().composing != nil || !strings.Contains(f.view(), "Fonts, colours → Gmail default · e edit · O open in Gmail · esc") {
		t.Fatalf("no question:\n%s", f.view())
	}
	f.keys("O")
	if len(opened) != 1 || opened[0] != "https://mail.google.com/mail/?authuser=you%40work.example#drafts?compose=web1" {
		t.Errorf("opened %v", opened)
	}

	f.keys("enter", "e")
	if c := f.model().composing; c == nil || c.base.Body != "Big **news**\n" {
		t.Errorf("composing %+v", c)
	}
}

func (f *fixture) thread(id string) model.Thread {
	f.t.Helper()
	t, err := f.model().deps.Service.Thread(context.Background(), workAcct, id, model.AllMail)
	if err != nil {
		f.t.Fatal(err)
	}
	return t
}

func (f *fixture) unreadListed() []string {
	var ids []string
	for _, t := range f.model().threads {
		if t.Unread() {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

func TestReadTogether_UnreadOnOnePageMarkedRead(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	if len(unread) < 3 || len(unread) == len(f.listed()) {
		t.Fatalf("the demo Inbox should mix read and unread: unread %v of %v", unread, f.listed())
	}

	f.keys("T")

	if f.model().focus != paneReader || !strings.Contains(f.view(), f.togetherTitle(1, len(unread))) {
		t.Fatalf("not reading together:\n%s", f.view())
	}
	if f.currentID() != unread[0] {
		t.Errorf("current %s, want the first, %s", f.currentID(), unread[0])
	}
	page := strings.Join(f.model().readerLines(80), "\n")
	if !strings.Contains(page, "store_test.go:142") { // deep in the CI mail: unread messages open expanded
		t.Errorf("unread messages not expanded:\n%s", page)
	}
	for _, id := range unread {
		th := f.thread(id)
		if th.Unread() {
			t.Errorf("%s still unread", id)
		}
		if !strings.Contains(page, th.Subject()) {
			t.Errorf("%q not on the page", th.Subject())
		}
	}
}

func TestReadTogether_ArchiveOneAtATime(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	f.keys("T")

	f.keys("a") // the first: gone, the next one is current

	if slices.Contains(f.listed(), unread[0]) {
		t.Errorf("%s not archived", unread[0])
	}
	if f.model().focus != paneReader || f.currentID() != unread[1] || f.model().scroll != 0 {
		t.Fatalf("focus %v, current %s, scroll %d; want the reader on %s at the top", f.model().focus, f.currentID(), f.model().scroll, unread[1])
	}
	if !slices.Contains(f.listed(), unread[1]) || !strings.Contains(f.view(), f.togetherTitle(1, len(unread)-1)) {
		t.Errorf("archived more than one: listed %v\n%s", f.listed(), f.view())
	}

	f.keys("n") // keep the second, on to the third
	if f.currentID() != unread[2] || f.model().scroll == 0 {
		t.Errorf("n: current %s, scroll %d; want %s, scrolled to it", f.currentID(), f.model().scroll, unread[2])
	}
	f.keys("p")
	if f.currentID() != unread[1] || f.model().scroll != 0 {
		t.Errorf("p: current %s, scroll %d; want %s at the top", f.currentID(), f.model().scroll, unread[1])
	}
	f.keys("G") // the bottom: the last one is current
	if f.currentID() != unread[len(unread)-1] {
		t.Errorf("G: current %s, want the last, %s", f.currentID(), unread[len(unread)-1])
	}

	f.keys("u") // undo brings the first back to the Inbox and to the page, current
	if !slices.Contains(f.listed(), unread[0]) || !f.model().reading() || f.currentID() != unread[0] ||
		!strings.Contains(f.view(), f.togetherTitle(1, len(unread))) {
		t.Errorf("undo: listed %v, reading %v, current %s\n%s", f.listed(), f.model().reading(), f.currentID(), f.view())
	}

	for range len(unread) {
		f.keys("a")
	}
	if f.model().focus != paneList || f.model().reading() {
		t.Errorf("all archived: focus %v, still reading together", f.model().focus)
	}
	for _, id := range unread {
		if slices.Contains(f.listed(), id) {
			t.Errorf("%s not archived", id)
		}
	}
}

func TestReadTogether_SelectedOnes(t *testing.T) {
	f := newFixture(t, 80, 24)
	want := f.listed()[2:4]
	f.keys("j", "j", "x", "x")

	f.keys("T")

	var got []string
	for _, th := range f.model().togetherThreads() {
		got = append(got, th.ID)
	}
	if !slices.Equal(got, want) {
		t.Errorf("together = %v, want %v", got, want)
	}
	f.keys("esc")
	if f.model().focus != paneList || f.model().reading() {
		t.Errorf("esc: focus %v, still reading together", f.model().focus)
	}
}

func TestReadTogether_UndoAfterTheLastReopensThePage(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	f.keys("T")
	for range unread {
		f.keys("a")
	}

	f.keys("u")

	if !f.model().reading() || f.currentID() != unread[len(unread)-1] || !strings.Contains(f.view(), f.togetherTitle(1, 1)) {
		t.Errorf("reading %v, current %s; want the page back with %s\n%s", f.model().reading(), f.currentID(), unread[len(unread)-1], f.view())
	}
	f.keys("u")
	if f.currentID() != unread[len(unread)-2] || !strings.Contains(f.view(), f.togetherTitle(1, 2)) {
		t.Errorf("second undo: current %s, want %s\n%s", f.currentID(), unread[len(unread)-2], f.view())
	}
}

func TestUndo_History(t *testing.T) {
	f := newFixture(t, 100, 24)
	before := f.listed()

	f.keys("a", "*", "a", "a") // archive the 1st; star, archive the 2nd; archive the 3rd

	f.keys("u", "u", "u") // the 3rd and 2nd back, the star undone
	if l := f.listed(); slices.Contains(l, before[0]) || !slices.Contains(l, before[1]) || !slices.Contains(l, before[2]) {
		t.Errorf("three undos: listed %v, want %v back, not %s", l, before[1:3], before[0])
	}
	if f.thread(before[1]).Starred() {
		t.Errorf("%s still starred", before[1])
	}
	f.keys("u")
	if !slices.Equal(f.listed(), before) {
		t.Errorf("four undos: listed %v, want %v", f.listed(), before)
	}
	f.keys("u")
	if !strings.Contains(f.view(), "nothing to undo") {
		t.Errorf("view:\n%s", f.view())
	}
}

// togetherTitle is the reader's title reading together: at which of n, and its subject.
func (f *fixture) togetherTitle(at, n int) string {
	t, _ := f.model().current()
	return fmt.Sprintf("Together %d/%d · %s", at, n, output.Subject(t.Subject()))
}

func TestReadTogether_CurrentIsMarkedAllAlong(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	f.keys("T", "n")

	if !strings.Contains(f.view(), f.togetherTitle(2, len(unread))) {
		t.Errorf("title doesn't name the current one:\n%s", f.view())
	}
	cur := f.thread(unread[1])
	inCurrent := false
	for _, l := range f.model().readerLines(80) {
		if strings.Contains(l, output.Subject(f.thread(unread[2]).Subject())) {
			inCurrent = false // the next one starts
		}
		if strings.Contains(l, output.Subject(cur.Subject())) {
			inCurrent = true
			if !strings.Contains(l, "▶ "+output.Subject(cur.Subject())) {
				t.Errorf("current subject without ▶: %q", l)
			}
		}
		if strings.TrimSpace(l) != "" && strings.HasPrefix(l, "▌") != inCurrent { // not the gaps between
			t.Errorf("bar %v, want %v: %q", !inCurrent, inCurrent, l)
		}
	}
	f.keys("j", "j", "j") // scrolled into it: still marked
	if top := f.model().readerLines(80)[f.model().scroll]; !strings.HasPrefix(top, "▌") {
		t.Errorf("top line not marked: %q", top)
	}
}

// marked are the reader's lines with the bar, and the one with ▶.
func (f *fixture) marked() (barred []string, arrow string) {
	for _, l := range f.model().readerLines(80) {
		if strings.HasPrefix(l, "▌") {
			barred = append(barred, l)
		}
		if strings.Contains(l, "▶") {
			arrow = l
		}
	}
	return barred, arrow
}

func TestReader_MarksTheMessageReplyAnswers(t *testing.T) {
	f := newFixture(t, 80, 24)
	mine := model.Message{Account: workAcct, ID: "t-launch-me", ThreadID: "t-launch", Subject: "Re: Launch plan",
		Date: now.Add(-time.Minute), From: model.Address{Name: "Me", Email: workAcct}, Body: "On it.",
		Labels: []string{model.Inbox, model.Sent}}
	f.st.UpsertMessages(context.Background(), []model.Message{mine})
	f.keys("g", "i") // reload

	f.keys("enter") // the launch plan: Maya, Tom, Maya, then me

	barred, arrow := f.marked()
	if !strings.Contains(arrow, "▶ Maya Chen") || !strings.Contains(arrow, "<maya@work.example>") {
		t.Errorf("▶ on %q, want Maya's latest message", arrow)
	}
	page := strings.Join(barred, "\n")
	if !strings.Contains(page, "migration guide") || strings.Contains(page, "On it.") || strings.Contains(page, "Looks good") || strings.Contains(page, "Hi both") {
		t.Errorf("the bar isn't on Maya's latest message alone:\n%s", page)
	}
}

func TestReader_OneMessageIsntMarked(t *testing.T) {
	f := newFixture(t, 80, 24)
	f.keys("j", "enter") // the CI mail: one message

	if barred, arrow := f.marked(); len(barred) > 0 || arrow != "" {
		t.Errorf("marked %q, %q", barred, arrow)
	}
}

func TestReader_TabMovesBetweenMessages(t *testing.T) {
	f := newFixture(t, 80, 24)

	f.keys("enter") // the launch plan: Maya, Tom, Maya; opens on the one a reply answers

	arrow := func() string { _, a := f.marked(); return a }
	if f.model().scroll != 0 || !strings.Contains(arrow(), "<maya@work.example>") {
		t.Errorf("scroll %d, ▶ on %q; want the top, Maya's latest", f.model().scroll, arrow())
	}
	f.keys("shift+tab")
	if !strings.Contains(arrow(), "▶ Tom Okafor") {
		t.Errorf("shift+tab: ▶ on %q, want Tom's", arrow())
	}
	f.keys("shift+tab", "shift+tab")
	if !strings.Contains(arrow(), "Hi both") {
		t.Errorf("shift+tab at the first: ▶ on %q, want Maya's first", arrow())
	}
	f.keys("tab", "tab", "tab")
	if !strings.Contains(arrow(), "<maya@work.example>") {
		t.Errorf("tab at the last: ▶ on %q, want Maya's latest", arrow())
	}
}

func TestReadTogether_TabMovesWithinTheCurrentConversation(t *testing.T) {
	f := newFixture(t, 80, 24)
	f.keys("T") // the launch plan first

	f.keys("shift+tab")

	if _, arrow := f.marked(); !strings.Contains(arrow, "▶ Tom Okafor") {
		t.Errorf("▶ on %q, want Tom's", arrow)
	}
	if f.currentID() != "t-launch" {
		t.Errorf("current %s, want t-launch", f.currentID())
	}
}

func TestReader_ActionsApplyToTheMarkedMessage(t *testing.T) {
	for _, tc := range []struct{ keys, want string }{
		{"R", "To: Tom Okafor <tom@work.example>"},
		{"f", "From: Tom Okafor <tom@work.example>"},
	} {
		f := newFixture(t, 80, 24)
		f.keys("enter", "shift+tab") // Tom's message

		f.keys(tc.keys)

		c := f.model().composing
		if c == nil {
			t.Fatalf("%s: not composing", tc.keys)
		}
		if text := "To: " + c.base.To + "\n" + c.base.Body; !strings.Contains(text, tc.want) {
			t.Errorf("%s: want %q in\n%s", tc.keys, tc.want, text)
		}
	}

	f := newFixture(t, 80, 24)
	f.keys("enter", "shift+tab", "D") // Tom's message has none; Maya's latest has one
	if !strings.Contains(f.view(), "no attachments here") {
		t.Errorf("D on Tom's message:\n%s", f.view())
	}
}

func TestReadTogether_TabGoesOnToTheNextConversation(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	f.keys("T") // the launch plan first, ▶ on its last message

	f.keys("tab")

	if f.currentID() != unread[1] || !strings.Contains(f.view(), f.togetherTitle(2, len(unread))) {
		t.Errorf("tab at the last message: current %s, want %s\n%s", f.currentID(), unread[1], f.view())
	}
	f.keys("shift+tab")
	if _, arrow := f.marked(); f.currentID() != unread[0] || !strings.Contains(arrow, "<maya@work.example>") {
		t.Errorf("shift+tab: current %s, ▶ on %q; want Maya's latest in %s", f.currentID(), arrow, unread[0])
	}
}

func TestReader_EnterOpensAndClosesTheMarkedMessage(t *testing.T) {
	f := newFixture(t, 80, 24)
	page := func() string { return strings.Join(f.model().readerLines(80), "\n") }
	f.keys("enter", "shift+tab") // the launch plan, ▶ on Tom's message

	f.keys("enter", "shift+tab", "enter") // open Tom's, then Maya's first

	if p := page(); !strings.Contains(p, "last time they took longer") || !strings.Contains(p, "Freeze features") ||
		!strings.Contains(p, "migration guide") {
		t.Errorf("want all three open:\n%s", p)
	}
	f.keys("tab", "enter")
	if p := page(); strings.Contains(p, "last time they took longer") || !strings.Contains(p, "Freeze features") {
		t.Errorf("enter again: Tom's should close, the others stay:\n%s", p)
	}
}

func TestReader_JKMoveTheMarkWhenThereIsNothingToScroll(t *testing.T) {
	f := newFixture(t, 80, 24)
	f.keys("enter") // the launch plan fits: nothing to scroll
	arrow := func() string { _, a := f.marked(); return a }

	f.keys("k")
	if !strings.Contains(arrow(), "▶ Tom Okafor") {
		t.Errorf("k: ▶ on %q, want Tom's", arrow())
	}
	f.keys("k", "j", "enter")
	if !strings.Contains(arrow(), "▶ Tom Okafor") || !f.model().expanded["t-launch-02"] {
		t.Errorf("k, j, enter: ▶ on %q, open %v; want Tom's, open", arrow(), f.model().expanded)
	}
}

func TestReader_JKScrollFirst(t *testing.T) {
	f := newFixture(t, 80, 10)
	f.keys("enter") // the launch plan doesn't fit in 9 lines
	arrow := func() string { _, a := f.marked(); return a }

	f.keys("j")
	if f.model().scroll != 1 || !strings.Contains(arrow(), "<maya@work.example>") {
		t.Errorf("j: scroll %d, ▶ on %q; want scrolled, ▶ where it was", f.model().scroll, arrow())
	}
	f.keys("k", "k") // at the top: k moves ▶
	if f.model().scroll != 0 || !strings.Contains(arrow(), "▶ Tom Okafor") {
		t.Errorf("k at the top: scroll %d, ▶ on %q; want Tom's", f.model().scroll, arrow())
	}
}

func TestQuit_QInTheListElseZZ(t *testing.T) {
	quits := func(keys ...tea.KeyMsg) bool {
		var m tea.Model = newFixture(t, 80, 24).model()
		var cmd tea.Cmd
		for _, k := range keys {
			m, cmd = m.Update(k)
		}
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)
		return ok
	}
	r := func(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

	if !quits(r("q")) {
		t.Error("q in the list doesn't quit")
	}
	if quits(r("l"), r("q")) || quits(tea.KeyMsg{Type: tea.KeyEnter}, r("q")) {
		t.Error("q in the reader quits")
	}
	if !quits(r("Z"), r("Z")) {
		t.Error("ZZ doesn't quit")
	}
	if !quits(tea.KeyMsg{Type: tea.KeyCtrlC}) {
		t.Error("ctrl+c doesn't quit")
	}
}

func TestReader_JKReadTheNextMessage(t *testing.T) {
	f := newFixture(t, 80, 24)
	top := func() string { return f.model().readerLines(80)[f.model().scroll] }
	f.keys("enter") // the launch plan, ▶ on Maya's latest

	f.keys("K")
	if !strings.Contains(top(), "▶ Tom Okafor <tom@work.example>") {
		t.Errorf("K: top %q, want Tom's message, open", top())
	}
	f.keys("K")
	if !strings.Contains(top(), "▶ Maya Chen <maya@work.example>") || !f.model().expanded["t-launch-01"] {
		t.Errorf("K: top %q, want Maya's first, open", top())
	}
	f.keys("J")
	if !strings.Contains(top(), "▶ Tom Okafor <tom@work.example>") || !f.model().expanded["t-launch-01"] {
		t.Errorf("J: top %q, want Tom's, Maya's first still open", top())
	}
	scroll := f.model().scroll
	f.keys("j") // the rest fits below: j moves ▶ on, without scrolling back
	if _, arrow := f.marked(); f.model().scroll != scroll || !strings.Contains(arrow, "▶ Maya Chen <maya@work.example>") {
		t.Errorf("j after J: scroll %d (was %d), ▶ on %q; want Maya's latest", f.model().scroll, scroll, arrow)
	}
	f.keys("J") // the last: J keeps it, at the top
	if !strings.Contains(top(), "▶ Maya Chen <maya@work.example>") {
		t.Errorf("J at the last: top %q", top())
	}
}

func TestReadTogether_JGoesOnToTheNextConversation(t *testing.T) {
	f := newFixture(t, 80, 24)
	unread := f.unreadListed()
	f.keys("T")

	f.keys("J")

	if f.currentID() != unread[1] {
		t.Errorf("J at the last message: current %s, want %s", f.currentID(), unread[1])
	}
}

func TestList_JKStillPage(t *testing.T) {
	f := newFixture(t, 80, 10)

	f.keys("J")

	if f.model().cursor < 5 {
		t.Errorf("J in the list: cursor %d, want a page down", f.model().cursor)
	}
}

func TestView_DropsTerminalControlSequences(t *testing.T) {
	f := newFixture(t, 100, 24)
	evil := model.Message{Account: workAcct, ID: "evil1", ThreadID: "tevil", Date: now, Labels: []string{model.Inbox, model.Unread},
		Subject: "Hi\x1b]52;c;cHduZWQ=\x07 there", From: model.Address{Name: "Eve\x1b[31m", Email: "eve@x.example"},
		Snippet: "snip\x1b]8;;http://evil\x07link", Body: "body\x1b]0;title\x07\r\nfake\x1b[1A\x1b[2K",
		Attachments: []model.Attachment{{Filename: "invoice‮fdp.exe\x1b[8m"}}}
	f.st.UpsertMessages(context.Background(), []model.Message{evil})
	f.keys("g", "i", "g", "g") // the newest: on top

	for _, k := range []string{"", "enter"} { // the list, then the reader
		if k != "" {
			f.keys(k)
		}
		if v := f.view(); strings.ContainsAny(v, "\x1b\x07\r‮") {
			t.Errorf("after %q: %q", k, v)
		}
	}
}

func TestLaunch_NoPreviewUntilTheCursorMoves(t *testing.T) {
	f := newFixtureWith(t, 150, 30, Deps{NoPreviewAtLaunch: true})

	if v := f.view(); !strings.Contains(v, "No conversation selected") || strings.Count(v, f.thread(f.currentID()).Subject()) > 1 {
		t.Fatal("the reader previews a conversation at launch")
	}

	f.keys("j")

	if strings.Contains(f.view(), "No conversation selected") {
		t.Error("the reader doesn't follow the cursor once it moves")
	}
}

func TestLaunch_NoPreviewOpensOnEnter(t *testing.T) {
	f := newFixtureWith(t, 150, 30, Deps{NoPreviewAtLaunch: true})

	f.keys("enter", "esc")

	if strings.Contains(f.view(), "No conversation selected") {
		t.Error("the conversation opened isn't shown")
	}
}

func TestLaunch_SidebarHidden(t *testing.T) {
	f := newFixtureWith(t, 150, 30, Deps{HideSidebar: true})

	if f.model().sidebarShown() {
		t.Fatal("sidebar shown at launch")
	}

	f.keys("b")

	if !f.model().sidebarShown() {
		t.Error("b doesn't show it")
	}
}
