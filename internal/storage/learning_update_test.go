package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestUpdateLatestReviewUsesOwnerInsertionOrderBeforeDuplicateCheck(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	first := recordUpdateTestReview(t, store, "owner", "first", now.Add(time.Hour))
	second := recordUpdateTestReview(t, store, "owner", "second", now)
	third := recordUpdateTestReview(t, store, "owner", "third", now)
	recordUpdateTestReview(t, store, "other", "other", now.Add(2*time.Hour))
	for _, older := range []ReviewAttempt{first, second} {
		_, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{
			OwnerKey: "owner", ReviewToken: older.ReviewToken, Rating: older.Rating, Now: clockAt(now),
		}, nil)
		if !errors.Is(err, ErrNotLatestReview) {
			t.Fatalf("older token retry error = %v, want ErrNotLatestReview", err)
		}
		if _, err := store.sql.Exec("UPDATE review_attempts SET comment = 'changed' WHERE id = ?", older.ReviewID); err == nil {
			t.Fatal("trigger allowed an older review correction")
		}
	}
	corrected, duplicate, err := store.UpdateLatestReview(ctx, UpdateReviewInput{
		OwnerKey: "owner", ReviewToken: third.ReviewToken, Rating: domain.ReviewRatingAgain, Now: clockAt(now.Add(24 * time.Hour)),
	}, coreStorageReviewSchedule)
	if err != nil || duplicate || corrected.ReviewID != third.ReviewID || corrected.After.Repetitions != 1 || !corrected.ReviewedAt.Equal(third.ReviewedAt) {
		t.Fatalf("latest correction = %#v duplicate=%t error=%v", corrected, duplicate, err)
	}
	var count int
	if err := store.sql.QueryRow("SELECT count(*) FROM review_attempts WHERE owner_key = 'owner'").Scan(&count); err != nil || count != 3 {
		t.Fatalf("correction changed history count: %d error=%v", count, err)
	}
}

func TestUpdateLatestReviewRejectsUnavailableCardsAndTokens(t *testing.T) {
	for _, scenario := range []string{"missing", "cross-owner", "archived", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			store, now := reinforcementTestStore(t)
			ctx := context.Background()
			attempt := recordUpdateTestReview(t, store, "owner", "boundary", now)
			input := UpdateReviewInput{OwnerKey: "owner", ReviewToken: attempt.ReviewToken, Rating: attempt.Rating, Now: clockAt(now)}
			want := ErrNotFound
			switch scenario {
			case "missing":
				input.ReviewToken = "unknown-token"
			case "cross-owner":
				input.OwnerKey = "other"
			case "archived":
				if _, err := store.sql.Exec("UPDATE vocabulary_items SET learning_status = 'archived' WHERE id = ?", attempt.VocabularyItemID); err != nil {
					t.Fatal(err)
				}
				want = ErrArchived
			case "deleted":
				if err := store.DeleteVocabulary(ctx, "owner", attempt.VocabularyItemID); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := store.UpdateLatestReview(ctx, input, nil); !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

func TestUpdateLatestReviewRollsBackAttemptWhenCardUpdateFails(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	attempt := recordUpdateTestReview(t, store, "owner", "atomic", now)
	if _, err := store.sql.Exec(`CREATE TRIGGER reject_card_correction BEFORE UPDATE ON learning_cards
		BEGIN SELECT RAISE(ABORT, 'card update rejected'); END`); err != nil {
		t.Fatal(err)
	}
	comment := "replacement"
	if _, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{
		OwnerKey: "owner", ReviewToken: attempt.ReviewToken, Rating: domain.ReviewRatingAgain,
		Comment: &comment, Now: clockAt(now.Add(time.Hour)),
	}, coreStorageReviewSchedule); err == nil {
		t.Fatal("correction succeeded despite rejected card write")
	}
	if _, duplicate, err := store.RecordReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: attempt.ReviewToken, Rating: attempt.Rating, Comment: attempt.Comment,
	}, nil); err != nil || !duplicate {
		t.Fatalf("failed correction changed accepted attempt: duplicate=%t error=%v", duplicate, err)
	}
	card, err := scanLearningCard(store.sql.QueryRow("SELECT "+learningCardColumns+" FROM learning_cards card WHERE id = ?", attempt.LearningCardID))
	if err != nil || card != attempt.After {
		t.Fatalf("failed correction changed card: %#v error=%v; want %#v", card, err, attempt.After)
	}
}

func TestReviewCorrectionMigrationRestoresOnlyKnownPreviousReviewTimes(t *testing.T) {
	for _, missing := range []bool{false, true} {
		name := "known predecessor"
		if missing {
			name = "missing predecessor"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "legacy.sqlite")
			legacy := openLegacyDatabase(t, path, 16)
			oldStore := &DB{sql: legacy}
			now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
			item := savePresentationVocabulary(t, oldStore, "owner", "legacy", now)
			before, err := scanLearningCard(legacy.QueryRow("SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", item.ItemID))
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				before, _, err = coreStorageReviewSchedule(before, now, domain.ReviewRatingGood)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				first := insertLegacyReview(t, legacy, RecordReviewInput{
					OwnerKey: "owner", ReviewToken: before.ReviewToken, Rating: domain.ReviewRatingGood, Now: clockAt(now),
				}, before)
				before = first.After
			}
			reviewedAt := now.Add(24 * time.Hour)
			last := insertLegacyReview(t, legacy, RecordReviewInput{
				OwnerKey: "owner", ReviewToken: before.ReviewToken, Rating: domain.ReviewRatingGood, Now: clockAt(reviewedAt),
			}, before)
			if err := legacy.Close(); err != nil {
				t.Fatal(err)
			}
			store, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			corrected, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{
				OwnerKey: "owner", ReviewToken: last.ReviewToken, Rating: domain.ReviewRatingAgain, Now: clockAt(reviewedAt.Add(time.Hour)),
			}, func(card LearningCard, at time.Time, rating domain.ReviewRating) (LearningCard, float64, error) {
				if missing {
					t.Fatal("scheduler called without a restorable pre-review clock")
				}
				if !card.LastReviewAt.Equal(now) || card.Repetitions != before.Repetitions || !at.Equal(reviewedAt) {
					t.Fatalf("incorrectly restored pre-review state: %#v at %v", card, at)
				}
				return coreStorageReviewSchedule(card, at, rating)
			})
			if missing {
				if !errors.Is(err, ErrCorruptData) {
					t.Fatalf("missing pre-review clock error = %v, want ErrCorruptData", err)
				}
			} else if err != nil || corrected.After.Repetitions != last.After.Repetitions {
				t.Fatalf("migrated correction = %#v error=%v", corrected, err)
			}
		})
	}
}

func recordUpdateTestReview(t *testing.T, store *DB, owner, term string, now time.Time) ReviewAttempt {
	t.Helper()
	item := savePresentationVocabulary(t, store, owner, term, now)
	var token string
	if err := store.sql.QueryRow("SELECT review_token FROM learning_cards WHERE vocabulary_item_id = ?", item.ItemID).Scan(&token); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.RecordReview(context.Background(), RecordReviewInput{
		OwnerKey: owner, ReviewToken: token, Rating: domain.ReviewRatingGood, Comment: "original", Now: clockAt(now),
	}, coreStorageReviewSchedule)
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}
