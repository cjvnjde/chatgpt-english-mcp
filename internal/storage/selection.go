package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"english-learning-mcp/internal/domain"
)

// Selection only loads scheduling and presentation state; vocabulary content is
// hydrated after choosing a card, on the same transaction snapshot.
type selectionCard struct {
	cardID              string
	dueAt               time.Time
	fsrsState           int
	scheduledDays       uint64
	consecutiveFailures uint64
	lapses              uint64
	lastPresentationID  int64
	lastShownAt         time.Time
	usefulness          domain.Usefulness
	personalInterest    domain.PersonalInterest
}

const (
	newSelectionPool = iota
	stepSelectionPool
	reviewSelectionPool
)

type selectionPool struct {
	count  int
	weight float64
}

func loadSelectionCards(ctx context.Context, transaction *sql.Tx, ownerKey string) ([]selectionCard, int64, error) {
	var recentSinceID int64
	if err := transaction.QueryRowContext(ctx, `
		SELECT COALESCE(MIN(id), 0) FROM (
			SELECT id FROM learning_presentations
			WHERE owner_key = ? AND exercise_mode = ?
			ORDER BY id DESC LIMIT 3
		)
	`, ownerKey, productionExerciseMode).Scan(&recentSinceID); err != nil {
		return nil, 0, fmt.Errorf("read recent learning presentations: %w", err)
	}

	rows, err := transaction.QueryContext(ctx, `
		SELECT card.id, card.due_at, card.fsrs_state, card.scheduled_days,
			card.consecutive_failures, card.lapses,
			COALESCE(presentation.id, 0), presentation.shown_at, vocabulary.usefulness, vocabulary.personal_interest
		FROM learning_cards card
		JOIN vocabulary_items vocabulary ON vocabulary.id = card.vocabulary_item_id
		LEFT JOIN learning_presentations presentation ON presentation.id = (
			SELECT id FROM learning_presentations
			WHERE owner_key = ? AND learning_card_id = card.id
			ORDER BY id DESC LIMIT 1
		)
		WHERE vocabulary.owner_key = ?
			AND vocabulary.learning_status <> 'archived'
			AND card.exercise_mode = ?
	`, ownerKey, ownerKey, productionExerciseMode)
	if err != nil {
		return nil, 0, fmt.Errorf("read learning selection cards: %w", err)
	}
	defer rows.Close()

	var cards []selectionCard
	for rows.Next() {
		var card selectionCard
		var dueAt string
		var shownAt sql.NullString
		if err := rows.Scan(&card.cardID, &dueAt, &card.fsrsState, &card.scheduledDays,
			&card.consecutiveFailures, &card.lapses, &card.lastPresentationID, &shownAt, &card.usefulness,
			&card.personalInterest); err != nil {
			return nil, 0, fmt.Errorf("scan learning selection card: %w", err)
		}
		if !card.usefulness.Valid() {
			return nil, 0, fmt.Errorf("%w: learning candidate usefulness", ErrCorruptData)
		}
		if !card.personalInterest.Valid() {
			return nil, 0, fmt.Errorf("%w: learning candidate personal interest", ErrCorruptData)
		}
		card.dueAt, err = parseStoredTime(dueAt, "learning due date")
		if err != nil {
			return nil, 0, err
		}
		if shownAt.Valid {
			card.lastShownAt, err = parseStoredTime(shownAt.String, "learning presentation date")
			if err != nil {
				return nil, 0, err
			}
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("read learning selection cards: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, 0, fmt.Errorf("close learning selection cards: %w", err)
	}

	return cards, recentSinceID, nil
}

// A plan borrows the input cards. Building it never draws randomness or mutates
// scheduling state, so selection and admin likelihoods share the same policy.
type selectionPlan struct {
	pools         [3]selectionPool
	shares        [3]float64
	relaxed       [2]*selectionCard
	future        *selectionCard
	recentSinceID int64
	now           time.Time
}

func planLearningSelection(cards []selectionCard, recentSinceID int64, now time.Time) selectionPlan {
	plan := selectionPlan{recentSinceID: recentSinceID, now: now}
	var hasEligible bool
	var latestPresentationID int64
	var onlyAvailable, oldestRecent, secondOldestRecent *selectionCard
	var availableFuture, oldestFuture *selectionCard
	for index := range cards {
		card := &cards[index]
		// Future cards still identify which presentation would be an immediate repeat.
		latestPresentationID = max(latestPresentationID, card.lastPresentationID)
		recent := plan.inCooldown(card)
		if card.fsrsState == 0 || !card.dueAt.After(now) {
			hasEligible = true
			if recent {
				if oldestRecent == nil || presentedBefore(card, oldestRecent) {
					secondOldestRecent = oldestRecent
					oldestRecent = card
				} else if secondOldestRecent == nil || presentedBefore(card, secondOldestRecent) {
					secondOldestRecent = card
				}
				continue
			}
			onlyAvailable = card
			plan.pools[card.poolIndex()].add(card, now)
			continue
		}
		if oldestFuture == nil || futureBefore(card, oldestFuture) {
			oldestFuture = card
		}
		if !recent && (availableFuture == nil || futureBefore(card, availableFuture)) {
			availableFuture = card
		}
	}

	if !hasEligible {
		if availableFuture != nil {
			plan.future = availableFuture
		} else {
			plan.future = oldestFuture
		}
		return plan
	}
	// A rigid three-card exclusion forces four-card pools into a cycle.
	// Relax oldest exclusions only when fresh alternatives cannot break it.
	availableCount := plan.pools[newSelectionPool].count + plan.pools[stepSelectionPool].count + plan.pools[reviewSelectionPool].count
	if availableCount == 0 {
		plan.relaxed[0] = oldestRecent
		if secondOldestRecent != nil && secondOldestRecent.lastPresentationID < latestPresentationID {
			plan.relaxed[1] = secondOldestRecent
		}
	} else if availableCount == 1 && presentedRecently(onlyAvailable, now) &&
		oldestRecent != nil && oldestRecent.lastPresentationID < latestPresentationID {
		plan.relaxed[0] = oldestRecent
	}
	for _, card := range plan.relaxed {
		if card == nil {
			continue
		}
		plan.pools[card.poolIndex()].add(card, now)
	}

	// Complete selectable learning steps before introducing more material.
	if plan.pools[stepSelectionPool].count > 0 {
		plan.shares[stepSelectionPool] = 1
	} else if plan.pools[newSelectionPool].count == 0 {
		plan.shares[reviewSelectionPool] = 1
	} else if plan.pools[reviewSelectionPool].count == 0 {
		plan.shares[newSelectionPool] = 1
	} else {
		plan.shares[newSelectionPool] = 0.2
		plan.shares[reviewSelectionPool] = 0.8
	}
	return plan
}

func (plan *selectionPlan) inCooldown(card *selectionCard) bool {
	return card.lastPresentationID >= plan.recentSinceID && presentedRecently(card, plan.now)
}

func (plan *selectionPlan) eligible(card *selectionCard) bool {
	return (card.fsrsState == 0 || !card.dueAt.After(plan.now)) &&
		(card == plan.relaxed[0] || card == plan.relaxed[1] || !plan.inCooldown(card))
}

func selectLearningCard(cards []selectionCard, recentSinceID int64, now time.Time, random func() float64) (selectionCard, bool) {
	if len(cards) == 0 {
		return selectionCard{}, false
	}
	plan := planLearningSelection(cards, recentSinceID, now)
	if plan.future != nil {
		return *plan.future, true
	}
	selectedPool := reviewSelectionPool
	if plan.shares[stepSelectionPool] > 0 {
		selectedPool = stepSelectionPool
	} else if share := plan.shares[newSelectionPool]; share > 0 && (share == 1 || random() < share) {
		selectedPool = newSelectionPool
	}
	remaining := random() * plan.pools[selectedPool].weight
	var last *selectionCard
	for index := range cards {
		card := &cards[index]
		if card.poolIndex() != selectedPool || !plan.eligible(card) {
			continue
		}
		last = card
		remaining -= selectionWeight(card, now)
		if remaining < 0 {
			return *card, true
		}
	}

	// Floating-point summation can leave a tiny positive remainder at the edge.
	return *last, true
}

func (card *selectionCard) poolIndex() int {
	switch card.fsrsState {
	case 0:
		return newSelectionPool
	case 1, 3:
		return stepSelectionPool
	default:
		return reviewSelectionPool
	}
}

func futureBefore(card, other *selectionCard) bool {
	if card.lastPresentationID != other.lastPresentationID {
		return card.lastPresentationID < other.lastPresentationID
	}
	if !card.dueAt.Equal(other.dueAt) {
		return card.dueAt.Before(other.dueAt)
	}
	return card.cardID < other.cardID
}

func presentedBefore(card, other *selectionCard) bool {
	return card.lastPresentationID < other.lastPresentationID ||
		(card.lastPresentationID == other.lastPresentationID && card.cardID < other.cardID)
}

func presentedRecently(card *selectionCard, now time.Time) bool {
	return card.lastPresentationID > 0 && now.Sub(card.lastShownAt) < 30*time.Minute
}

func presentationRecency(card *selectionCard, now time.Time) float64 {
	if card.lastPresentationID > 0 {
		return 0.25 + 0.75*max(0, min(now.Sub(card.lastShownAt).Hours()/24, 1))
	}
	return 1
}

func (pool *selectionPool) add(card *selectionCard, now time.Time) {
	pool.count++
	pool.weight += selectionWeight(card, now)
}

func selectionWeight(card *selectionCard, now time.Time) float64 {
	interest := 1.0
	switch card.personalInterest {
	case domain.PersonalInterestLow:
		interest = 0.5
	case domain.PersonalInterestHigh:
		interest = 2
	}
	if card.fsrsState == 0 {
		usefulness := 1.0
		switch card.usefulness {
		case domain.UsefulnessLow:
			usefulness = 0.5
		case domain.UsefulnessHigh:
			usefulness = 2
		}
		return presentationRecency(card, now) * usefulness * interest
	}

	intervalHours := max(float64(card.scheduledDays)*24, 24)
	exposure := 1.0
	if card.poolIndex() == stepSelectionPool {
		// Learning steps operate in minutes; cooldown already supplies spacing.
		intervalHours = (10 * time.Minute).Hours()
	} else {
		exposure = presentationRecency(card, now)
	}
	urgency := 1 + min(max(now.Sub(card.dueAt).Hours(), 0)/intervalHours, 4)
	failures := 1 + 0.5*float64(min(card.consecutiveFailures, 2)) + 0.25*float64(min(card.lapses, 3))
	return urgency * failures * exposure * interest
}
