package storage

import (
	"context"
	"database/sql"
	"errors"
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

func TestSpacedRepetitionMigrationInitializesActiveVocabularyAndImmutableHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	legacy := openLegacyDatabase(t, path, 3)
	for _, item := range []struct {
		id     string
		term   string
		status domain.LearningStatus
	}{
		{id: "active-id", term: "active", status: domain.LearningStatusLearning},
		{id: "archived-id", term: "archived", status: domain.LearningStatusArchived},
	} {
		if _, err := legacy.ExecContext(ctx, `
			INSERT INTO vocabulary_items(
				id, owner_key, term, normalized_term, created_at, updated_at,
				custom_description, learning_status, notes_json, examples_json, tags_json
			) VALUES (?, 'owner', ?, ?, '2026-09-01T00:00:00Z', '2026-09-01T00:00:00Z', '', ?, '[]', '[]', '[]')
		`, item.id, item.term, item.term, item.status); err != nil {
			t.Fatalf("insert legacy vocabulary %q: %v", item.term, err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open(legacy) error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	assertColumn(t, store, "learning_cards", "stability")
	assertColumn(t, store, "learning_cards", "difficulty")
	assertColumn(t, store, "learning_cards", "retrievability")
	assertColumn(t, store, "learning_cards", "repetitions")
	assertColumn(t, store, "learning_cards", "lapses")
	assertColumn(t, store, "learning_cards", "review_token")
	assertColumn(t, store, "review_attempts", "comment")

	var cardCount int
	if err := store.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM learning_cards").Scan(&cardCount); err != nil {
		t.Fatalf("count migrated learning cards: %v", err)
	}
	if cardCount != 1 {
		t.Fatalf("migrated learning card count = %d, want 1", cardCount)
	}
	var itemID string
	var mode domain.ExerciseMode
	var dueAt string
	var reviewToken string
	if err := store.sql.QueryRowContext(
		ctx,
		"SELECT vocabulary_item_id, exercise_mode, due_at, review_token FROM learning_cards",
	).Scan(&itemID, &mode, &dueAt, &reviewToken); err != nil {
		t.Fatalf("read migrated learning card: %v", err)
	}
	if itemID != "active-id" || mode != domain.ExerciseModeProduction || dueAt != "2026-09-01T00:00:00Z" || reviewToken == "" {
		t.Fatalf("migrated learning card = item %q mode %q due %q token %q", itemID, mode, dueAt, reviewToken)
	}

	reviewedAt := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	attempt, duplicate, err := store.RecordReview(ctx, RecordReviewInput{
		OwnerKey:    "owner",
		ReviewToken: reviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     "Needed a context clue.",
		Now:         reviewedAt,
	}, func(card LearningCard, now time.Time, rating domain.ReviewRating) (LearningCard, float64, error) {
		card.DueAt = now.Add(24 * time.Hour)
		card.Stability = 1
		card.Difficulty = 5
		card.Retrievability = 1
		card.ScheduledDays = 1
		card.Repetitions = 1
		card.FSRSState = 2
		card.LastReviewAt = now
		card.LastRating = rating
		return card, 0, nil
	})
	if err != nil || duplicate {
		t.Fatalf("RecordReview() = attempt %#v duplicate %t error %v", attempt, duplicate, err)
	}
	if attempt.Comment != "Needed a context clue." || attempt.After.ReviewToken == "" || attempt.After.ReviewToken == reviewToken {
		t.Fatalf("recorded review token or comment = %#v", attempt)
	}
	if _, err := store.sql.ExecContext(ctx, "UPDATE review_attempts SET rating = 'easy' WHERE id = ?", attempt.ReviewID); err == nil {
		t.Fatal("immutable review UPDATE succeeded")
	}
	if _, err := store.sql.ExecContext(ctx, "DELETE FROM review_attempts WHERE id = ?", attempt.ReviewID); err == nil {
		t.Fatal("immutable review DELETE succeeded")
	}
}

func openLegacyDatabase(t *testing.T, path string, version int) *sql.DB {
	t.Helper()
	ctx := context.Background()
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	legacy.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = legacy.Close() })
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if _, err := legacy.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create legacy migration table: %v", err)
	}
	for _, migration := range migrations[:version] {
		if _, err := legacy.ExecContext(ctx, migration.contents); err != nil {
			t.Fatalf("apply legacy migration %d: %v", migration.version, err)
		}
		if _, err := legacy.ExecContext(
			ctx,
			"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
			migration.version,
			migration.name,
			migration.checksum,
			"2026-09-01T00:00:00Z",
		); err != nil {
			t.Fatalf("record legacy migration %d: %v", migration.version, err)
		}
	}
	return legacy
}

func TestNextLearningItemKeepsEligibleCardsAheadOfFutureAndIsolatesOwners(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)

	fixtures := []struct {
		term     string
		owner    string
		status   domain.LearningStatus
		dueAt    time.Time
		state    int
		failures int
	}{
		{term: "troublesome", owner: "owner", status: domain.LearningStatusLearning, dueAt: now.Add(-72 * time.Hour), state: 2, failures: 2},
		{term: "due", owner: "owner", status: domain.LearningStatusLearned, dueAt: now, state: 2},
		{term: "new", owner: "owner", status: domain.LearningStatusNew, dueAt: now.Add(48 * time.Hour)},
		{term: "near-future", owner: "owner", status: domain.LearningStatusLearned, dueAt: now.Add(time.Nanosecond), state: 2},
		{term: "failed-future", owner: "owner", status: domain.LearningStatusLearning, dueAt: now.Add(24 * time.Hour), state: 1, failures: 10},
		{term: "archived", owner: "owner", status: domain.LearningStatusArchived, dueAt: now},
		{term: "foreign", owner: "another-owner", status: domain.LearningStatusNew, dueAt: now},
	}
	for _, fixture := range fixtures {
		created, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: fixture.owner, Term: fixture.term, NormalizedTerm: fixture.term,
			Status: fixture.status, Tags: []string{}, Notes: []string{}, Examples: []string{}, Now: now,
		})
		if err != nil || !created {
			t.Fatalf("SaveVocabulary(%q) = item %#v created %t error %v", fixture.term, item, created, err)
		}
		if _, err := store.sql.ExecContext(ctx, `
			UPDATE learning_cards SET due_at = ?, fsrs_state = ?, consecutive_failures = ?
			WHERE vocabulary_item_id = ?
		`, TimeString(fixture.dueAt), fixture.state, fixture.failures, item.ItemID); err != nil {
			t.Fatalf("prepare %q learning state: %v", fixture.term, err)
		}
	}

	eligible := map[string]bool{"troublesome": true, "due": true, "new": true}
	for selection := range 5 {
		selected, err := store.NextLearningItem(ctx, "owner", now)
		if err != nil {
			t.Fatalf("NextLearningItem() error = %v", err)
		}
		term := selected.Vocabulary.Term
		switch {
		case selection < 3:
			if !eligible[term] {
				t.Fatalf("selected %q while due/new cards remain: %v", term, eligible)
			}
			delete(eligible, term)
		case selection == 3:
			if term != "near-future" {
				t.Fatalf("future fallback = %q, want near-future before failed-future", term)
			}
		case selection == 4:
			if term != "failed-future" {
				t.Fatalf("remaining active card = %q, want failed-future", term)
			}
		}
		if selected.Card.ReviewToken == "" {
			t.Fatal("selected card has no review token")
		}
		if _, err := store.sql.ExecContext(ctx,
			"UPDATE vocabulary_items SET learning_status = 'archived' WHERE id = ?",
			selected.Vocabulary.ItemID,
		); err != nil {
			t.Fatalf("archive selected %q: %v", term, err)
		}
	}
	if _, err := store.NextLearningItem(ctx, "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("NextLearningItem(empty) error = %v, want ErrNotFound", err)
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
