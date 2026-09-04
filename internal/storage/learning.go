package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"english-learning-mcp/internal/domain"
)

const productionExerciseMode = domain.ExerciseModeProduction

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
}

// LearningCandidate combines separately persisted content and scheduling state.
type LearningCandidate struct {
	Vocabulary domain.VocabularyItem
	Card       LearningCard
}

// ReviewComment describes feedback saved with an immutable review attempt.
type ReviewComment struct {
	Comment    string
	Rating     domain.ReviewRating
	ReviewedAt time.Time
}

// ReviewAttempt is the immutable result of one accepted review submission.
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
}

type RecordReviewInput struct {
	OwnerKey    string
	ReviewToken string
	Rating      domain.ReviewRating
	Comment     string
	Now         time.Time
}

type ScheduleReview func(LearningCard, time.Time, domain.ReviewRating) (LearningCard, float64, error)

func (db *DB) NextLearningItem(ctx context.Context, ownerKey string, now time.Time) (LearningCandidate, error) {
	row := db.sql.QueryRowContext(ctx, `
		SELECT `+learningCardColumns+`
		FROM learning_cards card
		JOIN vocabulary_items vocabulary ON vocabulary.id = card.vocabulary_item_id
		WHERE vocabulary.owner_key = ?
		  AND vocabulary.learning_status <> 'archived'
		  AND card.exercise_mode = ?
		ORDER BY
			CASE
				WHEN card.fsrs_state <> 0 AND card.due_at <= ? AND (card.consecutive_failures > 0 OR card.lapses >= 3) THEN 0
				WHEN card.fsrs_state <> 0 AND card.due_at <= ? THEN 1
				WHEN card.fsrs_state = 0 THEN 2
				ELSE 3
			END,
			card.consecutive_failures DESC,
			card.lapses DESC,
			CASE WHEN card.fsrs_state = 0 THEN vocabulary.created_at ELSE card.due_at END ASC,
			card.id ASC
		LIMIT 1
	`, ownerKey, productionExerciseMode, TimeString(now), TimeString(now))
	card, err := scanLearningCard(row)
	if errors.Is(err, sql.ErrNoRows) {
		return LearningCandidate{}, ErrNotFound
	}
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("select next learning card: %w", err)
	}
	item, err := db.VocabularyByID(ctx, ownerKey, card.VocabularyItemID)
	if err != nil {
		return LearningCandidate{}, fmt.Errorf("load selected vocabulary item: %w", err)
	}
	return LearningCandidate{Vocabulary: item, Card: card}, nil
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
		ORDER BY reviewed_at DESC, id DESC`
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

	existing, err := reviewAttemptByToken(ctx, transaction, input.OwnerKey, input.ReviewToken)
	if err == nil {
		if existing.Rating != input.Rating || existing.Comment != input.Comment {
			return ReviewAttempt{}, false, ErrIdempotencyConflict
		}
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

	now := input.Now.UTC()
	next, previousRetrievability, err := schedule(card, now, input.Rating)
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
		Rating:                 input.Rating,
		Comment:                input.Comment,
		ReviewedAt:             now,
		PreviousDueAt:          card.DueAt,
		PreviousRetrievability: previousRetrievability,
		After:                  next,
	}
	if err := insertReviewAttempt(ctx, transaction, input.OwnerKey, attempt, card); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := updateLearningCard(ctx, transaction, next, now); err != nil {
		return ReviewAttempt{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return ReviewAttempt{}, false, fmt.Errorf("commit review transaction: %w", err)
	}

	return attempt, false, nil
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
	card.review_token`

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
			consecutive_failures_after
		FROM review_attempts
		WHERE owner_key = ? AND submission_id = ?
	`, ownerKey, reviewToken)

	var attempt ReviewAttempt
	var reviewedAt string
	var previousDueAt string
	var dueAfter string
	if err := row.Scan(
		&attempt.ReviewID,
		&attempt.ReviewToken,
		&attempt.VocabularyItemID,
		&attempt.LearningCardID,
		&attempt.ExerciseMode,
		&attempt.Rating,
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
	); err != nil {
		return ReviewAttempt{}, err
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
	_, err := transaction.ExecContext(ctx, `
		INSERT INTO review_attempts(
			id, owner_key, submission_id, vocabulary_item_id, learning_card_id,
			exercise_mode, rating, comment, reviewed_at,
			due_before, stability_before, difficulty_before, retrievability_before,
			scheduled_days_before, repetitions_before, lapses_before, fsrs_state_before,
			remaining_steps_before, consecutive_failures_before,
			due_after, stability_after, difficulty_after, retrievability_after,
			scheduled_days_after, repetitions_after, lapses_after, fsrs_state_after,
			remaining_steps_after, consecutive_failures_after
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		attempt.ReviewID,
		ownerKey,
		attempt.ReviewToken,
		attempt.VocabularyItemID,
		attempt.LearningCardID,
		attempt.ExerciseMode,
		attempt.Rating,
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
			updated_at = ?
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
