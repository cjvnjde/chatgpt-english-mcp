package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/settings"
)

type FocusProgress struct {
	BatchID   string `json:"batchId,omitempty"`
	Total     int    `json:"total"`
	Learned   int    `json:"learned"`
	Remaining int    `json:"remaining"`
	Due       int    `json:"due"`
}

type FocusItem struct {
	ItemID        string                `json:"itemId"`
	Term          string                `json:"term"`
	Context       string                `json:"context,omitempty"`
	Status        domain.LearningStatus `json:"status"`
	MasteryStreak uint64                `json:"masteryStreak"`
	DueAt         string                `json:"dueAt"`
	fsrsState     int
	dueAt         time.Time
}

type LearningFocus struct {
	FocusProgress
	LearningMode   string      `json:"learningMode"`
	FocusBatchSize int         `json:"focusBatchSize"`
	CreatedAt      string      `json:"createdAt,omitempty"`
	NextDueAt      string      `json:"nextDueAt,omitempty"`
	Items          []FocusItem `json:"items"`
}

// LearningFocus manages a single persistent batch. Starting/resuming focus and
// changing its mode share the same transaction as membership changes.
func (db *DB) LearningFocus(ctx context.Context, owner, action string, itemIDs []string, clock func() time.Time) (LearningFocus, error) {
	if action == "" {
		action = "status"
	}
	if action != "status" && action != "start" && action != "stop" {
		return LearningFocus{}, apperr.New(apperr.InvalidArgument, "action must be status, start, or stop")
	}
	if itemIDs != nil && (action != "start" || len(itemIDs) == 0) {
		return LearningFocus{}, apperr.New(apperr.InvalidArgument, "itemIds must be a nonempty list used only with start")
	}
	if len(itemIDs) > 100 {
		return LearningFocus{}, apperr.New(apperr.InvalidArgument, "itemIds must contain at most 100 IDs")
	}
	seen := make(map[string]bool, len(itemIDs))
	for _, id := range itemIDs {
		if strings.TrimSpace(id) != id || id == "" || utf8.RuneCountInString(id) > 200 || seen[id] {
			return LearningFocus{}, apperr.New(apperr.InvalidArgument, "itemIds must contain unique, nonempty saved item IDs without surrounding whitespace")
		}
		seen[id] = true
	}
	tx, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: action == "status"})
	if err != nil {
		return LearningFocus{}, err
	}
	defer tx.Rollback()
	snapshot, err := algorithmSettings(ctx, tx, owner)
	if err != nil {
		return LearningFocus{}, err
	}
	now := clock().UTC()
	focus, err := loadLearningFocus(ctx, tx, owner, snapshot.Values, now)
	if err != nil {
		return LearningFocus{}, err
	}
	if action == "start" {
		if itemIDs != nil {
			if len(itemIDs) > snapshot.Values.FocusBatchSize {
				return LearningFocus{}, apperr.New(apperr.InvalidArgument, "itemIds exceeds the configured focusBatchSize; change it in Admin Settings first")
			}
			items, err := readFocusItems(ctx, tx, owner, itemIDs)
			if err != nil {
				return LearningFocus{}, err
			}
			if len(items) != len(itemIDs) {
				return LearningFocus{}, apperr.New(apperr.InvalidArgument, "every itemId must identify an available saved meaning owned by this learner")
			}
			for _, item := range items {
				if item.Status == domain.LearningStatusLearned {
					return LearningFocus{}, apperr.New(apperr.InvalidArgument, "choose new or learning meanings; use reinforcement for learned words")
				}
			}
			currentIDs := make([]string, len(focus.Items))
			for i, item := range focus.Items {
				currentIDs[i] = item.ItemID
			}
			if !slices.Equal(currentIDs, itemIDs) {
				if err := replaceLearningFocus(ctx, tx, owner, itemIDs, now); err != nil {
					return LearningFocus{}, err
				}
				focus, err = loadLearningFocus(ctx, tx, owner, snapshot.Values, now)
				if err != nil {
					return LearningFocus{}, err
				}
			}
		} else {
			focus, err = prepareLearningFocus(ctx, tx, owner, snapshot.Values, now, true)
			if err != nil {
				return LearningFocus{}, err
			}
		}
	}
	if action != "status" {
		mode := "focused"
		if action == "stop" {
			mode = "mixed"
		}
		if snapshot.Values.LearningMode != mode {
			snapshot.Values.LearningMode = mode
			encoded, err := json.Marshal(snapshot.Values)
			if err != nil {
				return LearningFocus{}, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO algorithm_settings(owner_key, values_json, revision)
				VALUES (?, ?, 1) ON CONFLICT(owner_key) DO UPDATE SET
				values_json = excluded.values_json, revision = algorithm_settings.revision + 1`, owner, string(encoded)); err != nil {
				return LearningFocus{}, err
			}
		}
		focus.LearningMode = mode
	}
	return focus, tx.Commit()
}

func loadLearningFocus(ctx context.Context, tx *sql.Tx, owner string, values settings.Values, now time.Time) (LearningFocus, error) {
	focus := LearningFocus{LearningMode: values.LearningMode, FocusBatchSize: values.FocusBatchSize, Items: []FocusItem{}}
	err := tx.QueryRowContext(ctx, "SELECT id, created_at FROM learning_focus_batches WHERE owner_key = ?", owner).Scan(&focus.BatchID, &focus.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return focus, nil
	}
	if err != nil {
		return focus, fmt.Errorf("read learning focus: %w", err)
	}
	focus.Items, err = readFocusItems(ctx, tx, owner, nil)
	if err != nil {
		return focus, err
	}
	focus.count(now)
	return focus, nil
}

func readFocusItems(ctx context.Context, tx *sql.Tx, owner string, ids []string) ([]FocusItem, error) {
	query := `SELECT v.id, v.term, v.context, v.learning_status, c.mastery_streak, c.due_at, c.fsrs_state
		FROM vocabulary_items v JOIN learning_cards c ON c.vocabulary_item_id = v.id`
	args := []any{owner, productionExerciseMode}
	if ids == nil {
		query += ` JOIN learning_focus_items f ON f.vocabulary_item_id = v.id AND f.owner_key = v.owner_key`
	}
	query += ` WHERE v.owner_key = ? AND c.exercise_mode = ? AND v.learning_status <> 'archived'`
	if ids != nil {
		query += ` AND v.id IN (` + placeholders(len(ids)) + `)`
		for _, id := range ids {
			args = append(args, id)
		}
	} else {
		query += ` ORDER BY f.position`
	}
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("read focus members: %w", err)
	}
	defer rows.Close()
	items := []FocusItem{}
	for rows.Next() {
		var item FocusItem
		if err := rows.Scan(&item.ItemID, &item.Term, &item.Context, &item.Status, &item.MasteryStreak, &item.DueAt, &item.fsrsState); err != nil {
			return nil, err
		}
		item.dueAt, err = parseStoredTime(item.DueAt, "focus due date")
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if ids != nil {
		positions := make(map[string]int, len(ids))
		for i, id := range ids {
			positions[id] = i
		}
		slices.SortFunc(items, func(a, b FocusItem) int { return positions[a.ItemID] - positions[b.ItemID] })
	}
	return items, rows.Err()
}

func (focus *LearningFocus) count(now time.Time) {
	focus.Total = len(focus.Items)
	focus.Learned, focus.Remaining, focus.Due, focus.NextDueAt = 0, 0, 0, ""
	var next time.Time
	for _, item := range focus.Items {
		if item.Status == domain.LearningStatusLearned {
			focus.Learned++
			continue
		}
		focus.Remaining++
		due := item.dueAt
		if item.fsrsState == 0 || !due.After(now) {
			focus.Due++
		}
		if item.fsrsState == 0 {
			due = now
		}
		if next.IsZero() || due.Before(next) {
			next = due
		}
	}
	if !next.IsZero() {
		focus.NextDueAt = TimeString(next)
	}
}

// With persist=false, admin previews use the same deterministic next batch
// without creating it or changing presentation history.
func prepareLearningFocus(ctx context.Context, tx *sql.Tx, owner string, values settings.Values, now time.Time, persist bool) (LearningFocus, error) {
	focus, err := loadLearningFocus(ctx, tx, owner, values, now)
	if err != nil || focus.Remaining > 0 {
		return focus, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT v.id FROM vocabulary_items v
		JOIN learning_cards c ON c.vocabulary_item_id = v.id AND c.exercise_mode = ?
		WHERE v.owner_key = ? AND v.learning_status IN ('new', 'learning')
		ORDER BY CASE v.learning_status WHEN 'learning' THEN 0 ELSE 1 END,
			CASE v.personal_interest WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
			CASE v.usefulness WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END,
			v.created_at, v.id LIMIT ?`, productionExerciseMode, owner, values.FocusBatchSize)
	if err != nil {
		return LearningFocus{}, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return LearningFocus{}, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return LearningFocus{}, err
	}
	if err := rows.Err(); err != nil {
		return LearningFocus{}, err
	}
	if len(ids) == 0 {
		return focus, nil
	}
	if persist {
		if err := replaceLearningFocus(ctx, tx, owner, ids, now); err != nil {
			return LearningFocus{}, err
		}
		return loadLearningFocus(ctx, tx, owner, values, now)
	}
	items, err := readFocusItems(ctx, tx, owner, ids)
	focus = LearningFocus{LearningMode: values.LearningMode, FocusBatchSize: values.FocusBatchSize, Items: items}
	focus.count(now)
	return focus, err
}

func replaceLearningFocus(ctx context.Context, tx *sql.Tx, owner string, ids []string, now time.Time) error {
	id, err := NewID()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM learning_focus_batches WHERE owner_key = ?", owner); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO learning_focus_batches(owner_key, id, created_at) VALUES (?, ?, ?)", owner, id, TimeString(now)); err != nil {
		return err
	}
	for position, itemID := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO learning_focus_items(owner_key, vocabulary_item_id, position) VALUES (?, ?, ?)", owner, itemID, position); err != nil {
			return err
		}
	}
	return nil
}

func restrictToFocus(cards []selectionCard, focus LearningFocus) {
	members := make(map[string]bool, len(focus.Items))
	for _, item := range focus.Items {
		if item.Status != domain.LearningStatusLearned {
			members[item.ItemID] = true
		}
	}
	for i := range cards {
		cards[i].outsideFocus = !members[cards[i].vocabularyItemID]
	}
}
