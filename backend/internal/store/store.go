package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the SQLite database. All methods take a context and run
// transactional writes so multi-step operations commit or roll back together.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path.
// ":memory:" is supported for tests. Time strings are stored as fixed-width
// UTC RFC3339 so lexicographic order equals chronological order.
func Open(path string) (*Store, error) {
	if path == "" {
		path = "tododay.db"
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// A single connection serializes writers; SQLite allows many readers, but
	// task mutations are frequent and short-lived, so correctness first.
	db.SetMaxOpenConns(1)
	st := &Store{db: db}
	return st, nil
}

// DB exposes the underlying handle for migrations and tests.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

// timeLayout is a fixed-width UTC timestamp: lexicographic == chronological.
const timeLayout = "2006-01-02T15:04:05.000000000Z"

func nowUTC() time.Time { return time.Now().UTC() }

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ExecTx runs fn inside a transaction, committing on nil error.
func (s *Store) ExecTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
