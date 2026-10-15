package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate applies pending migrations in order and records them in
// schema_migrations. Each migration runs in its own transaction.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	ups := map[int64]string{}
	for _, e := range names {
		name := e.Name()
		if strings.HasSuffix(name, ".up.sql") {
			base := strings.TrimSuffix(name, ".up.sql")
			b, err := migrationFS.ReadFile("migrations/" + name)
			if err != nil {
				return err
			}
			v, err := migrationVersion(base)
			if err != nil {
				return err
			}
			ups[v] = string(b)
		}
	}
	versions := make([]int64, 0, len(ups))
	for v := range ups {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })

	for _, v := range versions {
		var exists int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, v).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		if err := s.ExecTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, ups[v]); err != nil {
				return fmt.Errorf("apply %d: %w", v, err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
				v, formatTime(nowUTC())); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// RollbackAll undoes every applied migration (newest first) and clears the
// version table. Destructive: it drops user data. Used for tests and for the
// documented rollback procedure.
func (s *Store) RollbackAll(ctx context.Context) error {
	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	downs := map[int64]string{}
	for _, e := range names {
		name := e.Name()
		if strings.HasSuffix(name, ".down.sql") {
			base := strings.TrimSuffix(name, ".down.sql")
			b, err := migrationFS.ReadFile("migrations/" + name)
			if err != nil {
				return err
			}
			v, err := migrationVersion(base)
			if err != nil {
				return err
			}
			downs[v] = string(b)
		}
	}
	versions := make([]int64, 0, len(downs))
	for v := range downs {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	for _, v := range versions {
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, v).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			continue
		}
		if err := s.ExecTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, downs[v]); err != nil {
				return fmt.Errorf("rollback %d: %w", v, err)
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = ?`, v); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `DROP TABLE IF EXISTS schema_migrations`)
	return err
}

// migrationVersion parses the leading numeric prefix of a migration base name
// such as "0001_init" → 1. Migrations must be named NNNN_description.sql.
func migrationVersion(base string) (int64, error) {
	digits := base
	if i := strings.IndexByte(base, '_'); i >= 0 {
		digits = base[:i]
	}
	var v int64
	if _, err := fmt.Sscanf(digits, "%d", &v); err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid migration name %q (want NNNN_name)", base)
	}
	return v, nil
}
