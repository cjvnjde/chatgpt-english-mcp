package storage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"english-learning-mcp/internal/domain"
)

func TestReinforcementDistributionCapsWordsAndDoesNotRewardExtraSenses(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	senses := []reinforcementSense{
		{itemID: "bank-finance", normalizedTerm: "bank", usefulness: domain.UsefulnessHigh, interest: domain.PersonalInterestHigh, commentCount: 50, practice: ReinforcementPractice{Difficulty: 4}},
		{itemID: "bright", normalizedTerm: "bright", usefulness: domain.UsefulnessHigh},
		{itemID: "calm", normalizedTerm: "calm"},
		{itemID: "deft", normalizedTerm: "deft"},
		{itemID: "eager", normalizedTerm: "eager"},
	}
	probabilities := func(senses []reinforcementSense) map[string]float64 {
		result := make(map[string]float64)
		total := 0.0
		for _, word := range planReinforcementSelection(senses, now) {
			if word.probability <= 0 || word.probability > 0.25 {
				t.Fatalf("word probability outside (0,.25]: %g", word.probability)
			}
			result[senses[word.senses[0]].normalizedTerm] = word.probability
			total += word.probability
		}
		if math.Abs(total-1) > 1e-14 {
			t.Fatalf("total probability = %.17g", total)
		}
		return result
	}
	before := probabilities(senses)
	if before["bank"] != 0.25 || before["bright"] != 0.25 || math.Abs(before["calm"]-1.0/6) > 1e-14 {
		t.Fatalf("capped mass was not proportionally redistributed: %v", before)
	}
	senses = append(senses, reinforcementSense{itemID: "bank-river", normalizedTerm: "bank"})
	if after := probabilities(senses); !reflect.DeepEqual(before, after) {
		t.Fatalf("extra meaning changed word distribution: before=%v after=%v", before, after)
	}
	words := planReinforcementSelection(senses, now)
	for _, test := range []struct {
		senseDraw float64
		itemID    string
	}{
		{0, "bank-finance"}, {0.999999, "bank-river"},
	} {
		draws := []float64{0, test.senseDraw}
		selected, probability := selectReinforcementSense(senses, words, now, func() float64 {
			draw := draws[0]
			draws = draws[1:]
			return draw
		})
		if selected.itemID != test.itemID || probability != 0.25 {
			t.Fatalf("sense draw %g selected %s with word probability %g", test.senseDraw, selected.itemID, probability)
		}
	}
	// The minimum valid pool is uniform regardless of weighting or sense count.
	four := append(append([]reinforcementSense{}, senses[:4]...), senses[5])
	for term, probability := range probabilities(four) {
		if probability != 0.25 {
			t.Fatalf("four-word pool: %s probability = %g", term, probability)
		}
	}
}

func TestReinforcementProbabilityUsesInterestUsefulnessFeedbackAndRecency(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name  string
		sense reinforcementSense
		ratio float64
	}{
		{"low usefulness remains reachable", reinforcementSense{usefulness: domain.UsefulnessLow}, 0.5},
		{"high usefulness", reinforcementSense{usefulness: domain.UsefulnessHigh}, 2},
		{"low interest remains reachable", reinforcementSense{interest: domain.PersonalInterestLow}, 0.5},
		{"high interest", reinforcementSense{interest: domain.PersonalInterestHigh}, 2},
		{"comments", reinforcementSense{commentCount: 3}, 4},
		{"comments saturate", reinforcementSense{commentCount: 100}, 9},
		{"independent difficulty", reinforcementSense{practice: ReinforcementPractice{Difficulty: 4}}, 5},
		{"just shown excluded", reinforcementSense{lastShownAt: now}, 0},
		{"cooldown expired", reinforcementSense{lastShownAt: now.Add(-6 * time.Hour)}, 0.4375},
		{"half day", reinforcementSense{lastShownAt: now.Add(-12 * time.Hour)}, 0.625},
		{"fully recovered", reinforcementSense{lastShownAt: now.Add(-48 * time.Hour)}, 1},
		{"future timestamp excluded", reinforcementSense{lastShownAt: now.Add(time.Hour)}, 0},
		{"combined low signals after cooldown", reinforcementSense{usefulness: domain.UsefulnessLow, interest: domain.PersonalInterestLow, lastShownAt: now.Add(-6 * time.Hour)}, 0.109375},
	} {
		t.Run(test.name, func(t *testing.T) {
			senses := make([]reinforcementSense, 64)
			for index := range senses {
				senses[index].normalizedTerm = fmt.Sprintf("word-%d", index)
			}
			senses[0] = test.sense
			senses[0].normalizedTerm = "target"
			var target, reference float64
			for _, word := range planReinforcementSelection(senses, now) {
				switch word.senses[0] {
				case 0:
					target = word.probability
				case 1:
					reference = word.probability
				}
			}
			if math.Abs(target/reference-test.ratio) > 1e-12 {
				t.Fatalf("relative probability = %g, want %g", target/reference, test.ratio)
			}
		})
	}
}

func TestReinforcementShortageCountsDistinctLearnedWordsWithoutIssuing(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	if _, err := store.NextReinforcementItem(ctx, "owner", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty selection error = %v", err)
	}
	for _, input := range []VocabularyCreate{
		{OwnerKey: "owner", Term: "bank", NormalizedTerm: "bank", SenseKey: "finance", Status: domain.LearningStatusLearned},
		{OwnerKey: "owner", Term: "Bank", NormalizedTerm: "bank", SenseKey: "river", Status: domain.LearningStatusLearned},
		{OwnerKey: "owner", Term: "calm", NormalizedTerm: "calm", Status: domain.LearningStatusLearned},
		{OwnerKey: "owner", Term: "deft", NormalizedTerm: "deft", Status: domain.LearningStatusLearned},
		{OwnerKey: "owner", Term: "new", NormalizedTerm: "new", Status: domain.LearningStatusNew},
		{OwnerKey: "owner", Term: "learning", NormalizedTerm: "learning", Status: domain.LearningStatusLearning},
		{OwnerKey: "owner", Term: "archived", NormalizedTerm: "archived", Status: domain.LearningStatusArchived},
		{OwnerKey: "other", Term: "other", NormalizedTerm: "other", Status: domain.LearningStatusLearned},
	} {
		input.Now = now
		if _, _, err := store.SaveVocabulary(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", now); !errors.Is(err, ErrReinforcementShortage) {
		t.Fatalf("three distinct learned words error = %v", err)
	}
	assertReinforcementRowCount(t, store, "reinforcement_presentations", 0)
	assertReinforcementRowCount(t, store, "reinforcement_practice", 0)
	saveReinforcementVocabulary(t, store, "eager", now)
	candidate, err := store.NextReinforcementItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if candidate.EligibleWordCount != 4 || candidate.SelectionProbability != 0.25 || candidate.Vocabulary.Status != domain.LearningStatusLearned {
		t.Fatalf("valid selection = %#v", candidate)
	}
}

func TestReinforcementFeedbackIsIndependentImmutableAndDurable(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	item := saveReinforcementVocabulary(t, store, "bank", now)
	before, err := scanLearningCard(store.sql.QueryRowContext(ctx, "SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", item.ItemID))
	if err != nil {
		t.Fatal(err)
	}
	var first ReinforcementPractice
	for index, test := range []struct {
		rating     domain.ReviewRating
		difficulty float64
	}{
		{domain.ReviewRatingAgain, 1}, {domain.ReviewRatingHard, 1.5},
		{domain.ReviewRatingAgain, 2.5}, {domain.ReviewRatingAgain, 3.5},
		{domain.ReviewRatingAgain, 4}, {domain.ReviewRatingAgain, 4},
		{domain.ReviewRatingGood, 3.5}, {domain.ReviewRatingEasy, 2.5},
		{domain.ReviewRatingEasy, 1.5}, {domain.ReviewRatingEasy, 0.5},
		{domain.ReviewRatingEasy, 0}, {domain.ReviewRatingGood, 0},
	} {
		token := fmt.Sprintf("practice-%d", index)
		seedReinforcementPresentation(t, store, item.ItemID, token, now)
		practice, duplicate, err := store.RecordReinforcementReview(ctx, RecordReviewInput{
			OwnerKey: "owner", ReviewToken: token, Rating: test.rating, Comment: "Keep every comment", Now: now.Add(time.Duration(index) * time.Nanosecond),
		})
		if err != nil || duplicate || practice.Difficulty != test.difficulty || practice.ReviewCount != index+1 || practice.LastRating != test.rating {
			t.Fatalf("review %d: practice=%#v duplicate=%t error=%v", index, practice, duplicate, err)
		}
		if index == 0 {
			first = practice
		}
	}
	retry := RecordReviewInput{OwnerKey: "owner", ReviewToken: "practice-0", Rating: domain.ReviewRatingAgain, Comment: "Keep every comment", Now: now.Add(24 * time.Hour)}
	practice, duplicate, err := store.RecordReinforcementReview(ctx, retry)
	if err != nil || !duplicate || practice != first {
		t.Fatalf("retry must replay original result, not current practice: %#v %t %v", practice, duplicate, err)
	}
	retry.Comment = "changed"
	if _, _, err := store.RecordReinforcementReview(ctx, retry); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed review error = %v", err)
	}
	after, err := scanLearningCard(store.sql.QueryRowContext(ctx, "SELECT "+learningCardColumns+" FROM learning_cards card WHERE vocabulary_item_id = ?", item.ItemID))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("reinforcement changed FSRS card: before=%#v after=%#v error=%v", before, after, err)
	}
	current, err := store.VocabularyByID(ctx, "owner", item.ItemID)
	if err != nil || current.Status != domain.LearningStatusLearned || current.UpdatedAt != item.UpdatedAt {
		t.Fatalf("reinforcement mutated vocabulary: %#v %v", current, err)
	}
	assertReinforcementRowCount(t, store, "learning_presentations", 0)
	assertReinforcementRowCount(t, store, "review_attempts", 0)
	for _, statement := range []string{
		"UPDATE reinforcement_attempts SET comment = 'lost'",
		"DELETE FROM reinforcement_attempts",
	} {
		if _, err := store.sql.ExecContext(ctx, statement); err == nil {
			t.Fatal("immutable feedback accepted a mutation")
		}
	}
	if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
		t.Fatal(err)
	}
	retry.Comment = "Keep every comment"
	if practice, duplicate, err := store.RecordReinforcementReview(ctx, retry); err != nil || !duplicate || practice != first {
		t.Fatalf("deletion destroyed accepted result: %#v %t %v", practice, duplicate, err)
	}
	assertReinforcementRowCount(t, store, "reinforcement_attempts", 12)
}

func TestReinforcementPendingTokensCheckOwnerStatusAndDeletion(t *testing.T) {
	for _, status := range []domain.LearningStatus{domain.LearningStatusNew, domain.LearningStatusLearning, domain.LearningStatusArchived} {
		t.Run(string(status), func(t *testing.T) {
			store, now := reinforcementTestStore(t)
			ctx := context.Background()
			item := saveReinforcementVocabulary(t, store, "bank", now)
			seedReinforcementPresentation(t, store, item.ItemID, "pending", now)
			input := RecordReviewInput{OwnerKey: "other", ReviewToken: "pending", Rating: domain.ReviewRatingGood, Now: now}
			if _, _, err := store.RecordReinforcementReview(ctx, input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("cross-owner review error = %v", err)
			}
			input.OwnerKey = "owner"
			if _, err := store.UpdateVocabulary(ctx, VocabularyUpdate{OwnerKey: "owner", ItemID: item.ItemID, Status: &status, Now: now}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.RecordReinforcementReview(ctx, input); !errors.Is(err, ErrNotLearned) {
				t.Fatalf("non-learned review error = %v", err)
			}
			if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := store.RecordReinforcementReview(ctx, input); !errors.Is(err, ErrNotFound) {
				t.Fatalf("deleted review error = %v", err)
			}
			assertReinforcementRowCount(t, store, "reinforcement_attempts", 0)
		})
	}
}

func TestReinforcementTransactionsRollbackIssuanceAndReview(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	for _, term := range []string{"bank", "calm", "deft", "eager"} {
		saveReinforcementVocabulary(t, store, term, now)
	}
	if _, err := store.sql.ExecContext(ctx, `CREATE TRIGGER reject_practice_insert BEFORE INSERT ON reinforcement_practice
		BEGIN SELECT RAISE(ABORT, 'practice unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", now); err == nil {
		t.Fatal("expected failed issuance")
	}
	assertReinforcementRowCount(t, store, "reinforcement_presentations", 0)
	if _, err := store.sql.ExecContext(ctx, "DROP TRIGGER reject_practice_insert"); err != nil {
		t.Fatal(err)
	}
	candidate, err := store.NextReinforcementItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, `CREATE TRIGGER reject_practice_update BEFORE UPDATE ON reinforcement_practice
		BEGIN SELECT RAISE(ABORT, 'practice unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	input := RecordReviewInput{OwnerKey: "owner", ReviewToken: candidate.ReviewToken, Rating: domain.ReviewRatingHard, Now: now}
	if _, _, err := store.RecordReinforcementReview(ctx, input); err == nil {
		t.Fatal("expected failed review")
	}
	assertReinforcementRowCount(t, store, "reinforcement_attempts", 0)
	if _, err := store.sql.ExecContext(ctx, "DROP TRIGGER reject_practice_update"); err != nil {
		t.Fatal(err)
	}
	if practice, duplicate, err := store.RecordReinforcementReview(ctx, input); err != nil || duplicate || practice.ReviewCount != 1 || practice.Difficulty != 0.5 {
		t.Fatalf("retry after rollback = %#v %t %v", practice, duplicate, err)
	}
}

func TestReinforcementCooldownExcludesWordsUntilExactExpiryAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "cooldown.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	bank := saveReinforcementVocabulary(t, store, "bank", now)
	for _, term := range []string{"calm", "deft", "eager"} {
		saveReinforcementVocabulary(t, store, term, now)
	}
	_, sibling, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "Bank", NormalizedTerm: "bank", SenseKey: "finance",
		Status: domain.LearningStatusArchived, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	seedReinforcementPresentation(t, store, bank.ItemID, "earlier", now)
	// A recently shown archived sibling still cools down the learned meaning.
	// Fractional timestamps must win over the same whole second ending in Z.
	latest := now.Add(time.Nanosecond)
	seedReinforcementPresentation(t, store, sibling.ItemID, "latest", latest)
	_, other, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "other", Term: "calm", NormalizedTerm: "calm",
		Status: domain.LearningStatusLearned, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(ctx, `INSERT INTO reinforcement_practice(vocabulary_item_id, last_shown_at) VALUES (?, ?)`,
		other.ItemID, TimeString(now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	store = reopened
	for _, at := range []time.Time{now.Add(-time.Hour), now.Add(30 * time.Minute), latest.Add(6*time.Hour - time.Nanosecond)} {
		if _, err := store.NextReinforcementItem(ctx, "owner", at); !errors.Is(err, ErrReinforcementShortage) {
			t.Fatalf("selection before expiry at %s: %v", at, err)
		}
	}
	assertReinforcementRowCount(t, store, "reinforcement_presentations", 2)
	candidate, err := store.NextReinforcementItem(ctx, "owner", latest.Add(6*time.Hour))
	if err != nil || candidate.EligibleWordCount != 4 || candidate.SelectionProbability != 0.25 {
		t.Fatalf("exact expiry must restore four eligible words: %#v, %v", candidate, err)
	}
}

func TestReinforcementCooldownPreservesVarietyAndCapWithoutRelaxation(t *testing.T) {
	store, now := reinforcementTestStore(t)
	ctx := context.Background()
	for _, term := range []string{"bank", "calm", "deft", "eager", "fair", "gentle", "honest"} {
		saveReinforcementVocabulary(t, store, term, now)
	}
	if _, _, err := store.SaveVocabulary(ctx, VocabularyCreate{
		OwnerKey: "owner", Term: "Bank", NormalizedTerm: "bank", SenseKey: "finance",
		Status: domain.LearningStatusLearned, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for index := range 4 {
		candidate, err := store.NextReinforcementItem(ctx, "owner", now)
		if err != nil {
			t.Fatal(err)
		}
		term := candidate.Vocabulary.NormalizedTerm
		if seen[term] || candidate.EligibleWordCount != 7-index || candidate.SelectionProbability <= 0 || candidate.SelectionProbability > 0.25 {
			t.Fatalf("cooldown or probability violated: %#v, already seen=%v", candidate, seen)
		}
		seen[term] = true
	}
	if _, err := store.NextReinforcementItem(ctx, "owner", now.Add(5*time.Hour)); !errors.Is(err, ErrReinforcementShortage) {
		t.Fatalf("must pause rather than relax cooldown or cap: %v", err)
	}
	assertReinforcementRowCount(t, store, "reinforcement_presentations", 4)
}

func reinforcementTestStore(t *testing.T) (*DB, time.Time) {
	t.Helper()
	store, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
}

func saveReinforcementVocabulary(t *testing.T, store *DB, term string, now time.Time) domain.VocabularyItem {
	t.Helper()
	_, item, err := store.SaveVocabulary(context.Background(), VocabularyCreate{
		OwnerKey: "owner", Term: term, NormalizedTerm: term, Status: domain.LearningStatusLearned, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return item
}

func seedReinforcementPresentation(t *testing.T, store *DB, itemID, token string, now time.Time) {
	t.Helper()
	if _, err := store.sql.ExecContext(context.Background(), `INSERT INTO reinforcement_practice(vocabulary_item_id, last_shown_at)
		VALUES (?, ?) ON CONFLICT(vocabulary_item_id) DO NOTHING`, itemID, TimeString(now)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.sql.ExecContext(context.Background(), `INSERT INTO reinforcement_presentations(review_token, owner_key, vocabulary_item_id, shown_at)
		VALUES (?, 'owner', ?, ?)`, token, itemID, TimeString(now)); err != nil {
		t.Fatal(err)
	}
}

func TestReinforcementCombinesCommentEvidenceAndRetainsItAfterReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reinforcement.sqlite")
	store, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	item := saveReinforcementVocabulary(t, store, "bank", now)
	normal, err := store.NextLearningItem(ctx, "owner", now)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = store.RecordReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: normal.Card.ReviewToken,
		Rating: domain.ReviewRatingHard, Comment: "Ordinary evidence", Now: now,
	}, func(card LearningCard, now time.Time, rating domain.ReviewRating, shownAt time.Time) (LearningCard, float64, error) {
		card.LastRating = rating
		card.LastReviewAt = now
		return card, 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	seedReinforcementPresentation(t, store, item.ItemID, "comment-token", now)
	input := RecordReviewInput{
		OwnerKey: "owner", ReviewToken: "comment-token", Rating: domain.ReviewRatingAgain,
		Comment: "Reinforcement evidence", Now: now.Add(time.Nanosecond),
	}
	accepted, _, err := store.RecordReinforcementReview(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	seedReinforcementPresentation(t, store, item.ItemID, "empty-token", now)
	if _, _, err := store.RecordReinforcementReview(ctx, RecordReviewInput{
		OwnerKey: "owner", ReviewToken: "empty-token", Rating: domain.ReviewRatingGood, Now: now.Add(2 * time.Nanosecond),
	}); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	senses, loadErr := loadReinforcementSenses(ctx, transaction, "owner")
	comments, commentsErr := reinforcementComments(ctx, transaction, "owner", item.ItemID)
	private, privateErr := reinforcementComments(ctx, transaction, "other", item.ItemID)
	_ = transaction.Rollback()
	if loadErr != nil || len(senses) != 1 || senses[0].commentCount != 2 {
		t.Fatalf("combined selection evidence = %#v, error=%v", senses, loadErr)
	}
	if commentsErr != nil || len(comments) != 2 || comments[0].Comment != "Reinforcement evidence" || comments[1].Comment != "Ordinary evidence" {
		t.Fatalf("newest-first evidence = %#v, error=%v", comments, commentsErr)
	}
	if privateErr != nil || len(private) != 0 {
		t.Fatalf("cross-owner evidence leaked: %#v %v", private, privateErr)
	}
	if err := store.DeleteVocabulary(ctx, "owner", item.ItemID); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	retry, duplicate, err := reopened.RecordReinforcementReview(ctx, input)
	if err != nil || !duplicate || retry != accepted {
		t.Fatalf("durable replay after deletion = %#v %t %v", retry, duplicate, err)
	}
	transaction, err = reopened.sql.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := reinforcementComments(ctx, transaction, "owner", item.ItemID)
	_ = transaction.Rollback()
	if err != nil || !reflect.DeepEqual(comments, retained) {
		t.Fatalf("comment evidence did not survive deletion/reopen: %#v %v", retained, err)
	}
}

func assertReinforcementRowCount(t *testing.T, store *DB, table string, want int) {
	t.Helper()
	var count int
	if err := store.sql.QueryRowContext(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s row count = %d, want %d", table, count, want)
	}
}
