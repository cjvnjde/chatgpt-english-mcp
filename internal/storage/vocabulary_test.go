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
	"english-learning-mcp/internal/usefulness"
)

func TestVocabularyPaginationOrdersStoredTimestampsChronologically(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "chronological.sqlite")
	legacy := openLegacyDatabase(t, path, 9)
	timestamps := []string{
		"2026-09-07T12:00:00Z",
		"2026-09-07T12:00:00.1Z",
		"2026-09-07T12:00:00.11Z",
		"2026-09-07T12:00:00.110000001Z",
		"2026-09-07T12:00:01Z",
	}
	terms := []string{"zero", "one", "two", "three", "four"}
	for index, timestamp := range timestamps {
		if _, err := legacy.ExecContext(ctx, `
			INSERT INTO vocabulary_items(id, owner_key, term, normalized_term, created_at, updated_at, sense_key)
			VALUES (?, 'owner', ?, ?, ?, ?, 'legacy')
		`, terms[index], terms[index], terms[index], timestamp, timestamp); err != nil {
			t.Fatal(err)
		}
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, sortOrder := range []string{"oldest", "recent"} {
		t.Run(sortOrder, func(t *testing.T) {
			query := VocabularyListQuery{OwnerKey: "owner", Sort: sortOrder, Limit: 1}
			for page := range timestamps {
				items, err := store.ListVocabulary(ctx, query)
				if err != nil || len(items) != 1 {
					t.Fatalf("page %d = %#v, error %v", page, items, err)
				}
				index := page
				if sortOrder == "recent" {
					index = len(timestamps) - 1 - page
				}
				item := items[0]
				if item.Term != terms[index] || item.UpdatedAt != timestamps[index] || item.CreatedAt != timestamps[index] {
					t.Fatalf("page %d = %#v; want %s at unchanged timestamp %s", page, item, terms[index], timestamps[index])
				}
				query.CursorPrimary, query.CursorID = item.UpdatedAt, item.ItemID
			}
			if items, err := store.ListVocabulary(ctx, query); err != nil || len(items) != 0 {
				t.Fatalf("final page = %#v, error %v", items, err)
			}
		})
	}
}

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
		OwnerKey: "owner", Term: "fixture bank sense", NormalizedTerm: "fixture bank sense", SenseKey: "finance",
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
	want.EditRevision++
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
	want.EditRevision++
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

func TestVocabularyPersonalInterestPersistsWithoutChangingLearningState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "personal-interest.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	input := VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "river",
		Status: domain.LearningStatusLearning, Tags: []string{"nature"},
		CustomDescription: "Land alongside a river", DescriptionSource: &domain.DescriptionSource{Title: "My source"},
		Notes: []string{"Keep this note"}, Examples: []string{"We sat on the bank."}, Context: "River bank", Now: now,
	}
	created, saved, err := store.SaveVocabulary(ctx, input)
	if err != nil || !created || saved.PersonalInterest != domain.PersonalInterestNormal {
		t.Fatalf("omitted interest save = %#v, created %t, error %v", saved, created, err)
	}
	if _, err := store.sql.ExecContext(ctx, `
		UPDATE learning_cards SET stability = 2.5, difficulty = 6, repetitions = 3,
			lapses = 1, fsrs_state = 2, scheduled_days = 2, consecutive_failures = 1
		WHERE vocabulary_item_id = ?
	`, saved.ItemID); err != nil {
		t.Fatal(err)
	}
	readCard := func(store *DB) LearningCard {
		t.Helper()
		card, err := scanLearningCard(store.sql.QueryRowContext(ctx,
			"SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", saved.ItemID))
		if err != nil {
			t.Fatal(err)
		}
		return card
	}
	before := readCard(store)
	high := domain.PersonalInterestHigh
	updated, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, PersonalInterest: &high, Now: now.Add(time.Hour),
	})
	want := saved
	want.PersonalInterest, want.UpdatedAt = high, TimeString(now.Add(time.Hour))
	want.EditRevision++
	if err != nil || !reflect.DeepEqual(updated, want) {
		t.Fatalf("interest-only update = %#v, error %v; want %#v", updated, err, want)
	}
	input.PersonalInterest = domain.PersonalInterestLow
	created, duplicate, err := store.SaveVocabulary(ctx, input)
	if err != nil || created || !reflect.DeepEqual(duplicate, want) {
		t.Fatalf("duplicate save replaced interest: %#v, created %t, error %v", duplicate, created, err)
	}
	input.SenseKey, input.Context = "finance", "Financial bank"
	created, low, err := store.SaveVocabulary(ctx, input)
	if err != nil || !created || low.PersonalInterest != domain.PersonalInterestLow {
		t.Fatalf("explicit low sense = %#v, created %t, error %v", low, created, err)
	}
	notes := []string{"Revised note"}
	updated, err = store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, Notes: &notes, Now: now.Add(2 * time.Hour),
	})
	want.Notes, want.UpdatedAt = notes, TimeString(now.Add(2*time.Hour))
	want.EditRevision++
	if err != nil || !reflect.DeepEqual(updated, want) {
		t.Fatalf("omitted interest update = %#v, error %v; want %#v", updated, err, want)
	}
	if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "another-owner", ItemID: saved.ItemID, PersonalInterest: &high, Now: now,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner update error = %v, want ErrNotFound", err)
	}
	for _, invalid := range []domain.PersonalInterest{"urgent", ""} {
		if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: "owner", ItemID: saved.ItemID, PersonalInterest: &invalid, Notes: &notes, Now: now,
		}); err == nil {
			t.Fatalf("invalid interest %q was accepted", invalid)
		}
	}
	input.NormalizedTerm, input.PersonalInterest = "invalid", "urgent"
	if _, _, err := store.SaveVocabulary(ctx, input); err == nil {
		t.Fatal("invalid interest create was accepted")
	}
	for _, invalid := range []any{"urgent", "", nil} {
		if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET personal_interest = ? WHERE id = ?", invalid, saved.ItemID); err == nil {
			t.Fatalf("database accepted invalid interest %#v", invalid)
		}
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
	if after := readCard(reopened); !reflect.DeepEqual(after, before) {
		t.Fatalf("interest changed learning state: before %#v, after %#v", before, after)
	}
	listed, err := reopened.ListVocabulary(ctx, VocabularyListQuery{OwnerKey: "owner", Sort: "oldest", Limit: 10})
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed senses = %#v, error %v", listed, err)
	}
	for _, item := range listed {
		expected := want
		if item.ItemID == low.ItemID {
			expected = low
		}
		if !reflect.DeepEqual(item, expected) {
			t.Fatalf("listed sense = %#v, want %#v", item, expected)
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

func TestVocabularyMetadataMigrationsPreserveVocabularyAndLearningState(t *testing.T) {
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
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO review_attempts (
			id, owner_key, submission_id, vocabulary_item_id, learning_card_id,
			exercise_mode, rating, comment, reviewed_at, due_before,
			stability_before, difficulty_before, retrievability_before,
			scheduled_days_before, repetitions_before, lapses_before, fsrs_state_before,
			remaining_steps_before, consecutive_failures_before,
			due_after, stability_after, difficulty_after, retrievability_after,
			scheduled_days_after, repetitions_after, lapses_after, fsrs_state_after,
			remaining_steps_after, consecutive_failures_after
		) VALUES (
			'review-id', 'owner', ?, 'learning-id', 'learning-id:production',
			'production', 'hard', 'Needed a hint', ?, '2026-09-01T00:00:00Z',
			0, 0, 0.7, 0, 0, 0, 0, 0, 0,
			?, 2.5, 6, 0.9, 2, 3, 1, 2, 1, 1
		)
	`, token, TimeString(now), TimeString(now.Add(48*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `
		UPDATE learning_cards
		SET due_at = ?, stability = 2.5, difficulty = 6, retrievability = 0.9,
			scheduled_days = 2, repetitions = 3, lapses = 1, fsrs_state = 2,
			last_review_at = ?, remaining_steps = 1, last_rating = 'hard',
			consecutive_failures = 1, review_token = 'next-token', updated_at = ?
		WHERE vocabulary_item_id = 'learning-id'
	`, TimeString(now.Add(48*time.Hour)), TimeString(now), TimeString(now)); err != nil {
		t.Fatal(err)
	}
	var reviewColumns string
	if err := legacy.QueryRowContext(ctx, "SELECT group_concat(name, ', ') FROM pragma_table_info('review_attempts')").Scan(&reviewColumns); err != nil {
		t.Fatal(err)
	}
	projections := map[string]string{
		"vocabulary_items": `id, owner_key, term, normalized_term, created_at, updated_at, lookup_id,
			custom_description, learning_status, description_source_json, notes_json, examples_json,
			tags_json, sense_key, context, selected_entry_index, selected_definition_index, selected_definition_json`,
		"dictionary_snapshots":   "*",
		"learning_cards":         "*",
		"learning_presentations": "*",
		"review_attempts":        reviewColumns,
	}
	for table := range projections {
		if _, err := legacy.ExecContext(ctx, "CREATE TEMP TABLE before_"+table+" AS SELECT "+projections[table]+" FROM "+table); err != nil {
			t.Fatal(err)
		}
	}
	migrations, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	historical := migrations[7]
	if _, err := legacy.ExecContext(ctx, historical.contents); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		var value domain.Usefulness
		if err := legacy.QueryRowContext(ctx, "SELECT usefulness FROM vocabulary_items WHERE id = ?", fixture.id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		if value != domain.UsefulnessNormal {
			t.Fatalf("migration008 assigned %s usefulness %q, want normal", fixture.id, value)
		}
	}
	if _, err := legacy.ExecContext(ctx,
		"INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
		historical.version, historical.name, historical.checksum, TimeString(now),
	); err != nil {
		t.Fatal(err)
	}
	hints := []domain.Usefulness{domain.UsefulnessLow, domain.UsefulnessNormal, domain.UsefulnessHigh, domain.UsefulnessLow}
	for index, fixture := range fixtures {
		if _, err := legacy.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = ? WHERE id = ?", hints[index], fixture.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.migrate(ctx); err != nil {
		t.Fatalf("migrate pre009 database: %v", err)
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
	for index, fixture := range fixtures {
		var hint domain.Usefulness
		if err := legacy.QueryRowContext(ctx, "SELECT usefulness_hint FROM vocabulary_items WHERE id = ?", fixture.id).Scan(&hint); err != nil {
			t.Fatal(err)
		}
		if hint != hints[index] {
			t.Fatalf("migrated %s hint = %q, want %q", fixture.id, hint, hints[index])
		}
		item, err := store.VocabularyByID(ctx, fixture.owner, fixture.id)
		if err != nil || item.Usefulness != usefulness.Estimate("bank", hint) || item.Status != fixture.status ||
			item.PersonalInterest != domain.PersonalInterestNormal {
			t.Fatalf("migrated %s = %#v, error %v", fixture.id, item, err)
		}
		notes := []string{"migrated edit"}
		edited, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: fixture.owner, ItemID: fixture.id, ExpectedRevision: &item.EditRevision,
			Notes: &notes, Now: now,
		})
		if err != nil || edited.EditRevision <= item.EditRevision {
			t.Fatalf("migrated item cannot be revision-edited: %#v, %v", edited, err)
		}
		if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: fixture.owner, ItemID: fixture.id, ExpectedRevision: &item.EditRevision,
			Notes: &item.Notes, Now: now,
		}); !errors.Is(err, ErrEditConflict) {
			t.Fatalf("migrated item accepted stale revision: %v", err)
		}
		low := domain.UsefulnessLow
		if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
			OwnerKey: fixture.owner, ItemID: fixture.id, Usefulness: &low, Now: now.Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"learning_cards", "learning_presentations", "review_attempts"} {
		assertPreserved(table, projections[table])
	}
}

func TestDuplicateVocabularySaveOnlyLinksRequestedSense(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	snapshot, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 1, FetchedAt: now, ExpiresAt: now,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{{Headword: "bank"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input := VocabularyCreate{OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "finance", Status: domain.LearningStatusNew, Now: now}
	_, finance, err := store.SaveVocabulary(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.SenseKey = "river"
	_, river, err := store.SaveVocabulary(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	input.SenseKey = "finance"
	input.LookupID = snapshot.ID
	created, duplicate, err := store.SaveVocabulary(ctx, input)
	if err != nil || created || duplicate.ItemID != finance.ItemID || duplicate.Lookup == nil || duplicate.Lookup.LookupID != snapshot.ID {
		t.Fatalf("retry = %#v, created %t, error %v", duplicate, created, err)
	}
	other, err := store.VocabularyByID(ctx, "owner", river.ItemID)
	if err != nil || other.Lookup != nil {
		t.Fatalf("retry modified another sense: %#v, error %v", other, err)
	}
}

func TestVocabularyWritesRollBackWhenStoredDataCannotBeRead(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	snapshot, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 1, FetchedAt: now, ExpiresAt: now,
		Data: domain.DictionarySnapshotData{Status: 200},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, "UPDATE dictionary_snapshots SET data_json = '[]' WHERE id = ?", snapshot.ID); err != nil {
		t.Fatal(err)
	}
	input := VocabularyCreate{OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "legacy", Status: domain.LearningStatusNew, LookupID: snapshot.ID, Now: now}
	if _, _, err := store.SaveVocabulary(ctx, input); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("save with corrupt lookup error = %v", err)
	}
	var count int
	if err := store.sql.QueryRowContext(ctx, "SELECT count(*) FROM vocabulary_items").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed save left %d vocabulary rows, error %v", count, err)
	}
	input.LookupID = ""
	_, saved, err := store.SaveVocabulary(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET tags_json = '[1]' WHERE id = ?", saved.ItemID); err != nil {
		t.Fatal(err)
	}
	status := domain.LearningStatusArchived
	if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{OwnerKey: "owner", ItemID: saved.ItemID, Status: &status, Now: now.Add(time.Hour)}); !errors.Is(err, ErrCorruptData) {
		t.Fatalf("update with corrupt metadata error = %v", err)
	}
	var storedStatus domain.LearningStatus
	var updatedAt string
	if err := store.sql.QueryRowContext(ctx, "SELECT learning_status, updated_at FROM vocabulary_items WHERE id = ?", saved.ItemID).Scan(&storedStatus, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if storedStatus != domain.LearningStatusNew || updatedAt != saved.UpdatedAt {
		t.Fatalf("failed update persisted status %q at %q", storedStatus, updatedAt)
	}
}

func TestVocabularyRevisionIgnoresNoOpAndPresentationWrites(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	_, saved, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", Status: domain.LearningStatusNew,
		Notes: []string{"original"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	noOp, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, ExpectedRevision: &saved.EditRevision,
		Status: &saved.Status, Notes: &saved.Notes, Now: now.Add(time.Minute),
	})
	if err != nil || noOp.EditRevision != saved.EditRevision {
		t.Fatalf("identical metadata invalidated revision: %#v, %v", noOp, err)
	}
	if _, err := store.NextLearningItem(ctx, "owner", clockAt(now.Add(time.Minute))); err != nil {
		t.Fatal(err)
	}
	tags := []string{"after presentation"}
	updated, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, ExpectedRevision: &saved.EditRevision,
		Tags: &tags, Now: now.Add(2 * time.Minute),
	})
	if err != nil || updated.EditRevision <= saved.EditRevision || !reflect.DeepEqual(updated.Notes, saved.Notes) {
		t.Fatalf("presentation invalidated draft or partial edit lost metadata: %#v, %v", updated, err)
	}
	// Metadata writers outside UpdateVocabulary must also invalidate drafts.
	if _, err := store.sql.ExecContext(ctx,
		"UPDATE vocabulary_items SET personal_interest = 'high' WHERE id = ?", saved.ItemID); err != nil {
		t.Fatal(err)
	}
	notes := []string{"stale edit"}
	if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: saved.ItemID, ExpectedRevision: &updated.EditRevision,
		Notes: &notes, Now: now.Add(3 * time.Minute),
	}); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("outside metadata writer did not invalidate revision: %v", err)
	}
	current, err := store.VocabularyByID(ctx, "owner", saved.ItemID)
	if err != nil || current.PersonalInterest != domain.PersonalInterestHigh ||
		!reflect.DeepEqual(current.Notes, saved.Notes) || current.EditRevision <= updated.EditRevision {
		t.Fatalf("stale writer changed outside edit: %#v, %v", current, err)
	}
}

func TestVocabularyConcurrentRevisionWritersHaveOneWinner(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "writers.sqlite")
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
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	_, saved, err := first.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", Status: domain.LearningStatusNew,
		CustomDescription: "preserved", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		item domain.VocabularyItem
		err  error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for index, store := range []*DB{first, second} {
		go func(index int, store *DB) {
			<-start
			notes := []string{[]string{"first", "second"}[index]}
			item, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
				OwnerKey: "owner", ItemID: saved.ItemID, ExpectedRevision: &saved.EditRevision,
				Notes: &notes, Now: now.Add(time.Minute),
			})
			results <- result{item, err}
		}(index, store)
	}
	close(start)
	var winner domain.VocabularyItem
	wins, conflicts := 0, 0
	for range 2 {
		result := <-results
		if result.err == nil {
			winner = result.item
			wins++
		} else if errors.Is(result.err, ErrEditConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent update failed unexpectedly: %v", result.err)
		}
	}
	current, err := first.VocabularyByID(ctx, "owner", saved.ItemID)
	if err != nil || wins != 1 || conflicts != 1 || !reflect.DeepEqual(current, winner) ||
		current.CustomDescription != saved.CustomDescription || current.EditRevision <= saved.EditRevision {
		t.Fatalf("revision race: wins=%d conflicts=%d current=%#v winner=%#v err=%v", wins, conflicts, current, winner, err)
	}
}

func TestVocabularyListFiltersResolvedSelectedSense(t *testing.T) {
	for _, test := range []struct {
		name       string
		entry      int
		definition int
		text       string
		noSense    bool
		wantPOS    string
	}{
		{name: "noun ignores other entry verb", entry: 0, definition: 1, text: "noun only", wantPOS: "noun"},
		{name: "exact indices win over duplicate text", entry: 1, definition: 0, text: "shared", wantPOS: "verb"},
		{name: "stale indices use first duplicate text", entry: 20, definition: 20, text: "shared", wantPOS: "noun"},
		{name: "mismatched indexed text falls back", entry: 0, definition: 1, text: "verb only", wantPOS: "verb"},
		{name: "negative index falls back", entry: -1, definition: -1, text: "verb only", wantPOS: "verb"},
		{name: "unresolved text has no POS", entry: 0, definition: 0, text: "removed"},
		{name: "missing POS does not borrow from duplicate", entry: 2, definition: 0, text: "shared"},
		{name: "lookup without selected sense", noSense: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, now := reinforcementTestStore(t)
			snapshot, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
				Provider: "cambridge", NormalizedTerm: "bank", ParserVersion: 1, FetchedAt: now, ExpiresAt: now,
				Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{
					{Headword: "bank", PartOfSpeech: " Noun ", Definitions: []domain.DictionaryDefinition{{Definition: "shared"}, {Definition: "noun only"}}},
					{Headword: "bank", PartOfSpeech: "verb", Definitions: []domain.DictionaryDefinition{{Definition: "shared"}, {Definition: "verb only"}}},
					{Headword: "bank", Definitions: []domain.DictionaryDefinition{{Definition: "shared"}}},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			input := VocabularyCreate{
				OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", LookupID: snapshot.ID,
				Status: domain.LearningStatusLearning, Now: now,
			}
			if !test.noSense {
				input.SelectedEntryIndex, input.SelectedDefinitionIndex = &test.entry, &test.definition
				input.SelectedDefinition = &domain.DictionaryDefinition{Definition: test.text}
			}
			_, saved, err := store.SaveVocabulary(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			for _, parts := range [][]string{{"noun"}, {"verb"}, {"noun", "verb"}} {
				items, err := store.ListVocabulary(ctx, VocabularyListQuery{
					OwnerKey: "owner", PartsOfSpeech: parts, Sort: "alphabetical", Limit: 10,
				})
				if err != nil {
					t.Fatal(err)
				}
				wantMatch := false
				for _, part := range parts {
					wantMatch = wantMatch || part == test.wantPOS
				}
				if wantMatch {
					if len(items) != 1 || !reflect.DeepEqual(items[0], saved) {
						t.Fatalf("POS %v returned %#v; want saved sense %#v", parts, items, saved)
					}
				} else if len(items) != 0 {
					t.Fatalf("POS %v matched wrong or unresolved sense: %#v", parts, items)
				}
			}
		})
	}
}

func TestVocabularyListCombinesExerciseFilters(t *testing.T) {
	ctx := context.Background()
	store, now := reinforcementTestStore(t)
	snapshot, err := store.InsertDictionarySnapshot(ctx, DictionarySnapshotInsert{
		Provider: "cambridge", NormalizedTerm: "bank account", ParserVersion: 1, FetchedAt: now, ExpiresAt: now,
		Data: domain.DictionarySnapshotData{Status: 200, Entries: []domain.DictionaryEntry{
			{Headword: "bank account", PartOfSpeech: "noun", Definitions: []domain.DictionaryDefinition{{Definition: "noun sense"}}},
			{Headword: "bank account", PartOfSpeech: "verb", Definitions: []domain.DictionaryDefinition{{Definition: "verb sense"}}},
			{Headword: "bank account", PartOfSpeech: "adjective", Definitions: []domain.DictionaryDefinition{{Definition: "adjective sense"}}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	saved := make(map[string]domain.VocabularyItem)
	for _, name := range []string{
		"noun", "verb", "owner", "word", "POS", "usefulness", "interest", "tag",
		"status", "query", "description", "lookup", "excluded",
	} {
		entry, definition := 0, 0
		input := VocabularyCreate{
			OwnerKey: "owner", Term: "bank account", NormalizedTerm: "bank account", SenseKey: name,
			LookupID: snapshot.ID, SelectedEntryIndex: &entry, SelectedDefinitionIndex: &definition,
			Status: domain.LearningStatusLearning, Usefulness: domain.UsefulnessHigh, PersonalInterest: domain.PersonalInterestHigh,
			Tags: []string{"practice", "O'Reilly"}, CustomDescription: "A saved description", Now: now,
		}
		switch name {
		case "verb":
			entry, input.Status = 1, domain.LearningStatusLearned
		case "owner":
			input.OwnerKey = "other-owner"
		case "word":
			input.Term, input.NormalizedTerm = "bank-account", "bank-account"
		case "POS":
			entry = 2
		case "usefulness":
			input.Usefulness = domain.UsefulnessLow
		case "interest":
			input.PersonalInterest = domain.PersonalInterestLow
		case "tag":
			input.Tags = []string{"practice"}
		case "status":
			input.Status = domain.LearningStatusArchived
		case "query":
			input.Term, input.NormalizedTerm = "checking account", "checking account"
		case "description":
			input.CustomDescription = ""
		case "lookup":
			input.LookupID = ""
		}
		input.SelectedDefinition = &snapshot.Data.Entries[entry].Definitions[definition]
		_, item, err := store.SaveVocabulary(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		// Exercise explicit saved metadata rather than usefulness inference.
		if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = ? WHERE id = ?", input.Usefulness, item.ItemID); err != nil {
			t.Fatal(err)
		}
		saved[name] = item
	}
	has := true
	query := VocabularyListQuery{
		OwnerKey: "owner", Query: "bank", TermType: "expression",
		Statuses:      []domain.LearningStatus{domain.LearningStatusLearning, domain.LearningStatusLearned},
		PartsOfSpeech: []string{"noun", "verb"}, Tags: []string{"practice", "O'Reilly"},
		Usefulness: domain.UsefulnessHigh, PersonalInterest: domain.PersonalInterestHigh,
		HasLookup: &has, HasCustomDescription: &has, ExcludeItemIDs: []string{saved["excluded"].ItemID},
		Sort: "alphabetical", Limit: 20,
	}
	for _, order := range []string{"alphabetical", "random"} {
		query.Sort = order
		items, err := store.ListVocabulary(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{saved["noun"].ItemID: true, saved["verb"].ItemID: true}
		for _, item := range items {
			if !want[item.ItemID] {
				t.Fatalf("%s returned unexpected or duplicate item %#v", order, item)
			}
			delete(want, item.ItemID)
		}
		if len(want) != 0 {
			t.Fatalf("%s missed eligible senses: %v", order, want)
		}
	}
	query.TermType = "word"
	items, err := store.ListVocabulary(ctx, query)
	if err != nil || len(items) != 1 || items[0].ItemID != saved["word"].ItemID {
		t.Fatalf("lexical word filter = %#v, error %v", items, err)
	}
	query.TermType, query.PartsOfSpeech, query.Statuses = "expression", nil, nil
	query.HasLookup = nil
	items, err = store.ListVocabulary(ctx, query)
	if err != nil {
		t.Fatal(err)
	}
	foundArchived, foundUnresolved := false, false
	for _, item := range items {
		foundArchived = foundArchived || item.ItemID == saved["status"].ItemID
		foundUnresolved = foundUnresolved || item.ItemID == saved["lookup"].ItemID
	}
	if !foundArchived || !foundUnresolved {
		t.Fatalf("omitted status/POS filters excluded saved items: %#v", items)
	}
}

func TestVocabularyRandomListingPreservesPendingLearningAndReinforcement(t *testing.T) {
	ctx := context.Background()
	store, now := reinforcementTestStore(t)
	_, learning, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "learning",
		Status: domain.LearningStatusLearning, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextLearningItem(ctx, "owner", clockAt(now)); err != nil {
		t.Fatal(err)
	}
	eligible := map[string]bool{learning.ItemID: true}
	var excluded string
	for _, sense := range []string{"finance", "river", "excluded"} {
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: sense,
			Status: domain.LearningStatusLearned, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if sense == "excluded" {
			excluded = item.ItemID
		} else {
			eligible[item.ItemID] = true
		}
		seedReinforcementPresentation(t, store, item.ItemID, "pending-"+sense, now)
	}
	state := func() map[string][]map[string]any {
		t.Helper()
		result := make(map[string][]map[string]any)
		for _, table := range []string{
			"vocabulary_items", "dictionary_snapshots", "learning_cards", "learning_presentations",
			"review_attempts", "reinforcement_practice", "reinforcement_presentations", "reinforcement_attempts",
		} {
			rows, err := store.sql.QueryContext(ctx, "SELECT * FROM "+adminIdentifier(table)+" ORDER BY rowid")
			if err != nil {
				t.Fatal(err)
			}
			result[table], err = adminScan(rows)
			if err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	before := state()
	query := VocabularyListQuery{OwnerKey: "owner", TermType: "word", ExcludeItemIDs: []string{excluded}, Sort: "random", Limit: 2}
	for _, limit := range []int{2, 10} {
		query.Limit = limit
		items, err := store.ListVocabulary(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		wantCount := min(limit, len(eligible))
		if len(items) != wantCount {
			t.Fatalf("random limit %d returned %d items; want %d", limit, len(items), wantCount)
		}
		seen := make(map[string]bool)
		for _, item := range items {
			if !eligible[item.ItemID] || seen[item.ItemID] {
				t.Fatalf("random list returned excluded or duplicate item %#v", item)
			}
			seen[item.ItemID] = true
		}
	}
	for _, cursor := range [][2]string{{"primary", ""}, {"", learning.ItemID}, {"primary", learning.ItemID}} {
		query.CursorPrimary, query.CursorID = cursor[0], cursor[1]
		if _, err := store.ListVocabulary(ctx, query); err == nil {
			t.Fatalf("random list accepted cursor %v", cursor)
		}
	}
	if after := state(); !reflect.DeepEqual(after, before) {
		t.Fatal("listing changed saved vocabulary, pending tokens, scheduling, or review state")
	}
}
