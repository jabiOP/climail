package tui

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/jabiOP/climail/internal/demo"
	"github.com/jabiOP/climail/internal/model"
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
	t  *testing.T
	st *store.Store
	m  tea.Model
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
	d.Service, d.Now = service.New(st), func() time.Time { return now }
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
		"ctrl+d": tea.KeyCtrlD, "ctrl+u": tea.KeyCtrlU, "backspace": tea.KeyBackspace, "space": tea.KeySpace}
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
	f := newFixtureWith(t, 100, 24, Deps{Sync: func(ctx context.Context) error {
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
