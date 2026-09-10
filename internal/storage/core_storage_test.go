package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/usefulness"
)

func TestConcurrentReviewHandlesKeepOneImmutableAttempt(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "review-race.sqlite")
	first, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	// Attempt the competing submission synchronously while the first scheduler
	// holds its snapshot, rather than depending on goroutine scheduling.
	if _, err := second.sql.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	savePresentationVocabulary(t, first, "owner", "resilient", now)
	candidate, err := first.NextLearningItem(ctx, "owner", clockAt(now))
	if err != nil {
		t.Fatal(err)
	}
	input := RecordReviewInput{OwnerKey: "owner", ReviewToken: candidate.Card.ReviewToken,
		Rating: domain.ReviewRatingGood, Comment: "Remembered independently.", Now: clockAt(now.Add(time.Minute))}
	var competingErr error
	attempt, duplicate, err := first.RecordReview(ctx, input, func(card LearningCard, now time.Time, rating domain.ReviewRating) (LearningCard, float64, error) { // Read-only snapshots must remain usable while a review is in flight.
		reader, err := second.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return LearningCard{}, 0, err
		}
		var count int
		readErr := reader.QueryRowContext(ctx, "SELECT count(*) FROM review_attempts").Scan(&count)
		rollbackErr := reader.Rollback()
		if readErr != nil {
			return LearningCard{}, 0, readErr
		}
		if rollbackErr != nil {
			return LearningCard{}, 0, rollbackErr
		}
		_, _, competingErr = second.RecordReview(ctx, input, coreStorageReviewSchedule)
		return coreStorageReviewSchedule(card, now, rating)
	})
	if err != nil || duplicate {
		t.Fatalf("first in-flight review failed: duplicate=%t error=%v", duplicate, err)
	}
	if competingErr == nil {
		t.Fatal("competing writer committed while the first review held its snapshot")
	}
	replayed, duplicate, err := second.RecordReview(ctx, input, nil)
	if err != nil || !duplicate || replayed.ReviewID != attempt.ReviewID || replayed.After.Repetitions != 1 || !replayed.After.DueAt.Equal(attempt.After.DueAt) {
		t.Fatalf("retry did not replay the one committed review: %#v duplicate=%t error=%v", replayed, duplicate, err)
	}
	var attempts int
	if err := second.sql.QueryRowContext(ctx, "SELECT count(*) FROM review_attempts").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 {
		t.Fatalf("accepted %d attempts for one token", attempts)
	}
}

func TestContextSenseMigrationPreservesIdentityAndLearningHistory(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "context-migration.sqlite")
	legacy := openLegacyDatabase(t, path, 13)
	oldStore := &DB{sql: legacy}
	now := time.Date(2026, 9, 10, 12, 0, 0, 123, time.UTC)
	contextValue := "  A  “RIVER”\tbank  "
	digest := sha256.Sum256([]byte(domain.NormalizeTerm(contextValue)))
	oldKey := fmt.Sprintf("%x", digest)
	input := VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", Status: domain.LearningStatusLearning,
		SenseKey: oldKey, Context: contextValue, CustomDescription: "My river example.",
		Notes: []string{"Keep my note."}, Tags: []string{"nature"}, Now: now,
	}
	beforeID := insertContextMigrationVocabulary(t, legacy, input)
	initialCard, err := scanLearningCard(legacy.QueryRowContext(ctx,
		"SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", beforeID))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := legacy.ExecContext(ctx, `
		INSERT INTO learning_presentations(owner_key, vocabulary_item_id, learning_card_id,
			exercise_mode, review_token, shown_at, due_at, selection_kind)
		VALUES ('owner', ?, ?, 'production', ?, ?, ?, 'new')
	`, beforeID, initialCard.CardID, initialCard.ReviewToken, TimeString(now), TimeString(initialCard.DueAt))
	if err != nil {
		t.Fatal(err)
	}
	presentationID, err := presented.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	reviewInput := RecordReviewInput{OwnerKey: "owner", ReviewToken: initialCard.ReviewToken,
		Rating: domain.ReviewRatingGood, Comment: "A useful river context.", Now: clockAt(now.Add(time.Minute))}
	attempt, _, err := oldStore.RecordReview(ctx, reviewInput, coreStorageReviewSchedule)
	if err != nil {
		t.Fatal(err)
	}
	legacyInput := input
	legacyInput.Term, legacyInput.NormalizedTerm, legacyInput.SenseKey = "legacy", "legacy", "legacy"
	legacyID := insertContextMigrationVocabulary(t, legacy, legacyInput)
	index := 0
	definition := domain.DictionaryDefinition{Definition: contextValue}
	definitionInput := input
	definitionInput.Term, definitionInput.NormalizedTerm = "shore", "shore"
	definitionInput.SelectedEntryIndex, definitionInput.SelectedDefinitionIndex = &index, &index
	definitionInput.SelectedDefinition = &definition
	definitionID := insertContextMigrationVocabulary(t, legacy, definitionInput)
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	loaded, err := store.VocabularyByID(ctx, "owner", beforeID)
	if err != nil || loaded.Term != input.Term || loaded.NormalizedTerm != input.NormalizedTerm ||
		loaded.Status != input.Status || loaded.Context != contextValue || loaded.CustomDescription != input.CustomDescription ||
		!reflect.DeepEqual(loaded.Notes, input.Notes) || !reflect.DeepEqual(loaded.Tags, input.Tags) ||
		loaded.CreatedAt != TimeString(now) || loaded.UpdatedAt != TimeString(now) ||
		loaded.Lookup != nil || loaded.Sense != nil {
		t.Fatalf("migration changed vocabulary content: %#v error=%v", loaded, err)
	}
	card, err := scanLearningCard(store.sql.QueryRowContext(ctx,
		"SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", beforeID))
	if err != nil || card != attempt.After {
		t.Fatalf("migration changed card identity or schedule: %#v error=%v; want %#v", card, err, attempt.After)
	}
	var presentationToken string
	if err := store.sql.QueryRowContext(ctx, "SELECT review_token FROM learning_presentations WHERE id = ?", presentationID).Scan(&presentationToken); err != nil || presentationToken != reviewInput.ReviewToken {
		t.Fatalf("migration changed presentation history: token=%q error=%v", presentationToken, err)
	}
	replayed, duplicate, err := store.RecordReview(ctx, reviewInput, nil)
	if err != nil || !duplicate || replayed.ReviewID != attempt.ReviewID {
		t.Fatalf("migration changed review idempotency: %#v duplicate=%t error=%v", replayed, duplicate, err)
	}
	input.SenseKey = "context:" + oldKey
	created, saved, err := store.SaveVocabulary(ctx, input)
	if err != nil || created || saved.ItemID != beforeID {
		t.Fatalf("resaving migrated context created another sense: id=%q created=%t error=%v", saved.ItemID, created, err)
	}
	definitionInput.Term, definitionInput.NormalizedTerm = "bank", "bank"
	created, selected, err := store.SaveVocabulary(ctx, definitionInput)
	if err != nil || !created || selected.ItemID == beforeID || selected.Sense == nil {
		t.Fatalf("identical-text definition did not coexist with context: %#v created=%t error=%v", selected, created, err)
	}
	for _, fixture := range []struct {
		id  string
		key string
	}{{legacyID, "legacy"}, {definitionID, oldKey}} {
		var key string
		if err := store.sql.QueryRowContext(ctx, "SELECT sense_key FROM vocabulary_items WHERE id = ?", fixture.id).Scan(&key); err != nil || key != fixture.key {
			t.Fatalf("migration changed non-context identity %q: key=%q error=%v", fixture.id, key, err)
		}
	}
}

func TestContextSenseMigrationConflictRollsBackWithoutLosingVocabulary(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "context-conflict.sqlite")
	legacy := openLegacyDatabase(t, path, 13)
	contextValue := "a river bank"
	digest := sha256.Sum256([]byte(domain.NormalizeTerm(contextValue)))
	oldKey := fmt.Sprintf("%x", digest)
	ids := make(map[string]string)
	for _, key := range []string{oldKey, "context:" + oldKey} {
		ids[key] = insertContextMigrationVocabulary(t, legacy, VocabularyCreate{
			OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", Status: domain.LearningStatusNew,
			SenseKey: key, Context: contextValue, CustomDescription: key, Now: time.Now(),
		})
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err == nil {
		store.Close()
		t.Fatal("migration accepted colliding context identities")
	}
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	for key, id := range ids {
		var storedID, description string
		if err := raw.QueryRowContext(ctx, "SELECT id, custom_description FROM vocabulary_items WHERE sense_key = ?", key).Scan(&storedID, &description); err != nil || storedID != id || description != key {
			t.Fatalf("migration lost a conflicting learner record: id=%q description=%q error=%v", storedID, description, err)
		}
	}
	var version int
	if err := raw.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&version); err != nil || version != 13 {
		t.Fatalf("failed migration was committed: version=%d error=%v", version, err)
	}
}

func coreStorageReviewSchedule(card LearningCard, now time.Time, rating domain.ReviewRating) (LearningCard, float64, error) {
	card.DueAt = now.Add(24 * time.Hour)
	card.Stability = 1
	card.Difficulty = 5
	card.Retrievability = 1
	card.ScheduledDays = 1
	card.Repetitions++
	card.FSRSState = 2
	card.LastReviewAt = now
	card.LastRating = rating
	return card, 0, nil
}

// Historical fixtures use their original SQL schema, never current hydration.
func insertContextMigrationVocabulary(t *testing.T, database *sql.DB, input VocabularyCreate) string {
	t.Helper()
	id, err := NewID()
	if err != nil {
		t.Fatal(err)
	}
	notes, err := encodeStringList(input.Notes, "fixture notes")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := encodeStringList(input.Tags, "fixture tags")
	if err != nil {
		t.Fatal(err)
	}
	definition, err := encodeOptionalDefinition(input.SelectedDefinition)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.ExecContext(context.Background(), `
		INSERT INTO vocabulary_items(id, owner_key, term, normalized_term, created_at, updated_at,
			learning_status, custom_description, notes_json, tags_json, sense_key, context,
			selected_entry_index, selected_definition_index, selected_definition_json, usefulness)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, input.OwnerKey, input.Term, input.NormalizedTerm, TimeString(input.Now), TimeString(input.Now),
		input.Status, input.CustomDescription, notes, tags, input.SenseKey, input.Context,
		input.SelectedEntryIndex, input.SelectedDefinitionIndex, definition, usefulness.Estimate(input.NormalizedTerm, input.Usefulness))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
