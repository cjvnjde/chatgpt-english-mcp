package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"english-learning-mcp/internal/domain"
)

const (
	maxStoredMediaBytes     = 10 << 20
	maxMediaSourceURLLength = 4096
	maxImageFilenameLength  = 255
	maxImageExampleLength   = 2000
	maxVocabularyImageCount = 12
)

const (
	MediaKindAudio = "audio"
	MediaKindImage = "image"
)

var (
	ErrInvalidMedia         = errors.New("invalid media")
	ErrVocabularyImageLimit = errors.New("vocabulary image limit reached")
)

var storedMediaTypes = map[string]struct{}{
	"audio/mpeg": {},
	"audio/mp4":  {},
	"audio/ogg":  {},
	"audio/wav":  {},
	"image/avif": {},
	"image/gif":  {},
	"image/jpeg": {},
	"image/png":  {},
	"image/webp": {},
}

type MediaObject struct {
	ID          string
	ContentType string
	ByteSize    int64
	Data        []byte
	SourceURL   string
	CreatedAt   time.Time
}

type MediaInsert struct {
	ContentType string
	Data        []byte
	SourceURL   string
	Now         time.Time
}

type VocabularyImageInsert struct {
	OwnerKey         string
	VocabularyItemID string
	ExpectedRevision int64
	Media            MediaInsert
	OriginalFilename string
	Example          string
}

func (db *DB) StoreMedia(ctx context.Context, input MediaInsert) (MediaObject, error) {
	if err := validateMediaInsert(input); err != nil {
		return MediaObject{}, err
	}
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return MediaObject{}, fmt.Errorf("begin media storage: %w", err)
	}
	defer transaction.Rollback()
	stored, err := storeMedia(ctx, transaction, input)
	if err != nil {
		return MediaObject{}, err
	}
	if err := transaction.Commit(); err != nil {
		return MediaObject{}, fmt.Errorf("commit media storage: %w", err)
	}
	return stored, nil
}

func (db *DB) MediaByID(ctx context.Context, id string) (MediaObject, error) {
	return scanMedia(db.sql.QueryRowContext(ctx, `
		SELECT id, content_type, byte_size, data, source_url, created_at
		FROM media_objects WHERE id = ?
	`, id))
}

func (db *DB) AttachVocabularyImage(ctx context.Context, input VocabularyImageInsert) (domain.VocabularyItem, error) {
	if input.ExpectedRevision <= 0 {
		return domain.VocabularyItem{}, fmt.Errorf("%w: expected revision must be positive", ErrInvalidMedia)
	}
	if err := validateMediaInsert(input.Media); err != nil {
		return domain.VocabularyItem{}, err
	}
	if !strings.HasPrefix(input.Media.ContentType, "image/") {
		return domain.VocabularyItem{}, fmt.Errorf("%w: vocabulary attachment must be an image", ErrInvalidMedia)
	}
	if !validMediaText(input.OriginalFilename, maxImageFilenameLength) || !validMediaText(input.Example, maxImageExampleLength) {
		return domain.VocabularyItem{}, fmt.Errorf("%w: invalid vocabulary image metadata", ErrInvalidMedia)
	}
	attachmentID, err := NewID()
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("begin vocabulary image attachment: %w", err)
	}
	defer transaction.Rollback()
	var revision, count int64
	if err := transaction.QueryRowContext(ctx, `
		SELECT edit_revision,
		       (SELECT COUNT(*) FROM vocabulary_images WHERE vocabulary_item_id = vocabulary_items.id)
		FROM vocabulary_items WHERE owner_key = ? AND id = ?
	`, input.OwnerKey, input.VocabularyItemID).Scan(&revision, &count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VocabularyItem{}, ErrNotFound
		}
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary image target: %w", err)
	}
	if revision != input.ExpectedRevision {
		return domain.VocabularyItem{}, ErrEditConflict
	}
	if count >= maxVocabularyImageCount {
		return domain.VocabularyItem{}, ErrVocabularyImageLimit
	}
	stored, err := storeMedia(ctx, transaction, input.Media)
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO vocabulary_images(
			id, owner_key, vocabulary_item_id, media_id, original_filename, example, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, attachmentID, input.OwnerKey, input.VocabularyItemID, stored.ID,
		input.OriginalFilename, input.Example, TimeString(input.Media.Now)); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("attach vocabulary image: %w", err)
	}
	item, err := scanVocabularyItem(transaction.QueryRowContext(ctx,
		vocabularySelect+" WHERE v.owner_key = ? AND v.id = ?", input.OwnerKey, input.VocabularyItemID))
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("commit vocabulary image attachment: %w", err)
	}
	return item, nil
}

func (db *DB) DeleteVocabularyImage(ctx context.Context, ownerKey, itemID, attachmentID string, expectedRevision int64) (domain.VocabularyItem, error) {
	if expectedRevision <= 0 {
		return domain.VocabularyItem{}, fmt.Errorf("%w: expected revision must be positive", ErrInvalidMedia)
	}
	transaction, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("begin vocabulary image deletion: %w", err)
	}
	defer transaction.Rollback()
	var revision int64
	if err := transaction.QueryRowContext(ctx,
		"SELECT edit_revision FROM vocabulary_items WHERE owner_key = ? AND id = ?", ownerKey, itemID,
	).Scan(&revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.VocabularyItem{}, ErrNotFound
		}
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary image target: %w", err)
	}
	if revision != expectedRevision {
		return domain.VocabularyItem{}, ErrEditConflict
	}
	result, err := transaction.ExecContext(ctx, `
		DELETE FROM vocabulary_images
		WHERE id = ? AND owner_key = ? AND vocabulary_item_id = ?
	`, attachmentID, ownerKey, itemID)
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("delete vocabulary image: %w", err)
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("read vocabulary image deletion: %w", err)
	}
	if deleted == 0 {
		return domain.VocabularyItem{}, ErrNotFound
	}
	item, err := scanVocabularyItem(transaction.QueryRowContext(ctx,
		vocabularySelect+" WHERE v.owner_key = ? AND v.id = ?", ownerKey, itemID))
	if err != nil {
		return domain.VocabularyItem{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.VocabularyItem{}, fmt.Errorf("commit vocabulary image deletion: %w", err)
	}
	return item, nil
}

func storeMedia(ctx context.Context, transaction *sql.Tx, input MediaInsert) (MediaObject, error) {
	digest := sha256.New()
	digest.Write([]byte(input.ContentType))
	digest.Write([]byte{0})
	digest.Write(input.Data)
	id := hex.EncodeToString(digest.Sum(nil))
	if _, err := transaction.ExecContext(ctx, `
		INSERT INTO media_objects(id, content_type, byte_size, data, source_url, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING
	`, id, input.ContentType, len(input.Data), input.Data, input.SourceURL, TimeString(input.Now)); err != nil {
		return MediaObject{}, fmt.Errorf("store media object: %w", err)
	}
	stored, err := scanMedia(transaction.QueryRowContext(ctx, `
		SELECT id, content_type, byte_size, data, source_url, created_at
		FROM media_objects WHERE id = ?
	`, id))
	if err != nil {
		return MediaObject{}, err
	}
	return stored, nil
}

func scanMedia(row rowScanner) (MediaObject, error) {
	var media MediaObject
	var createdAt string
	if err := row.Scan(&media.ID, &media.ContentType, &media.ByteSize, &media.Data, &media.SourceURL, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MediaObject{}, ErrNotFound
		}
		return MediaObject{}, fmt.Errorf("read media object: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil || int64(len(media.Data)) != media.ByteSize {
		return MediaObject{}, fmt.Errorf("%w: media object %s", ErrCorruptData, media.ID)
	}
	media.CreatedAt = parsed
	return media, nil
}

func validateMediaInsert(input MediaInsert) error {
	if _, ok := storedMediaTypes[input.ContentType]; !ok {
		return fmt.Errorf("%w: unsupported content type %q", ErrInvalidMedia, input.ContentType)
	}
	if len(input.Data) == 0 || len(input.Data) > maxStoredMediaBytes {
		return fmt.Errorf("%w: data must contain between 1 and %d bytes", ErrInvalidMedia, maxStoredMediaBytes)
	}
	if !validMediaText(input.SourceURL, maxMediaSourceURLLength) || input.Now.IsZero() {
		return fmt.Errorf("%w: invalid source or timestamp", ErrInvalidMedia)
	}
	return nil
}

func ValidatedMediaContentType(header string, contents []byte, kind string) (string, error) {
	declared, _, _ := mime.ParseMediaType(header)
	declared = canonicalMediaType(declared)
	detected := canonicalMediaType(http.DetectContentType(contents))
	if kind == MediaKindAudio && (declared == "audio/mp4" || detected == "video/mp4") {
		if isAudioMP4(contents) {
			return "audio/mp4", nil
		}
		return "", fmt.Errorf("%w: MP4 must contain a valid audio-only container", ErrInvalidMedia)
	}
	if supportedMediaType(detected, kind) {
		return detected, nil
	}
	if supportedMediaType(declared, kind) && detected == "application/octet-stream" {
		return declared, nil
	}
	return "", fmt.Errorf("%w: unsupported %s content type", ErrInvalidMedia, kind)
}

func canonicalMediaType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "audio/x-wav", "audio/wave", "audio/vnd.wave":
		return "audio/wav"
	case "image/jpg":
		return "image/jpeg"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func supportedMediaType(contentType, kind string) bool {
	if kind == MediaKindAudio {
		switch contentType {
		case "audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav":
			return true
		}
		return false
	}
	if kind != MediaKindImage {
		return false
	}
	switch contentType {
	case "image/avif", "image/gif", "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

func validMediaText(value string, maximum int) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00') && utf8.RuneCountInString(value) <= maximum
}
