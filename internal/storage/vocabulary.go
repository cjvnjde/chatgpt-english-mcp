package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"english-learning-mcp/internal/domain"
)

type SourceVersion struct {
	Provider       string
	ParserVersion  int
	DatasetVersion string
}

type VocabularyListQuery struct {
	OwnerKey      string
	Query         string
	Sort          string
	Limit         int
	CursorPrimary string
	CursorID      string
}

func (db *DB) SaveVocabulary(
	ctx context.Context,
	ownerKey string,
	term string,
	normalizedTerm string,
	lookupID string,
	customDescription *string,
	now time.Time,
) (created bool, item domain.VocabularyItem, err error) {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("begin vocabulary transaction: %w", err)
	}
	defer transaction.Rollback()

	var itemID string
	var savedLookupID string
	var savedDescription string
	err = transaction.QueryRowContext(ctx, `
		SELECT id, COALESCE(lookup_id, ''), custom_description
		FROM vocabulary_items
		WHERE owner_key = ? AND normalized_term = ?
	`, ownerKey, normalizedTerm).Scan(&itemID, &savedLookupID, &savedDescription)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		itemID, err = NewID()
		if err != nil {
			return false, domain.VocabularyItem{}, err
		}
		timestamp := TimeString(now)
		description := ""
		if customDescription != nil {
			description = *customDescription
		}
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO vocabulary_items(
				id, owner_key, term, normalized_term, created_at, updated_at,
				lookup_id, custom_description
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`,
			itemID,
			ownerKey,
			term,
			normalizedTerm,
			timestamp,
			timestamp,
			sql.NullString{String: lookupID, Valid: lookupID != ""},
			description,
		)
		created = true
	case err != nil:
		return false, domain.VocabularyItem{}, fmt.Errorf("find vocabulary item: %w", err)
	default:
		updatedLookupID := savedLookupID
		if lookupID != "" {
			updatedLookupID = lookupID
		}
		updatedDescription := savedDescription
		if customDescription != nil {
			updatedDescription = *customDescription
		}
		if updatedLookupID != savedLookupID || updatedDescription != savedDescription {
			_, err = transaction.ExecContext(ctx, `
				UPDATE vocabulary_items
				SET lookup_id = ?, custom_description = ?, updated_at = ?
				WHERE id = ? AND owner_key = ?
			`,
				sql.NullString{String: updatedLookupID, Valid: updatedLookupID != ""},
				updatedDescription,
				TimeString(now),
				itemID,
				ownerKey,
			)
		}
	}
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("save vocabulary item: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("commit vocabulary item: %w", err)
	}

	item, err = db.VocabularyByID(ctx, ownerKey, itemID)
	return created, item, err
}

func (db *DB) VocabularyByID(ctx context.Context, ownerKey, itemID string) (domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ? AND v.id = ?"
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, ownerKey, itemID))
}

func (db *DB) VocabularyByTerm(ctx context.Context, ownerKey, normalizedTerm string) (domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ? AND v.normalized_term = ?"
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, ownerKey, normalizedTerm))
}

func (db *DB) ListVocabulary(ctx context.Context, input VocabularyListQuery) ([]domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ?"
	arguments := []any{input.OwnerKey}
	if input.Query != "" {
		query += " AND instr(v.normalized_term, ?) > 0"
		arguments = append(arguments, input.Query)
	}

	switch input.Sort {
	case "recent":
		if input.CursorPrimary != "" {
			query += " AND (v.updated_at < ? OR (v.updated_at = ? AND v.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY v.updated_at DESC, v.id ASC"
	case "oldest":
		if input.CursorPrimary != "" {
			query += " AND (v.updated_at > ? OR (v.updated_at = ? AND v.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY v.updated_at ASC, v.id ASC"
	case "alphabetical":
		if input.CursorPrimary != "" {
			query += " AND (v.normalized_term > ? OR (v.normalized_term = ? AND v.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY v.normalized_term ASC, v.id ASC"
	default:
		return nil, fmt.Errorf("unsupported vocabulary sort %q", input.Sort)
	}
	query += " LIMIT ?"
	arguments = append(arguments, input.Limit)

	rows, err := db.sql.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list vocabulary items: %w", err)
	}
	defer rows.Close()

	items := make([]domain.VocabularyItem, 0, input.Limit)
	for rows.Next() {
		item, err := scanVocabularyItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate vocabulary items: %w", err)
	}
	return items, nil
}

func (db *DB) DeleteVocabulary(ctx context.Context, ownerKey, itemID string) error {
	result, err := db.sql.ExecContext(ctx, "DELETE FROM vocabulary_items WHERE owner_key = ? AND id = ?", ownerKey, itemID)
	if err != nil {
		return fmt.Errorf("delete vocabulary item: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read vocabulary delete result: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

const vocabularySelect = `
	SELECT
		v.id, v.term, v.normalized_term, v.custom_description,
		v.created_at, v.updated_at,
		snapshot.id, snapshot.provider, snapshot.normalized_term,
		snapshot.parser_version, snapshot.dataset_version, snapshot.data_json,
		snapshot.status, snapshot.source_url, snapshot.fetched_at,
		snapshot.expires_at, snapshot.active
	FROM vocabulary_items v
	LEFT JOIN dictionary_snapshots snapshot ON snapshot.id = v.lookup_id`

func scanVocabularyItem(scanner rowScanner) (domain.VocabularyItem, error) {
	var item domain.VocabularyItem
	var snapshotID sql.NullString
	var provider sql.NullString
	var snapshotTerm sql.NullString
	var parserVersion sql.NullInt64
	var datasetVersion sql.NullString
	var dataJSON sql.NullString
	var status sql.NullInt64
	var sourceURL sql.NullString
	var createdAt string
	var updatedAt string
	var fetchedAt sql.NullString
	var expiresAt sql.NullString
	var active sql.NullInt64
	if err := scanner.Scan(
		&item.ItemID,
		&item.Term,
		&item.NormalizedTerm,
		&item.CustomDescription,
		&createdAt,
		&updatedAt,
		&snapshotID,
		&provider,
		&snapshotTerm,
		&parserVersion,
		&datasetVersion,
		&dataJSON,
		&status,
		&sourceURL,
		&fetchedAt,
		&expiresAt,
		&active,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VocabularyItem{}, ErrNotFound
		}
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary item: %w", err)
	}
	if snapshotID.Valid {
		snapshot := DictionarySnapshot{
			ID:             snapshotID.String,
			Provider:       provider.String,
			NormalizedTerm: snapshotTerm.String,
			ParserVersion:  int(parserVersion.Int64),
			DatasetVersion: datasetVersion.String,
			Data: domain.DictionarySnapshotData{
				Status:    int(status.Int64),
				SourceURL: sourceURL.String,
			},
		}
		if err := decodeDictionaryData(
			&snapshot,
			dataJSON.String,
			fetchedAt.String,
			expiresAt.String,
			int(active.Int64),
		); err != nil {
			return domain.VocabularyItem{}, err
		}
		lookup := lookupFromSnapshot(&snapshot, item.Term)
		item.Lookup = &lookup
	}
	if _, err := time.Parse(time.RFC3339Nano, createdAt); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s created_at", ErrCorruptData, item.ItemID)
	}
	if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s updated_at", ErrCorruptData, item.ItemID)
	}

	item.CreatedAt = createdAt
	item.UpdatedAt = updatedAt
	return item, nil
}
