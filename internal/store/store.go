// Package store implements the SQLite persistence layer: schema migrations
// and the audit log. It uses modernc.org/sqlite (pure Go, no CGO) so the
// daemon builds as a static binary without a C toolchain, which keeps the
// 512 MB-class deployment target simple and self-contained.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const (
	driverName = "sqlite"
	dbFileName = "drevanta.db"
	// A single connection avoids SQLITE_BUSY entirely for the Sprint 1
	// write pattern (rare lifecycle audit inserts + readiness pings).
	maxOpenConns = 1
)

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database file inside dir.
func Open(dir string) (*Store, error) {
	path := filepath.Join(dir, dbFileName)
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db.SetMaxOpenConns(maxOpenConns)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// Ping checks raw database connectivity (used by /readyz).
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
