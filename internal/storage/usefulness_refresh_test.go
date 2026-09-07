package storage

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/usefulness"
)

func TestVocabularyInferenceCombinesHintsAndPreservesOmissions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	input := VocabularyCreate{
		OwnerKey: "owner", Term: "THE", NormalizedTerm: "the",
		Status: domain.LearningStatusNew, Now: now,
	}
	created, item, err := store.SaveVocabulary(ctx, input)
	if err != nil || !created || item.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("inferred create = %#v, created %t, error %v", item, created, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{})
	notes := []string{"A note without a hint"}
	updated, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: item.ItemID, Notes: &notes, Now: now.Add(time.Hour),
	})
	if err != nil || updated.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("omitted hint update = %#v, error %v", updated, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{})
	input.Usefulness = domain.UsefulnessLow
	created, duplicate, err := store.SaveVocabulary(ctx, input)
	if err != nil || created || !reflect.DeepEqual(duplicate, updated) {
		t.Fatalf("duplicate replaced inferred metadata = %#v, created %t, error %v", duplicate, created, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{})

	low := domain.UsefulnessLow
	updated, err = store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: item.ItemID, Usefulness: &low, Now: now.Add(2 * time.Hour),
	})
	if err != nil || updated.Usefulness != domain.UsefulnessNormal {
		t.Fatalf("two high sources plus low hint = %#v, error %v", updated, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{String: "low", Valid: true})
	input.Usefulness = domain.UsefulnessHigh
	created, duplicate, err = store.SaveVocabulary(ctx, input)
	if err != nil || created || !reflect.DeepEqual(duplicate, updated) {
		t.Fatalf("duplicate replaced explicit hint = %#v, created %t, error %v", duplicate, created, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{String: "low", Valid: true})
	if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "another-owner", ItemID: item.ItemID, Usefulness: &low, Now: now,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign owner hint update error = %v, want ErrNotFound", err)
	}
	unchanged, err := store.VocabularyByID(ctx, "owner", item.ItemID)
	if err != nil || !reflect.DeepEqual(unchanged, updated) {
		t.Fatalf("foreign update changed owner item = %#v, error %v", unchanged, err)
	}
	normal := domain.UsefulnessNormal
	updated, err = store.UpdateVocabulary(ctx, VocabularyUpdate{
		OwnerKey: "owner", ItemID: item.ItemID, Usefulness: &normal, Now: now.Add(3 * time.Hour),
	})
	if err != nil || updated.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("two high sources plus normal hint = %#v, error %v", updated, err)
	}
	assertUsefulnessHint(t, store, item.ItemID, sql.NullString{String: "normal", Valid: true})
}

func TestUsefulnessRefreshRetainsOriginalHintsAcrossRevisionsAndRestarts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "revisions.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	var saved []domain.VocabularyItem
	for _, fixture := range []struct {
		owner, sense string
		status       domain.LearningStatus
		hint         domain.Usefulness
	}{
		{"owner", "first", domain.LearningStatusNew, domain.UsefulnessLow},
		{"owner", "second", domain.LearningStatusLearning, ""},
		{"another-owner", "first", domain.LearningStatusLearned, domain.UsefulnessNormal},
		{"another-owner", "second", domain.LearningStatusArchived, domain.UsefulnessLow},
	} {
		_, item, err := store.SaveVocabulary(ctx, VocabularyCreate{
			OwnerKey: fixture.owner, Term: "the", NormalizedTerm: "the", SenseKey: fixture.sense,
			Status: fixture.status, Usefulness: fixture.hint, Notes: []string{"Preserve me"}, Now: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		saved = append(saved, item)
	}
	for cycle := range 2 {
		if _, err := store.sql.ExecContext(ctx, "UPDATE usefulness_inference_state SET revision = 'older-dataset'"); err != nil {
			t.Fatal(err)
		}
		if cycle == 0 {
			if _, err := store.sql.ExecContext(ctx, "UPDATE vocabulary_items SET usefulness = 'low'"); err != nil {
				t.Fatal(err)
			}
		}
		if cycle == 1 {
			if _, err := store.sql.ExecContext(ctx, `
				CREATE TRIGGER forbid_unchanged_usefulness BEFORE UPDATE OF usefulness ON vocabulary_items
				BEGIN SELECT RAISE(ABORT, 'unchanged usefulness rewritten'); END
			`); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		store, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		for index, expected := range saved {
			owner := "owner"
			if index >= 2 {
				owner = "another-owner"
			}
			actual, err := store.VocabularyByID(ctx, owner, expected.ItemID)
			if err != nil || !reflect.DeepEqual(actual, expected) {
				t.Fatalf("revision cycle %d item = %#v, error %v; want %#v", cycle, actual, err, expected)
			}
		}
		assertUsefulnessHint(t, store, saved[0].ItemID, sql.NullString{String: "low", Valid: true})
		assertUsefulnessHint(t, store, saved[1].ItemID, sql.NullString{})
		var revision string
		if err := store.sql.QueryRowContext(ctx, "SELECT revision FROM usefulness_inference_state WHERE id = 1").Scan(&revision); err != nil {
			t.Fatal(err)
		}
		if revision != usefulness.Revision {
			t.Fatalf("refresh revision = %q, want %q", revision, usefulness.Revision)
		}
	}
}

func TestUsefulnessMigrationRollsBackSchemaWhenBackfillFails(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre009-failure.sqlite")
	legacy := openLegacyDatabase(t, path, 8)
	if _, err := legacy.ExecContext(ctx, `
		INSERT INTO vocabulary_items(
			id, owner_key, term, normalized_term, created_at, updated_at, learning_status, usefulness, sense_key
		) VALUES ('broken-id', 'owner', 'Broken', ' BROKEN ',
			'2026-09-01T00:00:00Z', '2026-09-02T00:00:00Z', 'archived', 'low', 'legacy')
	`); err != nil {
		t.Fatal(err)
	}
	store, err := Open(ctx, path)
	if store != nil {
		store.Close()
	}
	if !errors.Is(err, ErrCorruptData) {
		t.Fatalf("pre009 corrupt backfill error = %v, want ErrCorruptData", err)
	}
	var migrationCount, hintColumns, stateTables int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM schema_migrations WHERE version = 9":                                  &migrationCount,
		"SELECT COUNT(*) FROM pragma_table_info('vocabulary_items') WHERE name = 'usefulness_hint'": &hintColumns,
		"SELECT COUNT(*) FROM sqlite_master WHERE name = 'usefulness_inference_state'":              &stateTables,
	} {
		if err := legacy.QueryRowContext(ctx, query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if migrationCount != 0 || hintColumns != 0 || stateTables != 0 {
		t.Fatalf("failed backfill committed schema: migrations %d, hint columns %d, state tables %d",
			migrationCount, hintColumns, stateTables)
	}
	var effective, updatedAt string
	if err := legacy.QueryRowContext(ctx, "SELECT usefulness, updated_at FROM vocabulary_items WHERE id = 'broken-id'").Scan(&effective, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if effective != "low" || updatedAt != "2026-09-02T00:00:00Z" {
		t.Fatalf("failed migration changed vocabulary: usefulness %q, updated_at %q", effective, updatedAt)
	}
}

func TestMatchingUsefulnessRevisionSkipsVocabularyScanAndWrites(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "unchanged.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	saved := savePresentationVocabulary(t, store, "owner", "the", time.Now())
	for _, statement := range []string{
		"PRAGMA ignore_check_constraints = ON",
		"UPDATE vocabulary_items SET usefulness_hint = 'corrupt'",
		"CREATE TRIGGER forbid_vocabulary_refresh BEFORE UPDATE ON vocabulary_items BEGIN SELECT RAISE(ABORT, 'unexpected refresh'); END",
		"CREATE TRIGGER forbid_revision_refresh BEFORE UPDATE ON usefulness_inference_state BEGIN SELECT RAISE(ABORT, 'unexpected revision write'); END",
	} {
		if _, err := store.sql.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("matching revision rescanned or rewrote vocabulary: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertUsefulnessHint(t, reopened, saved.ItemID, sql.NullString{String: "corrupt", Valid: true})
}

func TestUsefulnessRefreshRollsBackOnCorruptionAndWriteFailure(t *testing.T) {
	for _, corruption := range []struct {
		name, statement string
		wantCorrupt     bool
	}{
		{"invalid hint", "UPDATE vocabulary_items SET usefulness_hint = 'urgent' WHERE normalized_term = 'broken'", true},
		{"empty hint", "UPDATE vocabulary_items SET usefulness_hint = '' WHERE normalized_term = 'broken'", true},
		{"invalid effective", "UPDATE vocabulary_items SET usefulness = 'urgent' WHERE normalized_term = 'broken'", true},
		{"invalid normalized term", "UPDATE vocabulary_items SET normalized_term = ' BROKEN ' WHERE normalized_term = 'broken'", true},
		{"revision write", "CREATE TRIGGER fail_revision BEFORE UPDATE ON usefulness_inference_state BEGIN SELECT RAISE(ABORT, 'revision write blocked'); END", false},
	} {
		t.Run(corruption.name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "corrupt.sqlite")
			store, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			saved := savePresentationVocabulary(t, store, "owner", "the", time.Now())
			savePresentationVocabulary(t, store, "owner", "broken", time.Now())
			for _, statement := range []string{
				"UPDATE usefulness_inference_state SET revision = 'old-revision'",
				"UPDATE vocabulary_items SET usefulness = 'low' WHERE normalized_term = 'the'",
				"PRAGMA ignore_check_constraints = ON",
				corruption.statement,
			} {
				if _, err := store.sql.ExecContext(ctx, statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(ctx, path)
			if reopened != nil {
				reopened.Close()
			}
			if corruption.wantCorrupt {
				if !errors.Is(err, ErrCorruptData) {
					t.Fatalf("corrupt refresh error = %v, want ErrCorruptData", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "revision write blocked") {
				t.Fatalf("revision write error = %v", err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = raw.Close() })
			var effective, revision string
			if err := raw.QueryRowContext(ctx, "SELECT usefulness FROM vocabulary_items WHERE id = ?", saved.ItemID).Scan(&effective); err != nil {
				t.Fatal(err)
			}
			if err := raw.QueryRowContext(ctx, "SELECT revision FROM usefulness_inference_state WHERE id = 1").Scan(&revision); err != nil {
				t.Fatal(err)
			}
			if effective != "low" || revision != "old-revision" {
				t.Fatalf("failed refresh committed partial state: effective %q, revision %q", effective, revision)
			}
		})
	}
}

func TestUsefulnessHintAndRevisionDatabaseConstraints(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	savePresentationVocabulary(t, store, "owner", "the", time.Now())
	for _, statement := range []string{
		"UPDATE vocabulary_items SET usefulness_hint = ''",
		"UPDATE vocabulary_items SET usefulness_hint = 'urgent'",
		"UPDATE usefulness_inference_state SET revision = ''",
		"UPDATE usefulness_inference_state SET revision = NULL",
		"INSERT INTO usefulness_inference_state(id, revision) VALUES (2, 'extra')",
	} {
		if _, err := store.sql.ExecContext(ctx, statement); err == nil {
			t.Fatalf("database accepted %s", statement)
		}
	}
}

func assertUsefulnessHint(t *testing.T, store *DB, itemID string, want sql.NullString) {
	t.Helper()
	var hint sql.NullString
	if err := store.sql.QueryRowContext(context.Background(), "SELECT usefulness_hint FROM vocabulary_items WHERE id = ?", itemID).Scan(&hint); err != nil {
		t.Fatal(err)
	}
	if hint != want {
		t.Fatalf("item %s hint = %#v, want %#v", itemID, hint, want)
	}
}
