package learning

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
	fsrs "github.com/open-spaced-repetition/go-fsrs/v4"
)

const (
	maximumReviewTokenRunes = 200
	maximumCommentRunes     = 1000
)

type Store interface {
	NextLearningItem(ctx context.Context, ownerKey string, now time.Time) (storage.LearningCandidate, error)
	ReviewComments(ctx context.Context, ownerKey, vocabularyID string, includeAll bool) ([]storage.ReviewComment, error)
	RecordReview(
		ctx context.Context,
		input storage.RecordReviewInput,
		schedule storage.ScheduleReview,
	) (storage.ReviewAttempt, bool, error)
}

type Service struct {
	store     Store
	ownerKey  string
	scheduler *fsrs.FSRS
	now       func() time.Time
}

type ReviewFeedback struct {
	Text       string              `json:"text"`
	Rating     domain.ReviewRating `json:"rating"`
	ReviewedAt string              `json:"reviewedAt"`
}

type NextResult struct {
	ReviewToken   string           `json:"reviewToken"`
	Term          string           `json:"term"`
	Definition    string           `json:"definition,omitempty"`
	Example       string           `json:"example,omitempty"`
	Reason        string           `json:"reason"`
	Troublesome   bool             `json:"troublesome"`
	LatestComment *ReviewFeedback  `json:"latestComment,omitempty"`
	Comments      []ReviewFeedback `json:"comments,omitempty"`
}

type RecordOptions struct {
	ReviewToken string
	Rating      domain.ReviewRating
	Comment     string
}

type RecordResult struct {
	Recorded     bool   `json:"recorded"`
	Duplicate    bool   `json:"duplicate"`
	NextReviewAt string `json:"nextReviewAt"`
	Troublesome  bool   `json:"troublesome"`
}

func NewService(store Store, ownerKey string) *Service {
	return &Service{
		store:     store,
		ownerKey:  ownerKey,
		scheduler: fsrs.NewFSRS(fsrs.DefaultParam()),
		now:       time.Now,
	}
}

func (service *Service) Next(ctx context.Context, includeComments bool) (NextResult, error) {
	now := service.now().UTC()
	candidate, err := service.store.NextLearningItem(ctx, service.ownerKey, now)
	if errors.Is(err, storage.ErrNotFound) {
		return NextResult{}, apperr.New(apperr.NotFound, "no active vocabulary is available for learning")
	}
	if err != nil {
		return NextResult{}, apperr.Wrap(apperr.InternalError, "failed to select the next vocabulary item", err)
	}
	comments, err := service.store.ReviewComments(
		ctx,
		service.ownerKey,
		candidate.Vocabulary.ItemID,
		includeComments,
	)
	if err != nil {
		return NextResult{}, apperr.Wrap(apperr.InternalError, "failed to read review comments", err)
	}

	definition, example := tutoringContent(candidate.Vocabulary)
	result := NextResult{
		ReviewToken: candidate.Card.ReviewToken,
		Term:        candidate.Vocabulary.Term,
		Definition:  definition,
		Example:     example,
		Reason:      selectionReason(candidate.Card, now),
		Troublesome: isTroublesome(candidate.Card),
	}
	if len(comments) > 0 {
		latest := reviewFeedback(comments[0])
		result.LatestComment = &latest
	}
	if includeComments {
		result.Comments = make([]ReviewFeedback, 0, len(comments))
		for _, comment := range comments {
			result.Comments = append(result.Comments, reviewFeedback(comment))
		}
	}
	return result, nil
}

func (service *Service) Record(ctx context.Context, options RecordOptions) (RecordResult, error) {
	reviewToken := strings.TrimSpace(options.ReviewToken)
	tokenLength := utf8.RuneCountInString(reviewToken)
	if tokenLength == 0 || tokenLength > maximumReviewTokenRunes {
		return RecordResult{}, apperr.New(apperr.InvalidArgument, "reviewToken must contain 1 to 200 Unicode characters")
	}
	if !options.Rating.Valid() {
		return RecordResult{}, apperr.New(apperr.InvalidArgument, "rating must be again, hard, good, or easy")
	}
	comment := strings.TrimSpace(options.Comment)
	if utf8.RuneCountInString(comment) > maximumCommentRunes {
		return RecordResult{}, apperr.New(apperr.InvalidArgument, "comment must contain at most 1000 Unicode characters")
	}

	now := service.now().UTC()
	attempt, duplicate, err := service.store.RecordReview(ctx, storage.RecordReviewInput{
		OwnerKey:    service.ownerKey,
		ReviewToken: reviewToken,
		Rating:      options.Rating,
		Comment:     comment,
		Now:         now,
	}, service.schedule)
	if errors.Is(err, storage.ErrNotFound) {
		return RecordResult{}, apperr.New(apperr.NotFound, "the review token is invalid or no longer current")
	}
	if errors.Is(err, storage.ErrArchived) {
		return RecordResult{}, apperr.New(apperr.InvalidArgument, "archived vocabulary cannot be reviewed")
	}
	if errors.Is(err, storage.ErrIdempotencyConflict) {
		return RecordResult{}, apperr.New(apperr.InvalidArgument, "reviewToken was already used with different review data")
	}
	if err != nil {
		return RecordResult{}, apperr.Wrap(apperr.InternalError, "failed to record the review", err)
	}
	return RecordResult{
		Recorded:     true,
		Duplicate:    duplicate,
		NextReviewAt: storage.TimeString(attempt.After.DueAt),
		Troublesome:  isTroublesome(attempt.After),
	}, nil
}

func (service *Service) schedule(
	card storage.LearningCard,
	now time.Time,
	rating domain.ReviewRating,
) (storage.LearningCard, float64, error) {
	fsrsCard, err := toFSRSCard(card)
	if err != nil {
		return storage.LearningCard{}, 0, err
	}
	previousRetrievability, err := service.scheduler.Retrievability(fsrsCard, now)
	if err != nil {
		return storage.LearningCard{}, 0, fmt.Errorf("calculate FSRS retrievability: %w", err)
	}
	result, err := service.scheduler.Next(fsrsCard, now, toFSRSRating(rating))
	if err != nil {
		return storage.LearningCard{}, 0, fmt.Errorf("schedule FSRS review: %w", err)
	}
	afterRetrievability, err := service.scheduler.Retrievability(result.Card, now)
	if err != nil {
		return storage.LearningCard{}, 0, fmt.Errorf("calculate next FSRS retrievability: %w", err)
	}

	next := fromFSRSCard(card, result.Card)
	next.Retrievability = afterRetrievability
	next.LastRating = rating
	if rating == domain.ReviewRatingAgain {
		next.ConsecutiveFailures = card.ConsecutiveFailures + 1
	} else {
		next.ConsecutiveFailures = 0
	}
	return next, previousRetrievability, nil
}

func tutoringContent(item domain.VocabularyItem) (definition string, example string) {
	definition = strings.TrimSpace(item.CustomDescription)
	if len(item.Examples) > 0 {
		example = item.Examples[0]
	}
	if item.Lookup == nil {
		return definition, example
	}
	if item.Sense != nil {
		if definition == "" {
			definition = item.Sense.Definition.Definition
		}
		if example == "" && len(item.Sense.Definition.Examples) > 0 {
			example = item.Sense.Definition.Examples[0]
		}
		return definition, example
	}
	if definition == "" {
		if matched := contextualDefinition(item); matched != nil {
			definition = matched.Definition
			if example == "" && len(matched.Examples) > 0 {
				example = matched.Examples[0]
			}
			return definition, example
		}
	}
	for _, entry := range item.Lookup.Entries {
		for _, candidate := range entry.Definitions {
			if definition == "" {
				definition = candidate.Definition
			}
			if example == "" && len(candidate.Examples) > 0 {
				example = candidate.Examples[0]
			}
			if definition != "" && example != "" {
				return definition, example
			}
		}
	}
	return definition, example
}

func contextualDefinition(item domain.VocabularyItem) *domain.DictionaryDefinition {
	contextParts := make([]string, 0, len(item.Tags)+len(item.Notes)+len(item.Examples))
	contextParts = append(contextParts, item.Tags...)
	contextParts = append(contextParts, item.Notes...)
	contextParts = append(contextParts, item.Examples...)
	contextWords := wordSet(strings.Join(contextParts, " "))
	if len(contextWords) == 0 || item.Lookup == nil {
		return nil
	}
	bestScore := 0
	var best *domain.DictionaryDefinition
	for entryIndex := range item.Lookup.Entries {
		entry := &item.Lookup.Entries[entryIndex]
		for definitionIndex := range entry.Definitions {
			definition := &entry.Definitions[definitionIndex]
			score := 0
			for word := range wordSet(definition.Definition + " " + definition.Guideword) {
				if _, found := contextWords[word]; found {
					score++
				}
			}
			if score > bestScore {
				bestScore, best = score, definition
			}
		}
	}
	return best
}

func wordSet(value string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return character < 'a' || character > 'z'
	})
	result := make(map[string]struct{}, len(words))
	for _, word := range words {
		if len(word) > 2 {
			result[word] = struct{}{}
		}
	}
	return result
}

func reviewFeedback(comment storage.ReviewComment) ReviewFeedback {
	return ReviewFeedback{
		Text:       comment.Comment,
		Rating:     comment.Rating,
		ReviewedAt: storage.TimeString(comment.ReviewedAt),
	}
}

func selectionReason(card storage.LearningCard, now time.Time) string {
	if card.FSRSState == int(fsrs.New) {
		return "new"
	}
	if card.DueAt.After(now) {
		return "early"
	}
	if isTroublesome(card) {
		return "troublesome"
	}
	if card.ConsecutiveFailures > 0 {
		return "failed"
	}
	if card.DueAt.Before(now.UTC().Truncate(24 * time.Hour)) {
		return "overdue"
	}
	return "due"
}

func isTroublesome(card storage.LearningCard) bool {
	return card.ConsecutiveFailures >= 2 || card.Lapses >= 3
}

func toFSRSCard(card storage.LearningCard) (fsrs.Card, error) {
	if card.FSRSState < int(fsrs.New) || card.FSRSState > int(fsrs.Relearning) {
		return fsrs.Card{}, fmt.Errorf("%w: invalid FSRS state %d", storage.ErrCorruptData, card.FSRSState)
	}
	return fsrs.Card{
		Due:            card.DueAt,
		Stability:      card.Stability,
		Difficulty:     card.Difficulty,
		ScheduledDays:  card.ScheduledDays,
		Reps:           card.Repetitions,
		Lapses:         card.Lapses,
		State:          fsrs.State(card.FSRSState),
		LastReview:     card.LastReviewAt,
		RemainingSteps: card.RemainingSteps,
	}, nil
}

func fromFSRSCard(existing storage.LearningCard, card fsrs.Card) storage.LearningCard {
	return storage.LearningCard{
		CardID:              existing.CardID,
		VocabularyItemID:    existing.VocabularyItemID,
		ExerciseMode:        existing.ExerciseMode,
		ReviewToken:         existing.ReviewToken,
		DueAt:               card.Due.UTC(),
		Stability:           card.Stability,
		Difficulty:          card.Difficulty,
		ScheduledDays:       card.ScheduledDays,
		Repetitions:         card.Reps,
		Lapses:              card.Lapses,
		FSRSState:           int(card.State),
		LastReviewAt:        card.LastReview.UTC(),
		RemainingSteps:      card.RemainingSteps,
		LastRating:          existing.LastRating,
		ConsecutiveFailures: existing.ConsecutiveFailures,
	}
}

func toFSRSRating(rating domain.ReviewRating) fsrs.Rating {
	switch rating {
	case domain.ReviewRatingAgain:
		return fsrs.Again
	case domain.ReviewRatingHard:
		return fsrs.Hard
	case domain.ReviewRatingGood:
		return fsrs.Good
	case domain.ReviewRatingEasy:
		return fsrs.Easy
	default:
		return fsrs.Manual
	}
}
