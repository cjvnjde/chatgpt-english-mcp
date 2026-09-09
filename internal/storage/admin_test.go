package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAdminHistorySearchPaginationAndChronology(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "live.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_, err = store.sql.Exec(`INSERT INTO vocabulary_items (id, owner_key, term, normalized_term, sense_key, created_at, updated_at) VALUES ('word', 'owner', 'Wringer', 'wringer', 'legacy', '2026-09-09T10:00:00Z', '2026-09-09T10:00:00Z')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, shown := range []string{"2026-09-09T10:00:00Z", "2026-09-09T10:00:00.1Z", "2026-09-10T10:00:00Z"} {
		_, err := store.sql.Exec(`INSERT INTO learning_presentations (owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind) VALUES ('owner', 'word', 'word:production', 'production', 'token', ?, ?, 'new')`, shown, shown)
		if err != nil {
			t.Fatal(err)
		}
	}
	query := AdminQuery{Query: "WRINGER", Sort: "shown_at", Direction: "asc", From: "2026-09-09", To: "2026-09-09", Limit: 1}
	page, err := store.AdminRows(ctx, "learning_presentations", query)
	if err != nil || page.Total != 2 || len(page.Rows) != 1 || page.Rows[0]["shown_at"] != "2026-09-09T10:00:00Z" {
		t.Fatalf("first page: %#v %v", page, err)
	}
	query.Offset = 1
	page, err = store.AdminRows(ctx, "learning_presentations", query)
	if err != nil || page.Rows[0]["shown_at"] != "2026-09-09T10:00:00.1Z" {
		t.Fatalf("second page: %#v %v", page, err)
	}
	file, cleanup, err := store.AdminBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	path := file.Name()
	cleanup()
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("temporary backup directory not removed: %v", err)
	}
}
