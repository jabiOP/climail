package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jabiOP/climail/internal/model"
)

// sentText is the latest mail the fake Gmail sent, as text.
func (h *harness) sentText(account string) string {
	h.t.Helper()
	sent := h.gmail[account].Sent()
	if len(sent) == 0 {
		h.t.Fatal("nothing sent")
	}
	return string(sent[len(sent)-1])
}

func TestCompose_EditThenSend(t *testing.T) {
	h := newHarness(t, work)
	h.edit = func(old string) string {
		return strings.Replace(old, "To: ", "To: bob@example.com", 1) + "Hello Bob\n"
	}
	h.stdin = "y\n"

	_, errOut, code := h.run("compose", "-s", "Lunch")

	if code != 0 || !strings.Contains(errOut, "Send “Lunch” to bob?") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if raw := h.sentText(work); !strings.Contains(raw, "To: <bob@example.com>") || !strings.Contains(raw, "Hello Bob") {
		t.Errorf("sent:\n%s", raw)
	}
	if out := h.mustRun("list", "sent", "--offline"); !strings.Contains(out, "Lunch") {
		t.Errorf("Sent = %q, want the sent mail pulled back", out)
	}
}

func TestCompose_QuitSavesToDrafts(t *testing.T) {
	h := newHarness(t, work)
	h.edit = func(old string) string { return strings.Replace(old, "To: ", "To: bob@example.com", 1) }
	h.stdin = "q\n"

	_, errOut, _ := h.run("compose", "-s", "Later")

	if len(h.gmail[work].Sent()) != 0 || !strings.Contains(errOut, "Saved in Drafts.") {
		t.Errorf("stderr %q", errOut)
	}
	if out := h.mustRun("list", "drafts", "--offline"); !strings.Contains(out, "Later") {
		t.Errorf("Drafts = %q", out)
	}
	if files, _ := filepath.Glob(filepath.Join(h.app.Paths.DraftDir(), "*.eml")); len(files) != 0 {
		t.Errorf("local drafts = %v", files)
	}
}

func TestCompose_UnreadableDraftStaysOnDisk(t *testing.T) {
	h := newHarness(t, work)
	h.edit = func(old string) string { return "Frm: oops\n" + old }
	h.stdin = "q\n"

	_, errOut, _ := h.run("compose")

	files, _ := filepath.Glob(filepath.Join(h.app.Paths.DraftDir(), "*.eml"))
	if len(files) != 1 || !strings.Contains(errOut, "Not saved. The draft is in") {
		t.Errorf("drafts %v, stderr %q", files, errOut)
	}
}

func TestCompose_UnchangedIsNotSent(t *testing.T) {
	h := newHarness(t, work)

	_, errOut, code := h.run("compose", "--to", "bob@example.com")

	if code != 0 || !strings.Contains(errOut, "wasn't changed") || len(h.gmail[work].Sent()) != 0 {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}

func TestCompose_ErrorOffersToEditAgain(t *testing.T) {
	h := newHarness(t, work)
	edits := 0
	h.edit = func(old string) string {
		edits++
		if edits == 1 {
			return old + "forgot the recipient\n"
		}
		return strings.Replace(old, "To: ", "To: bob@example.com", 1)
	}
	h.stdin = "e\ny\n"

	_, errOut, _ := h.run("compose")

	if !strings.Contains(errOut, "no recipients") || len(h.gmail[work].Sent()) != 1 {
		t.Errorf("edits %d, stderr %q", edits, errOut)
	}
}

func TestReply_ThreadsIntoConversation(t *testing.T) {
	h := newHarness(t, work)
	m := msg("a1", "a", "Plan", 30, "INBOX")
	m.MessageID = "<plan@example.com>"
	h.gmail[work].Deliver(m)
	h.edit = func(old string) string { return strings.Replace(old, "\n\n", "\n\nSounds good", 1) }
	h.stdin = "y\n"

	h.mustRun("reply", "a")

	raw := h.sentText(work)
	for _, want := range []string{"To: \"Alice\" <alice@example.com>", "Subject: Re: Plan", "In-Reply-To: <plan@example.com>",
		"Sounds good", "> body a1"} {
		if !strings.Contains(raw, want) {
			t.Errorf("sent mail lacks %q:\n%s", want, raw)
		}
	}
	if threads := h.mustRun("show", "a", "--offline"); !strings.Contains(threads, "── "+work) {
		t.Errorf("reply not in the conversation:\n%s", threads)
	}
}

func TestSend_FromStdin(t *testing.T) {
	h := newHarness(t, work)
	h.stdin = "Report attached.\n"
	file := filepath.Join(t.TempDir(), "r.txt")
	os.WriteFile(file, []byte("data"), 0o600)

	h.mustRun("send", "--to", "bob@example.com", "-s", "Report", "--attach", file)

	if raw := h.sentText(work); !strings.Contains(raw, "Report attached.") || !strings.Contains(raw, "filename=r.txt") {
		t.Errorf("sent:\n%s", raw)
	}
}

func TestSend_NoRecipientIsUsageError(t *testing.T) {
	h := newHarness(t, work)

	if _, _, code := h.run("send", "-s", "x"); code != 2 {
		t.Errorf("exit %d, want 2", code)
	}
}

func TestOutbox_RefusedMailCanBeRetriedOrDiscarded(t *testing.T) {
	h := newHarness(t, work)
	h.stdin = "x\n"
	h.mustRun("send", "--to", "bob@example.com", "-s", "One", "--offline")

	out := h.mustRun("outbox")
	if !strings.HasPrefix(out, "1\t“One” to bob\twaiting") {
		t.Fatalf("outbox = %q", out)
	}
	h.mustRun("outbox", "discard", "1")
	if out := h.mustRun("outbox"); out != "" {
		t.Errorf("after discard: %q", out)
	}
	if _, _, code := h.run("outbox", "retry", "1"); code != 1 {
		t.Errorf("retry of a discarded mail: exit %d", code)
	}
}

func TestContactsAndNamesAsRecipients(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(model.Message{ID: "m1", ThreadID: "t1", Date: now, Subject: "Hi", Labels: []string{"INBOX"},
		From: model.Address{Name: "Maya Chen", Email: "maya@example.com"}})

	if out := h.mustRun("contacts", "may"); out != "maya@example.com\tMaya Chen\n" {
		t.Errorf("contacts = %q", out)
	}
	h.stdin = "body\n"
	if _, errOut, code := h.run("send", "--to", "maya", "-s", "Hello"); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if raw := h.sentText(work); !strings.Contains(raw, `To: "Maya Chen" <maya@example.com>`) {
		t.Errorf("sent:\n%s", raw)
	}
	if _, errOut, code := h.run("send", "--to", "nobody", "-s", "x"); code == 0 || !strings.Contains(errOut, `no one matches "nobody"`) {
		t.Errorf("exit %d, stderr %q", code, errOut)
	}
}
