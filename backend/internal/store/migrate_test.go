package store

import (
	"context"
	"strings"
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
	if version != 5 {
		t.Fatalf("expected latest version 5, got %d", version)
	}

	// 每个 migration 都要有对应的 down：漏了 down 会让 RollbackAll 只回滚
	// 一部分，留下半旧半新的库。
	names, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	ups, downs := map[string]bool{}, map[string]bool{}
	for _, e := range names {
		n := e.Name()
		if strings.HasSuffix(n, ".up.sql") {
			ups[strings.TrimSuffix(n, ".up.sql")] = true
		}
		if strings.HasSuffix(n, ".down.sql") {
			downs[strings.TrimSuffix(n, ".down.sql")] = true
		}
	}
	for base := range ups {
		if !downs[base] {
			t.Errorf("migration %s 缺少对应的 .down.sql", base)
		}
	}
	for base := range downs {
		if !ups[base] {
			t.Errorf("%s.down.sql 没有对应的 .up.sql", base)
		}
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
