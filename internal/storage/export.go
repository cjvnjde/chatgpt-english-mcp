package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"english-learning-mcp/internal/domain"
)

type VocabularySnapshotItem struct {
	SourceID   string                `json:"sourceId"`
	Vocabulary domain.VocabularyItem `json:"vocabulary"`
}

type VocabularySnapshot struct {
	SchemaVersion int                      `json:"schemaVersion"`
	Namespace     string                   `json:"namespace"`
	Owner         string                   `json:"owner"`
	Digest        string                   `json:"digest"`
	ItemCount     int                      `json:"itemCount"`
	Complete      bool                     `json:"complete"`
	Items         []VocabularySnapshotItem `json:"items"`
}

func (db *DB) ExportVocabulary(ctx context.Context, owner, namespace string) (VocabularySnapshot, error) {
	if strings.TrimSpace(owner) == "" || strings.TrimSpace(namespace) == "" {
		return VocabularySnapshot{}, fmt.Errorf("snapshot owner and namespace must not be empty")
	}
	transaction, err := db.sql.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return VocabularySnapshot{}, fmt.Errorf("begin vocabulary export: %w", err)
	}
	defer transaction.Rollback()

	rows, err := transaction.QueryContext(ctx, vocabularySelect+" WHERE v.owner_key = ? ORDER BY v.id ASC", owner)
	if err != nil {
		return VocabularySnapshot{}, fmt.Errorf("query vocabulary export: %w", err)
	}
	defer rows.Close()

	items := make([]VocabularySnapshotItem, 0)
	prefix := namespace + ":" + base64.RawURLEncoding.EncodeToString([]byte(owner)) + ":"
	for rows.Next() {
		item, err := scanVocabularyItem(rows)
		if err != nil {
			return VocabularySnapshot{}, err
		}
		if item.ItemID == "" || strings.TrimSpace(item.Term) == "" || strings.TrimSpace(item.NormalizedTerm) == "" || item.Tags == nil || item.Notes == nil || item.Examples == nil {
			return VocabularySnapshot{}, fmt.Errorf("%w: incomplete vocabulary export record", ErrCorruptData)
		}
		items = append(items, VocabularySnapshotItem{SourceID: prefix + item.ItemID, Vocabulary: item})
	}
	if err := rows.Err(); err != nil {
		return VocabularySnapshot{}, fmt.Errorf("iterate vocabulary export: %w", err)
	}
	if err := rows.Close(); err != nil {
		return VocabularySnapshot{}, fmt.Errorf("close vocabulary export rows: %w", err)
	}
	if err := transaction.Rollback(); err != nil {
		return VocabularySnapshot{}, fmt.Errorf("finish vocabulary export: %w", err)
	}

	encoded, err := json.Marshal(items)
	if err != nil {
		return VocabularySnapshot{}, fmt.Errorf("encode vocabulary export: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return VocabularySnapshot{
		SchemaVersion: 2,
		Namespace:     namespace,
		Owner:         owner,
		Digest:        hex.EncodeToString(digest[:]),
		ItemCount:     len(items),
		Complete:      true,
		Items:         items,
	}, nil
}
