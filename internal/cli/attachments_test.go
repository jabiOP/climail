package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jabiOP/climail/internal/model"
)

func withFiles(m model.Message, names ...string) model.Message {
	for _, n := range names {
		m.Attachments = append(m.Attachments, model.Attachment{Filename: n, Size: 2000})
	}
	return m
}

func TestAttachments_List(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(withFiles(msg("a1", "a", "Docs", 30, "INBOX"), "a.pdf", "b.png"))

	out := h.mustRun("attachments", "a")

	if out != "1\ta.pdf\t2 KB\tAlice\n2\tb.png\t2 KB\tAlice\n" {
		t.Errorf("got %q", out)
	}
}

func TestDownload_ChosenOnesIntoFolder(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(withFiles(msg("a1", "a", "Docs", 30, "INBOX"), "a.pdf", "b.png"))
	h.gmail[work].SetAttachmentData("a1", 1, []byte("PNG"))
	h.mustRun("sync")
	dir := t.TempDir()

	out := h.mustRun("download", "a", "2", "-o", dir)

	if out != filepath.Join(dir, "b.png")+"\n" {
		t.Errorf("got %q", out)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "b.png")); string(data) != "PNG" {
		t.Errorf("saved %q", data)
	}
	if _, _, code := h.run("download", "a", "3", "-o", dir); code != 2 {
		t.Errorf("attachment 3 of 2: exit %d", code)
	}
}

func TestForward_SendsAttachmentsAlong(t *testing.T) {
	h := newHarness(t, work)
	h.gmail[work].Deliver(withFiles(msg("a1", "a", "Report", 30, "INBOX"), "report.pdf"))
	h.edit = func(old string) string { return strings.Replace(old, "To: ", "To: bob@example.com", 1) }
	h.stdin = "y\n"

	_, errOut, _ := h.run("forward", "a")

	if !strings.Contains(errOut, "“Fwd: Report” to bob (1 attached)") {
		t.Errorf("stderr %q", errOut)
	}
	if raw := h.sentText(work); !strings.Contains(raw, "filename=report.pdf") {
		t.Errorf("sent:\n%s", raw)
	}
}
