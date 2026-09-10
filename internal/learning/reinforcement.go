package learning

import (
	"context"
	"errors"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/storage"
)

type ReinforcementPractice struct {
	ReviewCount    int                 `json:"reviewCount"`
	Difficulty     float64             `json:"difficulty"`
	LastRating     domain.ReviewRating `json:"lastRating,omitempty"`
	LastReviewedAt string              `json:"lastReviewedAt,omitempty"`
}

type ReinforcementNextResult struct {
	ItemID               string                  `json:"itemId"`
	ReviewToken          string                  `json:"reviewToken"`
	Term                 string                  `json:"term"`
	Usefulness           domain.Usefulness       `json:"usefulness"`
	PersonalInterest     domain.PersonalInterest `json:"personalInterest"`
	Definition           string                  `json:"definition,omitempty"`
	Context              string                  `json:"context,omitempty"`
	Example              string                  `json:"example,omitempty"`
	Comments             []ReviewFeedback        `json:"comments"`
	CommentCount         int                     `json:"commentCount"`
	SelectionProbability float64                 `json:"selectionProbability"`
	EligibleWordCount    int                     `json:"eligibleWordCount"`
	Practice             ReinforcementPractice   `json:"practice"`
}

type ReinforcementReviewResult struct {
	Recorded  bool                  `json:"recorded"`
	Duplicate bool                  `json:"duplicate"`
	Practice  ReinforcementPractice `json:"practice"`
}

func (service *Service) ReinforcementNext(ctx context.Context) (ReinforcementNextResult, error) {
	candidate, err := service.store.NextReinforcementItem(ctx, service.ownerKey, service.now().UTC())
	if errors.Is(err, storage.ErrNotFound) {
		return ReinforcementNextResult{}, apperr.New(apperr.NotFound, "no learned vocabulary is available for reinforcement")
	}
	if errors.Is(err, storage.ErrReinforcementShortage) {
		return ReinforcementNextResult{}, apperr.New(apperr.InvalidArgument, "reinforcement requires at least four distinct learned words outside the six-hour cooldown to keep each word's probability at or below 25 percent; wait for cooldowns to expire or add more learned vocabulary")
	}
	if err != nil {
		return ReinforcementNextResult{}, apperr.Wrap(apperr.InternalError, "failed to select reinforcement vocabulary", err)
	}
	definition, example := tutoringContent(candidate.Vocabulary)
	comments := make([]ReviewFeedback, 0, len(candidate.Comments))
	for _, comment := range candidate.Comments {
		comments = append(comments, reviewFeedback(comment))
	}
	return ReinforcementNextResult{
		ItemID:               candidate.Vocabulary.ItemID,
		ReviewToken:          candidate.ReviewToken,
		Term:                 candidate.Vocabulary.Term,
		Usefulness:           candidate.Vocabulary.Usefulness,
		PersonalInterest:     candidate.Vocabulary.PersonalInterest,
		Definition:           definition,
		Context:              candidate.Vocabulary.Context,
		Example:              example,
		Comments:             comments,
		CommentCount:         len(comments),
		SelectionProbability: candidate.SelectionProbability,
		EligibleWordCount:    candidate.EligibleWordCount,
		Practice:             reinforcementPractice(candidate.Practice),
	}, nil
}

func (service *Service) ReinforcementReview(ctx context.Context, options RecordOptions) (ReinforcementReviewResult, error) {
	options, err := validateRecordOptions(options)
	if err != nil {
		return ReinforcementReviewResult{}, err
	}
	practice, duplicate, err := service.store.RecordReinforcementReview(ctx, storage.RecordReviewInput{
		OwnerKey:    service.ownerKey,
		ReviewToken: options.ReviewToken,
		Rating:      options.Rating,
		Comment:     options.Comment,
		Now:         service.now().UTC(),
	})
	if errors.Is(err, storage.ErrNotFound) {
		return ReinforcementReviewResult{}, apperr.New(apperr.NotFound, "the reinforcement review token is invalid or its vocabulary was deleted")
	}
	if errors.Is(err, storage.ErrNotLearned) {
		return ReinforcementReviewResult{}, apperr.New(apperr.InvalidArgument, "only learned vocabulary can receive reinforcement reviews")
	}
	if errors.Is(err, storage.ErrIdempotencyConflict) {
		return ReinforcementReviewResult{}, apperr.New(apperr.InvalidArgument, "reviewToken was already used with different review data")
	}
	if err != nil {
		return ReinforcementReviewResult{}, apperr.Wrap(apperr.InternalError, "failed to record reinforcement review", err)
	}
	return ReinforcementReviewResult{Recorded: true, Duplicate: duplicate, Practice: reinforcementPractice(practice)}, nil
}

func reinforcementPractice(practice storage.ReinforcementPractice) ReinforcementPractice {
	result := ReinforcementPractice{
		ReviewCount: practice.ReviewCount,
		Difficulty:  practice.Difficulty,
		LastRating:  practice.LastRating,
	}
	if !practice.LastReviewedAt.IsZero() {
		result.LastReviewedAt = storage.TimeString(practice.LastReviewedAt)
	}
	return result
}
