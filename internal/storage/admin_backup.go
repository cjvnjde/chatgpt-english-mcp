package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// AdminBackup creates a standalone consistent snapshot, including committed WAL data.
// The caller owns the returned file and must always call cleanup after streaming it.
func (db *DB) AdminBackup(ctx context.Context) (*os.File, func(), error) {
	directory, err := os.MkdirTemp("", "english-mcp-export-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create backup directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	path := filepath.Join(directory, "english-mcp.sqlite")
	if _, err := db.sql.ExecContext(ctx, "VACUUM main INTO ?", path); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("snapshot database: %w", err)
	}
	file, err := os.Open(path)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("open snapshot: %w", err)
	}
	return file, func() { _ = file.Close(); cleanup() }, nil
}
