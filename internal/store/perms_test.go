package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jabiOP/climail/internal/store"
)

func TestOpen_CacheIsPrivate(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "old.db")
	if err := os.WriteFile(old, nil, 0o644); err != nil { // made by an earlier version, open to others
		t.Fatal(err)
	}

	for _, path := range []string{filepath.Join(dir, "new.db"), old} {
		st, err := store.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.EnsureAccount(ctx, acct); err != nil { // a write: the WAL files exist
			t.Fatal(err)
		}
		for _, f := range []string{path, path + "-wal", path + "-shm"} {
			if fi, err := os.Stat(f); err == nil && fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: %v, want 0600", filepath.Base(f), fi.Mode().Perm())
			}
		}
		st.Close()
	}
}
