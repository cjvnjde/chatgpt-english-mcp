package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"english-learning-mcp/internal/apperr"
	"english-learning-mcp/internal/settings"
)

type AlgorithmSettings struct {
	Values   settings.Values `json:"values"`
	Revision int64           `json:"revision"`
}

type settingsQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (db *DB) AlgorithmSettings(ctx context.Context, owner string) (AlgorithmSettings, error) {
	return algorithmSettings(ctx, db.sql, owner)
}

// algorithmSettings reads from the operation's transaction, never a process cache.
func algorithmSettings(ctx context.Context, queryer settingsQueryer, owner string) (AlgorithmSettings, error) {
	var snapshot AlgorithmSettings
	var encoded string
	err := queryer.QueryRowContext(ctx, "SELECT values_json, revision FROM algorithm_settings WHERE owner_key = ?", owner).Scan(&encoded, &snapshot.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return AlgorithmSettings{Values: settings.Defaults()}, nil
	}
	if err != nil {
		return snapshot, fmt.Errorf("read algorithm settings: %w", err)
	}
	if err := json.Unmarshal([]byte(encoded), &snapshot.Values); err != nil {
		return AlgorithmSettings{}, fmt.Errorf("%w: algorithm settings JSON: %v", ErrCorruptData, err)
	}
	if err := snapshot.Values.Validate(); err != nil {
		return AlgorithmSettings{}, fmt.Errorf("%w: algorithm settings: %v", ErrCorruptData, err)
	}
	return snapshot, nil
}

// UpdateAlgorithmSettings atomically compares and replaces the complete snapshot.
func (db *DB) UpdateAlgorithmSettings(ctx context.Context, owner string, values settings.Values, expectedRevision int64) (AlgorithmSettings, error) {
	if err := values.Validate(); err != nil {
		return AlgorithmSettings{}, apperr.Wrap(apperr.InvalidArgument, err.Error(), err)
	}
	if expectedRevision < 0 {
		return AlgorithmSettings{}, apperr.New(apperr.InvalidArgument, "expectedRevision must be a non-negative integer")
	}
	// Encode empty step lists as arrays, including callers supplying nil slices.
	if values.LearningStepsMinutes == nil {
		values.LearningStepsMinutes = []float64{}
	}
	if values.RelearningStepsMinutes == nil {
		values.RelearningStepsMinutes = []float64{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return AlgorithmSettings{}, fmt.Errorf("encode algorithm settings: %w", err)
	}
	var revision int64
	if expectedRevision == 0 {
		err = db.sql.QueryRowContext(ctx, `
			INSERT INTO algorithm_settings(owner_key, values_json, revision) VALUES (?, ?, 1)
			ON CONFLICT(owner_key) DO NOTHING RETURNING revision
		`, owner, string(encoded)).Scan(&revision)
	} else {
		err = db.sql.QueryRowContext(ctx, `
			UPDATE algorithm_settings SET values_json = ?, revision = revision + 1
			WHERE owner_key = ? AND revision = ? RETURNING revision
		`, string(encoded), owner, expectedRevision).Scan(&revision)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AlgorithmSettings{}, apperr.New(apperr.Conflict, "Algorithm settings changed; reload before saving")
	}
	if err != nil {
		return AlgorithmSettings{}, fmt.Errorf("update algorithm settings: %w", err)
	}
	return AlgorithmSettings{Values: values, Revision: revision}, nil
}
