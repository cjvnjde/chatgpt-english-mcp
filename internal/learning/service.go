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
	NextLearningItem(ctx context.Context, ownerKey string, clock func() time.Time) (storage.LearningCandidate, error)
	NextReinforcementItem(ctx context.Context, ownerKey string, clock func() time.Time) (storage.ReinforcementCandidate, error)
	RecordReinforcementReview(ctx context.Context, input storage.RecordReviewInput) (storage.ReinforcementPractice, bool, error)
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
	ItemID           string                  `json:"itemId"`
	PresentationID   int64                   `json:"presentationId"`
	ShownAt          string                  `json:"shownAt"`
	ReviewToken      string                  `json:"reviewToken"`
	Term             string                  `json:"term"`
	Context          string                  `json:"context,omitempty"`
	Usefulness       domain.Usefulness       `json:"usefulness"`
	PersonalInterest domain.PersonalInterest `json:"personalInterest"`
	Definition       string                  `json:"definition,omitempty"`
	Example          string                  `json:"example,omitempty"`
	Reason           string                  `json:"reason"`
	Troublesome      bool                    `json:"troublesome"`
	LatestComment    *ReviewFeedback         `json:"latestComment,omitempty"`
	Comments         []ReviewFeedback        `json:"comments,omitempty"`
}

type RecordOptions struct {
	ReviewToken string
	Rating      domain.ReviewRating
	Comment     string
}

type RecordResult struct {
	Recorded        bool                `json:"recorded"`
	Duplicate       bool                `json:"duplicate"`
	NextReviewAt    string              `json:"nextReviewAt"`
	Troublesome     bool                `json:"troublesome"`
	EffectiveRating domain.ReviewRating `json:"effectiveRating"`
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
	candidate, err := service.store.NextLearningItem(ctx, service.ownerKey, service.now)
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
		ItemID:           candidate.Vocabulary.ItemID,
		PresentationID:   candidate.PresentationID,
		ShownAt:          storage.TimeString(candidate.ShownAt),
		ReviewToken:      candidate.Card.ReviewToken,
		Term:             candidate.Vocabulary.Term,
		Context:          candidate.Vocabulary.Context,
		Usefulness:       candidate.Vocabulary.Usefulness,
		PersonalInterest: candidate.Vocabulary.PersonalInterest,
		Definition:       definition,
		Example:          example,
		Reason:           selectionReason(candidate.Card, candidate.ShownAt),
		Troublesome:      isTroublesome(candidate.Card),
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
	options, err := validateRecordOptions(options)
	if err != nil {
		return RecordResult{}, err
	}

	attempt, duplicate, err := service.store.RecordReview(ctx, storage.RecordReviewInput{
		OwnerKey:    service.ownerKey,
		ReviewToken: options.ReviewToken,
		Rating:      options.Rating,
		Comment:     options.Comment,
		Now:         service.now,
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
		Recorded:        true,
		Duplicate:       duplicate,
		NextReviewAt:    storage.TimeString(attempt.After.DueAt),
		Troublesome:     isTroublesome(attempt.After),
		EffectiveRating: attempt.After.LastRating,
	}, nil
}

func validateRecordOptions(options RecordOptions) (RecordOptions, error) {
	options.ReviewToken = strings.TrimSpace(options.ReviewToken)
	tokenLength := utf8.RuneCountInString(options.ReviewToken)
	if tokenLength == 0 || tokenLength > maximumReviewTokenRunes {
		return RecordOptions{}, apperr.New(apperr.InvalidArgument, "reviewToken must contain 1 to 200 Unicode characters")
	}
	if !options.Rating.Valid() {
		return RecordOptions{}, apperr.New(apperr.InvalidArgument, "rating must be again, hard, good, or easy")
	}
	options.Comment = strings.TrimSpace(options.Comment)
	if utf8.RuneCountInString(options.Comment) > maximumCommentRunes {
		return RecordOptions{}, apperr.New(apperr.InvalidArgument, "comment must contain at most 1000 Unicode characters")
	}
	return options, nil
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
	if item.Sense != nil {
		if definition == "" {
			definition = item.Sense.Definition.Definition
		}
		if example == "" && len(item.Sense.Definition.Examples) > 0 {
			example = item.Sense.Definition.Examples[0]
		}
		return definition, example
	}
	if item.Lookup == nil || definition != "" {
		return definition, example
	}
	if matched, hasContext := contextualDefinition(item); hasContext {
		if matched != nil {
			definition = matched.Definition
			if example == "" && len(matched.Examples) > 0 {
				example = matched.Examples[0]
			}
		}
		// Context without a unique match requires clarification, not an
		// unrelated first dictionary definition.
		return definition, example
	}
	for _, entry := range item.Lookup.Entries {
		for _, candidate := range entry.Definitions {
			if strings.TrimSpace(candidate.Definition) == "" {
				continue
			}
			definition = candidate.Definition
			if example == "" && len(candidate.Examples) > 0 {
				example = candidate.Examples[0]
			}
			return definition, example
		}
	}
	return definition, example
}

func contextualDefinition(item domain.VocabularyItem) (*domain.DictionaryDefinition, bool) {
	hasContext := strings.TrimSpace(item.Context) != "" || len(item.Tags)+len(item.Notes)+len(item.Examples) > 0
	if !hasContext || item.Lookup == nil {
		return nil, hasContext
	}
	contextParts := make([]string, 0, 1+len(item.Tags)+len(item.Notes)+len(item.Examples))
	contextParts = append(contextParts, item.Context)
	contextParts = append(contextParts, item.Tags...)
	contextParts = append(contextParts, item.Notes...)
	contextParts = append(contextParts, item.Examples...)
	contextWords := wordSet(strings.Join(contextParts, " "))
	for word := range strings.FieldsFuncSeq(item.NormalizedTerm, func(character rune) bool {
		return character < 'a' || character > 'z'
	}) {
		delete(contextWords, word)
	}
	if len(contextWords) == 0 {
		return nil, true
	}
	bestScore := 0
	var best *domain.DictionaryDefinition
	ambiguous := false
	for entryIndex := range item.Lookup.Entries {
		entry := &item.Lookup.Entries[entryIndex]
		for definitionIndex := range entry.Definitions {
			definition := &entry.Definitions[definitionIndex]
			if strings.TrimSpace(definition.Definition) == "" {
				continue
			}
			score := 0
			for word := range wordSet(definition.Definition + " " + definition.Guideword) {
				if _, found := contextWords[word]; found {
					score++
				}
			}
			if score > bestScore {
				bestScore, best = score, definition
				ambiguous = false
			} else if score > 0 && score == bestScore {
				ambiguous = true
			}
		}
	}
	if ambiguous {
		return nil, true
	}
	return best, true
}

func wordSet(value string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return character < 'a' || character > 'z'
	})
	result := make(map[string]struct{}, len(words))
	for _, word := range words {
		if len(word) <= 2 {
			continue
		}
		// Function words identify sentence structure, not dictionary senses.
		switch word {
		case "about", "above", "after", "again", "against", "all", "also", "among",
			"any", "are", "around", "because", "been", "before", "being", "below",
			"beside", "between", "both", "but", "can", "could", "did", "does",
			"doing", "done", "during", "each", "for", "from", "had", "has", "have",
			"having", "her", "here", "hers", "herself", "him", "himself", "his",
			"how", "into", "its", "itself", "just", "may", "might", "more", "most",
			"must", "off", "once", "one", "only", "other", "our", "ours", "ourselves",
			"out", "over", "own", "same", "shall", "she", "should", "since", "some",
			"such", "than", "that", "the", "their", "theirs", "them", "themselves",
			"then", "there", "these", "they", "this", "those", "through", "too",
			"under", "until", "upon", "very", "was", "were", "what", "when",
			"where", "whether", "which", "while", "who", "whom", "whose", "why",
			"will", "with", "within", "would", "you", "your", "yours", "yourself",
			"yourselves":
			continue
		}
		result[word] = struct{}{}
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
