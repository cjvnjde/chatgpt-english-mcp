package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"english-learning-mcp/internal/domain"
)

type SourceVersion struct {
	Provider       string
	ParserVersion  int
	DatasetVersion string
}

type VocabularyListQuery struct {
	OwnerKey       string
	Query          string
	CEFR           []domain.CEFRLevel
	HasExplanation *bool
	Sort           string
	Limit          int
	CursorPrimary  string
	CursorID       string
	CurrentSource  SourceVersion
}

func (db *DB) SaveVocabulary(
	ctx context.Context,
	ownerKey string,
	term string,
	normalizedTerm string,
	now time.Time,
	currentSource SourceVersion,
) (created bool, item domain.VocabularyItem, err error) {
	id, err := NewID()
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	timestamp := TimeString(now)
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO vocabulary_items(id, owner_key, term, normalized_term, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key, normalized_term) DO NOTHING
	`, id, ownerKey, term, normalizedTerm, timestamp, timestamp)
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("save vocabulary item: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("read vocabulary save result: %w", err)
	}

	item, err = db.VocabularyByTerm(ctx, ownerKey, normalizedTerm, currentSource)
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	return rowsAffected == 1, item, nil
}

func (db *DB) VocabularyByID(
	ctx context.Context,
	ownerKey string,
	itemID string,
	currentSource SourceVersion,
) (domain.VocabularyItem, error) {
	query, arguments := vocabularySelect(currentSource)
	query += " WHERE v.owner_key = ? AND v.id = ?"
	arguments = append(arguments, ownerKey, itemID)
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, arguments...))
}

func (db *DB) VocabularyByTerm(
	ctx context.Context,
	ownerKey string,
	normalizedTerm string,
	currentSource SourceVersion,
) (domain.VocabularyItem, error) {
	query, arguments := vocabularySelect(currentSource)
	query += " WHERE v.owner_key = ? AND v.normalized_term = ?"
	arguments = append(arguments, ownerKey, normalizedTerm)
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, arguments...))
}

func (db *DB) ListVocabulary(ctx context.Context, input VocabularyListQuery) ([]domain.VocabularyItem, error) {
	query, arguments := vocabularySelect(input.CurrentSource)
	query += " WHERE v.owner_key = ?"
	arguments = append(arguments, input.OwnerKey)

	if input.Query != "" {
		query += " AND instr(v.normalized_term, ?) > 0"
		arguments = append(arguments, input.Query)
	}
	if len(input.CEFR) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(input.CEFR)), ",")
		query += ` AND EXISTS (
			SELECT 1
			FROM explanations filter_explanation
			JOIN dictionary_snapshots filter_snapshot ON filter_snapshot.id = filter_explanation.lookup_id
			WHERE filter_explanation.owner_key = v.owner_key
			  AND filter_explanation.normalized_term = v.normalized_term
			  AND filter_snapshot.active = 1
			  AND filter_snapshot.provider = ?
			  AND filter_snapshot.parser_version = ?
			  AND filter_snapshot.dataset_version = ?
			  AND filter_explanation.cefr_level IN (` + placeholders + ")\n\t\t)"
		arguments = append(arguments, input.CurrentSource.Provider, input.CurrentSource.ParserVersion, input.CurrentSource.DatasetVersion)
		for _, level := range input.CEFR {
			arguments = append(arguments, string(level))
		}
	}
	if input.HasExplanation != nil {
		existsOperator := "EXISTS"
		if !*input.HasExplanation {
			existsOperator = "NOT EXISTS"
		}
		query += " AND " + existsOperator + ` (
			SELECT 1
			FROM explanations presence_explanation
			JOIN dictionary_snapshots presence_snapshot ON presence_snapshot.id = presence_explanation.lookup_id
			WHERE presence_explanation.owner_key = v.owner_key
			  AND presence_explanation.normalized_term = v.normalized_term
			  AND presence_snapshot.active = 1
			  AND presence_snapshot.provider = ?
			  AND presence_snapshot.parser_version = ?
			  AND presence_snapshot.dataset_version = ?
		)`
		arguments = append(arguments, input.CurrentSource.Provider, input.CurrentSource.ParserVersion, input.CurrentSource.DatasetVersion)
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

func (db *DB) TouchVocabulary(ctx context.Context, ownerKey, normalizedTerm string, now time.Time) error {
	_, err := db.sql.ExecContext(ctx, `
		UPDATE vocabulary_items SET updated_at = ?
		WHERE owner_key = ? AND normalized_term = ?
	`, TimeString(now), ownerKey, normalizedTerm)
	if err != nil {
		return fmt.Errorf("touch vocabulary item: %w", err)
	}
	return nil
}

func vocabularySelect(current SourceVersion) (string, []any) {
	query := `
		SELECT
			v.id,
			v.term,
			v.normalized_term,
			(
				SELECT COUNT(*)
				FROM explanations count_explanation
				JOIN dictionary_snapshots count_snapshot ON count_snapshot.id = count_explanation.lookup_id
				WHERE count_explanation.owner_key = v.owner_key
				  AND count_explanation.normalized_term = v.normalized_term
				  AND count_snapshot.active = 1
				  AND count_snapshot.provider = ?
				  AND count_snapshot.parser_version = ?
				  AND count_snapshot.dataset_version = ?
			),
			COALESCE((
				SELECT group_concat(DISTINCT cefr_explanation.cefr_level)
				FROM explanations cefr_explanation
				JOIN dictionary_snapshots cefr_snapshot ON cefr_snapshot.id = cefr_explanation.lookup_id
				WHERE cefr_explanation.owner_key = v.owner_key
				  AND cefr_explanation.normalized_term = v.normalized_term
				  AND cefr_explanation.cefr_level IS NOT NULL
				  AND cefr_snapshot.active = 1
				  AND cefr_snapshot.provider = ?
				  AND cefr_snapshot.parser_version = ?
				  AND cefr_snapshot.dataset_version = ?
			), ''),
			v.created_at,
			v.updated_at
		FROM vocabulary_items v`
	arguments := []any{
		current.Provider,
		current.ParserVersion,
		current.DatasetVersion,
		current.Provider,
		current.ParserVersion,
		current.DatasetVersion,
	}
	return query, arguments
}

func scanVocabularyItem(scanner rowScanner) (domain.VocabularyItem, error) {
	var item domain.VocabularyItem
	var levels string
	if err := scanner.Scan(
		&item.ItemID,
		&item.Term,
		&item.NormalizedTerm,
		&item.ExplanationCount,
		&levels,
		&item.CreatedAt,
		&item.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VocabularyItem{}, ErrNotFound
		}
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary item: %w", err)
	}
	if _, err := time.Parse(time.RFC3339Nano, item.CreatedAt); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s created_at", ErrCorruptData, item.ItemID)
	}
	if _, err := time.Parse(time.RFC3339Nano, item.UpdatedAt); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s updated_at", ErrCorruptData, item.ItemID)
	}

	item.CEFRLevels = make([]domain.CEFRLevel, 0)
	if levels != "" {
		for _, value := range strings.Split(levels, ",") {
			level := domain.CEFRLevel(value)
			if !level.Valid() {
				return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s CEFR level", ErrCorruptData, item.ItemID)
			}
			item.CEFRLevels = append(item.CEFRLevels, level)
		}
		sort.Slice(item.CEFRLevels, func(left, right int) bool {
			return cefrRank(item.CEFRLevels[left]) < cefrRank(item.CEFRLevels[right])
		})
	}
	return item, nil
}

func cefrRank(level domain.CEFRLevel) int {
	for index, candidate := range domain.CEFRLevels {
		if level == candidate {
			return index
		}
	}
	return len(domain.CEFRLevels)
}
