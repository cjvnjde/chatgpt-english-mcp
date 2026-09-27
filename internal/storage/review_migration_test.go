package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestSingleRatingMigrationPreservesSchedulingAndOriginalRetry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "single-rating.sqlite")
	legacy := openLegacyDatabase(t, path, 19)
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	itemID := insertContextMigrationVocabulary(t, legacy, VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", Status: domain.LearningStatusLearning,
		SenseKey: "legacy", Now: now,
	})
	before, err := scanLearningCard(legacy.QueryRow("SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", itemID))
	if err != nil {
		t.Fatal(err)
	}
	input := RecordReviewInput{OwnerKey: "owner", ReviewToken: before.ReviewToken, Rating: domain.ReviewRatingGood,
		Comment: "Original request.", Now: clockAt(now)}
	attempt := insertLegacyReview(t, legacy, input, before)
	if _, err := legacy.Exec("UPDATE review_attempts SET effective_rating = 'easy' WHERE id = ?", attempt.ReviewID); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("UPDATE learning_cards SET last_rating = 'easy' WHERE id = ?", before.CardID); err != nil {
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
	retry, duplicate, err := store.RecordReview(ctx, input, nil)
	if err != nil || !duplicate || retry.Rating != domain.ReviewRatingEasy || retry.After.LastRating != retry.Rating ||
		!retry.After.DueAt.Equal(attempt.After.DueAt) || retry.After.Repetitions != attempt.After.Repetitions {
		t.Fatalf("historical schedule/replay changed: %#v duplicate=%t error=%v", retry, duplicate, err)
	}
	input.Rating = domain.ReviewRatingEasy
	if _, _, err := store.RecordReview(ctx, input, nil); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed payload accepted: %v", err)
	}
	var oldColumns int
	if err := store.sql.QueryRow("SELECT count(*) FROM pragma_table_info('review_attempts') WHERE name = 'effective_rating'").Scan(&oldColumns); err != nil || oldColumns != 0 {
		t.Fatalf("old column remains: %d, %v", oldColumns, err)
	}
	if _, err := store.sql.Exec("UPDATE review_attempts SET reviewed_at = 'changed' WHERE id = ?", attempt.ReviewID); err == nil {
		t.Fatal("migration did not restore the history update guard")
	}
}
