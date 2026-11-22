package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrate_FillsContactsForMessagesAlreadyCached(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:2] { // the schema before the people column
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	_, err = db.ExecContext(ctx, `PRAGMA user_version = 2;
		INSERT INTO messages (account_id, id, thread_id, ts, labels, search, data) VALUES ('me@x.com', 'm1', 't1', 0, ',INBOX,', '',
		'{"From":{"Name":"Maya","Email":"maya@x.com"},"To":[{"Name":"","Email":"me@x.com"}],"Cc":null,"Body":"hi"}')`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Contacts(ctx, "me@x.com")
	if err != nil || len(got) != 1 || got[0].Name != "Maya" || got[0].Email != "maya@x.com" {
		t.Errorf("Contacts = %v, %v", got, err)
	}
}
