package learning

import (
	"context"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

func TestReinforcementServiceShortageAndReviewBoundaries(t *testing.T) {
	store, service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	_, err := service.ReinforcementNext(ctx)
	assertApplicationCode(t, err, apperr.NotFound)
	for _, term := range []string{"bank", "calm", "deft"} {
		saveVocabulary(t, store, term, now, domain.LearningStatusLearned)
	}
	_, err = service.ReinforcementNext(ctx)
	assertApplicationCode(t, err, apperr.InvalidArgument)
	saveVocabulary(t, store, "eager", now, domain.LearningStatusLearned)
	next, err := service.ReinforcementNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ReinforcementNext(ctx)
	assertApplicationCode(t, err, apperr.InvalidArgument)
	for _, options := range []RecordOptions{
		{Rating: domain.ReviewRatingGood},
		{ReviewToken: strings.Repeat("界", maximumReviewTokenRunes+1), Rating: domain.ReviewRatingGood},
		{ReviewToken: next.ReviewToken, Rating: "invalid"},
		{ReviewToken: next.ReviewToken, Rating: domain.ReviewRatingGood, Comment: strings.Repeat("界", maximumCommentRunes+1)},
	} {
		_, err := service.ReinforcementReview(ctx, options)
		assertApplicationCode(t, err, apperr.InvalidArgument)
	}
	options := RecordOptions{ReviewToken: "  " + next.ReviewToken + "\n", Rating: domain.ReviewRatingAgain, Comment: "  Needed a prompt.  "}
	first, err := service.ReinforcementReview(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Recorded || first.Duplicate || first.Practice.ReviewCount != 1 || first.Practice.Difficulty != 1 || first.Practice.LastRating != domain.ReviewRatingAgain {
		t.Fatalf("review result = %#v", first)
	}
	now = now.Add(time.Hour)
	options.ReviewToken = next.ReviewToken
	options.Comment = "Needed a prompt."
	retry, err := service.ReinforcementReview(ctx, options)
	if err != nil || !retry.Duplicate || retry.Practice != first.Practice {
		t.Fatalf("normalized retry = %#v, error=%v", retry, err)
	}
	options.Rating = domain.ReviewRatingHard
	_, err = service.ReinforcementReview(ctx, options)
	assertApplicationCode(t, err, apperr.InvalidArgument)
	other := NewService(store, "other")
	_, err = other.ReinforcementReview(ctx, options)
	assertApplicationCode(t, err, apperr.NotFound)
}

func TestReinforcementTokensAndPracticeStaySeparateFromNormalLearning(t *testing.T) {
	store, service := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	for _, term := range []string{"bank", "calm", "deft", "eager", "fair", "gentle"} {
		saveVocabulary(t, store, term, now, domain.LearningStatusLearned)
	}
	normal := nextWord(t, service, false)
	first, err := service.ReinforcementNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ReinforcementNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReviewToken == second.ReviewToken || first.ReviewToken == normal.ReviewToken {
		t.Fatal("reinforcement must issue a separate token per presentation")
	}
	_, err = service.Record(ctx, RecordOptions{ReviewToken: first.ReviewToken, Rating: domain.ReviewRatingGood})
	assertApplicationCode(t, err, apperr.NotFound)
	_, err = service.ReinforcementReview(ctx, RecordOptions{ReviewToken: normal.ReviewToken, Rating: domain.ReviewRatingGood})
	assertApplicationCode(t, err, apperr.NotFound)
	// Both pending reinforcement tokens survive a normal review and each other.
	recordReview(t, service, normal.ReviewToken, domain.ReviewRatingGood, "Ordinary feedback")
	for _, token := range []string{first.ReviewToken, second.ReviewToken} {
		result, err := service.ReinforcementReview(ctx, RecordOptions{ReviewToken: token, Rating: domain.ReviewRatingGood})
		if err != nil || result.Practice.LastRating != domain.ReviewRatingGood || result.Practice.Difficulty != 0 {
			t.Fatalf("good reinforcement must not be timing-promoted: %#v %v", result, err)
		}
	}
	pending, err := service.ReinforcementNext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	status := domain.LearningStatusLearning
	if _, err := store.UpdateVocabulary(ctx, storage.VocabularyUpdate{OwnerKey: "owner", ItemID: pending.ItemID, Status: &status, Now: now}); err != nil {
		t.Fatal(err)
	}
	_, err = service.ReinforcementReview(ctx, RecordOptions{ReviewToken: pending.ReviewToken, Rating: domain.ReviewRatingEasy})
	assertApplicationCode(t, err, apperr.InvalidArgument)
}
