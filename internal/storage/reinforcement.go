package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"sort"
	"time"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

var (
	ErrReinforcementShortage = errors.New("not enough distinct learned words outside the configured reinforcement cooldown for the configured probability cap")
	ErrNotLearned            = errors.New("vocabulary item is not learned")
)

// ReinforcementPractice is independent of the FSRS learning card.
type ReinforcementPractice struct {
	ReviewCount    int
	Difficulty     float64
	LastRating     domain.ReviewRating
	LastReviewedAt time.Time
	StatusAfter    domain.LearningStatus
}

type ReinforcementCandidate struct {
	Vocabulary           domain.VocabularyItem
	ReviewToken          string
	Comments             []ReviewComment
	SelectionProbability float64
	EligibleWordCount    int
	Practice             ReinforcementPractice
}

type reinforcementSense struct {
	itemID          string
	normalizedTerm  string
	usefulness      domain.Usefulness
	interest        domain.PersonalInterest
	commentCount    int
	lastShownAt     time.Time
	wordLastShownAt time.Time
	practice        ReinforcementPractice
}

type reinforcementWord struct {
	senses      []int
	weight      float64
	probability float64
	lastShownAt time.Time
}

func reinforcementWeight(sense reinforcementSense, now time.Time, values settings.Values) float64 {
	usefulness, interest := 1.0, 1.0
	switch sense.usefulness {
	case domain.UsefulnessLow:
		usefulness = values.LowUsefulnessWeight
	case domain.UsefulnessHigh:
		usefulness = values.HighUsefulnessWeight
	}
	switch sense.interest {
	case domain.PersonalInterestLow:
		interest = values.LowInterestWeight
	case domain.PersonalInterestHigh:
		interest = values.HighInterestWeight
	}
	recency := 1.0
	if !sense.lastShownAt.IsZero() {
		recency = 0.25 + 0.75*max(0, min(now.Sub(sense.lastShownAt).Hours()/values.ExposureRecoveryHours, 1))
	}
	// Historical comments add priority only while difficulty remains unresolved.
	// Good/easy reviews reduce both difficulty and its share of the comment bonus.
	commentBonus := float64(min(sense.commentCount, 8)) * sense.practice.Difficulty / 4
	return usefulness * interest * (1 + commentBonus) * (1 + sense.practice.Difficulty) * recency
}

// Word mass uses its strongest sense, not the sum: adding meanings cannot buy
// additional word probability. Water filling redistributes capped mass before
// sampling; clamping and renormalizing would violate the configured ceiling.
func planReinforcementSelection(senses []reinforcementSense, now time.Time, values settings.Values) []reinforcementWord {
	words := make([]reinforcementWord, 0)
	byTerm := make(map[string]int)
	for index, sense := range senses {
		wordIndex, found := byTerm[sense.normalizedTerm]
		if !found {
			wordIndex = len(words)
			byTerm[sense.normalizedTerm] = wordIndex
			words = append(words, reinforcementWord{})
		}
		word := &words[wordIndex]
		word.senses = append(word.senses, index)
		word.weight = max(word.weight, reinforcementWeight(sense, now, values))
		if sense.lastShownAt.After(word.lastShownAt) {
			word.lastShownAt = sense.lastShownAt
		}
		if sense.wordLastShownAt.After(word.lastShownAt) {
			word.lastShownAt = sense.wordLastShownAt
		}
	}
	eligible := words[:0]
	for _, word := range words {
		if word.lastShownAt.IsZero() || now.Sub(word.lastShownAt).Hours() >= values.ReinforcementCooldownHours {
			eligible = append(eligible, word)
		}
	}
	words = eligible
	if len(words) < reinforcementMinimumWords(values) {
		return words
	}
	sort.SliceStable(words, func(i, j int) bool { return words[i].weight > words[j].weight })
	remainingWeight := 0.0
	for _, word := range words {
		remainingWeight += word.weight
	}
	remainingMass := 1.0
	for index := range words {
		probability := remainingMass * words[index].weight / remainingWeight
		if probability >= values.ReinforcementMaxWordShare {
			words[index].probability = values.ReinforcementMaxWordShare
			remainingMass -= values.ReinforcementMaxWordShare
			remainingWeight -= words[index].weight
			continue
		}
		for rest := index; rest < len(words); rest++ {
			words[rest].probability = min(values.ReinforcementMaxWordShare, remainingMass*words[rest].weight/remainingWeight)
		}
		break
	}
	return words
}

func reinforcementMinimumWords(values settings.Values) int {
	return max(4, int(math.Ceil(1/values.ReinforcementMaxWordShare)))
}

func selectReinforcementSense(senses []reinforcementSense, words []reinforcementWord, now time.Time, random func() float64, values settings.Values) (reinforcementSense, float64) {
	draw := random()
	word := &words[len(words)-1]
	for index := range words {
		draw -= words[index].probability
		if draw < 0 {
			word = &words[index]
			break
		}
	}
	total := 0.0
	for _, index := range word.senses {
		total += reinforcementWeight(senses[index], now, values)
	}
	draw = random() * total
	for _, index := range word.senses {
		draw -= reinforcementWeight(senses[index], now, values)
		if draw < 0 {
			return senses[index], word.probability
		}
	}
	return senses[word.senses[len(word.senses)-1]], word.probability
}

func (db *DB) NextReinforcementItem(ctx context.Context, ownerKey string, clock func() time.Time) (ReinforcementCandidate, error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ReinforcementCandidate{}, fmt.Errorf("begin reinforcement presentation: %w", err)
	}
	defer transaction.Rollback()
	snapshot, err := algorithmSettings(ctx, transaction, ownerKey)
	if err != nil {
		return ReinforcementCandidate{}, err
	}
	now := clock().UTC()
	senses, err := loadReinforcementSenses(ctx, transaction, ownerKey)
	if err != nil {
		return ReinforcementCandidate{}, err
	}
	if len(senses) == 0 {
		return ReinforcementCandidate{}, ErrNotFound
	}
	words := planReinforcementSelection(senses, now, snapshot.Values)
	if len(words) < reinforcementMinimumWords(snapshot.Values) {
		return ReinforcementCandidate{}, ErrReinforcementShortage
	}
	selected, probability := selectReinforcementSense(senses, words, now, rand.Float64, snapshot.Values)
	item, err := scanVocabularyItem(transaction.QueryRowContext(ctx,
		vocabularySelect+" WHERE v.owner_key = ? AND v.id = ?", ownerKey, selected.itemID))
	if err != nil {
		return ReinforcementCandidate{}, fmt.Errorf("load reinforcement vocabulary: %w", err)
	}
	comments, err := reinforcementComments(ctx, transaction, ownerKey, selected.itemID)
	if err != nil {
		return ReinforcementCandidate{}, err
	}
	token, err := NewID()
	if err != nil {
		return ReinforcementCandidate{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO reinforcement_presentations(review_token, owner_key, vocabulary_item_id, shown_at)
		VALUES (?, ?, ?, ?)
	`, token, ownerKey, selected.itemID, TimeString(now)); err != nil {
		return ReinforcementCandidate{}, fmt.Errorf("record reinforcement presentation: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO reinforcement_practice(vocabulary_item_id, last_shown_at) VALUES (?, ?)
		ON CONFLICT(vocabulary_item_id) DO UPDATE SET last_shown_at = excluded.last_shown_at
	`, selected.itemID, TimeString(now)); err != nil {
		return ReinforcementCandidate{}, fmt.Errorf("update reinforcement presentation recency: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return ReinforcementCandidate{}, fmt.Errorf("commit reinforcement presentation: %w", err)
	}
	return ReinforcementCandidate{
		Vocabulary: item, ReviewToken: token, Comments: comments, Practice: selected.practice,
		SelectionProbability: probability, EligibleWordCount: len(words),
	}, nil
}

func loadReinforcementSenses(ctx context.Context, transaction *sql.Tx, ownerKey string) ([]reinforcementSense, error) {
	rows, err := transaction.QueryContext(ctx, `
		WITH word_presentations AS (
			SELECT v.normalized_term, MAX(rtrim(p.last_shown_at, 'Z')) AS shown_at
			FROM vocabulary_items v
			JOIN reinforcement_practice p ON p.vocabulary_item_id = v.id
			WHERE v.owner_key = ?
			GROUP BY v.normalized_term
		)
		SELECT v.id, v.normalized_term, v.usefulness, v.personal_interest,
			(SELECT count(*) FROM review_attempts r WHERE r.owner_key = v.owner_key AND r.vocabulary_item_id = v.id AND r.comment <> '') +
			(SELECT count(*) FROM reinforcement_attempts r WHERE r.owner_key = v.owner_key AND r.vocabulary_item_id = v.id AND r.comment <> ''),
			COALESCE(p.review_count, 0), COALESCE(p.difficulty, 0),
			COALESCE(p.last_rating, ''), COALESCE(p.last_reviewed_at, ''), COALESCE(p.last_shown_at, ''),
			COALESCE(word.shown_at || 'Z', '')
		FROM vocabulary_items v
		LEFT JOIN reinforcement_practice p ON p.vocabulary_item_id = v.id
		LEFT JOIN word_presentations word ON word.normalized_term = v.normalized_term
		WHERE v.owner_key = ? AND v.learning_status = 'learned'
		ORDER BY v.normalized_term, v.id
	`, ownerKey, ownerKey)
	if err != nil {
		return nil, fmt.Errorf("load reinforcement candidates: %w", err)
	}
	defer rows.Close()
	senses := make([]reinforcementSense, 0)
	for rows.Next() {
		var sense reinforcementSense
		var reviewedAt, shownAt, wordShownAt string
		if err := rows.Scan(&sense.itemID, &sense.normalizedTerm, &sense.usefulness, &sense.interest,
			&sense.commentCount, &sense.practice.ReviewCount, &sense.practice.Difficulty,
			&sense.practice.LastRating, &reviewedAt, &shownAt, &wordShownAt); err != nil {
			return nil, fmt.Errorf("scan reinforcement candidate: %w", err)
		}
		sense.practice.StatusAfter = domain.LearningStatusLearned
		if reviewedAt != "" {
			sense.practice.LastReviewedAt, err = parseStoredTime(reviewedAt, "reinforcement review date")
			if err != nil {
				return nil, err
			}
		}
		if shownAt != "" {
			sense.lastShownAt, err = parseStoredTime(shownAt, "reinforcement presentation date")
			if err != nil {
				return nil, err
			}
		}
		if wordShownAt != "" {
			sense.wordLastShownAt, err = parseStoredTime(wordShownAt, "reinforcement word presentation date")
			if err != nil {
				return nil, err
			}
		}
		senses = append(senses, sense)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reinforcement candidates: %w", err)
	}
	return senses, nil
}

func reinforcementComments(ctx context.Context, transaction *sql.Tx, ownerKey, itemID string) ([]ReviewComment, error) {
	rows, err := transaction.QueryContext(ctx, `
		SELECT comment, rating, reviewed_at FROM (
			SELECT comment, rating, reviewed_at, id FROM review_attempts
			WHERE owner_key = ? AND vocabulary_item_id = ? AND comment <> ''
			UNION ALL
			SELECT comment, rating, reviewed_at, id FROM reinforcement_attempts
			WHERE owner_key = ? AND vocabulary_item_id = ? AND comment <> ''
		) ORDER BY rtrim(reviewed_at, 'Z') DESC, id DESC
	`, ownerKey, itemID, ownerKey, itemID)
	if err != nil {
		return nil, fmt.Errorf("load reinforcement comments: %w", err)
	}
	defer rows.Close()
	comments := make([]ReviewComment, 0)
	for rows.Next() {
		var comment ReviewComment
		var reviewedAt string
		if err := rows.Scan(&comment.Comment, &comment.Rating, &reviewedAt); err != nil {
			return nil, fmt.Errorf("scan reinforcement comment: %w", err)
		}
		comment.ReviewedAt, err = parseStoredTime(reviewedAt, "reinforcement comment date")
		if err != nil {
			return nil, err
		}
		comments = append(comments, comment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reinforcement comments: %w", err)
	}
	return comments, nil
}

func (db *DB) RecordReinforcementReview(ctx context.Context, input RecordReviewInput) (ReinforcementPractice, bool, error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return ReinforcementPractice{}, false, fmt.Errorf("begin reinforcement review: %w", err)
	}
	defer transaction.Rollback()

	var practice ReinforcementPractice
	var rating domain.ReviewRating
	var comment, reviewedAt string
	err = transaction.QueryRowContext(ctx, `
		SELECT rating, comment, reviewed_at, review_count_after, difficulty_after, status_after
		FROM reinforcement_attempts WHERE owner_key = ? AND review_token = ?
	`, input.OwnerKey, input.ReviewToken).Scan(&rating, &comment, &reviewedAt, &practice.ReviewCount, &practice.Difficulty, &practice.StatusAfter)
	if err == nil {
		if rating != input.Rating || comment != input.Comment {
			return ReinforcementPractice{}, false, ErrIdempotencyConflict
		}
		practice.LastRating = rating
		practice.LastReviewedAt, err = parseStoredTime(reviewedAt, "reinforcement review date")
		return practice, true, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReinforcementPractice{}, false, fmt.Errorf("read reinforcement attempt: %w", err)
	}
	var itemID string
	var status domain.LearningStatus
	err = transaction.QueryRowContext(ctx, `
		SELECT v.id, v.learning_status, p.review_count, p.difficulty
		FROM reinforcement_presentations presentation
		JOIN vocabulary_items v ON v.id = presentation.vocabulary_item_id AND v.owner_key = presentation.owner_key
		JOIN reinforcement_practice p ON p.vocabulary_item_id = v.id
		WHERE presentation.owner_key = ? AND presentation.review_token = ?
	`, input.OwnerKey, input.ReviewToken).Scan(&itemID, &status, &practice.ReviewCount, &practice.Difficulty)
	if errors.Is(err, sql.ErrNoRows) {
		return ReinforcementPractice{}, false, ErrNotFound
	}
	if err != nil {
		return ReinforcementPractice{}, false, fmt.Errorf("load reinforcement review state: %w", err)
	}
	if status != domain.LearningStatusLearned {
		return ReinforcementPractice{}, false, ErrNotLearned
	}
	practice.ReviewCount++
	practice.LastRating = input.Rating
	practice.LastReviewedAt = input.Now().UTC()
	practice.StatusAfter = domain.LearningStatusLearned
	switch input.Rating {
	case domain.ReviewRatingAgain:
		practice.Difficulty += 1
		practice.StatusAfter = domain.LearningStatusLearning
	case domain.ReviewRatingHard:
		practice.Difficulty += 0.5
	case domain.ReviewRatingGood:
		practice.Difficulty -= 0.5
	case domain.ReviewRatingEasy:
		practice.Difficulty -= 1
	}
	practice.Difficulty = max(0, min(practice.Difficulty, 4))
	id, err := NewID()
	if err != nil {
		return ReinforcementPractice{}, false, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO reinforcement_attempts(id, review_token, owner_key, vocabulary_item_id,
			rating, comment, reviewed_at, review_count_after, difficulty_after, status_after)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, input.ReviewToken, input.OwnerKey, itemID, input.Rating, input.Comment,
		TimeString(practice.LastReviewedAt), practice.ReviewCount, practice.Difficulty, practice.StatusAfter); err != nil {
		return ReinforcementPractice{}, false, fmt.Errorf("insert reinforcement review: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE reinforcement_practice SET review_count = ?, difficulty = ?, last_rating = ?, last_reviewed_at = ?
		WHERE vocabulary_item_id = ?
	`, practice.ReviewCount, practice.Difficulty, practice.LastRating, TimeString(practice.LastReviewedAt), itemID); err != nil {
		return ReinforcementPractice{}, false, fmt.Errorf("update reinforcement practice: %w", err)
	}
	if input.Rating == domain.ReviewRatingAgain || input.Rating == domain.ReviewRatingHard {
		// Reinforcement does not fabricate FSRS repetitions or change memory
		// parameters. Failure only brings the next production review forward.
		_, err := transaction.ExecContext(ctx, `
			UPDATE learning_cards SET mastery_streak = 0,
				due_at = CASE WHEN ? = 'again' AND rtrim(due_at, 'Z') > rtrim(?, 'Z') THEN ? ELSE due_at END,
				updated_at = ?
			WHERE vocabulary_item_id = ? AND exercise_mode = ?
		`, input.Rating, TimeString(practice.LastReviewedAt), TimeString(practice.LastReviewedAt),
			TimeString(practice.LastReviewedAt), itemID, productionExerciseMode)
		if err != nil {
			return ReinforcementPractice{}, false, fmt.Errorf("reset reinforcement mastery evidence: %w", err)
		}
	}
	if err := updateReviewStatus(ctx, transaction, itemID, practice.StatusAfter, practice.LastReviewedAt); err != nil {
		return ReinforcementPractice{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return ReinforcementPractice{}, false, fmt.Errorf("commit reinforcement review: %w", err)
	}
	return practice, false, nil
}
