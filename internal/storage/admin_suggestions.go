package storage

import (
	"cmp"
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"time"

	"english-learning-mcp/internal/domain"
)

type AdminSuggestion struct {
	VocabularyItemID string                `json:"vocabularyItemId"`
	CardID           string                `json:"cardId"`
	Term             string                `json:"term"`
	Context          string                `json:"context,omitempty"`
	Status           domain.LearningStatus `json:"status"`
	Usefulness       domain.Usefulness     `json:"usefulness"`
	DueAt            string                `json:"dueAt"`
	LastShownAt      string                `json:"lastShownAt,omitempty"`
	Pool             string                `json:"pool"`
	Probability      float64               `json:"probability"`
	Reason           string                `json:"reason"`
}

type AdminSuggestionsPage struct {
	Owner       string            `json:"owner"`
	GeneratedAt string            `json:"generatedAt"`
	Total       int               `json:"total"`
	Selectable  int               `json:"selectable"`
	Limit       int               `json:"limit"`
	Offset      int               `json:"offset"`
	Rows        []AdminSuggestion `json:"rows"`
}

// likelihood projects the shared selection plan without consuming a random draw.
func (plan *selectionPlan) likelihood(card *selectionCard) (float64, string) {
	if plan.future != nil {
		if card == plan.future {
			return 1, "early"
		}
		if plan.inCooldown(card) && !plan.inCooldown(plan.future) {
			return 0, "cooldown"
		}
		return 0, "waiting"
	}
	if card.fsrsState != 0 && card.dueAt.After(plan.now) {
		return 0, "not_due"
	}
	if !plan.eligible(card) {
		return 0, "cooldown"
	}
	pool := card.poolIndex()
	if plan.shares[pool] == 0 {
		return 0, "learning_first"
	}
	probability := plan.shares[pool] * (selectionWeight(card, plan.now) / plan.pools[pool].weight)
	if pool == newSelectionPool {
		return probability, "new"
	}
	return probability, "due"
}

func (db *DB) AdminSuggestions(ctx context.Context, owner string, limit, offset int) (AdminSuggestionsPage, error) {
	page := AdminSuggestionsPage{Owner: owner, Limit: limit, Offset: offset, Rows: []AdminSuggestion{}}
	if limit < 1 || limit > 200 || offset < 0 {
		return page, ErrAdminQuery
	}
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, fmt.Errorf("begin admin suggestions: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	page.GeneratedAt = TimeString(now)
	cards, recentSinceID, err := loadSelectionCards(ctx, tx, owner)
	if err != nil {
		return page, err
	}
	plan := planLearningSelection(cards, recentSinceID, now)
	type rank struct {
		card        *selectionCard
		probability float64
		reason      string
	}
	ranks := make([]rank, len(cards))
	for index := range cards {
		card := &cards[index]
		probability, reason := plan.likelihood(card)
		ranks[index] = rank{card: card, probability: probability, reason: reason}
		if probability > 0 {
			page.Selectable++
		}
	}
	slices.SortFunc(ranks, func(a, b rank) int {
		if order := cmp.Compare(b.probability, a.probability); order != 0 {
			return order
		}
		if order := a.card.dueAt.Compare(b.card.dueAt); order != 0 {
			return order
		}
		return cmp.Compare(a.card.cardID, b.card.cardID)
	})
	page.Total = len(cards)
	if offset >= page.Total {
		return page, tx.Commit()
	}
	// Bound by the remaining count before addition to avoid overflowing offset.
	selected := ranks[offset : offset+min(limit, page.Total-offset)]
	page.Rows = make([]AdminSuggestion, len(selected))
	positions := make(map[string]int, len(selected))
	args := make([]any, 0, len(selected)+2)
	args = append(args, owner, productionExerciseMode)
	poolNames := [3]string{"new", "learning", "review"}
	for index, ranked := range selected {
		card := ranked.card
		row := &page.Rows[index]
		row.CardID = card.cardID
		row.Usefulness = card.usefulness
		row.DueAt = TimeString(card.dueAt)
		if !card.lastShownAt.IsZero() {
			row.LastShownAt = TimeString(card.lastShownAt)
		}
		row.Pool = poolNames[card.poolIndex()]
		row.Probability, row.Reason = ranked.probability, ranked.reason
		positions[card.cardID] = index
		args = append(args, card.cardID)
	}
	// Hydrate only labels on this page, not definitions or dictionary snapshots.
	rows, err := tx.QueryContext(ctx, `
		SELECT card.id, vocabulary.id, vocabulary.term, vocabulary.context, vocabulary.learning_status
		FROM learning_cards card
		JOIN vocabulary_items vocabulary ON vocabulary.id = card.vocabulary_item_id
		WHERE vocabulary.owner_key = ? AND card.exercise_mode = ?
			AND vocabulary.learning_status <> 'archived'
			AND card.id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(selected)), ",")+`)`, args...)
	if err != nil {
		return page, fmt.Errorf("read admin suggestion labels: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cardID, itemID, term, context, status string
		if err := rows.Scan(&cardID, &itemID, &term, &context, &status); err != nil {
			return page, fmt.Errorf("scan admin suggestion labels: %w", err)
		}
		row := &page.Rows[positions[cardID]]
		row.VocabularyItemID, row.Term, row.Context = itemID, term, context
		row.Status = domain.LearningStatus(status)
	}
	if err := rows.Err(); err != nil {
		return page, fmt.Errorf("read admin suggestion labels: %w", err)
	}
	if err := rows.Close(); err != nil {
		return page, fmt.Errorf("close admin suggestion labels: %w", err)
	}
	return page, tx.Commit()
}
