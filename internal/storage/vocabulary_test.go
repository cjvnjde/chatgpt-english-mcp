package storage

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestVocabularyUsefulnessPersistsWithoutReplacingExistingMetadata(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "usefulness.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	input := VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "finance",
		Status: domain.LearningStatusLearning, Tags: []string{"finance"},
		CustomDescription: "A useful institution", DescriptionSource: &domain.DescriptionSource{Title: "My source"},
		Notes: []string{"Remember this"}, Examples: []string{"I visited the bank."}, Now: now,
	}
	created, saved, err := store.SaveVocabulary(ctx, input)
	if err != nil || !created || saved.Usefulness != domain.UsefulnessNormal || input.Usefulness != "" {
		t.Fatalf("default save = %#v, created %t, error %v; input usefulness %q", saved, created, err, input.Usefulness)
	}
	high := domain.UsefulnessHigh
	updated, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, Usefulness: &high, Now: now.Add(time.Hour),
	})
	want := saved
	want.Usefulness = high
	want.UpdatedAt = TimeString(now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(updated, want) {
		t.Fatalf("usefulness-only update = %#v, error %v; want %#v", updated, err, want)
	}
	input.Usefulness = domain.UsefulnessLow
	created, duplicate, err := store.SaveVocabulary(ctx, input)
	if err != nil || created || !reflect.DeepEqual(duplicate, want) {
		t.Fatalf("duplicate save replaced metadata: %#v, created %t, error %v", duplicate, created, err)
	}
	notes := []string{"A revised note"}
	updated, err = store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, Notes: &notes, Now: now.Add(2 * time.Hour),
	})
	want.Notes = notes
	want.UpdatedAt = TimeString(now.Add(2 * time.Hour))
	if err != nil || !reflect.DeepEqual(updated, want) {
		t.Fatalf("metadata update did not preserve usefulness: %#v, error %v", updated, err)
	}
	input.SenseKey = "river"
	created, low, err := store.SaveVocabulary(ctx, input)
	if err != nil || !created || low.Usefulness != domain.UsefulnessLow {
		t.Fatalf("explicit low save = %#v, created %t, error %v", low, created, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	loaded, err := reopened.VocabularyByID(ctx, "owner", saved.ItemID)
	if err != nil || !reflect.DeepEqual(loaded, want) {
		t.Fatalf("reopened item = %#v, error %v; want %#v", loaded, err, want)
	}
	listed, err := reopened.ListVocabulary(ctx, VocabularyListQuery{OwnerKey: "owner", Sort: "alphabetical", Limit: 10})
	if err != nil || len(listed) != 2 {
		t.Fatalf("list = %#v, error %v", listed, err)
	}
	for _, item := range listed {
		expected := want
		if item.ItemID == low.ItemID {
			expected = low
		}
		if !reflect.DeepEqual(item, expected) {
			t.Fatalf("listed item = %#v; want %#v", item, expected)
		}
	}
}

func TestVocabularyRejectsInvalidUsefulnessWritesAndCorruptReads(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	saved := savePresentationVocabulary(t, store, "owner", "bank", now)
	invalid := domain.Usefulness("urgent")
	if _, _, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "invalid", NormalizedTerm: "invalid",
		Status: domain.LearningStatusNew, Usefulness: invalid, Now: now,
	}); err == nil || !strings.Contains(err.Error(), "usefulness") {
		t.Fatalf("invalid create error = %v", err)
	}
	for _, value := range []domain.Usefulness{invalid, ""} {
		if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: "owner", ItemID: saved.ItemID, Usefulness: &value, Now: now.Add(time.Hour),
		}); err == nil || !strings.Contains(err.Error(), "usefulness") {
			t.Fatalf("invalid update %q error = %v", value, err)
		}
	}
	for _, value := range []any{"urgent", "", nil} {
		if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = ? WHERE id = ?", value, saved.ItemID); err == nil {
			t.Fatalf("database accepted invalid usefulness %#v", value)
		}
	}
	unchanged, err := store.VocabularyByID(ctx, "owner", saved.ItemID)
	if err != nil || !reflect.DeepEqual(unchanged, saved) {
		t.Fatalf("rejected writes changed vocabulary: %#v, error %v", unchanged, err)
	}
	if _, err := store.VocabularyByTerm(ctx, "owner", "invalid"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("invalid create persisted: %v", err)
	}
	if _, err := store.sql.ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = 'urgent' WHERE id = ?", saved.ItemID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VocabularyByID(ctx, "owner", saved.ItemID); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("corrupt read error = %v; want ErrCorruptData", err)
	}
	if _, err := store.ListVocabulary(ctx, VocabularyListQuery{OwnerKey: "owner", Sort: "recent", Limit: 10}); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("corrupt list error = %v; want ErrCorruptData", err)
	}
	if snapshot, err := store.ExportVocabulary(ctx, "owner", "english-mcp"); !errors.Is(err, ErrCorruptData) || snapshot.Complete {
		t.Fatalf("corrupt export = %#v, error %v", snapshot, err)
	}
}

func TestUsefulnessMigrationPreservesVocabularyAndLearningState(t *testing.T) {
	ctx := context.Background()
	legacy := openLegacyDatabase(t, filepath.Join(t.TempDir(), "pre008.sqlite"), 7)
	store := &DB{sql: legacy}
	if err := store.configure(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	lookup, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 1,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{{
			Headword: "bank", PartOfSpeech: "noun", Definitions: []domain.DictionaryDefinition{{Definition: "An institution"}},
		}}},
		FetchedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	fixtures := []struct {
		id, owner, sense string
		status           domain.LearningStatus
	}{
		{"new-id", "owner", "finance", domain.LearningStatusNew},
		{"learning-id", "owner", "river", domain.LearningStatusLearning},
		{"learned-id", "other-owner", "finance", domain.LearningStatusLearned},
		{"archived-id", "other-owner", "river", domain.LearningStatusArchived},
	}
	for _, fixture := range fixtures {
		if _, err := legacy.ExecContext(ctx, `
			INSERT INTO vocabulary_items(
				id, owner_key, term, normalized_term, created_at, updated_at, lookup_id,
				custom_description, learning_status, description_source_json, notes_json,
				examples_json, tags_json, sense_key, context, selected_entry_index,
				selected_definition_index, selected_definition_json
			) VALUES (?, ?, 'Bank', 'bank', '2026-09-01T00:00:00Z', '2026-09-02T00:00:00Z', ?,
				'Personal meaning', ?, '{"title":"My source"}', '["note"]', '["example"]', '["tag"]',
				?, 'Original context', 0, 0, '{"definition":"An institution"}')
		`, fixture.id, fixture.owner, lookup.ID, fixture.status, fixture.sense); err != nil {
			t.Fatal(err)
		}
	}
	var token string
	if err := legacy.QueryRowContext(ctx, "SELECT review_token FROM learning_cards WHERE vocabulary_item_id = 'learning-id'").Scan(&token); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO learning_presentations(owner_key, vocabulary_item_id, learning_card_id, exercise_mode, review_token, shown_at, due_at, selection_kind)
		VALUES ('owner', 'learning-id', 'learning-id:production', 'production', ?, '2026-09-06T11:00:00Z', '2026-09-01T00:00:00Z', 'new')
	`, token); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: token, Rating: domain.ReviewRatingHard, Comment: "Needed a hint", Now: now,
	}, func(card LearningCard, reviewedAt time.Time, rating domain.ReviewRating) (LearningCard, float64, error) {
		card.DueAt = reviewedAt.Add(48 * time.Hour)
		card.Stability = 2.5
		card.Difficulty = 6
		card.Retrievability = 0.9
		card.ScheduledDays = 2
		card.Repetitions = 3
		card.Lapses = 1
		card.FSRSState = 2
		card.LastReviewAt = reviewedAt
		card.RemainingSteps = 1
		card.LastRating = rating
		card.ConsecutiveFailures = 1
		return card, 0.7, nil
	}); err != nil {
		t.Fatal(err)
	}
	projections := map[string]string{
		"vocabulary_items": `id, owner_key, term, normalized_term, created_at, updated_at, lookup_id,
			custom_description, learning_status, description_source_json, notes_json, examples_json,
			tags_json, sense_key, context, selected_entry_index, selected_definition_index, selected_definition_json`,
		"dictionary_snapshots":   "*",
		"learning_cards":         "*",
		"learning_presentations": "*",
		"review_attempts":        "*",
	}
	for table := range projections {
		if _, err := legacy.ExecContext(ctx, "CREATE TEMP TABLE before_"+table+" AS SELECT * FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("migrate pre008 database: %v", err)
	}
	assertPreserved := func(table, columns string) {
		t.Helper()
		for _, query := range []string{
			"SELECT " + columns + " FROM " + table + " EXCEPT SELECT * FROM before_" + table,
			"SELECT * FROM before_" + table + " EXCEPT SELECT " + columns + " FROM " + table,
		} {
			var differences int
			if err := legacy.QueryRowContext(ctx, "SELECT COUNT(*) FROM ("+query+")").Scan(&differences); err != nil {
				t.Fatal(err)
			}
			if differences != 0 {
				t.Fatalf("%s changed %d rows", table, differences)
			}
		}
	}
	for table, columns := range projections {
		assertPreserved(table, columns)
	}
	for _, fixture := range fixtures {
		item, err := store.VocabularyByID(ctx, fixture.owner, fixture.id)
		if err != nil || item.Usefulness != domain.UsefulnessNormal || item.Status != fixture.status {
			t.Fatalf("migrated %s = %#v, error %v", fixture.id, item, err)
		}
		low := domain.UsefulnessLow
		if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: fixture.owner, ItemID: fixture.id, Usefulness: &low, Now: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"learning_cards", "learning_presentations", "review_attempts"} {
		assertPreserved(table, "*")
	}
}
