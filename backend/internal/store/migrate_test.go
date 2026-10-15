package store

import (
	"context"
	"testing"
)

func TestMigrateAndRollback(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var version int
	if err := st.DB().QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 2 {
		t.Fatalf("expected latest version 2, got %d", version)
	}

	// Re-running migrations is a no-op.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	if err := st.RollbackAll(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var tables int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected all tables dropped, %d remain", tables)
	}

	// And migration can be applied again from scratch.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("re-migrate after rollback: %v", err)
	}
}
