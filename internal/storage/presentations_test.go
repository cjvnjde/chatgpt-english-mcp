package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestPresentationHistoryRetainsEveryIssuanceAfterDeletionAndReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 14, 0, 0, 123456789, time.FixedZone("local", 2*60*60))
	item := savePresentationVocabulary(t, store, "owner", "meticulous", now.Add(-time.Hour))

	first, err := store.NextLearningItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.NextLearningItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.PresentationID == second.PresentationID || first.Card.ReviewToken != second.Card.ReviewToken {
		t.Fatalf("separate presentations must have distinct IDs and preserve the unreviewed token: first=%#v second=%#v", first, second)
	}
	if !first.ShownAt.Equal(now) || !second.ShownAt.Equal(now) {
		t.Fatalf("presentation times = %v, %v; want %v", first.ShownAt, second.ShownAt, now)
	}
	var reviewCount int
	if err := store.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM review_attempts").Scan(&reviewCount); err != nil {
		t.Fatal(err)
	}
	if reviewCount != 0 {
		t.Fatal("showing a word must not create a review")
	}
	for _, statement := range []string{
		"UPDATE learning_presentations SET shown_at = '2020-01-01T00:00:00Z' WHERE id = ?",
		"DELETE FROM learning_presentations WHERE id = ?",
	} {
		if _, err := store.sql.ExecContext(ctx, statement, first.PresentationID); err == nil || !strings.Contains(err.Error(), "learning presentations are immutable") {
			t.Fatalf("history mutation error = %v, want immutable-history rejection", err)
		}
	}
	if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextLearningItem(ctx, "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("selection after deletion error = %v, want ErrNotFound", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	rows, err := reopened.sql.QueryContext(ctx, `
		SELECT id, vocabulary_item_id, review_token, shown_at, due_at, selection_kind
		FROM learning_presentations WHERE owner_key = 'owner' ORDER BY id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, wantID := range []int64{first.PresentationID, second.PresentationID} {
		if !rows.Next() {
			t.Fatalf("missing presentation %d after deletion/reopen: %v", wantID, rows.Err())
		}
		var id int64
		var itemID, token, shownAt, dueAt, kind string
		if err := rows.Scan(&id, &itemID, &token, &shownAt, &dueAt, &kind); err != nil {
			t.Fatal(err)
		}
		if id != wantID || itemID != item.ItemID || token != first.Card.ReviewToken || shownAt != TimeString(now) || dueAt != TimeString(first.Card.DueAt) || kind != "new" {
			t.Fatalf("retained presentation = %d %q %q %q %q %q", id, itemID, token, shownAt, dueAt, kind)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra presentation or iteration failure: %v", rows.Err())
	}
}

func TestPresentationInsertFailureDoesNotReturnASelection(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	savePresentationVocabulary(t, store, "owner", "resilient", now)
	if _, err := store.sql.ExecContext(ctx, `
		CREATE TRIGGER reject_presentation BEFORE INSERT ON learning_presentations
		BEGIN SELECT RAISE(ABORT, 'presentation storage unavailable'); END
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextLearningItem(ctx, "owner", now); err == nil || !strings.Contains(err.Error(), "presentation storage unavailable") {
		t.Fatalf("NextLearningItem() error = %v, want presentation persistence failure", err)
	}
	if _, err := store.sql.ExecContext(ctx, "DROP TRIGGER reject_presentation"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextLearningItem(ctx, "owner", now); err != nil {
		t.Fatalf("selection after storage recovery: %v", err)
	}
	var count int
	if err := store.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM learning_presentations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("committed presentations = %d, want only the successful issuance", count)
	}
}

func TestConcurrentPresentationsRespectRecentHistory(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, term := range []string{"adapt", "brisk", "candid", "dwell"} {
		savePresentationVocabulary(t, store, "owner", term, now)
	}
	results := make(chan LearningCandidate, 4)
	failures := make(chan error, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			candidate, err := store.NextLearningItem(ctx, "owner", now)
			if err != nil {
				failures <- err
				return
			}
			results <- candidate
		})
	}
	workers.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for candidate := range results {
		if seen[candidate.Vocabulary.ItemID] {
			t.Fatalf("concurrent selections repeated %q despite fresh alternatives", candidate.Vocabulary.Term)
		}
		seen[candidate.Vocabulary.ItemID] = true
	}
	if len(seen) != 4 {
		t.Fatalf("concurrent selections reached %d words, want all four", len(seen))
	}
}

func TestPresentationMigrationPreservesScheduleWithoutInventingHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "previous.sqlite")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = legacy.Close() })
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY, name TEXT NOT NULL,
			checksum TEXT NOT NULL, applied_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatal(err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:6] {
		if _, err := legacy.ExecContext(ctx, migration.contents); err != nil {
			t.Fatal(err)
		}
		if _, err := legacy.ExecContext(ctx,
			"INSERT INTO schema_migrations VALUES (?, ?, ?, ?)",
			migration.version, migration.name, migration.checksum, "2026-09-05T12:00:00Z",
		); err != nil {
			t.Fatal(err)
		}
	}
	previous := &DB{sql: legacy}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	item := savePresentationVocabulary(t, previous, "owner", "steadfast", now.Add(-24*time.Hour))
	if _, err := legacy.ExecContext(ctx, `
		UPDATE learning_cards SET fsrs_state = 2, repetitions = 5, stability = 4,
			due_at = ?, last_review_at = ? WHERE vocabulary_item_id = ?
	`, TimeString(now.Add(time.Hour)), TimeString(now.Add(-24*time.Hour)), item.ItemID); err != nil {
		t.Fatal(err)
	}
	before, err := scanLearningCard(legacy.QueryRowContext(ctx, "SELECT "+learningCardColumns+" FROM learning_cards card"))
	if err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var count int
	if err := store.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM learning_presentations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("migration fabricated %d historical presentations", count)
	}
	selected, err := store.NextLearningItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Card != before {
		t.Fatalf("migration or presentation changed existing schedule: before=%#v after=%#v", before, selected.Card)
	}
}

func savePresentationVocabulary(t *testing.T, store *DB, ownerKey, term string, now time.Time) domain.VocabularyItem {
	t.Helper()
	_, item, err := store.SaveVocabulary(context.Background(), VocabularyCreate{
		OwnerKey: ownerKey, Term: term, NormalizedTerm: term,
		Status: domain.LearningStatusNew, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}
