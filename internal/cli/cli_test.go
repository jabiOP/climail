package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jabiOP/climail/internal/cli"
	"github.com/jabiOP/climail/internal/config"
	"github.com/jabiOP/climail/internal/gmail/gmailtest"
	"github.com/jabiOP/climail/internal/model"
	"github.com/jabiOP/climail/internal/syncer"
)

const (
	work     = "work@example.com"
	personal = "personal@example.com"
)

var now = time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC)

// harness runs climail commands in-process against temp dirs and fake Gmails.
type harness struct {
	t     *testing.T
	app   *cli.App
	gmail map[string]*gmailtest.Fake
	stdin string
}

func newHarness(t *testing.T, accounts ...string) *harness {
	t.Helper()
	h := &harness{t: t, gmail: map[string]*gmailtest.Fake{}}
	dir := t.TempDir()
	h.app = &cli.App{
		Paths:  config.Paths{ConfigDir: dir + "/config", DataDir: dir + "/data"},
		Config: config.Default(),
		Now:    func() time.Time { return now },
		Remote: func(_ context.Context, account string) (syncer.Remote, error) { return h.gmail[account], nil },
	}
	t.Cleanup(func() { h.app.Close() })
	st, err := h.app.Store()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range accounts {
		h.gmail[a] = gmailtest.New(a)
		h.gmail[a].SetLabels(model.Label{ID: "Label_1", Name: "Work/Clients"})
		if err := st.EnsureAccount(context.Background(), a); err != nil { // as `auth login` would
			t.Fatal(err)
		}
	}
	return h
}

func (h *harness) run(args ...string) (string, string, int) {
	h.t.Helper()
	var stdout, stderr bytes.Buffer
	h.app.Stdin, h.app.Stdout, h.app.Stderr = strings.NewReader(h.stdin), &stdout, &stderr
	code := cli.Run(context.Background(), h.app, args)
	return stdout.String(), stderr.String(), code
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, errOut, code := h.run(args...)
	if code != 0 {
		h.t.Fatalf("climail %s: exit %d\n%s", strings.Join(args, " "), code, errOut)
	}
	return out
}

func msg(id, th, subject string, minutes int, labels ...string) model.Message {
	return model.Message{ID: id, ThreadID: th, Subject: subject, Labels: labels, Body: "body " + id,
		From: model.Address{Name: "Alice", Email: "alice@example.com"}, Date: now.Add(time.Duration(-minutes) * time.Minute)}
}

func TestList_InboxAsTSVInAPipe(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "Hello", 30, "INBOX", "UNREAD"), msg("b1", "b", "Old", 90, "INBOX"), msg("c1", "c", "Sent", 10, "SENT"))

	out := h.mustRun("list")

	want := "a\t2026-11-20T14:30:00Z\t1\tAlice\tHello\t1\nb\t2026-11-20T13:30:00Z\t0\tAlice\tOld\t1\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestList_LabelByNameAndSearch(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "Invoice", 30, "Label_1"), msg("b1", "b", "Lunch", 20, "Label_1"))

	out := h.mustRun("list", "work/clients", "-q", "invoice")

	if !strings.HasPrefix(out, "a\t") || strings.Count(out, "\n") != 1 {
		t.Errorf("got %q, want just a", out)
	}
}

func TestList_UnknownLabelIsUsageError(t *testing.T) {
	h := newHarness(t, work)

	_, errOut, code := h.run("list", "nope")

	if code != 2 || !strings.Contains(errOut, `no label "nope"`) {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestShow_PrintsConversation(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "Hello", 30, "INBOX"), msg("a2", "a", "Re: Hello", 20, "INBOX"))

	out := h.mustRun("show", "a")

	if !strings.HasPrefix(out, "Hello\n") || !strings.Contains(out, "body a1") || !strings.Contains(out, "body a2") {
		t.Errorf("got:\n%s", out)
	}
}

func TestArchive_ChangesGmail(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "Hello", 30, "INBOX"))
	h.mustRun("sync")

	_, errOut, code := h.run("archive", "a")

	if code != 0 || !strings.Contains(errOut, "Archived 1 conversation.") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
	if m, _ := h.gmail[work].Get("a1"); m.Has(model.Inbox) {
		t.Errorf("still in Gmail's inbox: %v", m.Labels)
	}
}

func TestActions_ReadIDsFromStdin(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "One", 30, "INBOX", "UNREAD"), msg("b1", "b", "Two", 20, "INBOX", "UNREAD"))
	h.stdin = h.mustRun("list")

	h.mustRun("read", "-")

	for _, id := range []string{"a1", "b1"} {
		if m, _ := h.gmail[work].Get(id); m.Unread() {
			t.Errorf("%s still unread", id)
		}
	}
}

func TestActions_OfflineQueues(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "One", 30, "INBOX"))
	h.mustRun("sync")

	h.mustRun("trash", "a", "--offline")

	if m, _ := h.gmail[work].Get("a1"); m.Has(model.Trash) {
		t.Error("reached Gmail despite --offline")
	}
	if out := h.mustRun("auth", "status"); !strings.Contains(out, "1 change(s) pending") {
		t.Errorf("status = %q", out)
	}
	if out := h.mustRun("list", "trash", "--offline"); !strings.HasPrefix(out, "a\t") {
		t.Errorf("trash = %q", out)
	}
}

func TestAccountFlag_PrefixPicksAccount(t *testing.T) {
	h := newHarness(t, work, personal)
	h.gmail[personal].Deliver(msg("p1", "p", "Family", 30, "INBOX"))
	h.gmail[work].Deliver(msg("w1", "w", "Boss", 30, "INBOX"))

	out := h.mustRun("list", "-a", "pers")

	if !strings.Contains(out, "Family") || strings.Contains(out, "Boss") {
		t.Errorf("got %q", out)
	}
}

func TestAccountFlag_Errors(t *testing.T) {
	h := newHarness(t, "ann@x.com", "andy@x.com")

	if _, errOut, code := h.run("list", "-a", "an"); code != 2 || !strings.Contains(errOut, "matches several") {
		t.Errorf("ambiguous: exit %d, %q", code, errOut)
	}
	if _, errOut, code := h.run("list", "-a", "zed"); code != 2 || !strings.Contains(errOut, `no account "zed"`) {
		t.Errorf("unknown: exit %d, %q", code, errOut)
	}
}

func TestNotLoggedIn(t *testing.T) {
	h := newHarness(t)

	_, errOut, code := h.run("list")

	if code != 1 || !strings.Contains(errOut, "climail auth login") {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestSync_AllAccounts(t *testing.T) {
	h := newHarness(t, work, personal)
	h.gmail[work].Deliver(msg("w1", "w", "Boss", 30, "INBOX"))
	h.gmail[personal].Deliver(msg("p1", "p", "Family", 30, "INBOX"))

	h.mustRun("sync")

	for _, a := range []string{work, personal} {
		if out := h.mustRun("list", "-a", a, "--offline"); strings.Count(out, "\n") != 1 {
			t.Errorf("%s: %q", a, out)
		}
	}
}

func TestLabels_TSV(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(msg("a1", "a", "Hello", 30, "INBOX", "UNREAD"))

	out := h.mustRun("labels")

	if !strings.HasPrefix(out, "INBOX\tInbox\t1\n") || !strings.Contains(out, "Label_1\tWork/Clients\t0\n") {
		t.Errorf("got:\n%s", out)
	}
}

func TestDemo_ListsMadeUpInboxWithoutTouchingRealData(t *testing.T) {
	h := newHarness(t)

	out := h.mustRun("demo")

	if !strings.Contains(out, "Launch plan for the 2.0 release") {
		t.Errorf("got %q", out)
	}
	if _, err := h.app.Accounts(context.Background()); err == nil {
		t.Error("the demo added an account to the real store")
	}
}
