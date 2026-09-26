package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestContextRevisionMigrationPreservesExistingRecords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	legacy := openLegacyDatabase(t, path, 17)
	if _, err := legacy.Exec(`INSERT INTO vocabulary_items
		(id, owner_key, term, normalized_term, context, sense_key, created_at, updated_at)
		VALUES ('saved', 'owner', 'bank', 'bank', 'river', 'context:river', '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var beforeRevision int64
	if err := store.sql.QueryRow(`SELECT edit_revision FROM vocabulary_items WHERE id = 'saved'`).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	// A context and its coupled identity advance the revision only once.
	if _, err := store.sql.Exec(`UPDATE vocabulary_items SET context = 'finance', sense_key = 'context:finance' WHERE id = 'saved'`); err != nil {
		t.Fatal(err)
	}
	var revision int64
	var contextValue, created, updated string
	if err := store.sql.QueryRow(`SELECT edit_revision, context, created_at, updated_at FROM vocabulary_items WHERE id = 'saved'`).Scan(&revision, &contextValue, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if revision != beforeRevision+1 || contextValue != "finance" || created != "2026-09-01T00:00:00Z" || updated != created {
		t.Fatalf("upgrade changed unrelated data or missed revision: %d %q %q %q", revision, contextValue, created, updated)
	}
	if _, err := store.sql.Exec(`UPDATE vocabulary_items SET context = context, sense_key = sense_key WHERE id = 'saved'`); err != nil {
		t.Fatal(err)
	}
	if err := store.sql.QueryRow(`SELECT edit_revision FROM vocabulary_items WHERE id = 'saved'`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if revision != beforeRevision+1 {
		t.Fatal("no-op changed revision")
	}
	for index, statement := range []string{
		`UPDATE vocabulary_items SET context = 'revised context' WHERE id = 'saved'`,
		`UPDATE vocabulary_items SET sense_key = 'context:revised' WHERE id = 'saved'`,
	} {
		if _, err := store.sql.Exec(statement); err != nil {
			t.Fatal(err)
		}
		if err := store.sql.QueryRow(`SELECT edit_revision FROM vocabulary_items WHERE id = 'saved'`).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if revision != beforeRevision+2+int64(index) {
			t.Fatal("independent context or identity change did not advance revision")
		}
	}
}
