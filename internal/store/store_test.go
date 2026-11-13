package store

import (
	"context"
	"testing"
)

func TestMigrateIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	v, err := s.CurrentVersion(ctx)
	if err != nil {
		t.Fatalf("current version: %v", err)
	}
	if v != latestVersion() {
		t.Fatalf("version = %d, want %d", v, latestVersion())
	}
}

func TestReadyAfterMigrate(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()

	if err := s.Ready(ctx); err == nil {
		t.Fatal("ready before migrate should fail")
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("ready after migrate: %v", err)
	}
}

func TestAuditInsertAndReadBack(t *testing.T) {
	s := openTestStore(t)
	defer s.Close()
	ctx := context.Background()

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := s.Audit(ctx, "drevantad", "daemon_started", "daemon", map[string]any{"version": "test"}); err != nil {
		t.Fatalf("audit: %v", err)
	}
	var actor, action, detail string
	err := s.db.QueryRow(`SELECT actor, action, detail FROM audit_events ORDER BY id DESC LIMIT 1`).
		Scan(&actor, &action, &detail)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if actor != "drevantad" || action != "daemon_started" {
		t.Fatalf("actor/action = %s/%s", actor, action)
	}
	if detail != `{"version":"test"}` {
		t.Fatalf("detail = %s", detail)
	}
}

func openTestStore(t *testing.T) *Store {
	return OpenTestStore(t)
}

// OpenTestStore is the exported test-store helper for sibling packages
// (internal/metrics tests need a real migrated store).
func OpenTestStore(t testing.TB) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
