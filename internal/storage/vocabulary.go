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

type SourceVersion struct {
	Provider       string
	ParserVersion  int
	DatasetVersion string
}

type VocabularyCreate struct {
	OwnerKey                string
	Term                    string
	NormalizedTerm          string
	LookupID                string
	Status                  domain.LearningStatus
	Tags                    []string
	CustomDescription       string
	DescriptionSource       *domain.DescriptionSource
	Notes                   []string
	Examples                []string
	SenseKey                string
	Context                 string
	SelectedEntryIndex      *int
	SelectedDefinitionIndex *int
	SelectedDefinition      *domain.DictionaryDefinition
	Now                     time.Time
}

type VocabularyUpdate struct {
	OwnerKey             string
	ItemID               string
	Status               *domain.LearningStatus
	Tags                 *[]string
	CustomDescription    *string
	SetDescriptionSource bool
	DescriptionSource    *domain.DescriptionSource
	Notes                *[]string
	Examples             *[]string
	Now                  time.Time
}

type VocabularyListQuery struct {
	OwnerKey             string
	Query                string
	Statuses             []domain.LearningStatus
	Tags                 []string
	HasLookup            *bool
	HasCustomDescription *bool
	Sort                 string
	Limit                int
	CursorPrimary        string
	CursorID             string
}

func (db *DB) SaveVocabulary(
	ctx context.Context,
	input VocabularyCreate,
) (created bool, item domain.VocabularyItem, err error) {
	itemID, err := NewID()
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	tagsJSON, err := encodeStringList(input.Tags, "vocabulary tags")
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	notesJSON, err := encodeStringList(input.Notes, "vocabulary notes")
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	examplesJSON, err := encodeStringList(input.Examples, "vocabulary examples")
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	descriptionSourceJSON, err := encodeOptionalJSON(input.DescriptionSource, "description source")
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}
	selectedDefinitionJSON, err := encodeOptionalDefinition(input.SelectedDefinition)
	if err != nil {
		return false, domain.VocabularyItem{}, err
	}

	timestamp := TimeString(input.Now)
	result, err := db.sql.ExecContext(ctx, `
		INSERT INTO vocabulary_items(
			id, owner_key, term, normalized_term, created_at, updated_at,
			lookup_id, custom_description, learning_status,
			description_source_json, notes_json, examples_json, tags_json,
			sense_key, context, selected_entry_index, selected_definition_index, selected_definition_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(owner_key, normalized_term, sense_key) DO NOTHING
	`,
		itemID,
		input.OwnerKey,
		input.Term,
		input.NormalizedTerm,
		timestamp,
		timestamp,
		sql.NullString{String: input.LookupID, Valid: input.LookupID != ""},
		input.CustomDescription,
		input.Status,
		descriptionSourceJSON,
		notesJSON,
		examplesJSON,
		tagsJSON,
		input.SenseKey,
		input.Context,
		input.SelectedEntryIndex,
		input.SelectedDefinitionIndex,
		selectedDefinitionJSON,
	)
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("save vocabulary item: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, domain.VocabularyItem{}, fmt.Errorf("read vocabulary save result: %w", err)
	}
	created = rowsAffected == 1
	if !created && input.LookupID != "" {
		if _, err := db.sql.ExecContext(ctx, `
			UPDATE vocabulary_items
			SET lookup_id = ?
			WHERE owner_key = ? AND normalized_term = ? AND lookup_id IS NULL
		`, input.LookupID, input.OwnerKey, input.NormalizedTerm); err != nil {
			return false, domain.VocabularyItem{}, fmt.Errorf("link existing vocabulary lookup: %w", err)
		}
	}

	item, err = db.VocabularyBySense(ctx, input.OwnerKey, input.NormalizedTerm, input.SenseKey)
	return created, item, err
}

func (db *DB) UpdateVocabulary(ctx context.Context, input VocabularyUpdate) (domain.VocabularyItem, error) {
	assignments := make([]string, 0, 7)
	arguments := make([]any, 0, 10)
	if input.Status != nil {
		assignments = append(assignments, "learning_status = ?")
		arguments = append(arguments, *input.Status)
	}
	if input.Tags != nil {
		encoded, err := encodeStringList(*input.Tags, "vocabulary tags")
		if err != nil {
			return domain.VocabularyItem{}, err
		}
		assignments = append(assignments, "tags_json = ?")
		arguments = append(arguments, encoded)
	}
	if input.CustomDescription != nil {
		assignments = append(assignments, "custom_description = ?")
		arguments = append(arguments, *input.CustomDescription)
	}
	if input.SetDescriptionSource {
		encoded, err := encodeOptionalJSON(input.DescriptionSource, "description source")
		if err != nil {
			return domain.VocabularyItem{}, err
		}
		assignments = append(assignments, "description_source_json = ?")
		arguments = append(arguments, encoded)
	}
	if input.Notes != nil {
		encoded, err := encodeStringList(*input.Notes, "vocabulary notes")
		if err != nil {
			return domain.VocabularyItem{}, err
		}
		assignments = append(assignments, "notes_json = ?")
		arguments = append(arguments, encoded)
	}
	if input.Examples != nil {
		encoded, err := encodeStringList(*input.Examples, "vocabulary examples")
		if err != nil {
			return domain.VocabularyItem{}, err
		}
		assignments = append(assignments, "examples_json = ?")
		arguments = append(arguments, encoded)
	}
	if len(assignments) == 0 {
		return domain.VocabularyItem{}, fmt.Errorf("vocabulary update has no changes")
	}

	assignments = append(assignments, "updated_at = ?")
	arguments = append(arguments, TimeString(input.Now), input.OwnerKey, input.ItemID)
	result, err := db.sql.ExecContext(ctx, `
		UPDATE vocabulary_items
		SET `+strings.Join(assignments, ", ")+`
		WHERE owner_key = ? AND id = ?
	`, arguments...)
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("update vocabulary item: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary update result: %w", err)
	}
	if rowsAffected == 0 {
		return domain.VocabularyItem{}, ErrNotFound
	}

	return db.VocabularyByID(ctx, input.OwnerKey, input.ItemID)
}

func (db *DB) VocabularyByID(ctx context.Context, ownerKey, itemID string) (domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ? AND v.id = ?"
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, ownerKey, itemID))
}

func (db *DB) VocabularyByTerm(ctx context.Context, ownerKey, normalizedTerm string) (domain.VocabularyItem, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, "SELECT count(*) FROM vocabulary_items WHERE owner_key = ? AND normalized_term = ?", ownerKey, normalizedTerm).Scan(&count); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("count vocabulary senses: %w", err)
	}
	if count == 0 {
		return domain.VocabularyItem{}, ErrNotFound
	}
	if count > 1 {
		return domain.VocabularyItem{}, ErrAmbiguous
	}
	query := vocabularySelect + " WHERE v.owner_key = ? AND v.normalized_term = ?"
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, ownerKey, normalizedTerm))
}

func (db *DB) VocabularyBySense(ctx context.Context, ownerKey, normalizedTerm, senseKey string) (domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ? AND v.normalized_term = ? AND v.sense_key = ?"
	return scanVocabularyItem(db.sql.QueryRowContext(ctx, query, ownerKey, normalizedTerm, senseKey))
}

func (db *DB) ListVocabulary(ctx context.Context, input VocabularyListQuery) ([]domain.VocabularyItem, error) {
	query := vocabularySelect + " WHERE v.owner_key = ?"
	arguments := []any{input.OwnerKey}
	if input.Query != "" {
		query += " AND instr(v.normalized_term, ?) > 0"
		arguments = append(arguments, input.Query)
	}
	if len(input.Statuses) > 0 {
		query += " AND v.learning_status IN (" + placeholders(len(input.Statuses)) + ")"
		for _, status := range input.Statuses {
			arguments = append(arguments, status)
		}
	}
	for _, tag := range input.Tags {
		query += ` AND EXISTS (
			SELECT 1 FROM json_each(v.tags_json) saved_tag WHERE saved_tag.value = ?
		)`
		arguments = append(arguments, tag)
	}
	if input.HasLookup != nil {
		if *input.HasLookup {
			query += " AND v.lookup_id IS NOT NULL"
		} else {
			query += " AND v.lookup_id IS NULL"
		}
	}
	if input.HasCustomDescription != nil {
		if *input.HasCustomDescription {
			query += " AND v.custom_description <> ''"
		} else {
			query += " AND v.custom_description = ''"
		}
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
		v.id, v.term, v.normalized_term, v.learning_status, v.tags_json,
		v.custom_description, v.description_source_json, v.notes_json,
		v.examples_json, v.context, v.selected_entry_index, v.selected_definition_index,
		v.selected_definition_json, v.created_at, v.updated_at,
		snapshot.id, snapshot.provider, snapshot.normalized_term,
		snapshot.parser_version, snapshot.dataset_version, snapshot.data_json,
		snapshot.status, snapshot.source_url, snapshot.fetched_at,
		snapshot.expires_at, snapshot.active
	FROM vocabulary_items v
	LEFT JOIN dictionary_snapshots snapshot ON snapshot.id = v.lookup_id`

func scanVocabularyItem(scanner rowScanner) (domain.VocabularyItem, error) {
	var item domain.VocabularyItem
	var tagsJSON string
	var descriptionSourceJSON sql.NullString
	var notesJSON string
	var examplesJSON string
	var contextValue string
	var entryIndex sql.NullInt64
	var definitionIndex sql.NullInt64
	var selectedDefinitionJSON sql.NullString
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
		&item.Status,
		&tagsJSON,
		&item.CustomDescription,
		&descriptionSourceJSON,
		&notesJSON,
		&examplesJSON,
		&contextValue,
		&entryIndex,
		&definitionIndex,
		&selectedDefinitionJSON,
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
	if !item.Status.Valid() {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary item %s learning_status", ErrCorruptData, item.ItemID)
	}
	if err := decodeJSON(tagsJSON, &item.Tags, item.ItemID, "tags"); err != nil {
		return domain.VocabularyItem{}, err
	}
	if err := decodeJSON(notesJSON, &item.Notes, item.ItemID, "notes"); err != nil {
		return domain.VocabularyItem{}, err
	}
	if err := decodeJSON(examplesJSON, &item.Examples, item.ItemID, "examples"); err != nil {
		return domain.VocabularyItem{}, err
	}
	if descriptionSourceJSON.Valid {
		item.DescriptionSource = &domain.DescriptionSource{}
		if err := decodeJSON(
			descriptionSourceJSON.String,
			item.DescriptionSource,
			item.ItemID,
			"description source",
		); err != nil {
			return domain.VocabularyItem{}, err
		}
	}
	if selectedDefinitionJSON.Valid {
		definition := domain.DictionaryDefinition{}
		if err := decodeJSON(selectedDefinitionJSON.String, &definition, item.ItemID, "selected definition"); err != nil {
			return domain.VocabularyItem{}, err
		}
		item.Sense = &domain.VocabularySense{Context: contextValue, EntryIndex: int(entryIndex.Int64), DefinitionIndex: int(definitionIndex.Int64), Definition: definition}
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
		if item.Sense != nil {
			for _, entry := range lookup.Entries {
				for _, definition := range entry.Definitions {
					if definition.Definition == item.Sense.Definition.Definition {
						item.Sense.Headword = entry.Headword
						item.Sense.PartOfSpeech = entry.PartOfSpeech
						item.Sense.Pronunciations = entry.Pronunciations
					}
				}
			}
		}
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

func encodeOptionalDefinition(value *domain.DictionaryDefinition) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode selected definition: %w", err)
	}
	return string(encoded), nil
}

func encodeJSON(value any, label string) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", label, err)
	}
	return string(encoded), nil
}

func encodeOptionalJSON(value *domain.DescriptionSource, label string) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := encodeJSON(value, label)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func encodeStringList(values []string, label string) (string, error) {
	if values == nil {
		values = []string{}
	}
	return encodeJSON(values, label)
}

func decodeJSON(encoded string, target any, itemID, label string) error {
	if err := json.Unmarshal([]byte(encoded), target); err != nil {
		return fmt.Errorf("%w: vocabulary item %s %s", ErrCorruptData, itemID, label)
	}
	return nil
}

func placeholders(count int) string {
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}
