package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"english-learning-mcp/internal/domain"
)

type ExplanationRecord struct {
	ID                      string
	OwnerKey                string
	Term                    string
	NormalizedTerm          string
	Context                 string
	NormalizedContext       string
	LookupID                string
	SelectedEntryIndex      *int
	SelectedDefinitionIndex *int
	Learner                 domain.LearnerContent
	CEFR                    *domain.CEFR
	LexicalRelations        *domain.LexicalRelations
	Generator               domain.Generator
	CreatedAt               time.Time
	UpdatedAt               time.Time
	Snapshot                *DictionarySnapshot
	Stale                   bool
}

type ExplanationUpsert struct {
	OwnerKey                string
	Term                    string
	NormalizedTerm          string
	Context                 string
	NormalizedContext       string
	LookupID                string
	SelectedEntryIndex      *int
	SelectedDefinitionIndex *int
	Learner                 domain.LearnerContent
	CEFR                    *domain.CEFR
	LexicalRelations        *domain.LexicalRelations
	Generator               domain.Generator
	Now                     time.Time
	CurrentSource           SourceVersion
}

type ExplanationListQuery struct {
	OwnerKey       string
	NormalizedTerm string
	CEFR           []domain.CEFRLevel
	OnlySaved      bool
	IncludeStale   bool
	Sort           string
	Limit          int
	CursorPrimary  string
	CursorID       string
	CurrentSource  SourceVersion
}

func (db *DB) UpsertExplanation(ctx context.Context, input ExplanationUpsert) (bool, *ExplanationRecord, error) {
	learnerJSON, err := json.Marshal(input.Learner)
	if err != nil {
		return false, nil, fmt.Errorf("encode learner explanation: %w", err)
	}
	cefrJSON, cefrLevel, err := nullableJSON(input.CEFR)
	if err != nil {
		return false, nil, fmt.Errorf("encode CEFR: %w", err)
	}
	if input.CEFR != nil {
		cefrLevel = string(input.CEFR.Level)
	}
	lexicalJSON, _, err := nullableJSON(input.LexicalRelations)
	if err != nil {
		return false, nil, fmt.Errorf("encode lexical relations: %w", err)
	}

	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, fmt.Errorf("begin explanation transaction: %w", err)
	}
	defer transaction.Rollback()

	var id string
	created := false
	err = transaction.QueryRowContext(ctx, `
		SELECT id
		FROM explanations
		WHERE owner_key = ? AND normalized_term = ? AND normalized_context = ?
		  AND lookup_id = ? AND generator_name = ? AND generator_version = ?
	`,
		input.OwnerKey,
		input.NormalizedTerm,
		input.NormalizedContext,
		input.LookupID,
		input.Generator.Name,
		input.Generator.Version,
	).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id, err = NewID()
		if err != nil {
			return false, nil, err
		}
		created = true
		_, err = transaction.ExecContext(ctx, `
			INSERT INTO explanations(
				id, owner_key, term, normalized_term, context, normalized_context,
				lookup_id, selected_entry_index, selected_definition_index,
				learner_json, cefr_json, cefr_level, lexical_relations_json,
				generator_name, generator_model, generator_version, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`,
			id,
			input.OwnerKey,
			input.Term,
			input.NormalizedTerm,
			input.Context,
			input.NormalizedContext,
			input.LookupID,
			input.SelectedEntryIndex,
			input.SelectedDefinitionIndex,
			string(learnerJSON),
			cefrJSON,
			nullableString(cefrLevel),
			lexicalJSON,
			input.Generator.Name,
			input.Generator.Model,
			input.Generator.Version,
			TimeString(input.Now),
			TimeString(input.Now),
		)
	case err != nil:
		return false, nil, fmt.Errorf("find explanation upsert target: %w", err)
	default:
		_, err = transaction.ExecContext(ctx, `
			UPDATE explanations
			SET term = ?, context = ?, selected_entry_index = ?, selected_definition_index = ?,
			    learner_json = ?, cefr_json = ?, cefr_level = ?, lexical_relations_json = ?,
			    generator_model = ?, updated_at = ?
			WHERE id = ? AND owner_key = ?
		`,
			input.Term,
			input.Context,
			input.SelectedEntryIndex,
			input.SelectedDefinitionIndex,
			string(learnerJSON),
			cefrJSON,
			nullableString(cefrLevel),
			lexicalJSON,
			input.Generator.Model,
			TimeString(input.Now),
			id,
			input.OwnerKey,
		)
	}
	if err != nil {
		return false, nil, fmt.Errorf("write explanation: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE vocabulary_items SET updated_at = ?
		WHERE owner_key = ? AND normalized_term = ?
	`, TimeString(input.Now), input.OwnerKey, input.NormalizedTerm); err != nil {
		return false, nil, fmt.Errorf("touch explanation vocabulary item: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return false, nil, fmt.Errorf("commit explanation: %w", err)
	}

	record, err := db.ExplanationByID(ctx, input.OwnerKey, id, input.CurrentSource)
	if err != nil {
		return false, nil, err
	}
	return created, record, nil
}

func (db *DB) ExplanationByID(
	ctx context.Context,
	ownerKey string,
	explanationID string,
	currentSource SourceVersion,
) (*ExplanationRecord, error) {
	query, arguments := explanationSelect(currentSource)
	query += " WHERE e.owner_key = ? AND e.id = ?"
	arguments = append(arguments, ownerKey, explanationID)
	return scanExplanationRecord(db.sql.QueryRowContext(ctx, query, arguments...))
}

func (db *DB) ExplanationByNaturalKey(
	ctx context.Context,
	ownerKey string,
	normalizedTerm string,
	normalizedContext string,
	generatorName string,
	generatorVersion string,
	currentOnly bool,
	currentSource SourceVersion,
) (*ExplanationRecord, error) {
	query, arguments := explanationSelect(currentSource)
	query += ` WHERE e.owner_key = ? AND e.normalized_term = ? AND e.normalized_context = ?
		AND e.generator_name = ? AND e.generator_version = ?`
	arguments = append(arguments, ownerKey, normalizedTerm, normalizedContext, generatorName, generatorVersion)
	if currentOnly {
		query += ` AND snapshot.active = 1 AND snapshot.provider = ?
			AND snapshot.parser_version = ? AND snapshot.dataset_version = ?`
		arguments = append(arguments, currentSource.Provider, currentSource.ParserVersion, currentSource.DatasetVersion)
	}
	query += " ORDER BY e.updated_at DESC, e.id ASC LIMIT 1"
	return scanExplanationRecord(db.sql.QueryRowContext(ctx, query, arguments...))
}

func (db *DB) ListExplanations(ctx context.Context, input ExplanationListQuery) ([]*ExplanationRecord, error) {
	query, arguments := explanationSelect(input.CurrentSource)
	query += " WHERE e.owner_key = ?"
	arguments = append(arguments, input.OwnerKey)
	if input.NormalizedTerm != "" {
		query += " AND e.normalized_term = ?"
		arguments = append(arguments, input.NormalizedTerm)
	}
	if len(input.CEFR) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(input.CEFR)), ",")
		query += " AND e.cefr_level IN (" + placeholders + ")"
		for _, level := range input.CEFR {
			arguments = append(arguments, string(level))
		}
	}
	if input.OnlySaved {
		query += ` AND EXISTS (
			SELECT 1 FROM vocabulary_items saved
			WHERE saved.owner_key = e.owner_key AND saved.normalized_term = e.normalized_term
		)`
	}
	if !input.IncludeStale {
		query += ` AND snapshot.active = 1 AND snapshot.provider = ?
			AND snapshot.parser_version = ? AND snapshot.dataset_version = ?`
		arguments = append(arguments, input.CurrentSource.Provider, input.CurrentSource.ParserVersion, input.CurrentSource.DatasetVersion)
	}

	switch input.Sort {
	case "recent":
		if input.CursorPrimary != "" {
			query += " AND (e.updated_at < ? OR (e.updated_at = ? AND e.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY e.updated_at DESC, e.id ASC"
	case "oldest":
		if input.CursorPrimary != "" {
			query += " AND (e.updated_at > ? OR (e.updated_at = ? AND e.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY e.updated_at ASC, e.id ASC"
	case "alphabetical":
		if input.CursorPrimary != "" {
			query += " AND (e.normalized_term > ? OR (e.normalized_term = ? AND e.id > ?))"
			arguments = append(arguments, input.CursorPrimary, input.CursorPrimary, input.CursorID)
		}
		query += " ORDER BY e.normalized_term ASC, e.id ASC"
	default:
		return nil, fmt.Errorf("unsupported explanation sort %q", input.Sort)
	}
	if input.Limit > 0 {
		query += " LIMIT ?"
		arguments = append(arguments, input.Limit)
	}

	rows, err := db.sql.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list explanations: %w", err)
	}
	defer rows.Close()

	records := make([]*ExplanationRecord, 0)
	for rows.Next() {
		record, err := scanExplanationRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate explanations: %w", err)
	}
	return records, nil
}

func (db *DB) DeleteExplanation(ctx context.Context, ownerKey, explanationID string, now time.Time) error {
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin explanation delete transaction: %w", err)
	}
	defer transaction.Rollback()

	var normalizedTerm string
	if err := transaction.QueryRowContext(ctx, `
		SELECT normalized_term FROM explanations WHERE owner_key = ? AND id = ?
	`, ownerKey, explanationID).Scan(&normalizedTerm); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("find explanation for delete: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM explanations WHERE owner_key = ? AND id = ?", ownerKey, explanationID); err != nil {
		return fmt.Errorf("delete explanation: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `
		UPDATE vocabulary_items SET updated_at = ?
		WHERE owner_key = ? AND normalized_term = ?
	`, TimeString(now), ownerKey, normalizedTerm); err != nil {
		return fmt.Errorf("touch vocabulary after explanation delete: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit explanation delete: %w", err)
	}
	return nil
}

func explanationSelect(current SourceVersion) (string, []any) {
	query := `
		SELECT
			e.id, e.owner_key, e.term, e.normalized_term, e.context, e.normalized_context,
			e.lookup_id, e.selected_entry_index, e.selected_definition_index,
			e.learner_json, e.cefr_json, e.lexical_relations_json,
			e.generator_name, e.generator_model, e.generator_version,
			e.created_at, e.updated_at,
			snapshot.id, snapshot.provider, snapshot.normalized_term, snapshot.parser_version,
			snapshot.dataset_version, snapshot.data_json, snapshot.status, snapshot.source_url,
			snapshot.fetched_at, snapshot.expires_at, snapshot.active,
			CASE WHEN snapshot.active = 1 AND snapshot.provider = ?
			          AND snapshot.parser_version = ? AND snapshot.dataset_version = ?
			     THEN 0 ELSE 1 END
		FROM explanations e
		JOIN dictionary_snapshots snapshot ON snapshot.id = e.lookup_id`
	return query, []any{current.Provider, current.ParserVersion, current.DatasetVersion}
}

func scanExplanationRecord(scanner rowScanner) (*ExplanationRecord, error) {
	var record ExplanationRecord
	var selectedEntry sql.NullInt64
	var selectedDefinition sql.NullInt64
	var learnerJSON string
	var cefrJSON sql.NullString
	var lexicalJSON sql.NullString
	var createdAt string
	var updatedAt string
	var snapshotDataJSON string
	var snapshotStatus int
	var snapshotSourceURL string
	var snapshotFetchedAt string
	var snapshotExpiresAt string
	var snapshotActive int
	var stale int
	snapshot := &DictionarySnapshot{}

	if err := scanner.Scan(
		&record.ID,
		&record.OwnerKey,
		&record.Term,
		&record.NormalizedTerm,
		&record.Context,
		&record.NormalizedContext,
		&record.LookupID,
		&selectedEntry,
		&selectedDefinition,
		&learnerJSON,
		&cefrJSON,
		&lexicalJSON,
		&record.Generator.Name,
		&record.Generator.Model,
		&record.Generator.Version,
		&createdAt,
		&updatedAt,
		&snapshot.ID,
		&snapshot.Provider,
		&snapshot.NormalizedTerm,
		&snapshot.ParserVersion,
		&snapshot.DatasetVersion,
		&snapshotDataJSON,
		&snapshotStatus,
		&snapshotSourceURL,
		&snapshotFetchedAt,
		&snapshotExpiresAt,
		&snapshotActive,
		&stale,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("read explanation: %w", err)
	}
	if selectedEntry.Valid != selectedDefinition.Valid {
		return nil, fmt.Errorf("%w: explanation %s selected indexes", ErrCorruptData, record.ID)
	}
	if selectedEntry.Valid {
		entryIndex := int(selectedEntry.Int64)
		definitionIndex := int(selectedDefinition.Int64)
		record.SelectedEntryIndex = &entryIndex
		record.SelectedDefinitionIndex = &definitionIndex
	}
	if err := json.Unmarshal([]byte(learnerJSON), &record.Learner); err != nil {
		return nil, fmt.Errorf("%w: explanation %s learner data", ErrCorruptData, record.ID)
	}
	normalizeLearnerContent(&record.Learner)
	if cefrJSON.Valid {
		record.CEFR = &domain.CEFR{}
		if err := json.Unmarshal([]byte(cefrJSON.String), record.CEFR); err != nil {
			return nil, fmt.Errorf("%w: explanation %s CEFR data", ErrCorruptData, record.ID)
		}
	}
	if lexicalJSON.Valid {
		record.LexicalRelations = &domain.LexicalRelations{}
		if err := json.Unmarshal([]byte(lexicalJSON.String), record.LexicalRelations); err != nil {
			return nil, fmt.Errorf("%w: explanation %s lexical data", ErrCorruptData, record.ID)
		}
		if record.LexicalRelations.Synonyms == nil {
			record.LexicalRelations.Synonyms = []string{}
		}
		if record.LexicalRelations.Antonyms == nil {
			record.LexicalRelations.Antonyms = []string{}
		}
	}

	var err error
	record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return nil, fmt.Errorf("%w: explanation %s created_at", ErrCorruptData, record.ID)
	}
	record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: explanation %s updated_at", ErrCorruptData, record.ID)
	}
	if err := json.Unmarshal([]byte(snapshotDataJSON), &snapshot.Data); err != nil {
		return nil, fmt.Errorf("%w: dictionary snapshot %s", ErrCorruptData, snapshot.ID)
	}
	snapshot.Data.Status = snapshotStatus
	snapshot.Data.SourceURL = snapshotSourceURL
	normalizeDictionaryData(&snapshot.Data)
	snapshot.FetchedAt, err = time.Parse(time.RFC3339Nano, snapshotFetchedAt)
	if err != nil {
		return nil, fmt.Errorf("%w: dictionary snapshot %s fetched_at", ErrCorruptData, snapshot.ID)
	}
	snapshot.ExpiresAt, err = time.Parse(time.RFC3339Nano, snapshotExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("%w: dictionary snapshot %s expires_at", ErrCorruptData, snapshot.ID)
	}
	snapshot.Active = snapshotActive == 1
	record.Snapshot = snapshot
	record.Stale = stale == 1
	return &record, nil
}

func nullableJSON(value any) (jsonValue any, scalar string, err error) {
	if value == nil {
		return nil, "", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", err
	}
	return string(encoded), "", nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func normalizeLearnerContent(content *domain.LearnerContent) {
	if content.Notes == nil {
		content.Notes = []string{}
	}
	if content.Examples == nil {
		content.Examples = []string{}
	}
	if content.Alternatives == nil {
		content.Alternatives = []domain.LearnerAlternative{}
	}
}
