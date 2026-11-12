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
	{
		version: 2,
		name:    "identity_onboarding_schema",
		up: `
-- Operator identity (Sprint 2 onboarding). Exactly one administrator is
-- created by the first-owner claim flow in this sprint.
CREATE TABLE IF NOT EXISTS users (
        id            INTEGER PRIMARY KEY AUTOINCREMENT,
        username      TEXT NOT NULL UNIQUE,
        password_hash TEXT NOT NULL,          -- argon2id PHC string
        mfa_secret    TEXT NOT NULL DEFAULT '', -- base32 TOTP secret, empty until setup
        mfa_enabled   INTEGER NOT NULL DEFAULT 0,
        mfa_last_step INTEGER NOT NULL DEFAULT -1, -- replay guard: last accepted step
        created_at    TEXT NOT NULL,
        updated_at    TEXT NOT NULL
);

-- Singleton row (id = 1) tracking the first-owner onboarding state machine:
--   unclaimed -> mfa_pending -> active
CREATE TABLE IF NOT EXISTS instance_state (
        id             INTEGER PRIMARY KEY CHECK (id = 1),
        status         TEXT NOT NULL DEFAULT 'unclaimed',
        admin_user_id  INTEGER REFERENCES users(id),
        mfa_mode       TEXT NOT NULL DEFAULT '', -- 'totp' | 'skipped' | ''
        claimed_at     TEXT NOT NULL DEFAULT '',
        mfa_decided_at TEXT NOT NULL DEFAULT '',
        updated_at     TEXT NOT NULL
);

-- First-owner claim tokens: only the SHA-256 hash is ever stored.
-- A token is single-use: consumed_at / revoked_at are set on first use
-- or explicit revocation; expired tokens are never accepted.
CREATE TABLE IF NOT EXISTS claim_tokens (
        id          INTEGER PRIMARY KEY AUTOINCREMENT,
        token_hash  TEXT NOT NULL UNIQUE,
        created_at  TEXT NOT NULL,
        expires_at  TEXT NOT NULL,
        consumed_at TEXT NOT NULL DEFAULT '',
        revoked_at  TEXT NOT NULL DEFAULT ''
);

-- Browser sessions. Only the SHA-256 hash of the cookie value is stored.
-- expires_at slides on activity; absolute_at is the hard lifetime cap.
CREATE TABLE IF NOT EXISTS sessions (
        id           INTEGER PRIMARY KEY AUTOINCREMENT,
        token_hash   TEXT NOT NULL UNIQUE,
        user_id      INTEGER NOT NULL REFERENCES users(id),
        scope        TEXT NOT NULL,          -- 'onboarding' | 'full'
        created_at   TEXT NOT NULL,
        absolute_at  TEXT NOT NULL,
        expires_at   TEXT NOT NULL,
        last_seen_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions (user_id);

-- Single-use MFA recovery codes (hashed; shown to the operator once).
CREATE TABLE IF NOT EXISTS recovery_codes (
        id        INTEGER PRIMARY KEY AUTOINCREMENT,
        user_id   INTEGER NOT NULL REFERENCES users(id),
        code_hash TEXT NOT NULL,
        used_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_recovery_codes_user ON recovery_codes (user_id);
`,
	},
	{
		version: 3,
		name:    "user_email",
		up: `
-- Optional account email (login identifier + password-reset target).
-- Empty means unset. Uniqueness is enforced case-insensitively over
-- non-empty values only (partial unique index).
ALTER TABLE users ADD COLUMN email TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users (lower(email)) WHERE email != '';
`,
	},
	{
		version: 4,
		name:    "smtp_and_password_resets",
		up: `
-- Singleton mail-delivery configuration (v0.4.17). The password is a
-- stored secret: the DB file (drevanta:drevanta, 0640) is the boundary
-- and the API never echoes it back, only reports password_set.
CREATE TABLE IF NOT EXISTS smtp_config (
        id         INTEGER PRIMARY KEY CHECK (id = 1),
        enabled    INTEGER NOT NULL DEFAULT 0,
        host       TEXT NOT NULL DEFAULT '',
        port       INTEGER NOT NULL DEFAULT 587,
        username   TEXT NOT NULL DEFAULT '',
        password   TEXT NOT NULL DEFAULT '',
        from_addr  TEXT NOT NULL DEFAULT '',
        updated_at TEXT NOT NULL DEFAULT ''
);

-- Single-use, expiring password-reset tokens. Only the SHA-256 hash is
-- stored; a new request invalidates the previous unconsumed token.
CREATE TABLE IF NOT EXISTS password_resets (
        id          INTEGER PRIMARY KEY AUTOINCREMENT,
        token_hash  TEXT NOT NULL UNIQUE,
        user_id     INTEGER NOT NULL REFERENCES users(id),
        created_at  TEXT NOT NULL,
        expires_at  TEXT NOT NULL,
        consumed_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_password_resets_user ON password_resets (user_id);
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

// AuditEvent is one read-back row of the audit log.
type AuditEvent struct {
	ID     int64
	TS     time.Time
	Actor  string
	Action string
	Target string
	Detail string // raw JSON object
}

// RecentAuditEvents returns up to limit events, newest first, filtered
// to one action when action is non-empty. Read-only diagnostics for
// tests and the CLI; it never contains secret values.
func (s *Store) RecentAuditEvents(ctx context.Context, action string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, ts, actor, action, target, detail FROM audit_events`
	args := []any{}
	if action != "" {
		q += ` WHERE action = ?`
		args = append(args, action)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("audit select: %w", err)
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		var ts string
		if err := rows.Scan(&e.ID, &ts, &e.Actor, &e.Action, &e.Target, &e.Detail); err != nil {
			return nil, fmt.Errorf("audit scan: %w", err)
		}
		e.TS = parseTime(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}
