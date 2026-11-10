package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type migration struct {
	version int
	name    string
	up      string
}

// migrations is the ordered, append-only list of schema versions.
// Never edit an applied migration; add a new entry instead.
var migrations = []migration{
	{
		version: 1,
		name:    "initial_audit_schema",
		up: `
CREATE TABLE IF NOT EXISTS audit_events (
	id     INTEGER PRIMARY KEY AUTOINCREMENT,
	ts     TEXT NOT NULL,              -- RFC3339Nano, UTC
	actor  TEXT NOT NULL,              -- who/what produced the event
	action TEXT NOT NULL,              -- machine-readable event name
	target TEXT NOT NULL DEFAULT '',  -- object the event applies to
	detail TEXT NOT NULL DEFAULT '{}' -- free-form JSON payload
);
CREATE INDEX IF NOT EXISTS idx_audit_events_ts     ON audit_events (ts);
CREATE INDEX IF NOT EXISTS idx_audit_events_action ON audit_events (action);
`,
	},
}

// latestVersion is the highest applied-for schema version.
func latestVersion() int {
	return migrations[len(migrations)-1].version
}

// Migrate applies all pending migrations. It is idempotent and safe to
// call on every start-up.
func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	name       TEXT NOT NULL,
	applied_at TEXT NOT NULL
)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	for _, m := range migrations {
		var count int
		err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, m.version).Scan(&count)
		if err != nil {
			return fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if count > 0 {
			continue
		}

		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, m.up); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %d (%s): %w", m.version, m.name, err)
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
			m.version, m.name, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}

// CurrentVersion returns the currently applied schema version.
func (s *Store) CurrentVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// Ready reports whether the store is reachable and fully migrated
// (used by GET /readyz).
func (s *Store) Ready(ctx context.Context) error {
	if err := s.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
	v, err := s.CurrentVersion(ctx)
	if err != nil {
		return fmt.Errorf("schema version: %w", err)
	}
	if v != latestVersion() {
		return fmt.Errorf("schema version %d, want %d", v, latestVersion())
	}
	return nil
}

// Audit appends one event to the audit log. detail may be nil.
func (s *Store) Audit(ctx context.Context, actor, action, target string, detail map[string]any) error {
	d := "{}"
	if detail != nil {
		b, err := json.Marshal(detail)
		if err != nil {
			d = fmt.Sprintf(`{"marshal_error":%q}`, err.Error())
		} else {
			d = string(b)
		}
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO audit_events (ts, actor, action, target, detail)
VALUES (?, ?, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano), actor, action, target, d)
	if err != nil {
		return fmt.Errorf("audit insert (%s/%s): %w", actor, action, err)
	}
	return nil
}
