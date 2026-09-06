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

	var dueWeight, newWeight float64
	var oldestEligible, earliestFuture, oldestFuture *selectionCard
	for index := range cards {
		card := &cards[index]
		recent := card.lastPresentationID > 0 && card.lastPresentationID >= recentSinceID &&
			now.Sub(card.lastShownAt) < 30*time.Minute
		if card.fsrsState == 0 || !card.dueAt.After(now) {
			if oldestEligible == nil || presentedBefore(card, oldestEligible) {
				oldestEligible = card
			}
			if recent {
				continue
			}
			if card.fsrsState == 0 {
				newWeight += selectionWeight(card, now)
			} else {
				dueWeight += selectionWeight(card, now)
			}
			continue
		}
		if oldestFuture == nil || presentedBefore(card, oldestFuture) {
			oldestFuture = card
		}
		if !recent && (earliestFuture == nil || card.dueAt.Before(earliestFuture.dueAt) ||
			(card.dueAt.Equal(earliestFuture.dueAt) && card.cardID < earliestFuture.cardID)) {
			earliestFuture = card
		}
	}

	if oldestEligible == nil {
		if earliestFuture != nil {
			return *earliestFuture, true
		}
		return *oldestFuture, true
	}
	if dueWeight == 0 && newWeight == 0 {
		return *oldestEligible, true
	}

	chooseNew := dueWeight == 0 || (newWeight > 0 && random() < 0.2)
	totalWeight := dueWeight
	if chooseNew {
		totalWeight = newWeight
	}
	remaining := random() * totalWeight
	var last *selectionCard
	for index := range cards {
		card := &cards[index]
		if (card.fsrsState == 0) != chooseNew || (card.fsrsState != 0 && card.dueAt.After(now)) {
			continue
		}
		if card.lastPresentationID > 0 && card.lastPresentationID >= recentSinceID &&
			now.Sub(card.lastShownAt) < 30*time.Minute {
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

func presentedBefore(card, other *selectionCard) bool {
	return card.lastPresentationID < other.lastPresentationID ||
		(card.lastPresentationID == other.lastPresentationID && card.cardID < other.cardID)
}

func selectionWeight(card *selectionCard, now time.Time) float64 {
	usefulness := 1.0
	switch card.usefulness {
	case domain.UsefulnessLow:
		usefulness = 0.5
	case domain.UsefulnessHigh:
		usefulness = 2
	}
	recency := 1.0
	if card.lastPresentationID > 0 {
		recency = 0.25 + 0.75*max(0, min(now.Sub(card.lastShownAt).Hours()/24, 1))
	}
	if card.fsrsState == 0 {
		return recency * usefulness
	}

	intervalHours := max(float64(card.scheduledDays)*24, 24)
	urgency := 1 + min(max(now.Sub(card.dueAt).Hours(), 0)/intervalHours, 4)
	failures := 1 + 0.5*float64(min(card.consecutiveFailures, 2)) + 0.25*float64(min(card.lapses, 3))
	return urgency * failures * recency * usefulness
}
