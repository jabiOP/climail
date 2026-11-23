// Package download saves attachments to disk.
package download

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jabiOP/climail/internal/model"
)

// Fetch gets the data of a message's index-th attachment from Gmail.
type Fetch func(ctx context.Context, messageID string, index int) ([]byte, error)

// All downloads attachments into dir and returns the paths they were saved at.
func All(ctx context.Context, fetch Fetch, refs []model.AttachmentRef, dir string) ([]string, error) {
	var paths []string
	for _, r := range refs {
		data, err := fetch(ctx, r.MessageID, r.Index)
		if err != nil {
			return paths, fmt.Errorf("download %s: %w", r.Filename, err)
		}
		path, err := Save(dir, r.Filename, data)
		if err != nil {
			return paths, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// Save writes data into dir as name, private to the user, never overwriting:
// a second "report.pdf" becomes "report (1).pdf". The name can't leave dir.
// On macOS the file is marked as downloaded (see quarantine).
func Save(dir, name string, data []byte) (string, error) {
	name = filepath.Base(strings.NewReplacer("/", "_", "\\", "_", "\x00", "").Replace(name))
	if name == "." || name == "" {
		name = "attachment"
	}
	if err := os.MkdirAll(ExpandHome(dir), 0o700); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 0; ; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		path := filepath.Join(ExpandHome(dir), candidate)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			err = quarantine(path)
		}
		return path, err
	}
}

// ExpandHome turns a leading "~/" into the home folder.
func ExpandHome(path string) string {
	if rest, ok := strings.CutPrefix(path, "~/"); ok {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, rest)
		}
	}
	return path
}
