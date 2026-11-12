package store

// v0.4.17: password-reset tokens and SMTP delivery configuration.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrResetRateLimited is returned when too many reset tokens were
// requested for one account in a short window.
var ErrResetRateLimited = errors.New("too many reset requests")

// SMTPConfig is the singleton mail-delivery configuration (row id = 1).
// The password is stored as configured — the DB file itself is the
// secret boundary (drevanta:drevanta, 0640); it is never echoed back
// through the API, only reported as password_set.
type SMTPConfig struct {
	Enabled     bool
	Host        string
	Port        int
	Username    string
	Password    string // never returned by the API layer
	FromAddr    string
	PasswordSet bool // whether a password is stored
}

type PasswordReset struct {
	ID        int64
	TokenHash string
	UserID    int64
	CreatedAt time.Time
	ExpiresAt time.Time
	Consumed  bool
}

// GetSMTPConfig returns the singleton mail configuration (zero value
// when never configured).
func (s *Store) GetSMTPConfig(ctx context.Context) (SMTPConfig, error) {
	var (
		c                      SMTPConfig
		enabled                int
		host, user, pass, from string
		port                   int
	)
	err := s.db.QueryRowContext(ctx, `
SELECT enabled, host, port, username, password, from_addr
FROM smtp_config WHERE id = 1`).
		Scan(&enabled, &host, &port, &user, &pass, &from)
	if errors.Is(err, sql.ErrNoRows) {
		return SMTPConfig{}, nil
	}
	if err != nil {
		return c, fmt.Errorf("load smtp config: %w", err)
	}
	c = SMTPConfig{
		Enabled:     enabled != 0,
		Host:        host,
		Port:        port,
		Username:    user,
		Password:    pass,
		FromAddr:    from,
		PasswordSet: pass != "",
	}
	return c, nil
}

// SetSMTPConfig writes the singleton mail configuration. An empty
// password keeps the previously stored one.
func (s *Store) SetSMTPConfig(ctx context.Context, c SMTPConfig) error {
	prev, err := s.GetSMTPConfig(ctx)
	if err != nil {
		return err
	}
	password := c.Password
	if password == "" {
		password = prev.Password
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO smtp_config (id, enabled, host, port, username, password, from_addr, updated_at)
VALUES (1, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
        enabled = excluded.enabled,
        host = excluded.host,
        port = excluded.port,
        username = excluded.username,
        password = excluded.password,
        from_addr = excluded.from_addr,
        updated_at = excluded.updated_at`,
		boolInt(c.Enabled), c.Host, c.Port, c.Username, password, c.FromAddr, nowStr())
	if err != nil {
		return fmt.Errorf("save smtp config: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// CreatePasswordReset stores a fresh single-use reset token (hashed) and
// invalidates the account's previous unconsumed one. It refuses when the
// account already has 3 unconsumed tokens issued in the last hour.
func (s *Store) CreatePasswordReset(ctx context.Context, tokenHash string, userID int64, ttl time.Duration) error {
	// The window counts every request, consumed or not: each new request
	// invalidates the previous token, so counting only unconsumed rows
	// would never trip the cap.
	var recent int
	err := s.db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM password_resets
WHERE user_id = ? AND created_at > ?`,
		userID, time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)).Scan(&recent)
	if err != nil {
		return fmt.Errorf("count recent resets: %w", err)
	}
	if recent >= 3 {
		return ErrResetRateLimited
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE password_resets SET consumed_at = ?
WHERE user_id = ? AND consumed_at = ''`, nowStr(), userID); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("invalidate previous resets: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO password_resets (token_hash, user_id, created_at, expires_at)
VALUES (?, ?, ?, ?)`,
		tokenHash, userID,
		now.Format(time.RFC3339Nano), now.Add(ttl).Format(time.RFC3339Nano)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("insert password reset: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

// ConsumePasswordReset validates and consumes a reset token: it must be
// the account's live token, unconsumed and unexpired.
func (s *Store) ConsumePasswordReset(ctx context.Context, tokenHash string) (int64, error) {
	var (
		id, userID   int64
		created, exp string
		consumed     string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, user_id, created_at, expires_at, consumed_at
FROM password_resets WHERE token_hash = ?`, tokenHash).
		Scan(&id, &userID, &created, &exp, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("lookup password reset: %w", err)
	}
	if consumed != "" {
		return 0, ErrNotFound
	}
	if !parseTime(exp).After(time.Now().UTC()) {
		return 0, ErrNotFound
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE password_resets SET consumed_at = ? WHERE id = ? AND consumed_at = ''`,
		nowStr(), id)
	if err != nil {
		return 0, fmt.Errorf("consume password reset: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, ErrNotFound
	}
	return userID, nil
}

// DeleteAllSessions ends every session of a user (used after a password
// RESET, where the acting party is by definition not signed in).
func (s *Store) DeleteAllSessions(ctx context.Context, userID int64) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return 0, fmt.Errorf("delete all sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return n, nil
}
