package learning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

func TestNextReturnsOneCompactNewItemThenClosestFutureReview(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	description := "Extremely careful about small details."
	created, item, err := store.SaveVocabulary(ctx, storage.VocabularyCreate{
		OwnerKey:          "owner",
		Term:              "meticulous",
		NormalizedTerm:    "meticulous",
		Status:            domain.LearningStatusNew,
		Usefulness:        domain.UsefulnessHigh,
		Tags:              []string{},
		CustomDescription: description,
		Notes:             []string{},
		Examples:          []string{"She was meticulous when checking the figures."},
		Now:               now.Add(-time.Hour),
	})
	if err != nil || !created {
		t.Fatalf("SaveVocabulary() = item %#v created %t error %v", item, created, err)
	}

	first, err := service.Next(ctx, false)
	if err != nil {
		t.Fatalf("Next(new) error = %v", err)
	}
	if first.Term != "meticulous" || first.Definition != description || first.Example == "" || first.Reason != "new" {
		t.Fatalf("Next(new) = %#v", first)
	}
	if first.Usefulness != domain.UsefulnessHigh {
		t.Fatalf("Next(new) usefulness = %q", first.Usefulness)
	}
	if first.ReviewToken == "" || first.LatestComment != nil || first.Comments != nil || first.Troublesome {
		t.Fatalf("Next(new) metadata = %#v", first)
	}

	now = now.Add(time.Second + time.Nanosecond)
	repeated := nextWord(t, service, false)
	if first.PresentationID <= 0 || repeated.PresentationID == first.PresentationID {
		t.Fatalf("presentations must have distinct identities: first = %#v, repeated = %#v", first, repeated)
	}
	if repeated.Term != first.Term || repeated.ReviewToken != first.ReviewToken {
		t.Fatalf("presenting again must preserve the pending review: first = %#v, repeated = %#v", first, repeated)
	}
	if repeated.ShownAt != storage.TimeString(now) || !mustParseTime(t, repeated.ShownAt).After(mustParseTime(t, first.ShownAt)) {
		t.Fatalf("presentation times must record each issuance: first = %q, repeated = %q", first.ShownAt, repeated.ShownAt)
	}

	review, err := service.Record(ctx, RecordOptions{
		ReviewToken: first.ReviewToken,
		Rating:      domain.ReviewRatingGood,
	})
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if !review.Recorded || review.Duplicate || review.NextReviewAt == "" || review.Troublesome {
		t.Fatalf("Record() = %#v", review)
	}

	closest, err := service.Next(ctx, false)
	if err != nil {
		t.Fatalf("Next(early) error = %v", err)
	}
	if closest.Term != first.Term || closest.Reason != "early" || closest.ReviewToken == first.ReviewToken {
		t.Fatalf("Next(early) = %#v, first = %#v", closest, first)
	}
}

func TestNextChoosesNewVocabularyBeforeEarlyReview(t *testing.T) {
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "reviewed", now.Add(-2*time.Hour), domain.LearningStatusNew)
	first := nextWord(t, service, false)
	recordReview(t, service, first.ReviewToken, domain.ReviewRatingEasy, "")
	saveVocabulary(t, store, "unseen", now.Add(-time.Hour), domain.LearningStatusNew)

	next := nextWord(t, service, false)
	if next.Term != "unseen" || next.Reason != "new" {
		t.Fatalf("Next() = %#v, want unseen new vocabulary", next)
	}
}

func TestRecordSupportsAllRatingsWithFSRSScheduling(t *testing.T) {
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	ratings := []domain.ReviewRating{
		domain.ReviewRatingAgain,
		domain.ReviewRatingHard,
		domain.ReviewRatingGood,
		domain.ReviewRatingEasy,
	}
	dueTimes := make([]time.Time, 0, len(ratings))
	for _, rating := range ratings {
		store, service := newTestService(t)
		service.now = func() time.Time { return now }
		saveVocabulary(t, store, string(rating), now.Add(-time.Hour), domain.LearningStatusNew)
		next := nextWord(t, service, false)
		result := recordReview(t, service, next.ReviewToken, rating, "")
		dueTimes = append(dueTimes, mustParseTime(t, result.NextReviewAt))
	}
	for index := 1; index < len(dueTimes); index++ {
		if !dueTimes[index].After(dueTimes[index-1]) {
			t.Fatalf("rating due times = %v, want strictly increasing", dueTimes)
		}
	}
}

func TestCommentsAndTroublesomeStateGuideLaterReviews(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "economical", now.Add(-time.Hour), domain.LearningStatusNew)

	first := nextWord(t, service, false)
	firstReview := recordReview(t, service, first.ReviewToken, domain.ReviewRatingAgain, "Confused it with economic.")
	now = mustParseTime(t, firstReview.NextReviewAt)
	second := nextWord(t, service, false)
	if second.LatestComment == nil || second.LatestComment.Text != "Confused it with economic." || second.Comments != nil {
		t.Fatalf("Next(latest comment) = %#v", second)
	}
	secondReview := recordReview(t, service, second.ReviewToken, domain.ReviewRatingAgain, "Could not recall the ending.")
	if !secondReview.Troublesome {
		t.Fatalf("second failed review = %#v", secondReview)
	}

	duplicate := recordReview(t, service, second.ReviewToken, domain.ReviewRatingAgain, "Could not recall the ending.")
	if !duplicate.Duplicate || duplicate.NextReviewAt != secondReview.NextReviewAt {
		t.Fatalf("duplicate review = %#v, first = %#v", duplicate, secondReview)
	}
	_, err := service.Record(ctx, RecordOptions{
		ReviewToken: second.ReviewToken,
		Rating:      domain.ReviewRatingAgain,
		Comment:     "Different retry data.",
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)

	now = mustParseTime(t, secondReview.NextReviewAt)
	withHistory := nextWord(t, service, true)
	if !withHistory.Troublesome || withHistory.Reason != "troublesome" {
		t.Fatalf("Next(troublesome) = %#v", withHistory)
	}
	if withHistory.LatestComment == nil || withHistory.LatestComment.Text != "Could not recall the ending." {
		t.Fatalf("latest troublesome comment = %#v", withHistory.LatestComment)
	}
	if len(withHistory.Comments) != 2 || withHistory.Comments[1].Text != "Confused it with economic." {
		t.Fatalf("all troublesome comments = %#v", withHistory.Comments)
	}
}

func TestArchivedVocabularyIsExcludedAndTokenCannotBeReviewed(t *testing.T) {
	ctx := context.Background()
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "already archived", now.Add(-2*time.Hour), domain.LearningStatusArchived)
	active := saveVocabulary(t, store, "archive me", now.Add(-time.Hour), domain.LearningStatusNew)
	next := nextWord(t, service, false)
	archived := domain.LearningStatusArchived
	if _, err := store.UpdateVocabulary(ctx, storage.VocabularyUpdate{
		OwnerKey: "owner",
		ItemID:   active.ItemID,
		Status:   &archived,
		Now:      now,
	}); err != nil {
		t.Fatalf("UpdateVocabulary(archived) error = %v", err)
	}

	_, err := service.Next(ctx, false)
	assertApplicationCode(t, err, apperr.NotFound)
	_, err = service.Record(ctx, RecordOptions{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)
}

func TestRecordValidatesCommentLength(t *testing.T) {
	store, service := newTestService(t)
	now := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	saveVocabulary(t, store, "verbose", now, domain.LearningStatusNew)
	next := nextWord(t, service, false)
	_, err := service.Record(context.Background(), RecordOptions{
		ReviewToken: next.ReviewToken,
		Rating:      domain.ReviewRatingGood,
		Comment:     strings.Repeat("x", maximumCommentRunes+1),
	})
	assertApplicationCode(t, err, apperr.InvalidArgument)
}

func TestTutoringContentUsesSelectedSense(t *testing.T) {
	item := domain.VocabularyItem{
		Examples: []string{},
		Sense: &domain.VocabularySense{Definition: domain.DictionaryDefinition{
			Definition: "to move a boat using oars",
			Examples:   []string{"Row for your life!"},
		}},
		Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{{Definitions: []domain.DictionaryDefinition{{Definition: "a line of things"}}}}},
	}
	definition, example := tutoringContent(item)
	if definition != "to move a boat using oars" || example != "Row for your life!" {
		t.Fatalf("tutoringContent() = %q, %q", definition, example)
	}
}

func TestTutoringContentInfersLegacySenseFromLearnerMetadata(t *testing.T) {
	item := domain.VocabularyItem{
		Tags:     []string{"boats", "verbs"},
		Notes:    []string{"Move a boat through the water using oars."},
		Examples: []string{"Row for your life!"},
		Lookup: &domain.DictionaryLookupResult{Entries: []domain.DictionaryEntry{
			{Definitions: []domain.DictionaryDefinition{{Definition: "a line of things arranged next to each other"}}},
			{Definitions: []domain.DictionaryDefinition{{Definition: "to move a boat through water using oars"}}},
		}},
	}
	definition, example := tutoringContent(item)
	if definition != "to move a boat through water using oars" || example != "Row for your life!" {
		t.Fatalf("tutoringContent() = %q, %q", definition, example)
	}
}

func newTestService(t *testing.T) (*storage.DB, *Service) {
	t.Helper()
	store, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, NewService(store, "owner")
}

func saveVocabulary(
	t *testing.T,
	store *storage.DB,
	term string,
	now time.Time,
	status domain.LearningStatus,
) domain.VocabularyItem {
	t.Helper()
	created, item, err := store.SaveVocabulary(context.Background(), storage.VocabularyCreate{
		OwnerKey:       "owner",
		Term:           term,
		NormalizedTerm: term,
		Status:         status,
		Tags:           []string{},
		Notes:          []string{},
		Examples:       []string{},
		Now:            now,
	})
	if err != nil {
		t.Fatalf("SaveVocabulary(%q) error = %v", term, err)
	}
	if !created {
		t.Fatalf("SaveVocabulary(%q) created = false", term)
	}
	return item
}

func nextWord(t *testing.T, service *Service, includeComments bool) NextResult {
	t.Helper()
	result, err := service.Next(context.Background(), includeComments)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	return result
}

func recordReview(
	t *testing.T,
	service *Service,
	reviewToken string,
	rating domain.ReviewRating,
	comment string,
) RecordResult {
	t.Helper()
	result, err := service.Record(context.Background(), RecordOptions{
		ReviewToken: reviewToken,
		Rating:      rating,
		Comment:     comment,
	})
	if err != nil {
		t.Fatalf("Record(%s) error = %v", rating, err)
	}
	return result
}

func mustParseTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatalf("time.Parse(%q) error = %v", value, err)
	}
	return parsed
}

func assertApplicationCode(t *testing.T, err error, code apperr.Code) {
	t.Helper()
	var applicationError *apperr.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("error = %v, want application code %s", err, code)
	}
}
