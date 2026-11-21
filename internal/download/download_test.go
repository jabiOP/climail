package download_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/jabiOP/climail/internal/download"
	"github.com/jabiOP/climail/internal/model"
)

func TestSave_NeverOverwrites(t *testing.T) {
	dir := t.TempDir()

	a, _ := download.Save(dir, "report.pdf", []byte("1"))
	b, _ := download.Save(dir, "report.pdf", []byte("2"))

	if filepath.Base(a) != "report.pdf" || filepath.Base(b) != "report (1).pdf" {
		t.Errorf("paths %s, %s", a, b)
	}
	if data, _ := os.ReadFile(a); string(data) != "1" {
		t.Errorf("first file overwritten: %q", data)
	}
}

func TestSave_NameCannotLeaveDir(t *testing.T) {
	dir := t.TempDir()

	for _, name := range []string{"../../evil.sh", "/etc/passwd", "", ".."} {
		path, err := download.Save(dir, name, []byte("x"))
		if err != nil || filepath.Dir(path) != dir {
			t.Errorf("Save(%q) = %s, %v", name, path, err)
		}
	}
}

func TestAll(t *testing.T) {
	dir := t.TempDir()
	fetch := func(_ context.Context, id string, i int) ([]byte, error) {
		if id == "bad" {
			return nil, errors.New("offline")
		}
		return []byte(id), nil
	}
	refs := []model.AttachmentRef{{MessageID: "m1", Attachment: model.Attachment{Filename: "a.txt"}},
		{MessageID: "bad", Attachment: model.Attachment{Filename: "b.txt"}}}

	paths, err := download.All(context.Background(), fetch, refs, dir)

	if err == nil || len(paths) != 1 {
		t.Errorf("paths %v, err %v", paths, err)
	}
	if diff := cmp.Diff([]string{filepath.Join(dir, "a.txt")}, paths); diff != "" {
		t.Errorf("mismatch (-want +got):\n%s", diff)
	}
}
