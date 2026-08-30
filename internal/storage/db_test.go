package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestOpenConfiguresAndMigratesPersistentSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "english-mcp.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	assertPragma(t, store, "foreign_keys", 1)
	assertPragma(t, store, "busy_timeout", 5000)
	assertColumn(t, store, "vocabulary_items", "lookup_id")
	assertColumn(t, store, "vocabulary_items", "custom_description")
	assertColumn(t, store, "vocabulary_items", "learning_status")
	assertColumn(t, store, "vocabulary_items", "tags_json")
	assertColumn(t, store, "vocabulary_items", "description_source_json")
	assertColumn(t, store, "vocabulary_items", "notes_json")
	assertColumn(t, store, "vocabulary_items", "examples_json")
	var explanationTables int
	if err := store.sql.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'explanations'",
	).Scan(&explanationTables); err != nil {
		t.Fatalf("check explanations table: %v", err)
	}
	if explanationTables != 0 {
		t.Fatal("obsolete explanations table still exists")
	}
	var journalMode string
	if err := store.sql.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	description := "An external description."
	created, saved, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey:          "owner",
		Term:              "Bank",
		NormalizedTerm:    "bank",
		Status:            domain.LearningStatusLearning,
		Tags:              []string{"finance"},
		CustomDescription: description,
		DescriptionSource: &domain.DescriptionSource{Title: "External source", URL: "https://example.test"},
		Notes:             []string{"Personal note."},
		Examples:          []string{"I visited the bank."},
		Now:               time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveVocabulary() error = %v", err)
	}
	if !created {
		t.Fatal("SaveVocabulary() created = false")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.VocabularyByID(ctx, "owner", saved.ItemID)
	if err != nil {
		t.Fatalf("VocabularyByID() after reopen error = %v", err)
	}
	if loaded.Term != "Bank" || loaded.NormalizedTerm != "bank" || loaded.Status != domain.LearningStatusLearning {
		t.Fatalf("persisted vocabulary identity = %#v", loaded)
	}
	if loaded.CustomDescription != description || loaded.DescriptionSource == nil || loaded.DescriptionSource.Title != "External source" {
		t.Fatalf("persisted description = %#v", loaded)
	}
	if len(loaded.Tags) != 1 || len(loaded.Notes) != 1 || len(loaded.Examples) != 1 || loaded.Lookup != nil {
		t.Fatalf("persisted vocabulary = %#v", loaded)
	}
}

func assertPragma(t *testing.T, store *DB, name string, want int) {
	t.Helper()
	var got int
	if err := store.sql.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("read PRAGMA %s: %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}

func assertColumn(t *testing.T, store *DB, table, column string) {
	t.Helper()
	rows, err := store.sql.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatalf("read columns for %s: %v", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var sequence int
		var name string
		var dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatalf("scan columns for %s: %v", table, err)
		}
		if name == column {
			return
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate columns for %s: %v", table, err)
	}
	t.Fatalf("column %s.%s does not exist", table, column)
}
