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
			COALESCE(presentation.id, 0), presentation.shown_at, vocabulary.usefulness
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
			&card.consecutiveFailures, &card.lapses, &card.lastPresentationID, &shownAt, &card.usefulness); err != nil {
			return nil, 0, fmt.Errorf("scan learning selection card: %w", err)
		}
		if !card.usefulness.Valid() {
			return nil, 0, fmt.Errorf("%w: learning candidate usefulness", ErrCorruptData)
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

func selectLearningCard(cards []selectionCard, recentSinceID int64, now time.Time, random func() float64) (selectionCard, bool) {
	if len(cards) == 0 {
		return selectionCard{}, false
	}

	var pools [3]selectionPool
	var hasEligible bool
	var latestPresentationID int64
	var onlyAvailable, oldestRecent, secondOldestRecent *selectionCard
	var availableFuture, oldestFuture *selectionCard
	for index := range cards {
		card := &cards[index]
		// Future cards still identify which presentation would be an immediate repeat.
		latestPresentationID = max(latestPresentationID, card.lastPresentationID)
		recent := card.lastPresentationID >= recentSinceID && presentedRecently(card, now)
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
			pools[card.poolIndex()].add(card, now)
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
			return *availableFuture, true
		}
		return *oldestFuture, true
	}
	// A rigid three-card exclusion forces four-card pools into a cycle.
	// Relax oldest exclusions only when fresh alternatives cannot break it.
	var relaxed [2]*selectionCard
	availableCount := pools[newSelectionPool].count + pools[stepSelectionPool].count + pools[reviewSelectionPool].count
	if availableCount == 0 {
		relaxed[0] = oldestRecent
		if secondOldestRecent != nil && secondOldestRecent.lastPresentationID < latestPresentationID {
			relaxed[1] = secondOldestRecent
		}
	} else if availableCount == 1 && presentedRecently(onlyAvailable, now) &&
		oldestRecent != nil && oldestRecent.lastPresentationID < latestPresentationID {
		relaxed[0] = oldestRecent
	}
	for _, card := range relaxed {
		if card == nil {
			continue
		}
		pools[card.poolIndex()].add(card, now)
	}

	// Complete selectable learning steps before introducing more material.
	selectedPool := reviewSelectionPool
	if pools[stepSelectionPool].count > 0 {
		selectedPool = stepSelectionPool
	} else if pools[newSelectionPool].count > 0 &&
		(pools[reviewSelectionPool].count == 0 || random() < 0.2) {
		selectedPool = newSelectionPool
	}
	remaining := random() * pools[selectedPool].weight
	var last *selectionCard
	for index := range cards {
		card := &cards[index]
		if card.poolIndex() != selectedPool || (card.fsrsState != 0 && card.dueAt.After(now)) {
			continue
		}
		if card != relaxed[0] && card != relaxed[1] &&
			card.lastPresentationID >= recentSinceID && presentedRecently(card, now) {
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
	if card.fsrsState == 0 {
		usefulness := 1.0
		switch card.usefulness {
		case domain.UsefulnessLow:
			usefulness = 0.5
		case domain.UsefulnessHigh:
			usefulness = 2
		}
		return presentationRecency(card, now) * usefulness
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
	return urgency * failures * exposure
}
