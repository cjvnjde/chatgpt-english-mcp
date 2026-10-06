package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

const productionExerciseMode = domain.ExerciseModeProduction

var ErrReviewReinforced = errors.New("review cannot be corrected after newer reinforcement feedback")

// LearningCard stores the FSRS state for one vocabulary item and exercise mode.
type LearningCard struct {
	CardID              string
	VocabularyItemID    string
	ExerciseMode        domain.ExerciseMode
	ReviewToken         string
	DueAt               time.Time
	Stability           float64
	Difficulty          float64
	Retrievability      float64
	ScheduledDays       uint64
	Repetitions         uint64
	Lapses              uint64
	FSRSState           int
	LastReviewAt        time.Time
	RemainingSteps      int
	LastRating          domain.ReviewRating
	ConsecutiveFailures uint64
	MasteryStreak       uint64
	LastMasteryAt       time.Time
}

// LearningCandidate combines separately persisted content and scheduling state.
type LearningCandidate struct {
	Focus          *LearningFocus
	IdleReason     string
	Vocabulary     domain.VocabularyItem
	Card           LearningCard
	PresentationID int64
	ShownAt        time.Time
	Settings       settings.Values
}

// ReviewComment describes feedback saved with an accepted review attempt.
type ReviewComment struct {
	Comment    string
	Rating     domain.ReviewRating
	ReviewedAt time.Time
}

// ReviewAttempt is the current result of one accepted review submission.
type ReviewAttempt struct {
	ReviewID               string
	ReviewToken            string
	VocabularyItemID       string
	LearningCardID         string
	ExerciseMode           domain.ExerciseMode
	Rating                 domain.ReviewRating
	Comment                string
	ReviewedAt             time.Time
	PreviousDueAt          time.Time
	PreviousRetrievability float64
	After                  LearningCard
	StatusAfter            domain.LearningStatus
	statusBefore           domain.LearningStatus
	reinforcementCount     int
	requestHash            string
	Settings               settings.Values
}

type RecordReviewInput struct {
	OwnerKey    string
	ReviewToken string
	Rating      domain.ReviewRating
	Comment     string
	Now         func() time.Time
}

type UpdateReviewInput struct {
	OwnerKey    string
	ReviewToken string
	Rating      domain.ReviewRating
	Comment     *string
	Now         func() time.Time
}

// ScheduleReview applies the final grade using the transaction's settings snapshot.
type ScheduleReview func(card LearningCard, now time.Time, rating domain.ReviewRating, values settings.Values) (LearningCard, float64, error)

func (db *DB) NextLearningItem(ctx context.Context, ownerKey string, clock func() time.Time) (LearningCandidate, error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("begin presentation transaction: %w", err)
	}
	defer transaction.Rollback()

	configuration, err := algorithmSettings(ctx, transaction, ownerKey)
	if err != nil {
		return LearningCandidate{}, err
	}
	shownAt := clock().UTC()
	cards, recentSinceID, err := loadSelectionCards(ctx, transaction, ownerKey, configuration.Values)
	if err != nil {
		return LearningCandidate{}, err
	}
	var focus *LearningFocus
	if configuration.Values.LearningMode == "focused" {
		batch, err := prepareLearningFocus(ctx, transaction, ownerKey, configuration.Values, shownAt, true)
		if err != nil {
			return LearningCandidate{}, err
		}
		focus = &batch
		restrictToFocus(cards, batch)
	}
	selected, ok := selectLearningCard(cards, recentSinceID, shownAt, rand.Float64, configuration.Values)
	if !ok {
		if focus != nil {
			return LearningCandidate{Focus: focus, IdleReason: "complete", Settings: configuration.Values}, transaction.Commit()
		}
		return LearningCandidate{}, ErrNotFound
	}
	card, err := scanLearningCard(transaction.QueryRowContext(ctx, `
		SELECT `+learningCardColumns+`
		FROM learning_cards card
		WHERE card.id = ?
	`, selected.cardID))
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("load selected learning card: %w", err)
	}
	item, err := scanVocabularyItem(transaction.QueryRowContext(
		ctx, vocabularySelect+" WHERE v.owner_key = ? AND v.id = ?", ownerKey, card.VocabularyItemID,
	))
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("load selected vocabulary item: %w", err)
	}

	selectionKind := "early"
	if card.FSRSState == 0 {
		selectionKind = "new"
	} else if !card.DueAt.After(shownAt) {
		selectionKind = "due"
	}
	result, err := transaction.ExecContext(ctx, `
		INSERT INTO learning_presentations (
			owner_key, vocabulary_item_id, learning_card_id, exercise_mode,
			review_token, shown_at, due_at, selection_kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, ownerKey, card.VocabularyItemID, card.CardID, card.ExerciseMode,
		card.ReviewToken, TimeString(shownAt), TimeString(card.DueAt), selectionKind)
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("record learning presentation: %w", err)
	}
	presentationID, err := result.LastInsertId()
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("read learning presentation ID: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return LearningCandidate{}, fmt.Errorf("commit presentation transaction: %w", err)
	}

	return LearningCandidate{
		Focus:      focus,
		Vocabulary: item, Card: card, PresentationID: presentationID, ShownAt: shownAt,
		Settings: configuration.Values,
	}, nil
}

func (db *DB) ReviewComments(
	ctx context.Context,
	ownerKey string,
	vocabularyID string,
	includeAll bool,
) ([]ReviewComment, error) {
	query := `
		SELECT comment, rating, reviewed_at
		FROM review_attempts
		WHERE owner_key = ? AND vocabulary_item_id = ? AND comment <> ''
		ORDER BY rtrim(reviewed_at, 'Z') DESC, id DESC`
	if !includeAll {
		query += " LIMIT 1"
	}
	rows, err := db.sql.QueryContext(ctx, query, ownerKey, vocabularyID)
	if err != nil {
		return nil, fmt.Errorf("list review comments: %w", err)
	}
	defer rows.Close()

	comments := make([]ReviewComment, 0)
	for rows.Next() {
		var comment ReviewComment
		var reviewedAt string
		if err := rows.Scan(&comment.Comment, &comment.Rating, &reviewedAt); err != nil {
			return nil, fmt.Errorf("scan review comment: %w", err)
		}
		comment.ReviewedAt, err = parseStoredTime(reviewedAt, "review comment date")
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate review comments: %w", err)
	}
	return comments, nil
}

func (db *DB) RecordReview(
	ctx context.Context,
	input RecordReviewInput,
	schedule ScheduleReview,
) (attempt ReviewAttempt, duplicate bool, err error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("begin review transaction: %w", err)
	}
	defer transaction.Rollback()

	configuration, err := algorithmSettings(ctx, transaction, input.OwnerKey)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	requestHash := reviewRequestHash(input.Rating, input.Comment)
	existing, err := reviewAttemptByToken(ctx, transaction, input.OwnerKey, input.ReviewToken)
	if err == nil {
		if existing.requestHash != requestHash {
			return ReviewAttempt{}, false, ErrIdempotencyConflict
		}
		existing.Settings = configuration.Values
		return existing, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReviewAttempt{}, false, fmt.Errorf("read existing review token: %w", err)
	}

	card, status, err := learningCardForReview(ctx, transaction, input.OwnerKey, input.ReviewToken)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewAttempt{}, false, ErrNotFound
	}
	if err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("read learning card: %w", err)
	}
	if status == domain.LearningStatusArchived {
		return ReviewAttempt{}, false, ErrArchived
	}

	now := input.Now().UTC()
	rating, err := timedReviewRating(ctx, transaction, input, card.CardID, now, configuration.Values.FastAnswerSeconds)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	next, previousRetrievability, err := schedule(card, now, rating, configuration.Values)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	applyMasteryReview(&next, card, now, rating)
	reinforcementCount, err := reinforcementReviewCount(ctx, transaction, card.VocabularyItemID)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	next.CardID = card.CardID
	next.VocabularyItemID = card.VocabularyItemID
	next.ExerciseMode = card.ExerciseMode
	next.ReviewToken, err = NewID()
	if err != nil {
		return ReviewAttempt{}, false, err
	}

	reviewID, err := NewID()
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	attempt = ReviewAttempt{
		ReviewID:               reviewID,
		ReviewToken:            input.ReviewToken,
		VocabularyItemID:       card.VocabularyItemID,
		LearningCardID:         card.CardID,
		ExerciseMode:           card.ExerciseMode,
		Rating:                 rating,
		Comment:                input.Comment,
		ReviewedAt:             now,
		PreviousDueAt:          card.DueAt,
		PreviousRetrievability: previousRetrievability,
		After:                  next,
		StatusAfter:            reviewStatusAfter(status, next, rating, configuration.Values),
		statusBefore:           status,
		reinforcementCount:     reinforcementCount,
		requestHash:            requestHash,
		Settings:               configuration.Values,
	}
	if err := insertReviewAttempt(ctx, transaction, input.OwnerKey, attempt, card); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := updateLearningCard(ctx, transaction, next, now); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := updateReviewStatus(ctx, transaction, card.VocabularyItemID, attempt.StatusAfter, now); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("commit review transaction: %w", err)
	}

	return attempt, false, nil
}

// UpdateLatestReview corrects a repetition in place, never advancing the review
// clock or consuming the card's pending token.
func (db *DB) UpdateLatestReview(
	ctx context.Context,
	input UpdateReviewInput,
	schedule ScheduleReview,
) (ReviewAttempt, bool, error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("begin review correction transaction: %w", err)
	}
	defer transaction.Rollback()
	configuration, err := algorithmSettings(ctx, transaction, input.OwnerKey)
	if err != nil {
		return ReviewAttempt{}, false, err
	}

	attempt, err := reviewAttemptByToken(ctx, transaction, input.OwnerKey, input.ReviewToken)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewAttempt{}, false, ErrNotFound
	}
	if err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("read review to correct: %w", err)
	}
	var latestID string
	if err := transaction.QueryRowContext(ctx, `
		SELECT id FROM review_attempts WHERE owner_key = ? ORDER BY rowid DESC LIMIT 1
	`, input.OwnerKey).Scan(&latestID); err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("read latest review: %w", err)
	}
	if attempt.ReviewID != latestID {
		return ReviewAttempt{}, false, ErrNotLatestReview
	}

	var status domain.LearningStatus
	current, err := scanLearningCardWithStatus(transaction.QueryRowContext(ctx, `
		SELECT `+learningCardColumns+`, vocabulary.learning_status
		FROM learning_cards card
		JOIN vocabulary_items vocabulary ON vocabulary.id = card.vocabulary_item_id
		WHERE vocabulary.owner_key = ? AND card.id = ?
			AND card.vocabulary_item_id = ? AND card.exercise_mode = ?
	`, input.OwnerKey, attempt.LearningCardID, attempt.VocabularyItemID, attempt.ExerciseMode), &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ReviewAttempt{}, false, ErrNotFound
	}
	if err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("read correction learning card: %w", err)
	}
	if status == domain.LearningStatusArchived {
		return ReviewAttempt{}, false, ErrArchived
	}
	comment := attempt.Comment
	if input.Comment != nil {
		comment = *input.Comment
	}
	attempt.After.ReviewToken = current.ReviewToken
	attempt.Settings = configuration.Values
	if attempt.Rating == input.Rating && attempt.Comment == comment {
		return attempt, true, nil
	}
	reinforcementCount, err := reinforcementReviewCount(ctx, transaction, attempt.VocabularyItemID)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	if reinforcementCount != attempt.reinforcementCount {
		return ReviewAttempt{}, false, ErrReviewReinforced
	}

	before, err := reviewCardBefore(ctx, transaction, input.OwnerKey, attempt)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	next, _, err := schedule(before, attempt.ReviewedAt, input.Rating, configuration.Values)
	if err != nil {
		return ReviewAttempt{}, false, err
	}
	applyMasteryReview(&next, before, attempt.ReviewedAt, input.Rating)
	if attempt.statusBefore == "" {
		// Legacy history has no trustworthy before-status or mastery evidence.
		next.MasteryStreak, next.LastMasteryAt = 0, time.Time{}
		attempt.StatusAfter = status
	} else {
		attempt.StatusAfter = reviewStatusAfter(attempt.statusBefore, next, input.Rating, configuration.Values)
	}
	next.CardID = current.CardID
	next.VocabularyItemID = current.VocabularyItemID
	next.ExerciseMode = current.ExerciseMode
	next.ReviewToken = current.ReviewToken
	attempt.Rating = input.Rating
	attempt.Comment = comment
	attempt.requestHash = reviewRequestHash(input.Rating, comment)
	attempt.After = next
	if err := updateReviewAttempt(ctx, transaction, attempt); err != nil {
		return ReviewAttempt{}, false, err
	}
	now := input.Now().UTC()
	if err := updateLearningCard(ctx, transaction, next, now); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := updateReviewStatus(ctx, transaction, attempt.VocabularyItemID, attempt.StatusAfter, now); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("commit review correction transaction: %w", err)
	}
	return attempt, false, nil
}

func reviewCardBefore(ctx context.Context, transaction *sql.Tx, ownerKey string, attempt ReviewAttempt) (LearningCard, error) {
	var lastReviewAt sql.NullString
	var lastMasteryAt sql.NullString
	before := LearningCard{
		CardID: attempt.LearningCardID, VocabularyItemID: attempt.VocabularyItemID,
		ExerciseMode: attempt.ExerciseMode, ReviewToken: attempt.ReviewToken,
		DueAt: attempt.PreviousDueAt, Retrievability: attempt.PreviousRetrievability,
	}
	if err := transaction.QueryRowContext(ctx, `
		SELECT stability_before, difficulty_before, scheduled_days_before,
			repetitions_before, lapses_before, fsrs_state_before,
			remaining_steps_before, consecutive_failures_before, last_review_at_before,
			mastery_streak_before, last_mastery_at_before,
			COALESCE((SELECT previous.rating
				FROM review_attempts previous
				WHERE previous.owner_key = review.owner_key
					AND previous.learning_card_id = review.learning_card_id
					AND previous.rowid < review.rowid
				ORDER BY previous.rowid DESC LIMIT 1), '')
		FROM review_attempts review WHERE owner_key = ? AND id = ?
	`, ownerKey, attempt.ReviewID).Scan(
		&before.Stability, &before.Difficulty, &before.ScheduledDays,
		&before.Repetitions, &before.Lapses, &before.FSRSState,
		&before.RemainingSteps, &before.ConsecutiveFailures, &lastReviewAt,
		&before.MasteryStreak, &lastMasteryAt, &before.LastRating,
	); err != nil {
		return LearningCard{}, fmt.Errorf("read pre-review snapshot: %w", err)
	}
	if lastReviewAt.Valid {
		var err error
		before.LastReviewAt, err = parseStoredTime(lastReviewAt.String, "pre-review date")
		if err != nil {
			return LearningCard{}, err
		}
	} else if before.Repetitions > 0 {
		return LearningCard{}, fmt.Errorf("%w: pre-review date is unavailable", ErrCorruptData)
	}
	if lastMasteryAt.Valid {
		var err error
		before.LastMasteryAt, err = parseStoredTime(lastMasteryAt.String, "pre-review mastery date")
		if err != nil {
			return LearningCard{}, err
		}
	}
	return before, nil
}

func updateReviewAttempt(ctx context.Context, transaction *sql.Tx, attempt ReviewAttempt) error {
	_, err := transaction.ExecContext(ctx, `
		UPDATE review_attempts SET rating = ?, request_hash = ?, comment = ?,
			due_after = ?, stability_after = ?, difficulty_after = ?, retrievability_after = ?,
			scheduled_days_after = ?, repetitions_after = ?, lapses_after = ?, fsrs_state_after = ?,
			remaining_steps_after = ?, consecutive_failures_after = ?, status_after = ?,
			mastery_streak_after = ?, last_mastery_at_after = ?
		WHERE id = ?
	`, attempt.Rating, attempt.requestHash, attempt.Comment,
		TimeString(attempt.After.DueAt), attempt.After.Stability, attempt.After.Difficulty, attempt.After.Retrievability,
		attempt.After.ScheduledDays, attempt.After.Repetitions, attempt.After.Lapses, attempt.After.FSRSState,
		attempt.After.RemainingSteps, attempt.After.ConsecutiveFailures, attempt.StatusAfter,
		attempt.After.MasteryStreak, nullableReviewTime(attempt.After.LastMasteryAt), attempt.ReviewID)
	if err != nil {
		return fmt.Errorf("update review attempt: %w", err)
	}
	return nil
}

const learningCardColumns = `
	card.id,
	card.vocabulary_item_id,
	card.exercise_mode,
	card.due_at,
	card.stability,
	card.difficulty,
	card.retrievability,
	card.scheduled_days,
	card.repetitions,
	card.lapses,
	card.fsrs_state,
	card.last_review_at,
	card.remaining_steps,
	card.last_rating,
	card.consecutive_failures,
	card.review_token,
	card.mastery_streak,
	card.last_mastery_at`

func learningCardForReview(
	ctx context.Context,
	transaction *sql.Tx,
	ownerKey string,
	reviewToken string,
) (LearningCard, domain.LearningStatus, error) {
	row := transaction.QueryRowContext(ctx, `
		SELECT `+learningCardColumns+`, vocabulary.learning_status
		FROM learning_cards card
		JOIN vocabulary_items vocabulary ON vocabulary.id = card.vocabulary_item_id
		WHERE vocabulary.owner_key = ? AND card.review_token = ?
	`, ownerKey, reviewToken)

	var status domain.LearningStatus
	card, err := scanLearningCardWithStatus(row, &status)
	return card, status, err
}

func scanLearningCard(scanner rowScanner) (LearningCard, error) {
	return scanLearningCardWithStatus(scanner, nil)
}

func scanLearningCardWithStatus(scanner rowScanner, status *domain.LearningStatus) (LearningCard, error) {
	var card LearningCard
	var dueAt string
	var lastReviewAt sql.NullString
	var lastRating sql.NullString
	var reviewToken sql.NullString
	var lastMasteryAt sql.NullString
	arguments := []any{
		&card.CardID,
		&card.VocabularyItemID,
		&card.ExerciseMode,
		&dueAt,
		&card.Stability,
		&card.Difficulty,
		&card.Retrievability,
		&card.ScheduledDays,
		&card.Repetitions,
		&card.Lapses,
		&card.FSRSState,
		&lastReviewAt,
		&card.RemainingSteps,
		&lastRating,
		&card.ConsecutiveFailures,
		&reviewToken,
		&card.MasteryStreak,
		&lastMasteryAt,
	}
	if status != nil {
		arguments = append(arguments, status)
	}
	if err := scanner.Scan(arguments...); err != nil {
		return LearningCard{}, err
	}
	if !reviewToken.Valid || reviewToken.String == "" {
		return LearningCard{}, fmt.Errorf("%w: learning card has no review token", ErrCorruptData)
	}
	card.ReviewToken = reviewToken.String

	var err error
	card.DueAt, err = parseStoredTime(dueAt, "learning due date")
	if err != nil {
		return LearningCard{}, err
	}
	if lastReviewAt.Valid {
		card.LastReviewAt, err = parseStoredTime(lastReviewAt.String, "last review date")
		if err != nil {
			return LearningCard{}, err
		}
	}
	if lastRating.Valid {
		card.LastRating = domain.ReviewRating(lastRating.String)
	}
	if lastMasteryAt.Valid {
		card.LastMasteryAt, err = parseStoredTime(lastMasteryAt.String, "last mastery date")
		if err != nil {
			return LearningCard{}, err
		}
	}
	return card, nil
}

func reviewAttemptByToken(
	ctx context.Context,
	transaction *sql.Tx,
	ownerKey string,
	reviewToken string,
) (ReviewAttempt, error) {
	row := transaction.QueryRowContext(ctx, `
		SELECT
			id,
			submission_id,
			vocabulary_item_id,
			learning_card_id,
			exercise_mode,
			rating,
			request_hash,
			comment,
			reviewed_at,
			due_before,
			retrievability_before,
			due_after,
			stability_after,
			difficulty_after,
			retrievability_after,
			scheduled_days_after,
			repetitions_after,
			lapses_after,
			fsrs_state_after,
			remaining_steps_after,
			consecutive_failures_after,
			COALESCE(status_before, ''), status_after, reinforcement_count_before,
			mastery_streak_after, last_mastery_at_after
		FROM review_attempts
		WHERE owner_key = ? AND submission_id = ?
	`, ownerKey, reviewToken)

	var attempt ReviewAttempt
	var reviewedAt string
	var previousDueAt string
	var dueAfter string
	var lastMasteryAt sql.NullString
	if err := row.Scan(
		&attempt.ReviewID,
		&attempt.ReviewToken,
		&attempt.VocabularyItemID,
		&attempt.LearningCardID,
		&attempt.ExerciseMode,
		&attempt.Rating,
		&attempt.requestHash,
		&attempt.Comment,
		&reviewedAt,
		&previousDueAt,
		&attempt.PreviousRetrievability,
		&dueAfter,
		&attempt.After.Stability,
		&attempt.After.Difficulty,
		&attempt.After.Retrievability,
		&attempt.After.ScheduledDays,
		&attempt.After.Repetitions,
		&attempt.After.Lapses,
		&attempt.After.FSRSState,
		&attempt.After.RemainingSteps,
		&attempt.After.ConsecutiveFailures,
		&attempt.statusBefore, &attempt.StatusAfter, &attempt.reinforcementCount,
		&attempt.After.MasteryStreak, &lastMasteryAt,
	); err != nil {
		return ReviewAttempt{}, err
	}

	if lastMasteryAt.Valid {
		var err error
		attempt.After.LastMasteryAt, err = parseStoredTime(lastMasteryAt.String, "review mastery date")
		if err != nil {
			return ReviewAttempt{}, err
		}
	}
	var err error
	attempt.ReviewedAt, err = parseStoredTime(reviewedAt, "review date")
	if err != nil {
		return ReviewAttempt{}, err
	}
	attempt.PreviousDueAt, err = parseStoredTime(previousDueAt, "previous due date")
	if err != nil {
		return ReviewAttempt{}, err
	}
	attempt.After.DueAt, err = parseStoredTime(dueAfter, "next due date")
	if err != nil {
		return ReviewAttempt{}, err
	}
	attempt.After.CardID = attempt.LearningCardID
	attempt.After.VocabularyItemID = attempt.VocabularyItemID
	attempt.After.ExerciseMode = attempt.ExerciseMode
	attempt.After.LastReviewAt = attempt.ReviewedAt
	attempt.After.LastRating = attempt.Rating

	return attempt, nil
}

func insertReviewAttempt(
	ctx context.Context,
	transaction *sql.Tx,
	ownerKey string,
	attempt ReviewAttempt,
	before LearningCard,
) error {
	var lastReviewAt any
	if !before.LastReviewAt.IsZero() {
		lastReviewAt = TimeString(before.LastReviewAt)
	}
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO review_attempts(
			id, owner_key, submission_id, vocabulary_item_id, learning_card_id,
			exercise_mode, rating, request_hash, comment, reviewed_at,
			due_before, stability_before, difficulty_before, retrievability_before,
			scheduled_days_before, repetitions_before, lapses_before, fsrs_state_before,
			remaining_steps_before, consecutive_failures_before, last_review_at_before,
			due_after, stability_after, difficulty_after, retrievability_after,
			scheduled_days_after, repetitions_after, lapses_after, fsrs_state_after,
			remaining_steps_after, consecutive_failures_after,
			status_before, status_after, reinforcement_count_before,
			mastery_streak_before, last_mastery_at_before, mastery_streak_after, last_mastery_at_after
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		attempt.ReviewID,
		ownerKey,
		attempt.ReviewToken,
		attempt.VocabularyItemID,
		attempt.LearningCardID,
		attempt.ExerciseMode,
		attempt.Rating,
		attempt.requestHash,
		attempt.Comment,
		TimeString(attempt.ReviewedAt),
		TimeString(before.DueAt),
		before.Stability,
		before.Difficulty,
		attempt.PreviousRetrievability,
		before.ScheduledDays,
		before.Repetitions,
		before.Lapses,
		before.FSRSState,
		before.RemainingSteps,
		before.ConsecutiveFailures,
		lastReviewAt,
		TimeString(attempt.After.DueAt),
		attempt.After.Stability,
		attempt.After.Difficulty,
		attempt.After.Retrievability,
		attempt.After.ScheduledDays,
		attempt.After.Repetitions,
		attempt.After.Lapses,
		attempt.After.FSRSState,
		attempt.After.RemainingSteps,
		attempt.After.ConsecutiveFailures,
		attempt.statusBefore, attempt.StatusAfter, attempt.reinforcementCount,
		before.MasteryStreak, nullableReviewTime(before.LastMasteryAt),
		attempt.After.MasteryStreak, nullableReviewTime(attempt.After.LastMasteryAt),
	)
	if err != nil {
		return fmt.Errorf("insert review attempt: %w", err)
	}
	return nil
}

func updateLearningCard(ctx context.Context, transaction *sql.Tx, card LearningCard, now time.Time) error {
	result, err := transaction.ExecContext(ctx, `
		UPDATE learning_cards
		SET due_at = ?,
			stability = ?,
			difficulty = ?,
			retrievability = ?,
			scheduled_days = ?,
			repetitions = ?,
			lapses = ?,
			fsrs_state = ?,
			last_review_at = ?,
			remaining_steps = ?,
			last_rating = ?,
			consecutive_failures = ?,
			review_token = ?,
			updated_at = ?,
			mastery_streak = ?,
			last_mastery_at = ?
		WHERE id = ?
	`,
		TimeString(card.DueAt),
		card.Stability,
		card.Difficulty,
		card.Retrievability,
		card.ScheduledDays,
		card.Repetitions,
		card.Lapses,
		card.FSRSState,
		TimeString(card.LastReviewAt),
		card.RemainingSteps,
		card.LastRating,
		card.ConsecutiveFailures,
		card.ReviewToken,
		TimeString(now),
		card.MasteryStreak,
		nullableReviewTime(card.LastMasteryAt),
		card.CardID,
	)
	if err != nil {
		return fmt.Errorf("update learning card: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read learning card update result: %w", err)
	}
	if rowsAffected != 1 {
		return fmt.Errorf("update learning card: %w", ErrNotFound)
	}
	return nil
}

func parseStoredTime(value string, label string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: invalid %s", ErrCorruptData, label)
	}
	return parsed.UTC(), nil
}

// Each UTC calendar day supplies at most one mastery success. A failure resets
// the streak without restoring credit already consumed that day.
func applyMasteryReview(next *LearningCard, before LearningCard, now time.Time, rating domain.ReviewRating) {
	next.MasteryStreak, next.LastMasteryAt = before.MasteryStreak, before.LastMasteryAt
	switch rating {
	case domain.ReviewRatingAgain, domain.ReviewRatingHard:
		next.MasteryStreak = 0
	case domain.ReviewRatingGood, domain.ReviewRatingEasy:
		if before.LastMasteryAt.IsZero() || now.UTC().Truncate(24*time.Hour).After(before.LastMasteryAt.UTC().Truncate(24*time.Hour)) {
			next.MasteryStreak++
			next.LastMasteryAt = now
		}
	}
}

func reviewStatusAfter(before domain.LearningStatus, next LearningCard, rating domain.ReviewRating, values settings.Values) domain.LearningStatus {
	if before == domain.LearningStatusArchived {
		return before
	}
	if rating == domain.ReviewRatingAgain {
		return domain.LearningStatusLearning
	}
	if before == domain.LearningStatusLearned {
		return before
	}
	if (rating == domain.ReviewRatingGood || rating == domain.ReviewRatingEasy) &&
		next.MasteryStreak >= uint64(values.MasteryDays) && next.FSRSState == 2 && next.ScheduledDays >= uint64(values.MasteryIntervalDays) {
		return domain.LearningStatusLearned
	}
	return domain.LearningStatusLearning
}

func reinforcementReviewCount(ctx context.Context, transaction *sql.Tx, itemID string) (int, error) {
	var count int
	err := transaction.QueryRowContext(ctx, `
		SELECT COALESCE((SELECT review_count FROM reinforcement_practice WHERE vocabulary_item_id = ?), 0)
	`, itemID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("read reinforcement correction guard: %w", err)
	}
	return count, nil
}

func updateReviewStatus(ctx context.Context, transaction *sql.Tx, itemID string, status domain.LearningStatus, now time.Time) error {
	_, err := transaction.ExecContext(ctx, `
		UPDATE vocabulary_items SET learning_status = ?, updated_at = ?
		WHERE id = ? AND learning_status <> ?
	`, status, TimeString(now), itemID, status)
	if err != nil {
		return fmt.Errorf("update reviewed vocabulary status: %w", err)
	}
	return nil
}

func nullableReviewTime(at time.Time) any {
	if at.IsZero() {
		return nil
	}
	return TimeString(at)
}

// The fingerprint preserves exact request replay without storing a second grade.
func reviewRequestHash(rating domain.ReviewRating, comment string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(string(rating)+"\x00"+comment)))
}

func timedReviewRating(ctx context.Context, tx *sql.Tx, input RecordReviewInput, cardID string, now time.Time, seconds int) (domain.ReviewRating, error) {
	if input.Rating != domain.ReviewRatingGood || seconds == 0 {
		return input.Rating, nil
	}
	var shownAt string
	err := tx.QueryRowContext(ctx, `
		SELECT shown_at FROM learning_presentations
		WHERE owner_key = ? AND review_token = ? AND learning_card_id = ?
		ORDER BY id LIMIT 1
	`, input.OwnerKey, input.ReviewToken, cardID).Scan(&shownAt)
	if errors.Is(err, sql.ErrNoRows) {
		return input.Rating, nil
	}
	if err != nil {
		return "", fmt.Errorf("read first review presentation: %w", err)
	}
	shown, err := parseStoredTime(shownAt, "first review presentation")
	if err != nil {
		return "", err
	}
	elapsed := now.Sub(shown)
	if elapsed >= 0 && elapsed < time.Duration(seconds)*time.Second {
		return domain.ReviewRatingEasy, nil
	}
	return input.Rating, nil
}
