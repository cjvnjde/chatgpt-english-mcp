package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"english-learning-mcp/internal/domain"
)

type DictionarySnapshot struct {
	ID             string
	Provider       string
	NormalizedTerm string
	ParserVersion  int
	DatasetVersion string
	Data           domain.DictionarySnapshotData
	FetchedAt      time.Time
	ExpiresAt      time.Time
	Active         bool
}

type DictionarySnapshotInsert struct {
	Provider       string
	NormalizedTerm string
	ParserVersion  int
	DatasetVersion string
	Data           domain.DictionarySnapshotData
	FetchedAt      time.Time
	ExpiresAt      time.Time
}

func (db *DB) ActiveDictionarySnapshot(
	ctx context.Context,
	provider string,
	normalizedTerm string,
	datasetVersion string,
	parserVersion int,
) (*DictionarySnapshot, error) {
	row := db.sql.QueryRowContext(ctx, `
		SELECT id, provider, normalized_term, parser_version, dataset_version,
		       data_json, status, source_url, fetched_at, expires_at, active
		FROM dictionary_snapshots
		WHERE provider = ? AND normalized_term = ? AND dataset_version = ?
		  AND parser_version = ? AND active = 1
	`, provider, normalizedTerm, datasetVersion, parserVersion)
	return scanDictionarySnapshot(row)
}

func (db *DB) DictionarySnapshotByID(ctx context.Context, id string) (*DictionarySnapshot, error) {
	row := db.sql.QueryRowContext(ctx, `
		SELECT id, provider, normalized_term, parser_version, dataset_version,
		       data_json, status, source_url, fetched_at, expires_at, active
		FROM dictionary_snapshots
		WHERE id = ?
	`, id)
	return scanDictionarySnapshot(row)
}

func (db *DB) InsertDictionarySnapshot(ctx context.Context, input DictionarySnapshotInsert) (*DictionarySnapshot, error) {
	id, err := NewID()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(input.Data)
	if err != nil {
		return nil, fmt.Errorf("encode dictionary snapshot: %w", err)
	}

	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin dictionary snapshot transaction: %w", err)
	}
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx, `
		UPDATE dictionary_snapshots
		SET active = 0
		WHERE provider = ? AND normalized_term = ? AND dataset_version = ?
		  AND parser_version = ? AND active = 1
	`, input.Provider, input.NormalizedTerm, input.DatasetVersion, input.ParserVersion); err != nil {
		return nil, fmt.Errorf("deactivate dictionary snapshot: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO dictionary_snapshots(
			id, provider, normalized_term, status, source_url, parser_version,
			dataset_version, data_json, fetched_at, expires_at, active
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
	`,
		id,
		input.Provider,
		input.NormalizedTerm,
		input.Data.Status,
		input.Data.SourceURL,
		input.ParserVersion,
		input.DatasetVersion,
		string(encoded),
		TimeString(input.FetchedAt),
		TimeString(input.ExpiresAt),
	); err != nil {
		return nil, fmt.Errorf("insert dictionary snapshot: %w", err)
	}
	if len(input.Data.Entries) > 0 {
		if _, err := transaction.ExecContext(ctx, `
			UPDATE vocabulary_items
			SET lookup_id = ?
			WHERE normalized_term = ?
			  AND (
				lookup_id IS NULL
				OR lookup_id IN (
					SELECT id
					FROM dictionary_snapshots
					WHERE provider = ? AND normalized_term = ? AND dataset_version = ?
					  AND parser_version = ? AND id <> ?
				)
			  )
		`,
			id,
			input.NormalizedTerm,
			input.Provider,
			input.NormalizedTerm,
			input.DatasetVersion,
			input.ParserVersion,
			id,
		); err != nil {
			return nil, fmt.Errorf("refresh saved vocabulary lookups: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return nil, fmt.Errorf("commit dictionary snapshot: %w", err)
	}

	return &DictionarySnapshot{
		ID:             id,
		Provider:       input.Provider,
		NormalizedTerm: input.NormalizedTerm,
		ParserVersion:  input.ParserVersion,
		DatasetVersion: input.DatasetVersion,
		Data:           input.Data,
		FetchedAt:      input.FetchedAt.UTC(),
		ExpiresAt:      input.ExpiresAt.UTC(),
		Active:         true,
	}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDictionarySnapshot(row rowScanner) (*DictionarySnapshot, error) {
	var snapshot DictionarySnapshot
	var dataJSON string
	var status int
	var sourceURL string
	var fetchedAt string
	var expiresAt string
	var active int
	if err := row.Scan(
		&snapshot.ID,
		&snapshot.Provider,
		&snapshot.NormalizedTerm,
		&snapshot.ParserVersion,
		&snapshot.DatasetVersion,
		&dataJSON,
		&status,
		&sourceURL,
		&fetchedAt,
		&expiresAt,
		&active,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read dictionary snapshot: %w", err)
	}
	snapshot.Data.Status = status
	snapshot.Data.SourceURL = sourceURL
	if err := decodeDictionaryData(&snapshot, dataJSON, fetchedAt, expiresAt, active); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func decodeDictionaryData(snapshot *DictionarySnapshot, dataJSON, fetchedAt, expiresAt string, active int) error {
	status := snapshot.Data.Status
	sourceURL := snapshot.Data.SourceURL
	if err := json.Unmarshal([]byte(dataJSON), &snapshot.Data); err != nil {
		return fmt.Errorf("%w: dictionary snapshot %s", ErrCorruptData, snapshot.ID)
	}
	snapshot.Data.Status = status
	snapshot.Data.SourceURL = sourceURL
	normalizeDictionaryData(&snapshot.Data)

	var err error
	snapshot.FetchedAt, err = time.Parse(time.RFC3339Nano, fetchedAt)
	if err != nil {
		return fmt.Errorf("%w: dictionary snapshot %s fetched_at", ErrCorruptData, snapshot.ID)
	}
	snapshot.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return fmt.Errorf("%w: dictionary snapshot %s expires_at", ErrCorruptData, snapshot.ID)
	}
	snapshot.Active = active == 1
	return nil
}

func lookupFromSnapshot(snapshot *DictionarySnapshot, requestedTerm string) domain.DictionaryLookupResult {
	return domain.DictionaryLookupResult{
		LookupID:       snapshot.ID,
		RequestedTerm:  requestedTerm,
		NormalizedTerm: snapshot.NormalizedTerm,
		Cache: domain.DictionaryCache{
			State:     domain.CacheHit,
			FetchedAt: TimeString(snapshot.FetchedAt),
		},
		Source: domain.SourceRef{
			Provider:       snapshot.Provider,
			SourceURL:      snapshot.Data.SourceURL,
			DatasetVersion: snapshot.DatasetVersion,
			ParserVersion:  snapshot.ParserVersion,
		},
		Status:       snapshot.Data.Status,
		Entries:      snapshot.Data.Entries,
		Suggestions:  snapshot.Data.Suggestions,
		Images:       snapshot.Data.Images,
		Idioms:       snapshot.Data.Idioms,
		Collocations: snapshot.Data.Collocations,
	}
}

func normalizeDictionaryData(data *domain.DictionarySnapshotData) {
	if data.Entries == nil {
		data.Entries = []domain.DictionaryEntry{}
	}
	if data.Suggestions == nil {
		data.Suggestions = []string{}
	}
	if data.Images == nil {
		data.Images = []domain.DictionaryImage{}
	}
	for entryIndex := range data.Entries {
		entry := &data.Entries[entryIndex]
		if entry.Definitions == nil {
			entry.Definitions = []domain.DictionaryDefinition{}
		}
		for definitionIndex := range entry.Definitions {
			definition := &entry.Definitions[definitionIndex]
			if definition.Examples == nil {
				definition.Examples = []string{}
			}
			if definition.Phrases == nil {
				definition.Phrases = []string{}
			}
			if definition.SeeAlso == nil {
				definition.SeeAlso = []string{}
			}
			if definition.Images == nil {
				definition.Images = []domain.DictionaryImage{}
			}
			if definition.Labels == nil {
				definition.Labels = []string{}
			}
		}
	}
}
