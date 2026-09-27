package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

func lifecycleSchedule(card LearningCard, now time.Time, rating domain.ReviewRating, values settings.Values) (LearningCard, float64, error) {
	card, retrievability, err := coreStorageReviewSchedule(card, now, rating, values)
	card.ScheduledDays = 21
	card.DueAt = now.Add(21 * 24 * time.Hour)
	return card, retrievability, err
}

func lifecycleCard(t *testing.T, store *DB, itemID string) LearningCard {
	t.Helper()
	card, err := scanLearningCard(store.sql.QueryRow("SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", itemID))
	if err != nil {
		t.Fatal(err)
	}
	return card
}

func lifecycleReview(t *testing.T, store *DB, itemID string, at time.Time, rating domain.ReviewRating) ReviewAttempt {
	t.Helper()
	card := lifecycleCard(t, store, itemID)
	attempt, duplicate, err := store.RecordReview(context.Background(), RecordReviewInput{
		OwnerKey: "owner", ReviewToken: card.ReviewToken, Rating: rating, Now: clockAt(at),
	}, lifecycleSchedule)
	if err != nil || duplicate {
		t.Fatalf("review: duplicate=%t error=%v", duplicate, err)
	}
	return attempt
}

func TestMasteryCountsFiveUTCDaysAndCorrectionRestoresEvidence(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := savePresentationVocabulary(t, store, "owner", "mastery", now)
	now = now.Truncate(24 * time.Hour).Add(24*time.Hour - time.Minute)
	first := lifecycleReview(t, store, item.ItemID, now, domain.ReviewRatingGood)
	if first.StatusAfter != domain.LearningStatusLearning || first.After.MasteryStreak != 1 {
		t.Fatalf("first answer: %#v", first)
	}
	repeat := lifecycleReview(t, store, item.ItemID, now.Add(time.Second), domain.ReviewRatingEasy)
	if repeat.After.MasteryStreak != 1 || !repeat.After.LastMasteryAt.Equal(now) {
		t.Fatalf("same-day credit: %#v", repeat.After)
	}
	// A distinct UTC day counts even one minute later. Earlier wall clocks do not.
	second := lifecycleReview(t, store, item.ItemID, now.Add(time.Minute), domain.ReviewRatingGood)
	backwards := lifecycleReview(t, store, item.ItemID, now.Add(-time.Hour), domain.ReviewRatingGood)
	if second.After.MasteryStreak != 2 || backwards.After.MasteryStreak != 2 || !backwards.After.LastMasteryAt.Equal(second.ReviewedAt) {
		t.Fatalf("UTC boundary or backwards clock: %#v %#v", second.After, backwards.After)
	}
	var fourth ReviewAttempt
	for day := 2; day <= 3; day++ {
		fourth = lifecycleReview(t, store, item.ItemID, now.Add(time.Duration(day)*24*time.Hour), domain.ReviewRatingGood)
	}
	if fourth.After.MasteryStreak != 4 || fourth.StatusAfter != domain.LearningStatusLearning {
		t.Fatalf("four recalls promoted: %#v", fourth)
	}
	fifth := lifecycleReview(t, store, item.ItemID, now.Add(4*24*time.Hour), domain.ReviewRatingEasy)
	if fifth.After.MasteryStreak != 5 || fifth.StatusAfter != domain.LearningStatusLearned {
		t.Fatalf("five recalls not learned: %#v", fifth)
	}
	pending := fifth.After.ReviewToken
	for _, rating := range []domain.ReviewRating{domain.ReviewRatingHard, domain.ReviewRatingEasy} {
		corrected, duplicate, err := store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: fifth.ReviewToken, Rating: rating, Now: clockAt(now.Add(10 * 24 * time.Hour))}, lifecycleSchedule)
		if err != nil || duplicate {
			t.Fatalf("correction: %t %v", duplicate, err)
		}
		wantStreak, wantStatus, wantAt := uint64(0), domain.LearningStatusLearning, fourth.After.LastMasteryAt
		if rating == domain.ReviewRatingEasy {
			wantStreak, wantStatus, wantAt = 5, domain.LearningStatusLearned, fifth.ReviewedAt
		}
		if corrected.After.MasteryStreak != wantStreak || corrected.StatusAfter != wantStatus || !corrected.After.LastMasteryAt.Equal(wantAt) || corrected.After.ReviewToken != pending {
			t.Fatalf("correction did not restore original evidence: %#v", corrected)
		}
	}
	failed := lifecycleReview(t, store, item.ItemID, fifth.ReviewedAt.Add(time.Minute), domain.ReviewRatingAgain)
	if failed.StatusAfter != domain.LearningStatusLearning || failed.After.MasteryStreak != 0 {
		t.Fatalf("failure not demoted: %#v", failed)
	}
	corrected, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: failed.ReviewToken, Rating: domain.ReviewRatingHard, Now: clockAt(now)}, lifecycleSchedule)
	if err != nil || corrected.StatusAfter != domain.LearningStatusLearned || corrected.After.MasteryStreak != 0 {
		t.Fatalf("learned pre-status not restored: %#v %v", corrected, err)
	}
}

func TestMasteryFailureDoesNotRefundDailyCredit(t *testing.T) {
	for _, rating := range []domain.ReviewRating{domain.ReviewRatingAgain, domain.ReviewRatingHard} {
		t.Run(string(rating), func(t *testing.T) {
			store, now := reinforcementTestStore(t)
			item := savePresentationVocabulary(t, store, "owner", "reset", now)
			lifecycleReview(t, store, item.ItemID, now, domain.ReviewRatingGood)
			failed := lifecycleReview(t, store, item.ItemID, now.Add(time.Minute), rating)
			recovered := lifecycleReview(t, store, item.ItemID, now.Add(2*time.Minute), domain.ReviewRatingEasy)
			if failed.After.MasteryStreak != 0 || recovered.After.MasteryStreak != 0 || !recovered.After.LastMasteryAt.Equal(now) {
				t.Fatalf("failure refunded daily credit: %#v %#v", failed.After, recovered.After)
			}
			next := lifecycleReview(t, store, item.ItemID, now.Add(24*time.Hour), domain.ReviewRatingGood)
			if next.After.MasteryStreak != 1 {
				t.Fatalf("next day did not restart: %#v", next.After)
			}
		})
	}
}

func TestMasteryRequiresMatureReviewSchedule(t *testing.T) {
	for _, state := range []int{1, 2} {
		store, now := reinforcementTestStore(t)
		item := savePresentationVocabulary(t, store, "owner", "immature", now)
		for day := range 4 {
			lifecycleReview(t, store, item.ItemID, now.Add(time.Duration(day)*24*time.Hour), domain.ReviewRatingGood)
		}
		card := lifecycleCard(t, store, item.ItemID)
		attempt, _, err := store.RecordReview(context.Background(), RecordReviewInput{OwnerKey: "owner", ReviewToken: card.ReviewToken, Rating: domain.ReviewRatingGood, Now: clockAt(now.Add(4 * 24 * time.Hour))}, func(card LearningCard, at time.Time, rating domain.ReviewRating, values settings.Values) (LearningCard, float64, error) {
			next, r, err := lifecycleSchedule(card, at, rating, settings.Defaults())
			next.FSRSState = state
			if state == 2 {
				next.ScheduledDays = 20
			}
			return next, r, err
		})
		if err != nil || attempt.After.MasteryStreak != 5 || attempt.StatusAfter != domain.LearningStatusLearning {
			t.Fatalf("immature promotion: %#v %v", attempt, err)
		}
	}
}

func TestLifecycleRollbackAndRetryAfterVocabularyDeletion(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := savePresentationVocabulary(t, store, "owner", "atomic-status", now)
	before := lifecycleCard(t, store, item.ItemID)
	if _, err := store.sql.Exec(`CREATE TRIGGER reject_status BEFORE UPDATE OF learning_status ON vocabulary_items BEGIN SELECT RAISE(ABORT, 'reject status'); END`); err != nil {
		t.Fatal(err)
	}
	input := RecordReviewInput{OwnerKey: "owner", ReviewToken: before.ReviewToken, Rating: domain.ReviewRatingGood, Now: clockAt(now)}
	if _, _, err := store.RecordReview(ctx, input, lifecycleSchedule); err == nil {
		t.Fatal("accepted partial review")
	}
	if after := lifecycleCard(t, store, item.ItemID); after != before {
		t.Fatalf("rollback changed card: %#v", after)
	}
	assertReinforcementRowCount(t, store, "review_attempts", 0)
	if _, err := store.sql.Exec("DROP TRIGGER reject_status"); err != nil {
		t.Fatal(err)
	}
	accepted, _, err := store.RecordReview(ctx, input, lifecycleSchedule)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
		t.Fatal(err)
	}
	retry, duplicate, err := store.RecordReview(ctx, input, nil)
	if err != nil || !duplicate || retry.StatusAfter != accepted.StatusAfter || retry.After.MasteryStreak != accepted.After.MasteryStreak || !retry.After.LastMasteryAt.Equal(accepted.After.LastMasteryAt) {
		t.Fatalf("deleted retry lost snapshot: %#v %t %v", retry, duplicate, err)
	}
}

func TestReinforcementFailureDemotesAtomicallyAndGuardsCorrection(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := saveReinforcementVocabulary(t, store, "bank", now)
	_, sibling, err := store.SaveVocabulary(ctx, VocabularyCreate{OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "river", Status: domain.LearningStatusLearned, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	accepted := lifecycleReview(t, store, item.ItemID, now, domain.ReviewRatingGood)
	before := lifecycleCard(t, store, item.ItemID)
	seedReinforcementPresentation(t, store, item.ItemID, "failure", now)
	// Deliberately earlier feedback time proves ordering uses review counts.
	feedbackAt := now.Add(-time.Hour)
	input := RecordReviewInput{OwnerKey: "owner", ReviewToken: "failure", Rating: domain.ReviewRatingAgain, Now: clockAt(feedbackAt)}
	if _, err := store.sql.Exec(`CREATE TRIGGER reject_demotion BEFORE UPDATE OF learning_status ON vocabulary_items BEGIN SELECT RAISE(ABORT, 'reject demotion'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordReinforcementReview(ctx, input); err == nil {
		t.Fatal("partial reinforcement accepted")
	}
	if card := lifecycleCard(t, store, item.ItemID); card != before {
		t.Fatalf("failed transaction changed schedule: %#v", card)
	}
	assertReinforcementRowCount(t, store, "reinforcement_attempts", 0)
	if _, err := store.sql.Exec("DROP TRIGGER reject_demotion"); err != nil {
		t.Fatal(err)
	}
	practice, duplicate, err := store.RecordReinforcementReview(ctx, input)
	if err != nil || duplicate || practice.StatusAfter != domain.LearningStatusLearning || practice.ReviewCount != 1 {
		t.Fatalf("reinforcement failure: %#v %t %v", practice, duplicate, err)
	}
	want := before
	want.DueAt, want.MasteryStreak = feedbackAt, 0
	if card := lifecycleCard(t, store, item.ItemID); card != want {
		t.Fatalf("failure altered memory/token or lost daily credit: %#v want %#v", card, want)
	}
	updated, err := store.VocabularyByID(ctx, "owner", item.ItemID)
	if err != nil || updated.Status != domain.LearningStatusLearning || updated.UpdatedAt != TimeString(feedbackAt) || updated.EditRevision != item.EditRevision+1 {
		t.Fatalf("demotion metadata: %#v %v", updated, err)
	}
	other, err := store.VocabularyByID(ctx, "owner", sibling.ItemID)
	if err != nil || other.Status != domain.LearningStatusLearned {
		t.Fatalf("other sense demoted: %#v %v", other, err)
	}
	if retry, duplicate, err := store.RecordReinforcementReview(ctx, input); err != nil || !duplicate || retry != practice {
		t.Fatalf("demoted retry: %#v %t %v", retry, duplicate, err)
	}
	if _, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: accepted.ReviewToken, Rating: domain.ReviewRatingEasy, Now: clockAt(now)}, lifecycleSchedule); !errors.Is(err, ErrReviewReinforced) {
		t.Fatalf("stale correction error: %v", err)
	}
	if retry, duplicate, err := store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: accepted.ReviewToken, Rating: accepted.Rating}, nil); err != nil || !duplicate || retry.StatusAfter != domain.LearningStatusLearned {
		t.Fatalf("identical correction: %#v %t %v", retry, duplicate, err)
	}
	recovered := lifecycleReview(t, store, item.ItemID, now.Add(time.Minute), domain.ReviewRatingGood)
	if recovered.After.MasteryStreak != 0 || recovered.StatusAfter != domain.LearningStatusLearning {
		t.Fatalf("pre-failure success reused: %#v", recovered)
	}
}

func TestLifecycleMigrationPreservesManualStatusesAndLegacyRetries(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-status.sqlite")
	legacy := openLegacyDatabase(t, path, 18)
	oldStore := &DB{sql: legacy}
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	item := saveReinforcementVocabulary(t, oldStore, "manual", now)
	archived := savePresentationVocabulary(t, oldStore, "owner", "archived", now)
	if _, err := legacy.Exec("UPDATE vocabulary_items SET learning_status = 'archived' WHERE id = ?", archived.ItemID); err != nil {
		t.Fatal(err)
	}
	card, err := scanLearningCard(legacy.QueryRow("SELECT "+legacyLearningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", item.ItemID))
	if err != nil {
		t.Fatal(err)
	}
	attempt := insertLegacyReview(t, legacy, RecordReviewInput{OwnerKey: "owner", ReviewToken: card.ReviewToken, Rating: domain.ReviewRatingGood, Now: clockAt(now)}, card)
	seedReinforcementPresentation(t, oldStore, item.ItemID, "legacy-practice", now)
	if _, err := legacy.Exec(`INSERT INTO reinforcement_attempts(id, review_token, owner_key, vocabulary_item_id, rating, comment, reviewed_at, review_count_after, difficulty_after) VALUES ('legacy-feedback','legacy-practice','owner',?,'again','',?,1,1)`, item.ItemID, TimeString(now)); err != nil {
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
	for _, original := range []domain.VocabularyItem{item, archived} {
		loaded, err := store.VocabularyByID(ctx, "owner", original.ItemID)
		want := domain.LearningStatusLearned
		if original.ItemID == archived.ItemID {
			want = domain.LearningStatusArchived
		}
		if err != nil || loaded.Status != want {
			t.Fatalf("migration rewrote manual status: %#v %v", loaded, err)
		}
	}
	corrected, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{OwnerKey: "owner", ReviewToken: attempt.ReviewToken, Rating: domain.ReviewRatingAgain, Now: clockAt(now)}, lifecycleSchedule)
	if err != nil || corrected.StatusAfter != domain.LearningStatusLearned || corrected.After.MasteryStreak != 0 {
		t.Fatalf("legacy correction invented history: %#v %v", corrected, err)
	}
	if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
		t.Fatal(err)
	}
	practice, duplicate, err := store.RecordReinforcementReview(ctx, RecordReviewInput{OwnerKey: "owner", ReviewToken: "legacy-practice", Rating: domain.ReviewRatingAgain})
	if err != nil || !duplicate || practice.StatusAfter != domain.LearningStatusLearned {
		t.Fatalf("legacy reinforcement result: %#v %t %v", practice, duplicate, err)
	}
	retry, duplicate, err := store.RecordReview(ctx, RecordReviewInput{OwnerKey: "owner", ReviewToken: attempt.ReviewToken, Rating: domain.ReviewRatingAgain}, nil)
	if err != nil || !duplicate || retry.StatusAfter != domain.LearningStatusLearned {
		t.Fatalf("legacy review result: %#v %t %v", retry, duplicate, err)
	}
}

func TestReinforcementHardResetsEvidenceWithoutDemotionOrDelay(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := saveReinforcementVocabulary(t, store, "effortful", now)
	lifecycleReview(t, store, item.ItemID, now, domain.ReviewRatingGood)
	before := lifecycleCard(t, store, item.ItemID)
	seedReinforcementPresentation(t, store, item.ItemID, "hard-practice", now)
	practice, _, err := store.RecordReinforcementReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: "hard-practice", Rating: domain.ReviewRatingHard, Now: clockAt(now.Add(time.Hour)),
	})
	if err != nil || practice.StatusAfter != domain.LearningStatusLearned {
		t.Fatalf("hard demoted: %#v %v", practice, err)
	}
	want := before
	want.MasteryStreak = 0
	if card := lifecycleCard(t, store, item.ItemID); card != want {
		t.Fatalf("hard changed schedule or refunded credit: %#v", card)
	}
	// An already-overdue card must not be postponed by subsequent failure.
	seedReinforcementPresentation(t, store, item.ItemID, "overdue-failure", now)
	if _, _, err := store.RecordReinforcementReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: "overdue-failure", Rating: domain.ReviewRatingAgain, Now: clockAt(before.DueAt.Add(time.Hour)),
	}); err != nil {
		t.Fatal(err)
	}
	if card := lifecycleCard(t, store, item.ItemID); card != want {
		t.Fatalf("failure postponed overdue card: %#v", card)
	}
}

func TestLifecycleCorrectionRollbackRestoresStatusAndMastery(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := saveReinforcementVocabulary(t, store, "rollback-correction", now)
	accepted := lifecycleReview(t, store, item.ItemID, now, domain.ReviewRatingGood)
	if _, err := store.sql.Exec(`CREATE TRIGGER reject_correction_status BEFORE UPDATE OF learning_status ON vocabulary_items BEGIN SELECT RAISE(ABORT, 'reject correction'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.UpdateLatestReview(ctx, UpdateReviewInput{
		OwnerKey: "owner", ReviewToken: accepted.ReviewToken, Rating: domain.ReviewRatingAgain, Now: clockAt(now.Add(time.Hour)),
	}, lifecycleSchedule); err == nil {
		t.Fatal("partial correction accepted")
	}
	if card := lifecycleCard(t, store, item.ItemID); card != accepted.After {
		t.Fatalf("failed correction changed card: %#v", card)
	}
	retry, duplicate, err := store.RecordReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: accepted.ReviewToken, Rating: accepted.Rating,
	}, nil)
	if err != nil || !duplicate || retry.StatusAfter != domain.LearningStatusLearned || retry.After.MasteryStreak != 1 || !retry.After.LastMasteryAt.Equal(now) {
		t.Fatalf("failed correction changed accepted snapshot: %#v %t %v", retry, duplicate, err)
	}
}
