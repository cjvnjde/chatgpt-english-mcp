package storage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type cursorEnvelope struct {
	Version int    `json:"v"`
	Kind    string `json:"k"`
	Sort    string `json:"s"`
	Filter  string `json:"f"`
	Primary string `json:"p"`
	ID      string `json:"i"`
}

func NewID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80

	encoded := make([]byte, 32)
	hex.Encode(encoded, bytes[:])
	return string(encoded[0:8]) + "-" + string(encoded[8:12]) + "-" + string(encoded[12:16]) + "-" + string(encoded[16:20]) + "-" + string(encoded[20:32]), nil
}

func TimeString(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func FilterDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode cursor filter: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func EncodeCursor(kind, sortOrder, filter, primary, id string) (string, error) {
	encoded, err := json.Marshal(cursorEnvelope{
		Version: 1,
		Kind:    kind,
		Sort:    sortOrder,
		Filter:  filter,
		Primary: primary,
		ID:      id,
	})
	if err != nil {
		return "", fmt.Errorf("encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeCursor(value, kind, sortOrder, filter string) (primary, id string, err error) {
	if value == "" {
		return "", "", nil
	}
	if len(value) > 4096 {
		return "", "", ErrInvalidCursor
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(value)
	if decodeErr != nil {
		return "", "", ErrInvalidCursor
	}

	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var cursor cursorEnvelope
	if decodeErr := decoder.Decode(&cursor); decodeErr != nil {
		return "", "", ErrInvalidCursor
	}
	if decodeErr := decoder.Decode(&struct{}{}); decodeErr != io.EOF {
		return "", "", ErrInvalidCursor
	}
	if cursor.Version != 1 || cursor.Kind != kind || cursor.Sort != sortOrder || cursor.Filter != filter || cursor.Primary == "" || cursor.ID == "" {
		return "", "", ErrInvalidCursor
	}
	return cursor.Primary, cursor.ID, nil
}
