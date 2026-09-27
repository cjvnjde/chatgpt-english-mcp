package storage

import (
	"context"
	"database/sql"
	"fmt"

	"english-learning-mcp/internal/domain"
)

// migrateSingleReviewRating preserves the original request identity and the
// actual scheduling grade, without retaining a second rating column.
func migrateSingleReviewRating(ctx context.Context, tx *sql.Tx) error {
	var guard string
	if err := tx.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'review_attempts_guard_update'`).Scan(&guard); err != nil {
		return fmt.Errorf("read review update guard: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DROP TRIGGER review_attempts_guard_update`); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, rating, comment FROM review_attempts`)
	if err != nil {
		return err
	}
	type request struct{ id, hash string }
	var requests []request
	for rows.Next() {
		var id, comment string
		var rating domain.ReviewRating
		if err := rows.Scan(&id, &rating, &comment); err != nil {
			rows.Close()
			return err
		}
		requests = append(requests, request{id, reviewRequestHash(rating, comment)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, request := range requests {
		if _, err := tx.ExecContext(ctx, `UPDATE review_attempts SET request_hash = ?, rating = COALESCE(effective_rating, rating) WHERE id = ?`, request.hash, request.id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE review_attempts DROP COLUMN effective_rating`); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, guard)
	return err
}
