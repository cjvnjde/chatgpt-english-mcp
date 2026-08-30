package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
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
	var journalMode string
	if err := store.sql.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	source := SourceVersion{Provider: "cambridge", ParserVersion: 12}
	created, saved, err := store.SaveVocabulary(ctx, "owner", "Bank", "bank", time.Now(), source)
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
	loaded, err := reopened.VocabularyByID(ctx, "owner", saved.ItemID, source)
	if err != nil {
		t.Fatalf("VocabularyByID() after reopen error = %v", err)
	}
	if loaded.Term != "Bank" || loaded.NormalizedTerm != "bank" {
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
