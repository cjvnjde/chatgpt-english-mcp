package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"english-learning-mcp/internal/domain"
	"english-learning-mcp/internal/usefulness"
)

func refreshUsefulness(ctx context.Context, transaction *sql.Tx) error {
	var revision string
	err := transaction.QueryRowContext(ctx, "SELECT revision FROM usefulness_inference_state WHERE id = 1").Scan(&revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read usefulness inference revision: %w", err)
	}
	if err == nil && revision == "" {
		return fmt.Errorf("%w: empty usefulness inference revision", ErrCorruptData)
	}
	if revision == usefulness.Revision {
		return nil
	}

	rows, err := transaction.QueryContext(ctx, "SELECT id, normalized_term, usefulness_hint, usefulness FROM vocabulary_items")
	if err != nil {
		return fmt.Errorf("read vocabulary for usefulness refresh: %w", err)
	}
	type change struct {
		itemID string
		value  domain.Usefulness
	}
	var changes []change
	for rows.Next() {
		var itemID, normalizedTerm string
		var hint sql.NullString
		var previous domain.Usefulness
		if err := rows.Scan(&itemID, &normalizedTerm, &hint, &previous); err != nil {
			rows.Close()
			return fmt.Errorf("%w: scan vocabulary for usefulness refresh: %v", ErrCorruptData, err)
		}
		if hint.Valid && !domain.Usefulness(hint.String).Valid() {
			rows.Close()
			return fmt.Errorf("%w: vocabulary item %s usefulness hint %q", ErrCorruptData, itemID, hint.String)
		}
		if !previous.Valid() {
			rows.Close()
			return fmt.Errorf("%w: vocabulary item %s usefulness %q", ErrCorruptData, itemID, previous)
		}
		if !domain.ValidTerm(normalizedTerm) || domain.NormalizeTerm(normalizedTerm) != normalizedTerm {
			rows.Close()
			return fmt.Errorf("%w: vocabulary item %s normalized term %q", ErrCorruptData, itemID, normalizedTerm)
		}
		effective := usefulness.Estimate(normalizedTerm, domain.Usefulness(hint.String))
		if effective != previous {
			changes = append(changes, change{itemID: itemID, value: effective})
		}
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close usefulness refresh rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate usefulness refresh rows: %w", err)
	}

	for _, item := range changes {
		if _, err := transaction.ExecContext(ctx,
			"UPDATE vocabulary_items SET usefulness = ? WHERE id = ?", item.value, item.itemID,
		); err != nil {
			return fmt.Errorf("refresh vocabulary item %s usefulness: %w", item.itemID, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO usefulness_inference_state(id, revision) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET revision = excluded.revision
	`, usefulness.Revision); err != nil {
		return fmt.Errorf("record usefulness inference revision: %w", err)
	}
	return nil
}
