package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound            = errors.New("record not found")
	ErrInvalidCursor       = errors.New("invalid cursor")
	ErrCorruptData         = errors.New("corrupt stored data")
	ErrArchived            = errors.New("vocabulary item is archived")
	ErrIdempotencyConflict = errors.New("review token conflicts with an existing attempt")
	ErrAmbiguous           = errors.New("more than one vocabulary sense matches")
)

type DB struct {
	sql *sql.DB
}

func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("SQLite path must not be empty")
	}
	filename := path
	query := make(url.Values)
	if strings.HasPrefix(path, "file:") {
		location, err := url.Parse(path)
		if err != nil {
			return nil, fmt.Errorf("parse SQLite URI: %w", err)
		}
		if location.User != nil || (location.Host != "" && location.Host != "localhost") {
			return nil, fmt.Errorf("SQLite URI must identify a local file")
		}
		query, err = url.ParseQuery(location.RawQuery)
		if err != nil {
			return nil, fmt.Errorf("parse SQLite URI parameters: %w", err)
		}
		filename = location.Path
		if location.Opaque != "" {
			filename, err = url.PathUnescape(location.Opaque)
			if err != nil {
				return nil, fmt.Errorf("decode SQLite filename: %w", err)
			}
		}
		if filename == "" {
			return nil, fmt.Errorf("SQLite URI must contain a filename")
		}
	}
	location := &url.URL{Scheme: "file", Opaque: filename}
	if filename != ":memory:" && query.Get("mode") != "memory" {
		absolutePath, err := filepath.Abs(filename)
		if err != nil {
			return nil, fmt.Errorf("resolve SQLite path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolutePath), 0o750); err != nil {
			return nil, fmt.Errorf("create SQLite directory: %w", err)
		}
		location = &url.URL{Scheme: "file", Path: absolutePath}
	}
	// Reapply connection-local settings after replacement connections, including
	// those discarded when a transaction's context is canceled.
	for _, pragma := range []string{"foreign_keys(ON)", "busy_timeout(5000)", "synchronous(NORMAL)"} {
		query.Add("_pragma", pragma)
	}
	// Reserve the writer before reading a mutation's snapshot. A deferred
	// read-to-write upgrade can fail immediately despite busy_timeout when
	// another connection has committed. ReadOnly transactions remain deferred.
	query.Set("_txlock", "immediate")
	location.RawQuery = query.Encode()
	dsn := location.String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	store := &DB{sql: database}
	if err := store.configure(ctx); err != nil {
		database.Close()
		return nil, err
	}
	if err := store.migrate(ctx); err != nil {
		database.Close()
		return nil, err
	}
	return store, nil
}

func (db *DB) Close() error {
	return db.sql.Close()
}

func (db *DB) configure(ctx context.Context) error {
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = NORMAL",
	}
	for _, statement := range pragmas {
		if _, err := db.sql.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}
	if err := db.sql.PingContext(ctx); err != nil {
		return fmt.Errorf("ping SQLite database: %w", err)
	}
	return nil
}
