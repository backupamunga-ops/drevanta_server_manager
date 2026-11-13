// Package storetest provides a real migrated SQLite store for sibling
// packages' tests (internal/metrics pins its sampler/alert/retention
// math against the live schema). It is only imported by test files.
package storetest

import (
	"context"
	"testing"

	"github.com/backupamunga-ops/drevanta_server_manager/internal/store"
)

// Open returns an opened, migrated store backed by a throwaway
// directory that is cleaned up with the test.
func Open(t testing.TB) *store.Store {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}
	return s
}
