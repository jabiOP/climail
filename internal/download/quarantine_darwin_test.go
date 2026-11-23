package download_test

import (
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/jabiOP/climail/internal/download"
)

// Saved attachments are marked as downloaded, as Mail and browsers do, so
// macOS asks before opening a risky one (a .terminal file runs commands).
func TestSave_MarksAsDownloadedOnMacOS(t *testing.T) {
	path, err := download.Save(t.TempDir(), "settings.terminal", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, "com.apple.quarantine", buf)

	if err != nil || !strings.Contains(string(buf[:n]), ";climail;") {
		t.Errorf("quarantine = %q, %v", buf[:max(n, 0)], err)
	}
}
